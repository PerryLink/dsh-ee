package api_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

const embedHost = "https://harness.example.com"

type embedFixture struct {
	st    *store.Store
	srv   *api.Server
	ts    *httptest.Server
	admin *client
	slug  string
}

// newEmbedFixture 起一个开启跨站嵌入的服务端，并建一个开放网页版的应用。
func newEmbedFixture(t *testing.T, mutate func(*api.Options)) *embedFixture {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	opts := api.Options{
		DataDir:         t.TempDir(),
		EmbedOrigins:    []string{embedHost},
		EmbedCrossSite:  true,
		EmbedSessionTTL: time.Hour,
	}
	if mutate != nil {
		mutate(&opts)
	}
	srv := api.New(st, box, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	f := &embedFixture{st: st, srv: srv, ts: ts, admin: admin, slug: "embed-app"}
	spec := map[string]any{
		"title": "嵌入应用", "description": "e2e", "persona": "你是嵌入助手。", "web": true,
		"tables": []any{map[string]any{
			"name": "notes", "title": "备注", "visibility": "own",
			"fields": []any{map[string]any{"name": "text", "title": "内容", "kind": "text"}},
		}},
		"workbench": map[string]any{"panes": []any{map[string]any{
			"id": "main", "type": "panel", "title": "主页",
			"widgets": []any{map[string]any{"type": "heading", "title": "内容"}},
		}}},
	}
	code, body = admin.do("POST", "/api/hub", map[string]any{"kind": "app", "slug": f.slug, "delivery": "auto", "spec": spec}, nil)
	expect(t, "建应用", code, 201, body)
	return f
}

// user 建一个员工并返回；密码是 employee-password-1。
func (f *embedFixture) user(t *testing.T, username string) store.User {
	t.Helper()
	code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
		"username": username, "password": "employee-password-1", "department": "研发部",
	}, nil)
	expect(t, "建员工 "+username, code, 201, body)
	return decode[store.User](t, body)
}

// token 直接往库里写一个 ee_at_ 令牌（明文用 plain）。
func (f *embedFixture) token(t *testing.T, userID int64, plain string, scopes []string) {
	t.Helper()
	tok := &store.AccessToken{
		UserID: userID, Name: plain, Prefix: plain, Source: "manual", Scopes: scopes,
		ClientName: "test-client", CreatedBy: 1,
	}
	if err := f.st.CreateAccessToken(context.Background(), tok, secret.Hash(plain)); err != nil {
		t.Fatal(err)
	}
}

func (f *embedFixture) mint(t *testing.T, token string, body map[string]any) (int, []byte) {
	t.Helper()
	return newClient(t, f.ts.URL).do("POST", "/api/embed/sessions", body, bearer(token))
}

