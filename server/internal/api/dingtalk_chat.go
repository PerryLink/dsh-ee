package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/dingtalk"
	"github.com/hikariming/dsh-ee/server/internal/oidc"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

type dingTalkPollKey struct {
	scope, sender string
	userID        int64
}

type dingTalkChatState struct {
	mu    sync.Mutex
	polls map[dingTalkPollKey]time.Time
}

func (s *Server) dingTalkBotConfig(ctx context.Context) (*oidc.Config, bool) {
	cfg, enabled := s.oidcEnabled(ctx)
	return cfg, enabled && cfg.Preset == dingtalk.Preset && cfg.DingTalkBot &&
		cfg.DingTalkCorpID != "" && cfg.ClientSecret != ""
}

func dingTalkIdentity(cfg *oidc.Config, u *store.User) (dingTalkPollKey, bool) {
	if cfg == nil || cfg.Preset != dingtalk.Preset || cfg.DingTalkCorpID == "" ||
		u == nil || u.AuthSource != store.AuthOIDC || u.AccountType != store.AccountHuman || u.Disabled {
		return dingTalkPollKey{}, false
	}
	sender, ok := strings.CutPrefix(u.ExternalDN, dingtalk.ExternalSubject(cfg.DingTalkCorpID, ""))
	return dingTalkPollKey{scope: dingTalkLoginTarget(cfg), sender: sender, userID: u.ID}, ok && sender != ""
}

func (s *Server) dingTalkPoll(key dingTalkPollKey, update bool) bool {
	state := &s.dingTalkState
	state.mu.Lock()
	defer state.mu.Unlock()
	if update {
		if state.polls == nil {
			state.polls = make(map[dingTalkPollKey]time.Time)
		}
		state.polls[key] = time.Now()
	}
	return time.Since(state.polls[key]) < time.Minute
}

func (s *Server) dingTalkOnline(ctx context.Context, key dingTalkPollKey) (bool, error) {
	if s.dingTalkPoll(key, false) {
		return true, nil
	}
	return s.store.DingTalkChatActive(ctx, key.scope, key.userID, key.sender)
}

func (s *Server) runDingTalkChat(ctx context.Context) {
	refresh := time.NewTicker(5 * time.Second)
	defer refresh.Stop()
	purge := time.NewTicker(time.Minute)
	defer purge.Stop()
	var previous *oidc.Config
	var done chan error
	var cancel context.CancelFunc
	stop := func() {
		if cancel != nil {
			cancel()
			<-done
			cancel, done = nil, nil
		}
	}
	defer stop()
	retryAt := time.Time{}
	backoff := 5 * time.Second
	for {
		cfg, enabled := s.dingTalkBotConfig(ctx)
		if !enabled || previous == nil || dingTalkLoginTarget(cfg) != dingTalkLoginTarget(previous) || cfg.ClientSecret != previous.ClientSecret {
			stop()
			previous = nil
			retryAt, backoff = time.Time{}, 5*time.Second
		}
		if enabled && done == nil && time.Now().After(retryAt) {
			previous = cfg
			child, stopStream := context.WithCancel(ctx)
			cancel = stopStream
			result := make(chan error, 1)
			done = result
			go func() {
				result <- s.dingTalkClient(cfg).Receive(child, s.opts.DingTalkStreamDial, func(ctx context.Context, msg dingtalk.BotMessage) error {
					return s.receiveDingTalkChat(ctx, cfg, msg)
				})
			}()
		}
		select {
		case <-ctx.Done():
			return
		case <-refresh.C:
		case <-purge.C:
			state := &s.dingTalkState
			state.mu.Lock()
			for k, t := range state.polls {
				if time.Since(t) >= time.Minute {
					delete(state.polls, k)
				}
			}
			state.mu.Unlock()
			if err := s.store.PurgeDingTalkChats(ctx); err != nil {
				s.log.Warn("清理过期钉钉聊天失败", "err", err)
			}
		case err := <-done:
			cancel()
			cancel, done = nil, nil
			if err != nil {
				s.log.Warn("钉钉 Stream 连接结束", "err", err)
			}
			retryAt = time.Now().Add(backoff)
			backoff = min(backoff*2, time.Minute)
		}
	}
}

