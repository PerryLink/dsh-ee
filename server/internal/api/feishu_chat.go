package api

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/feishu"
	"github.com/hikariming/dsh-ee/server/internal/oidc"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

type feishuChatPresence struct {
	lastPoll time.Time
	wake     chan struct{}
}

func (s *Server) chatPresence(key store.FeishuChatKey, poll, wake bool) (bool, <-chan struct{}) {
	s.feishuChatMu.Lock()
	defer s.feishuChatMu.Unlock()
	if s.feishuChatPresence == nil {
		s.feishuChatPresence = map[store.FeishuChatKey]*feishuChatPresence{}
	}
	p := s.feishuChatPresence[key]
	if p == nil {
		p = &feishuChatPresence{wake: make(chan struct{})}
		s.feishuChatPresence[key] = p
	}
	if poll {
		p.lastPoll = time.Now()
	}
	if wake {
		close(p.wake)
		p.wake = make(chan struct{})
	}
	return time.Since(p.lastPoll) < time.Minute, p.wake
}

func (s *Server) feishuBotConfig(ctx context.Context) (*oidc.Config, bool) {
	cfg, _ := s.oidcByPreset(ctx, "feishu")
	return cfg, cfg != nil && cfg.FeishuBot
}

func feishuChatScope(cfg *oidc.Config) string {
	base := feishu.NewClient(cfg.FeishuAPIBase, "", "", nil).BaseURL
	sum := sha256.Sum256([]byte(cfg.ClientID + "\x00" + normalizeOIDCIssuer(cfg.Issuer) + "\x00" + normalizeOIDCIssuer(base)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Server) feishuChatKey(ctx context.Context, cfg *oidc.Config, u *store.User) (store.FeishuChatKey, error) {
	if cfg == nil {
		return store.FeishuChatKey{}, nil
	}
	sender, err := s.chatSender(ctx, u, "feishu", feishu.ExternalSubject(""))
	if err != nil || sender == "" {
		return store.FeishuChatKey{}, err
	}
	return store.FeishuChatKey{AppScope: feishuChatScope(cfg), UserID: u.ID, SenderID: sender}, nil
}

func (s *Server) feishuChatOnline(ctx context.Context, key store.FeishuChatKey) (bool, error) {
	if online, _ := s.chatPresence(key, false, false); online {
		return true, nil
	}
	return s.store.HasActiveFeishuChat(ctx, key)
}

func (s *Server) handleFeishuChatStatus(w http.ResponseWriter, r *http.Request) {
	cfg, enabled := s.feishuBotConfig(r.Context())
	key, err := s.feishuChatKey(r.Context(), cfg, currentUser(r))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	var online bool
	if enabled && key.UserID != 0 {
		online, err = s.feishuChatOnline(r.Context(), key)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled, "bound": key.UserID != 0, "online": online})
}

func (s *Server) handleFeishuChatEvent(w http.ResponseWriter, r *http.Request) {
	cfg, _, err := s.loadOIDCByPreset(r.Context(), "feishu")
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if cfg == nil || !cfg.FeishuBot {
		writeErrCode(w, 404, codeFeishuChatDisabled, nil, "飞书机器人未开启")
		return
	}
	cb := verifiedFeishuCallback(w, r, cfg)
	if cb == nil {
		return
	}
	status := http.StatusOK
	defer func() { writeJSON(w, status, map[string]any{}) }()
	if cb.Header.EventType != "im.message.receive_v1" || cb.Event.Message.ChatType != "p2p" || cb.Event.Message.MessageID == "" {
		return
	}
	// Queue persistence is completed before returning; only outbound network calls are asynchronous.
	ctx := r.Context()
	msg := cb.Event.Message
	sender := cb.Event.Sender.SenderID.OpenID
	if sender == "" || len(feishu.ExternalSubject(sender)) > 512 {
		return
	}
	u, err := s.chatUserBySubject(ctx, feishu.ExternalSubject(sender))
	var content struct {
		Text string `json:"text"`
	}
	notice := ""
	key := store.FeishuChatKey{AppScope: feishuChatScope(cfg), SenderID: sender}
	lang := s.orgLanguage(ctx)
	if u != nil {
		lang = s.userLanguage(ctx, u)
	}
	if err != nil && !notFound(err) {
		s.log.Warn("读取飞书聊天用户失败", "err", err)
		status = http.StatusInternalServerError
		return
	}
	if !chatEmployee(u) {
		notice = feishu.Text(lang, feishu.MsgChatLogin, map[string]string{"url": s.publicBase(r)})
	} else if msg.MessageType != "text" || json.Unmarshal([]byte(msg.Content), &content) != nil || strings.TrimSpace(content.Text) == "" {
		notice = feishu.Text(lang, feishu.MsgChatTextOnly, nil)
	} else {
		key.UserID = u.ID
	}
	inserted, err := s.store.EnqueueFeishuChat(ctx, key, msg.MessageID, msg.ChatID, content.Text)
	if err != nil {
		s.log.Warn("保存飞书聊天消息失败", "err", err)
		status = http.StatusInternalServerError
		return
	}
	if !inserted {
		return
	}
	if key.UserID != 0 {
		s.chatPresence(key, false, true)
		s.auditAs(r, u, "feishu_chat.received", strconv.FormatInt(key.UserID, 10), nil)
		online, err := s.feishuChatOnline(ctx, key)
		if err != nil {
			s.log.Warn("读取飞书聊天在线状态失败", "err", err)
			return
		}
		if !online {
			notice = feishu.Text(lang, feishu.MsgChatOffline, nil)
		}
	}
	if notice != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			client := feishu.NewClient(cfg.FeishuAPIBase, cfg.ClientID, cfg.ClientSecret, s.oidcHTTP().HTTP)
			if _, err := client.ReplyText(ctx, msg.MessageID, notice); err != nil {
				s.log.Warn("发送飞书聊天提示失败", "err", err)
			}
		}()
	}
}