// exchange 用原始 HTTP 客户端，方便检查响应头（必须没有 Set-Cookie）。
func (f *embedFixture) exchange(t *testing.T, code string) (*http.Response, []byte) {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"code": code})
	req, _ := http.NewRequest("POST", f.ts.URL+"/api/auth/embed/exchange", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

func (f *embedFixture) mintAndExchange(t *testing.T, token string) string {
	t.Helper()
	code, body := f.mint(t, token, map[string]any{"app": f.slug, "host_origin": embedHost})
	expect(t, "mint", code, 200, body)
	minted := decode[struct{ Code string }](t, body)
	resp, raw := f.exchange(t, minted.Code)
	if resp.StatusCode != 200 {
		t.Fatalf("exchange 失败：%d %s", resp.StatusCode, raw)
	}
	return decode[struct{ Token string }](t, raw).Token
}

func (f *embedFixture) me(t *testing.T, token string) (int, []byte) {
	t.Helper()
	return newClient(t, f.ts.URL).do("GET", "/api/auth/me", nil, bearer(token))
}

func TestEmbedMintAndExchange(t *testing.T) {
	f := newEmbedFixture(t, nil)
	emp := f.user(t, "embed-emp")
	f.token(t, emp.ID, "ee_at_embed_emp", []string{rbac.ScopeHub, rbac.ScopeAgent})

	// 没带令牌、令牌没有 agent 范围：都换不了 code
	if code, body := f.mint(t, "", map[string]any{"app": f.slug, "host_origin": embedHost}); code != 401 {
		t.Fatalf("无令牌 mint 应 401：%d %s", code, body)
	}
	f.token(t, emp.ID, "ee_at_embed_hubonly", []string{rbac.ScopeHub})
	if code, body := f.mint(t, "ee_at_embed_hubonly", map[string]any{"app": f.slug, "host_origin": embedHost}); code != 403 {
		t.Fatalf("没有 agent 范围的令牌应 403：%d %s", code, body)
	}

	// 来源不在允许列表、应用不存在：拒绝
	code, body := f.mint(t, "ee_at_embed_emp", map[string]any{"app": f.slug, "host_origin": "https://evil.example.com"})
	expect(t, "来源不在白名单", code, 403, body)
	if got := decode[struct{ Code string }](t, body).Code; got != "origin_not_allowed" {
		t.Fatalf("来源错误码 = %q", got)
	}
	code, body = f.mint(t, "ee_at_embed_emp", map[string]any{"app": "no-such-app", "host_origin": embedHost})
	expect(t, "应用不存在", code, 404, body)
	if got := decode[struct{ Code string }](t, body).Code; got != "app_not_found" {
		t.Fatalf("应用错误码 = %q", got)
	}

	// 正常 mint：60 秒、绑定应用与来源
	code, body = f.mint(t, "ee_at_embed_emp", map[string]any{"app": f.slug, "host_origin": embedHost})
	expect(t, "mint", code, 200, body)
	var minted struct {
		Code       string `json:"code"`
		ExpiresIn  int    `json:"expires_in"`
		SessionTTL int    `json:"session_ttl"`
		App        struct{ Slug, Title string }
	}
	if err := json.Unmarshal(body, &minted); err != nil {
		t.Fatal(err)
	}
	if minted.Code == "" || minted.ExpiresIn != 60 || minted.SessionTTL != 3600 || minted.App.Slug != f.slug {
		t.Fatalf("mint 响应不对：%s", body)
	}

	// 先确认审计记了 mint
	logs, err := f.st.ListAudit(context.Background(), store.AuditFilter{Action: "embed.session_create"})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("mint 应该记 embed.session_create 审计")
	}

	// exchange：不带 cookie、不 Set-Cookie，换到 ee_emb_ limited 令牌
	resp, raw := f.exchange(t, minted.Code)
	if resp.StatusCode != 200 {
		t.Fatalf("exchange 失败：%d %s", resp.StatusCode, raw)
	}
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) != 0 {
		t.Fatalf("exchange 不能 Set-Cookie：%v", cookies)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("exchange 应 no-store，实际 %q", resp.Header.Get("Cache-Control"))
	}
	exchanged := decode[struct {
		Token   string `json:"token"`
		Type    string `json:"token_type"`
		Limited bool   `json:"limited"`
	}](t, raw)
	if !strings.HasPrefix(exchanged.Token, "ee_emb_") || exchanged.Type != "Bearer" || !exchanged.Limited {
		t.Fatalf("exchange 响应不对：%s", raw)
	}

	// code 一次性：同一个 code 再兑一次失败
	resp, raw = f.exchange(t, minted.Code)
	if resp.StatusCode != 400 || decode[struct{ Code string }](t, raw).Code != "embed_code_invalid" {
		t.Fatalf("code 应该一次性：%d %s", resp.StatusCode, raw)
	}
	resp, raw = f.exchange(t, "not-a-code")
	if resp.StatusCode != 400 || decode[struct{ Code string }](t, raw).Code != "embed_code_invalid" {
		t.Fatalf("无效 code 应 400 embed_code_invalid：%d %s", resp.StatusCode, raw)
	}

	// me：limited + embed 信息
	code, body = f.me(t, exchanged.Token)
	expect(t, "嵌入会话读 me", code, 200, body)
	var me struct {
		Limited bool `json:"limited"`
		Embed   *struct{ App, Host string }
	}
	_ = json.Unmarshal(body, &me)
	if !me.Limited || me.Embed == nil || me.Embed.App != f.slug || me.Embed.Host != embedHost {
		t.Fatalf("me 应该返回 limited + embed：%s", body)
	}

	// 路由白名单：页面需要的接口放行，其它一律 403 embed_scope
	c := newClient(t, f.ts.URL)
	for _, ok := range []struct{ method, path string }{
		{"GET", "/api/settings"},
		{"GET", "/api/me/menus"},
		{"GET", "/api/me/apps/" + f.slug + "/web"},
		{"GET", "/api/apps/" + f.slug + "/data"},
		{"GET", "/api/auth/methods"},
	} {
		code, body := c.do(ok.method, ok.path, nil, bearer(exchanged.Token))
		expect(t, "白名单内 "+ok.path, code, 200, body)
	}
	for _, blocked := range []struct{ method, path string }{
		{"GET", "/api/admin/users"},
		{"GET", "/api/me/tokens"},
		{"POST", "/api/me/tokens"},
		{"PUT", "/api/admin/settings"},
		{"GET", "/api/hub"},
	} {
		code, body := c.do(blocked.method, blocked.path, map[string]any{}, bearer(exchanged.Token))
		expect(t, "白名单外 "+blocked.path, code, 403, body)
		if got := decode[struct{ Code string }](t, body).Code; got != "embed_scope" {
			t.Fatalf("%s 的错误码 = %q", blocked.path, got)
		}
	}

	// logout：撤销会话，me 立刻 401
	if code, body := c.do("POST", "/api/auth/embed/logout", map[string]any{}, bearer(exchanged.Token)); code != 204 {
		t.Fatalf("logout 应 204：%d %s", code, body)
	}
	if code, _ := f.me(t, exchanged.Token); code != 401 {
		t.Fatalf("logout 后应 401：%d", code)
	}
	if logs, err := f.st.ListAudit(context.Background(), store.AuditFilter{Action: "embed.revoke"}); err != nil || len(logs) == 0 {
		t.Fatalf("logout 应该记 embed.revoke 审计：%v %d", err, len(logs))
	}
}

