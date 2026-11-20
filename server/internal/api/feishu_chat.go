package api

import (
	"context"
	"database/sql"
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

func (s *Server) chatPresence(userID int64, poll, wake bool) (bool, <-chan struct{}) {
	s.feishuChatMu.Lock()
	defer s.feishuChatMu.Unlock()
	if s.feishuChatPresence == nil {
		s.feishuChatPresence = map[int64]*feishuChatPresence{}
	}
	p := s.feishuChatPresence[userID]
	if p == nil {
		p = &feishuChatPresence{wake: make(chan struct{})}
		s.feishuChatPresence[userID] = p
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
	cfg, err := s.loadOIDC(ctx)
	return cfg, err == nil && cfg.Enabled && cfg.Preset == "feishu" && cfg.FeishuBot
}

func (s *Server) handleFeishuChatStatus(w http.ResponseWriter, r *http.Request) {
	_, enabled := s.feishuBotConfig(r.Context())
	u := currentUser(r)
	online, _ := s.chatPresence(u.ID, false, false)
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": enabled, "bound": u.AuthSource == store.AuthOIDC && strings.HasPrefix(u.ExternalDN, "feishu:"), "online": online})
}

func (s *Server) handleFeishuChatEvent(w http.ResponseWriter, r *http.Request) {
	cb := s.verifiedFeishuCallback(w, r, true)
	if cb == nil {
		return
	}
	defer writeJSON(w, http.StatusOK, map[string]any{})
	if cb.Header.EventType != "im.message.receive_v1" || cb.Event.Message.ChatType != "p2p" || cb.Event.Message.MessageID == "" {
		return
	}
	// Queue persistence is completed before returning; only outbound network calls are asynchronous.
	ctx := r.Context()
	msg := cb.Event.Message
	u, err := s.store.GetUserByExternalDN(ctx, store.AuthOIDC, feishu.ExternalSubject(cb.Event.Sender.SenderID.OpenID))
	var content struct {
		Text string `json:"text"`
	}
	notice := ""
	userID := int64(0)
	lang := s.orgLanguage(ctx)
	if u != nil {
		lang = s.userLanguage(ctx, u)
	}
	if err != nil && !notFound(err) {
		s.log.Warn("读取飞书聊天用户失败", "err", err)
		return
	}
	if u == nil || u.Disabled || u.AccountType != store.AccountHuman {
		notice = feishu.Text(lang, feishu.MsgChatLogin, map[string]string{"url": s.publicBase(r)})
	} else if msg.MessageType != "text" || json.Unmarshal([]byte(msg.Content), &content) != nil || strings.TrimSpace(content.Text) == "" {
		notice = feishu.Text(lang, feishu.MsgChatTextOnly, nil)
	} else {
		userID = u.ID
	}
	inserted, err := s.store.EnqueueFeishuChat(ctx, userID, msg.MessageID, msg.ChatID, content.Text)
	if err != nil {
		s.log.Warn("保存飞书聊天消息失败", "err", err)
		return
	}
	if !inserted {
		return
	}
	if userID != 0 {
		online, _ := s.chatPresence(userID, false, true)
		s.auditAs(r, u, "feishu_chat.received", strconv.FormatInt(userID, 10), nil)
		if !online {
			notice = feishu.Text(lang, feishu.MsgChatOffline, nil)
		}
	}
	if notice != "" {
		cfg, ok := s.feishuBotConfig(ctx)
		if !ok {
			return
		}
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
	if _, ok := s.feishuBotConfig(r.Context()); !ok {
		writeErrCode(w, 404, codeFeishuChatDisabled, nil, "飞书机器人未开启")
		return
	}
	wait, _ := strconv.Atoi(r.URL.Query().Get("wait"))
	wait = max(0, min(wait, 25))
	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	// Periodic checks also pick up deliveries from another server instance.
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		rp, p, _, ok := s.tokenRequest(w, r)
		if !ok {
			return
		}
		if !s.agentOn(r.Context(), p) {
			writeErrCode(w, http.StatusForbidden, codeAuthForbidden, nil, "这个令牌没有 Agent 权限")
			return
		}
		r = rp
		_, wake := s.chatPresence(currentUser(r).ID, true, false)
		m, err := s.store.NextFeishuChat(r.Context(), currentUser(r).ID)
		if err == nil {
			writeJSON(w, 200, m)
			return
		}
		if !errors.Is(err, sql.ErrNoRows) {
			s.internalErr(w, r, err)
			return
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
		if _, ok := s.feishuBotConfig(r.Context()); !ok {
			writeErrCode(w, 404, codeFeishuChatDisabled, nil, "飞书机器人未开启")
			return
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
	m, err := s.store.FeishuChatForReply(r.Context(), currentUser(r).ID, id)
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
	if err := s.store.FinishFeishuChatReply(r.Context(), currentUser(r).ID, id, text); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.auditAs(r, currentUser(r), "feishu_chat.replied", strconv.FormatInt(id, 10), nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}