func (s *Server) receiveDingTalkChat(stream context.Context, connected *oidc.Config, msg dingtalk.BotMessage) error {
	ctx, cancel := context.WithTimeout(stream, 4*time.Second)
	defer cancel()
	cfg, err := s.loadOIDC(ctx)
	if err != nil {
		return err
	}
	scope := dingTalkLoginTarget(connected)
	if !cfg.Enabled || !cfg.DingTalkBot || dingTalkLoginTarget(cfg) != scope || cfg.ClientSecret != connected.ClientSecret {
		return nil
	}
	if msg.ConversationType != "1" || msg.RobotCorpID != cfg.DingTalkCorpID ||
		(msg.SenderCorpID != "" && msg.SenderCorpID != cfg.DingTalkCorpID) ||
		msg.SenderUserID == "" || len(msg.SenderUserID) > 512 || msg.RobotCode == "" || len(msg.RobotCode) > 255 ||
		msg.ID == "" || len(msg.ID) > 255 {
		return nil
	}
	subject := dingtalk.ExternalSubject(cfg.DingTalkCorpID, msg.SenderUserID)
	u, err := s.store.GetUserByExternalDN(ctx, store.AuthOIDC, subject)
	if err != nil && !notFound(err) {
		return err
	}
	key, bound := dingTalkIdentity(cfg, u)
	lang := "zh"
	if u != nil {
		lang = s.userLanguage(ctx, u)
	}
	notice := ""
	text := strings.TrimSpace(msg.Text.Content)
	if !bound || key.sender != msg.SenderUserID {
		key.userID, text = 0, ""
		notice = dingTalkNotice(lang, "请先在 dsh-ee 登录页用钉钉登录，并在电脑上连接 DSH。", "Sign in to dsh-ee with DingTalk first, then connect DSH on your computer.")
	} else if msg.MessageType != "text" || text == "" {
		key.userID, text = 0, ""
		notice = dingTalkNotice(lang, "目前只支持文字消息。", "Only text messages are supported.")
	}
	if len(text) > 32*1024 {
		key.userID, text = 0, ""
		notice = dingTalkNotice(lang, "消息太长了，请拆成多条发送。", "This message is too long. Split it into shorter messages.")
	}
	inserted, err := s.store.EnqueueDingTalkChat(ctx, scope, key.userID, msg.ID, msg.SenderUserID, msg.RobotCode, text)
	if err != nil || !inserted {
		return err
	}
	if key.userID != 0 {
		if online, err := s.dingTalkOnline(ctx, key); err == nil && !online {
			notice = dingTalkNotice(lang, "你的 DSH 目前不在线；一小时内连接后会处理这条消息。", "Your DSH is offline. If you connect within an hour, DSH will process this message.")
		}
		go s.auditSystem(stream, "dingtalk_chat.received", strconv.FormatInt(key.userID, 10), nil)
	}
	if notice != "" {
		go func() {
			ctx, cancel := context.WithTimeout(stream, 15*time.Second)
			defer cancel()
			if err := s.dingTalkClient(cfg).SendText(ctx, msg.RobotCode, msg.SenderUserID, notice); err != nil {
				s.log.Warn("发送钉钉聊天提示失败", "err", err)
			}
		}()
	}
	return nil
}

func dingTalkNotice(lang, zh, en string) string {
	if lang == "zh" {
		return zh
	}
	return en
}

func (s *Server) handleDingTalkChatStatus(w http.ResponseWriter, r *http.Request) {
	cfg, enabled := s.dingTalkBotConfig(r.Context())
	key, bound := dingTalkIdentity(cfg, currentUser(r))
	online := false
	if enabled && bound {
		var err error
		online, err = s.dingTalkOnline(r.Context(), key)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"available": cfg != nil && cfg.Enabled && cfg.Preset == dingtalk.Preset, "enabled": enabled, "bound": bound, "online": online})
}

func (s *Server) handleDingTalkChatNext(w http.ResponseWriter, r *http.Request) {
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	timer := time.NewTimer(time.Duration(max(0, min(wait, 25))) * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		cfg, enabled := s.dingTalkBotConfig(r.Context())
		if !enabled {
			writeErrCode(w, 404, codeDingTalkChatDisabled, nil, "钉钉机器人未开启")
			return
		}
		rp, p, _, ok := s.tokenRequest(w, r)
		if !ok {
			return
		}
		if !s.agentOn(rp.Context(), p) {
			writeErrCode(w, 403, codeAuthForbidden, nil, "这个令牌没有 Agent 权限")
			return
		}
		key, bound := dingTalkIdentity(cfg, currentUser(rp))
		if !bound {
			writeErrCode(w, 404, codeDingTalkChatUnbound, nil, "请先用钉钉登录并连接 DSH")
			return
		}
		s.dingTalkPoll(key, true)
		msg, err := s.store.NextDingTalkChat(rp.Context(), key.scope, key.userID, key.sender)
		if err == nil {
			writeJSON(w, 200, msg)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			s.internalErr(w, rp, err)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-tick.C:
		}
	}
}

func (s *Server) handleDingTalkChatReply(w http.ResponseWriter, r *http.Request) {
	cfg, enabled := s.dingTalkBotConfig(r.Context())
	if !enabled {
		writeErrCode(w, 404, codeDingTalkChatDisabled, nil, "钉钉机器人未开启")
		return
	}
	key, bound := dingTalkIdentity(cfg, currentUser(r))
	if !bound {
		writeErrCode(w, 404, codeDingTalkChatUnbound, nil, "请先用钉钉登录并连接 DSH")
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		writeErrCode(w, 400, codeDingTalkChatEmpty, nil, "回复不能为空")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	msg, err := s.store.DingTalkChatForReply(r.Context(), key.scope, key.userID, id, key.sender)
	if errors.Is(err, sql.ErrNoRows) {
		writeErrCode(w, 404, codeDingTalkChatMissing, nil, "消息不存在或已过期")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if runes := []rune(text); len(runes) > 4000 {
		text = string(runes[:4000])
	}
	if err := s.dingTalkClient(cfg).SendText(r.Context(), msg.RobotCode, msg.SenderID, text); err != nil {
		s.log.Warn("钉钉聊天回复失败", "err", err)
		writeErrCode(w, 502, codeDingTalkChatSend, nil, "钉钉回复发送失败，请稍后重试")
		return
	}
	if err := s.store.FinishDingTalkChat(r.Context(), id); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, "dingtalk_chat.replied", strconv.FormatInt(id, 10), nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