func TestEmbedCodeExpiry(t *testing.T) {
	f := newEmbedFixture(t, func(o *api.Options) { o.EmbedCodeTTL = 20 * time.Millisecond })
	emp := f.user(t, "embed-exp")
	f.token(t, emp.ID, "ee_at_embed_exp", []string{rbac.ScopeHub, rbac.ScopeAgent})
	code, body := f.mint(t, "ee_at_embed_exp", map[string]any{"app": f.slug, "host_origin": embedHost})
	expect(t, "mint", code, 200, body)
	minted := decode[struct{ Code string }](t, body)
	time.Sleep(50 * time.Millisecond)
	resp, raw := f.exchange(t, minted.Code)
	if resp.StatusCode != 400 || decode[struct{ Code string }](t, raw).Code != "embed_code_expired" {
		t.Fatalf("过期 code 应 400 embed_code_expired：%d %s", resp.StatusCode, raw)
	}
}

func TestEmbedSessionExpiry(t *testing.T) {
	f := newEmbedFixture(t, func(o *api.Options) { o.EmbedSessionTTL = 30 * time.Millisecond })
	emp := f.user(t, "embed-ttl")
	f.token(t, emp.ID, "ee_at_embed_ttl", []string{rbac.ScopeHub, rbac.ScopeAgent})
	tok := f.mintAndExchange(t, "ee_at_embed_ttl")
	if code, body := f.me(t, tok); code != 200 {
		t.Fatalf("会话应可用：%d %s", code, body)
	}
	time.Sleep(60 * time.Millisecond)
	if code, _ := f.me(t, tok); code != 401 {
		t.Fatalf("过期会话应 401：%d", code)
	}
}

func TestEmbedSessionInvalidatedByPasswordChange(t *testing.T) {
	f := newEmbedFixture(t, nil)
	emp := f.user(t, "embed-pw")
	f.token(t, emp.ID, "ee_at_embed_pw", []string{rbac.ScopeHub, rbac.ScopeAgent})
	tok := f.mintAndExchange(t, "ee_at_embed_pw")

	code, body := f.admin.do("PATCH", "/api/admin/users/"+itoa(emp.ID), map[string]any{"password": "new-employee-password-1"}, nil)
	expect(t, "管理员重置密码", code, 200, body)
	if code, _ := f.me(t, tok); code != 401 {
		t.Fatalf("改密后嵌入会话应失效：%d", code)
	}

	// 重新登录拿新 code 后禁用账号，同样失效
	f.token(t, emp.ID, "ee_at_embed_pw2", []string{rbac.ScopeHub, rbac.ScopeAgent})
	tok = f.mintAndExchange(t, "ee_at_embed_pw2")
	code, body = f.admin.do("PATCH", "/api/admin/users/"+itoa(emp.ID), map[string]any{"disabled": true}, nil)
	expect(t, "禁用账号", code, 200, body)
	if code, _ := f.me(t, tok); code != 401 {
		t.Fatalf("禁用后嵌入会话应失效：%d", code)
	}
}