func (s *Server) handleFeishuChatNext(w http.ResponseWriter, r *http.Request) {
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	wait = max(0, min(wait, 25))
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	// Periodic checks also pick up deliveries from another server instance.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		cfg, enabled := s.feishuBotConfig(r.Context())
		if !enabled {
			writeErrCode(w, 404, codeFeishuChatDisabled, nil, "飞书机器人未开启")
			return
		}
		rp, p, _, ok := s.tokenRequest(w, r)
		if !ok {
			return
		}
		if !s.agentOn(r.Context(), p) {
			writeErrCode(w, http.StatusForbidden, codeAuthForbidden, nil, "这个令牌没有 Agent 权限")
			return
		}
		r = rp
		key, err := s.feishuChatKey(r.Context(), cfg, currentUser(r))
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		var wake <-chan struct{}
		if key.UserID != 0 {
			_, wake = s.chatPresence(key, true, false)
			m, err := s.store.NextFeishuChat(r.Context(), key)
			if err == nil {
				writeJSON(w, 200, m)
				return
			}
			if !errors.Is(err, sql.ErrNoRows) {
				s.internalErr(w, r, err)
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
			return
		case <-wake:
		case <-tick.C:
		}
	}
}

func (s *Server) handleFeishuChatReply(w http.ResponseWriter, r *http.Request) {
	cfg, ok := s.feishuBotConfig(r.Context())
	if !ok {
		writeErrCode(w, 404, codeFeishuChatDisabled, nil, "飞书机器人未开启")
		return
	}
	var in struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		writeErrCode(w, 400, codeFeishuChatEmpty, nil, "回复不能为空")
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	key, err := s.feishuChatKey(r.Context(), cfg, currentUser(r))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if key.UserID == 0 {
		writeErrCode(w, 404, codeFeishuChatMissing, nil, "消息不存在或已过期")
		return
	}
	m, err := s.store.FeishuChatForReply(r.Context(), key, id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErrCode(w, 404, codeFeishuChatMissing, nil, "消息不存在或已过期")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	client := feishu.NewClient(cfg.FeishuAPIBase, cfg.ClientID, cfg.ClientSecret, s.oidcHTTP().HTTP)
	text := feishu.Clip(in.Text, 4000)
	if _, err := client.ReplyText(r.Context(), m.MessageID, text); err != nil {
		writeErrCode(w, 502, codeFeishuChatSend, nil, "飞书回复发送失败，请稍后重试")
		return
	}
	if err := s.store.FinishFeishuChatReply(r.Context(), key, id, text); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.auditAs(r, currentUser(r), "feishu_chat.replied", strconv.FormatInt(id, 10), nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
