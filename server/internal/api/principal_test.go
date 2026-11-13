package api_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

type principalFixture struct {
	st    *store.Store
	srv   *api.Server
	ts    *httptest.Server
	admin *client
}

func newPrincipalFixture(t *testing.T) *principalFixture {
	return newPrincipalFixtureWith(t, nil)
}

// newPrincipalFixtureWith 允许测试调整 api.Options（例如旧令牌 scope 收敛开关）。
func newPrincipalFixtureWith(t *testing.T, mutate func(*api.Options)) *principalFixture {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	var sawUsage atomic.Bool
	up := fakeUpstream(t, &sawUsage)
	t.Cleanup(up.Close)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	opts := api.Options{DataDir: t.TempDir(), Upstream: up.Client()}
	if mutate != nil {
		mutate(&opts)
	}
	srv := api.New(st, box, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	f := &principalFixture{st: st, srv: srv, ts: ts, admin: newClient(t, ts.URL)}
	code, body := f.admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = f.admin.do("POST", "/api/admin/providers", map[string]any{
		"name": "principal", "base_url": up.URL, "api_key": upstreamSecret, "models": []string{"chat"},
	}, nil)
	expect(t, "创建模型上游", code, 201, body)
	return f
}

func (f *principalFixture) user(t *testing.T, username string, limit *int64) store.User {
	t.Helper()
	code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
		"username": username, "password": "employee-password-1", "department": "研发效能部",
	}, nil)
	expect(t, "创建员工 "+username, code, 201, body)
	u := decode[store.User](t, body)
	code, body = f.admin.do("PATCH", "/api/admin/users/"+itoa(u.ID)+"/quota", map[string]any{"monthly_token_limit": limit}, nil)
	expect(t, "设置账号额度", code, 200, body)
	return u
}