func TestEmbedMintRequiresHumanUser(t *testing.T) {
	// 机器人令牌不能嵌入网页（与网页登录的现有约束一致）。
	f := newEmbedFixture(t, nil)
	code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
		"username": "embed-bot", "account_type": "bot", "owner_id": 1,
	}, nil)
	expect(t, "建机器人", code, 201, body)
	botUser := decode[store.User](t, body)
	f.token(t, botUser.ID, "ee_at_embed_bot", []string{rbac.ScopeHub, rbac.ScopeAgent})
	code, body = f.mint(t, "ee_at_embed_bot", map[string]any{"app": f.slug, "host_origin": embedHost})
	expect(t, "机器人不能 mint", code, 403, body)
}

// E5：错误体带 stage/request_id；embed.deny 审计带 app/host（不记 code/令牌明文）。
func TestEmbedErrorObservability(t *testing.T) {
	f := newEmbedFixture(t, nil)
	emp := f.user(t, "embed-obs")
	f.token(t, emp.ID, "ee_at_embed_obs", []string{rbac.ScopeHub, rbac.ScopeAgent})

	raw := func(path string, body map[string]any, bearerToken string) (*http.Response, []byte) {
		t.Helper()
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", f.ts.URL+path, strings.NewReader(string(b)))
		req.Header.Set("Content-Type", "application/json")
		if bearerToken != "" {
			req.Header.Set("Authorization", "Bearer "+bearerToken)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		out, _ := io.ReadAll(resp.Body)
		return resp, out
	}

	// mint：来源不在白名单 → stage=mint + request_id，且与 X-Request-Id 一致
	resp, body := raw("/api/embed/sessions", map[string]any{"app": f.slug, "host_origin": "https://evil.example.com"}, "ee_at_embed_obs")
	if resp.StatusCode != 403 {
		t.Fatalf("mint 应 403：%d %s", resp.StatusCode, body)
	}
	var mintErr struct {
		Code      string `json:"code"`
		Stage     string `json:"stage"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &mintErr); err != nil {
		t.Fatal(err)
	}
	if mintErr.Code != "origin_not_allowed" || mintErr.Stage != "mint" || mintErr.RequestID == "" {
		t.Fatalf("mint 错误体不对：%s", body)
	}
	if got := resp.Header.Get("X-Request-Id"); got != mintErr.RequestID {
		t.Fatalf("X-Request-Id=%q 与错误体 request_id=%q 不一致", got, mintErr.RequestID)
	}

	// exchange：无效 code → stage=exchange
	resp, body = raw("/api/auth/embed/exchange", map[string]any{"code": "nope"}, "")
	if resp.StatusCode != 400 {
		t.Fatalf("exchange 应 400：%d %s", resp.StatusCode, body)
	}
	var exErr struct {
		Code      string `json:"code"`
		Stage     string `json:"stage"`
		RequestID string `json:"request_id"`
	}
	if err := json.Unmarshal(body, &exErr); err != nil {
		t.Fatal(err)
	}
	if exErr.Code != "embed_code_invalid" || exErr.Stage != "exchange" || exErr.RequestID == "" {
		t.Fatalf("exchange 错误体不对：%s", body)
	}

	// embed.deny 审计：带 reason + app + host_origin
	logs, err := f.st.ListAudit(context.Background(), store.AuditFilter{Action: "embed.deny"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range logs {
		if l.Detail["reason"] == "origin_not_allowed" && l.Detail["app"] == f.slug && l.Detail["host_origin"] == "https://evil.example.com" {
			found = true
		}
	}
	if !found {
		t.Fatalf("embed.deny 审计应带 reason/app/host_origin：%+v", logs)
	}
}
