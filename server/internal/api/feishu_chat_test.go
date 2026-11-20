package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestFeishuBotChat(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	replies := make(chan string, 20)
	idp := newFakeIdP(t)
	base := idp.Config.Handler
	idp.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"tenant"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/reply") {
			if r.Header.Get("Authorization") != "Bearer tenant" {
				t.Error("missing tenant token")
			}
			raw, _ := io.ReadAll(r.Body)
			replies <- r.URL.Path + " " + string(raw)
			_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om-reply"}}`))
			return
		}
		base.ServeHTTP(w, r)
	})
	box, _ := secret.NewBox(make([]byte, 32))
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: idp.Client()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "login", code, 200, body)
	cfg := map[string]any{"enabled": true, "preset": "feishu", "issuer": idp.URL, "clientId": "dsh-ee", "clientSecret": "secret", "feishuApiBase": idp.URL, "feishuBot": true}
	code, body = admin.do("PUT", "/api/admin/auth/oidc", cfg, nil)
	expect(t, "requires token", code, 400, body)
	cfg["feishuVerificationToken"] = feishuTestToken
	save := func() {
		t.Helper()
		c, b := admin.do("PUT", "/api/admin/auth/oidc", cfg, nil)
		expect(t, "save bot", c, 200, b)
	}
	save()
	code, body = admin.do("GET", "/api/admin/auth/oidc", nil, nil)
	if code != 200 || !strings.Contains(string(body), `"feishuBot":true`) {
		t.Fatalf("bot not persisted %d %s", code, body)
	}
	member, err := st.GetRoleByCode(ctx, "member")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string) (*store.User, map[string]string) {
		u := &store.User{Username: name, DisplayName: name, AuthSource: store.AuthOIDC, ExternalDN: "feishu:ou-" + name}
		if err := st.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		if err := st.SetUserRoles(ctx, u.ID, []int64{member.ID}); err != nil {
			t.Fatal(err)
		}
		token := "ee_at_" + secret.Token()
		if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: "DSH", Prefix: token[:12], Scopes: []string{"agent", "connections"}, ClientKind: store.ClientKindDSH, CreatedBy: u.ID}, secret.Hash(token)); err != nil {
			t.Fatal(err)
		}
		return u, map[string]string{"Authorization": "Bearer " + token}
	}
	u, bearer := mk("chat-user")
	_, other := mk("chat-other")
	c := newClient(t, ts.URL)
	for _, scope := range []string{"models", "connections"} {
		token := "ee_at_" + secret.Token()
		if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: scope, Prefix: token[:12], Scopes: []string{scope}, CreatedBy: u.ID}, secret.Hash(token)); err != nil {
			t.Fatal(err)
		}
		restricted := map[string]string{"Authorization": "Bearer " + token}
		code, body = c.do("GET", "/api/me/feishu-chat/next", nil, restricted)
		expect(t, scope+" cannot receive chat", code, 403, body)
		code, body = c.do("POST", "/api/me/feishu-chat/1/reply", map[string]string{"text": "restricted reply"}, restricted)
		expect(t, scope+" cannot reply", code, 403, body)
	}
	event := func(id, openID, kind string) map[string]any {
		return map[string]any{"schema": "2.0", "header": map[string]any{"token": feishuTestToken, "app_id": "dsh-ee", "event_type": "im.message.receive_v1"}, "event": map[string]any{"sender": map[string]any{"sender_id": map[string]string{"open_id": openID}}, "message": map[string]string{"message_id": id, "chat_id": "oc-chat", "chat_type": "p2p", "message_type": kind, "content": `{"text":"hello"}`}}}
	}
	post := func(e any, want int) {
		t.Helper()
		n, b := c.do("POST", "/api/auth/feishu/event", e, nil)
		expect(t, "event", n, want, b)
	}
	notice := func(fragment string) {
		t.Helper()
		select {
		case got := <-replies:
			if !strings.Contains(got, fragment) {
				t.Fatalf("reply %s missing %s", got, fragment)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no Feishu reply")
		}
	}
	for _, table := range []string{"users", "feishu_chat_messages"} {
		t.Run("retry on "+table+" failure", func(t *testing.T) {
			if _, err := st.DB().ExecContext(ctx, "RENAME TABLE "+table+" TO "+table+"_unavailable"); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := st.DB().ExecContext(ctx, "RENAME TABLE "+table+"_unavailable TO "+table); err != nil {
					t.Fatal(err)
				}
			}()
			n, b := c.do("POST", "/api/auth/feishu/event", event("storage-failure", "ou-chat-user", "text"), nil)
			expect(t, "retryable storage failure", n, 500, b)
		})
	}
	code, body = c.do("POST", "/api/auth/feishu/event", map[string]string{"type": "url_verification", "token": feishuTestToken, "challenge": "challenge-1"}, nil)
	if code != 200 || !strings.Contains(string(body), "challenge-1") {
		t.Fatalf("challenge %d %s", code, body)
	}
	bad := event("bad", "ou-chat-user", "text")
	bad["header"].(map[string]any)["token"] = "bad"
	post(bad, 403)
	bad = event("bad-app", "ou-chat-user", "text")
	bad["header"].(map[string]any)["app_id"] = "other"
	post(bad, 403)
	post(event("unknown", "ou-unknown", "text"), 200)
	notice("请先使用飞书登录")
	post(event("image", "ou-chat-user", "image"), 200)
	notice("只支持文字消息")
	post(event("first", "ou-chat-user", "text"), 200)
	notice("当前不在线")
	setFeishuTestOrgLanguage(t, admin, "es")
	post(event("unknown-es", "ou-unknown", "text"), 200)
	notice("Primero inicia sesión")
	lang := "en"
	if err := st.UpdateUser(ctx, u.ID, store.UserPatch{Language: &lang}); err != nil {
		t.Fatal(err)
	}
	post(event("image-en", "ou-chat-user", "image"), 200)
	notice("Only text messages")
	post(event("offline-en", "ou-chat-user", "text"), 200)
	notice("Your DSH is offline")
	if _, err := st.DB().ExecContext(ctx, `UPDATE feishu_chat_messages SET status='expired' WHERE message_id='offline-en'`); err != nil {
		t.Fatal(err)
	}
	lang = ""
	if err := st.UpdateUser(ctx, u.ID, store.UserPatch{Language: &lang}); err != nil {
		t.Fatal(err)
	}
	setFeishuTestOrgLanguage(t, admin, "zh")
	code, body = c.do("GET", "/api/me/feishu-chat/next", nil, other)
	expect(t, "isolation", code, 204, body)
	code, body = c.do("GET", "/api/me/feishu-chat/next", nil, bearer)
	expect(t, "deliver", code, 200, body)
	var m store.FeishuChatMessage
	if err := json.Unmarshal(body, &m); err != nil || m.Text != "hello" || m.ID == 0 || m.CreatedAt.IsZero() {
		t.Fatalf("message %s %v", body, err)
	}
	replyPath := fmt.Sprintf("/api/me/feishu-chat/%d/reply", m.ID)
	code, body = c.do("POST", replyPath, map[string]string{"text": "answer"}, other)
	expect(t, "reply isolation", code, 404, body)
	code, body = c.do("POST", replyPath, map[string]string{"text": " "}, bearer)
	expect(t, "empty reply", code, 400, body)
	code, body = c.do("POST", replyPath, map[string]string{"text": "**answer**"}, bearer)
	expect(t, "reply", code, 200, body)
	notice("/messages/first/reply")
	if _, err := st.DB().ExecContext(ctx, `UPDATE feishu_chat_messages SET id=1234567 WHERE id=?`, m.ID); err != nil {
		t.Fatal(err)
	}
	a := agent{dshClient{t: t, base: ts.URL, token: strings.TrimPrefix(bearer["Authorization"], "Bearer ")}}
	if out := a.ok("ee_feishu", map[string]any{"action": "reply", "id": 1234567, "text": "large id answer"}); out["ok"] != true {
		t.Fatalf("large message id reply: %v", out)
	}
	notice("/messages/first/reply")
	post(event("first", "ou-chat-user", "text"), 200)
	code, body = c.do("GET", "/api/me/feishu-chat/next", nil, bearer)
	expect(t, "retry dedupe", code, 204, body)
	code, body = c.do("GET", "/api/me/feishu-chat", nil, bearer)
	if code != 200 || !strings.Contains(string(body), `"online":true`) || !strings.Contains(string(body), `"bound":true`) {
		t.Fatalf("status %d %s", code, body)
	}
	result := make(chan int, 1)
	go func() { n, _ := c.do("GET", "/api/me/feishu-chat/next?wait=5", nil, bearer); result <- n }()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	post(event("wake", "ou-chat-user", "text"), 200)
	select {
	case n := <-result:
		if n != 200 || time.Since(start) > 2*time.Second {
			t.Fatalf("poll did not wake: %d", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("poll blocked")
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE feishu_chat_messages SET delivered_at=? WHERE message_id='wake'`, time.Now().Add(-11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	code, body = c.do("GET", "/api/me/feishu-chat/next", nil, bearer)
	expect(t, "redelivery", code, 200, body)
	if _, err := st.DB().ExecContext(ctx, `UPDATE feishu_chat_messages SET created_at=? WHERE message_id='wake'`, time.Now().Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	code, body = c.do("GET", "/api/me/feishu-chat/next", nil, bearer)
	expect(t, "expiry", code, 204, body)
	var status string
	if err := st.DB().QueryRowContext(ctx, `SELECT status FROM feishu_chat_messages WHERE message_id='wake'`).Scan(&status); err != nil || status != "expired" {
		t.Fatalf("expiry %s %v", status, err)
	}
	busyUser, busyBearer := mk("chat-busy")
	checkBusyStatus := func(want bool) {
		t.Helper()
		n, b := c.do("GET", "/api/me/feishu-chat", nil, busyBearer)
		if n != 200 || !strings.Contains(string(b), fmt.Sprintf(`"online":%t`, want)) {
			t.Fatalf("busy chat status want %t: %d %s", want, n, b)
		}
	}
	checkBusyStatus(false)
	busyKey := store.FeishuChatKey{UserID: busyUser.ID, SenderID: "ou-chat-busy"}
	if err := st.DB().QueryRowContext(ctx, `SELECT app_scope FROM feishu_chat_messages WHERE message_id='first'`).Scan(&busyKey.AppScope); err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnqueueFeishuChat(ctx, busyKey, "busy-turn", "oc-busy", "long-running work"); err != nil {
		t.Fatal(err)
	}
	checkBusyStatus(false)
	if _, err := st.NextFeishuChat(ctx, busyKey); err != nil {
		t.Fatal(err)
	}
	checkBusyStatus(true)
	post(event("busy-followup", "ou-chat-busy", "text"), 200)
	select {
	case got := <-replies:
		t.Fatalf("busy relay received false offline notice: %s", got)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE feishu_chat_messages SET delivered_at=? WHERE message_id='busy-turn'`, time.Now().Add(-11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	checkBusyStatus(false)
	post(event("busy-offline", "ou-chat-busy", "text"), 200)
	notice("/messages/busy-offline/reply")
	disabled := true
	revokedUser, revokedBearer := mk("chat-revoked")
	revokedResult := make(chan int, 1)
	go func() { n, _ := c.do("GET", "/api/me/feishu-chat/next?wait=5", nil, revokedBearer); revokedResult <- n }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		n, b := c.do("GET", "/api/me/feishu-chat", nil, revokedBearer)
		if n == 200 && strings.Contains(string(b), `"online":true`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revocation poll did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	token, err := st.AccessTokenByHash(ctx, secret.Hash(strings.TrimPrefix(revokedBearer["Authorization"], "Bearer ")))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeToken(ctx, token.ID); err != nil {
		t.Fatal(err)
	}
	post(event("revoked-wake", "ou-chat-revoked", "text"), 200)
	select {
	case n := <-revokedResult:
		if n != http.StatusUnauthorized {
			t.Fatalf("revoked poll returned %d", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("revoked poll did not finish")
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT status FROM feishu_chat_messages WHERE user_id=? AND message_id='revoked-wake'`, revokedUser.ID).Scan(&status); err != nil || status != "pending" {
		t.Fatalf("revoked poll claimed message: %s %v", status, err)
	}
	if err := st.UpdateUser(ctx, u.ID, store.UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	post(event("disabled", "ou-chat-user", "text"), 200)
	notice("请先使用飞书登录")
	code, body = c.do("GET", "/api/me/feishu-chat/next?wait=1", nil, other)
	expect(t, "poll timeout", code, 204, body)
	cfg["feishuEncryptKey"] = "encrypt-chat-key"
	save()
	post(event("plaintext", "ou-chat-other", "text"), 403)
	encrypted, headers := feishuEncrypt(t, "encrypt-chat-key", map[string]string{"type": "url_verification", "token": feishuTestToken, "challenge": "encrypted-challenge"})
	var encryptedBody map[string]any
	if err := json.Unmarshal(encrypted, &encryptedBody); err != nil {
		t.Fatal(err)
	}
	code, body = c.do("POST", "/api/auth/feishu/event", encryptedBody, headers)
	if code != 200 || !strings.Contains(string(body), "encrypted-challenge") {
		t.Fatalf("encrypted challenge %d %s", code, body)
	}
	headers["X-Lark-Signature"] = "bad"
	code, body = c.do("POST", "/api/auth/feishu/event", encryptedBody, headers)
	expect(t, "bad signature", code, 403, body)
	cfg["feishuEncryptKey"] = ""
	save()
	cfg["feishuBot"] = false
	save()
	post(event("off", "ou-chat-user", "text"), 404)
	code, body = c.do("GET", "/api/me/feishu-chat/next", nil, other)
	expect(t, "disabled poll", code, 404, body)
}