func (f *principalFixture) token(t *testing.T, userID int64, plain string, scopes []string, limit *int64, models []string) *store.AccessToken {
	t.Helper()
	token := &store.AccessToken{
		UserID: userID, Name: plain, Prefix: plain, Source: "manual", Scopes: scopes,
		MonthlyTokenLimit: limit, AllowedModels: models, ClientName: "test-client", CreatedBy: userID,
	}
	if err := f.st.CreateAccessToken(context.Background(), token, secret.Hash(plain)); err != nil {
		t.Fatal(err)
	}
	return token
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

func TestPrincipalScopesAndWhoami(t *testing.T) {
	f := newPrincipalFixture(t)
	u := f.user(t, "principal-user", nil)
	allScopes := []string{rbac.ScopeModels, rbac.ScopeRegistryRead, rbac.ScopeRegistryPublish, rbac.ScopeHub, rbac.ScopeConnections}
	f.token(t, u.ID, "ee_at_full", allScopes, nil, nil)
	c := newClient(t, f.ts.URL)
	code, body := c.do("GET", "/v1/models", nil, bearer("ee_at_full"))
	expect(t, "新令牌模型列表", code, 200, body)
	code, body = c.do("GET", "/api/dsh/catalog", nil, bearer("ee_at_full"))
	expect(t, "新令牌能力目录", code, 200, body)
	code, body = c.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, map[string]string{
		"Authorization": "Bearer ee_at_full", "Accept": "application/json, text/event-stream",
	})
	expect(t, "新令牌 MCP", code, 200, body)
	pkg := pluginPkg("@company/principal-token", "1.0.0", nil)
	tgz := makeTgz(t, pkg, bundleFile)
	code, body = c.do("PUT", "/npm/@company%2Fprincipal-token", publishBody("@company/principal-token", "1.0.0", tgz, pkg, ""), bearer("ee_at_full"))
	expect(t, "新令牌发布包", code, 201, body)
	code, body = f.admin.do("POST", "/api/admin/review", map[string]any{"name": "@company/principal-token", "version": "1.0.0", "action": "approve"}, nil)
	expect(t, "上架新包", code, 200, body)
	code, body = c.do("GET", "/npm/@company%2Fprincipal-token", nil, bearer("ee_at_full"))
	expect(t, "新令牌读取包", code, 200, body)
	f.token(t, u.ID, "ee_at_read_only", []string{rbac.ScopeModels, rbac.ScopeRegistryRead}, nil, nil)
	code, body = c.do("PUT", "/npm/@company%2Fread-only", publishBody("@company/read-only", "1.0.0", tgz, pkg, ""), bearer("ee_at_read_only"))
	expect(t, "只读 scope 不能发布", code, 403, body)
	code, body = c.do("GET", "/api/dsh/whoami", nil, bearer("ee_at_full"))
	expect(t, "whoami", code, 200, body)
	var me struct {
		Username string   `json:"username"`
		Scopes   []string `json:"scopes"`
		Prefix   string   `json:"token_prefix"`
		Client   string   `json:"client_name"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatal(err)
	}
	if me.Username != u.Username || me.Prefix != "ee_at_full" || me.Client != "test-client" || len(me.Scopes) != len(allScopes) {
		t.Fatalf("whoami 内容不对：%s", body)
	}

	f.token(t, u.ID, "ee_at_no_models", []string{rbac.ScopeHub}, nil, nil)
	code, body = c.do("GET", "/v1/models", nil, bearer("ee_at_no_models"))
	expect(t, "没有 models scope", code, 403, body)
	f.token(t, u.ID, "ee_at_no_hub", []string{rbac.ScopeModels, rbac.ScopeConnections}, nil, nil)
	code, body = c.do("GET", "/api/dsh/catalog", nil, bearer("ee_at_no_hub"))
	expect(t, "没有 hub scope", code, 403, body)
	f.token(t, u.ID, "ee_at_no_connections", []string{rbac.ScopeModels, rbac.ScopeHub}, nil, nil)
	code, body = c.do("POST", "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}, bearer("ee_at_no_connections"))
	expect(t, "没有 connections scope", code, 403, body)

	revoked := f.token(t, u.ID, "ee_at_revoked", allScopes, nil, nil)
	if err := f.st.RevokeToken(context.Background(), revoked.ID); err != nil {
		t.Fatal(err)
	}
	code, body = c.do("GET", "/v1/models", nil, bearer("ee_at_revoked"))
	expect(t, "吊销令牌", code, 401, body)
	if !strings.Contains(string(body), "token_revoked") {
		t.Fatalf("吊销错误码不对：%s", body)
	}
	past := time.Now().Add(-time.Hour)
	expired := f.token(t, u.ID, "ee_at_expired", allScopes, nil, nil)
	expired.ExpiresAt = &past
	if _, err := f.st.DB().ExecContext(context.Background(), "UPDATE access_tokens SET expires_at = ? WHERE id = ?", past, expired.ID); err != nil {
		t.Fatal(err)
	}
	code, body = c.do("GET", "/v1/models", nil, bearer("ee_at_expired"))
	expect(t, "过期令牌", code, 401, body)
	if !strings.Contains(string(body), "token_expired") {
		t.Fatalf("过期错误码不对：%s", body)
	}
	code, body = c.do("GET", "/v1/models", nil, bearer("ee_at_missing"))
	expect(t, "无效令牌", code, 401, body)
	if !strings.Contains(string(body), "token_invalid") {
		t.Fatalf("无效错误码不对：%s", body)
	}
}

func TestPrincipalTokenAndAccountQuota(t *testing.T) {
	f := newPrincipalFixture(t)
	scopes := []string{rbac.ScopeModels, rbac.ScopeHub, rbac.ScopeConnections}
	chat := func(token string) (int, []byte) {
		c := newClient(t, f.ts.URL)
		return c.do("POST", "/v1/chat/completions", map[string]any{"model": "chat", "messages": []map[string]string{{"role": "user", "content": "hi"}}}, bearer(token))
	}

	tokenUser := f.user(t, "token-limited", nil)
	limited := f.token(t, tokenUser.ID, "ee_at_limited", scopes, new(int64), nil)
	*limited.MonthlyTokenLimit = 10
	if _, err := f.st.DB().ExecContext(context.Background(), "UPDATE access_tokens SET monthly_token_limit = 10 WHERE id = ?", limited.ID); err != nil {
		t.Fatal(err)
	}
	code, body := chat("ee_at_limited")
	expect(t, "令牌额度内首次调用", code, 200, body)
	code, body = chat("ee_at_limited")
	expect(t, "令牌额度先用完", code, 429, body)
	used, err := f.st.TokenMonthUsage(context.Background(), limited.ID)
	if err != nil || used != 15 {
		t.Fatalf("令牌月度用量应为 15：%d %v", used, err)
	}

	accountUser := f.user(t, "account-limited", new(int64))
	code, body = f.admin.do("PATCH", "/api/admin/users/"+itoa(accountUser.ID)+"/quota", map[string]any{"monthly_token_limit": 10}, nil)
	expect(t, "账号额度设为 10", code, 200, body)
	f.token(t, accountUser.ID, "ee_at_account_limited", scopes, nil, nil)
	code, body = chat("ee_at_account_limited")
	expect(t, "账号额度内首次调用", code, 200, body)
	code, body = chat("ee_at_account_limited")
	expect(t, "账号额度先用完", code, 429, body)

	noAllowance := f.user(t, "no-allowance", new(int64))
	f.token(t, noAllowance.ID, "ee_at_no_allowance", scopes, nil, nil)
	code, body = chat("ee_at_no_allowance")
	expect(t, "账号未分配额度", code, 429, body)
}
