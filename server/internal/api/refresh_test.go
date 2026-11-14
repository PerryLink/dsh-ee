package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 刷新令牌（docs/token-lifecycle.md §3）的集成测试。需要数据库：
//
//	DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4000)/' go test ./internal/api -run TestRefreshToken -count=1

var refreshScopes = []string{"models", "registry:read", "hub", "connections"}

func enableRefresh(o *api.Options) {
	o.RefreshTokens = true
	o.RefreshAccessTTL = 30 * time.Minute
	o.RefreshTokenTTL = 2 * time.Hour
	o.RefreshFamilyTTL = 4 * time.Hour
}

type refreshIssue struct {
	Token            string `json:"token"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	Info             struct {
		ID         string `json:"id"`
		ClientKind string `json:"client_kind"`
	} `json:"info"`
}

type refreshPair struct {
	AccessToken      string   `json:"access_token"`
	TokenType        string   `json:"token_type"`
	ExpiresIn        int64    `json:"expires_in"`
	RefreshToken     string   `json:"refresh_token"`
	RefreshExpiresIn int64    `json:"refresh_expires_in"`
	Scopes           []string `json:"scopes"`
	ClientKind       string   `json:"client_kind"`
}

func loginClient(t *testing.T, f *principalFixture, username string) *client {
	t.Helper()
	c := newClient(t, f.ts.URL)
	code, body := c.do("POST", "/api/auth/login", map[string]string{"username": username, "password": "employee-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)
	return c
}

// issueRefreshableToken 用员工的网页会话签一枚手工令牌（默认带 refresh）。
func issueRefreshableToken(t *testing.T, f *principalFixture, username, name string, extra map[string]any) (refreshIssue, *client) {
	t.Helper()
	u := f.user(t, username, nil)
	c := loginClient(t, f, u.Username)
	req := map[string]any{"name": name, "scopes": refreshScopes}
	for k, v := range extra {
		req[k] = v
	}
	code, body := c.do("POST", "/api/me/tokens", req, nil)
	expect(t, "创建令牌", code, 201, body)
	return decode[refreshIssue](t, body), c
}

func refreshCall(c *client, refreshToken, clientKind string) (int, []byte) {
	req := map[string]any{"refresh_token": refreshToken}
	if clientKind != "" {
		req["client_kind"] = clientKind
	}
	return c.do("POST", "/api/auth/refresh", req, nil)
}

func TestRefreshTokenDisabled(t *testing.T) {
	f := newPrincipalFixture(t) // 默认：DSH_EE_REFRESH_TOKENS 关闭
	issued, employee := issueRefreshableToken(t, f, "refresh-off", "off-token", nil)
	if issued.RefreshToken != "" || issued.RefreshExpiresIn != 0 {
		t.Fatalf("关闭刷新令牌时不应返回 refresh token：%+v", issued)
	}
	anon := newClient(t, f.ts.URL)
	code, body := refreshCall(anon, "ee_rt_whatever", "")
	expect(t, "关闭时刷新端点 404", code, 404, body)
	if !bytes.Contains(body, []byte("刷新令牌未启用")) {
		t.Fatalf("关闭时的错误信息不对：%s", body)
	}
	// 关闭时设备登录同样不带 refresh token（沿用旧响应形状）
	device := newClient(t, f.ts.URL)
	code, body = device.do("POST", "/api/device/code", map[string]any{"client_name": "MacBook-off"}, nil)
	expect(t, "发起设备码", code, 200, body)
	dc := decodeAs[struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}](t, body)
	code, body = employee.do("GET", "/api/device/"+dc.UserCode, nil, nil)
	expect(t, "设备信息", code, 200, body)
	code, body = employee.do("POST", "/api/device/"+dc.UserCode, map[string]any{"approve": true}, nil)
	expect(t, "批准设备", code, 200, body)
	code, body = device.do("POST", "/api/device/token", map[string]string{"device_code": dc.DeviceCode}, nil)
	expect(t, "领取设备令牌", code, 200, body)
	if bytes.Contains(body, []byte("refresh_token")) {
		t.Fatalf("关闭刷新令牌时设备登录不应返回 refresh token：%s", body)
	}
}

func TestRefreshTokenRotationAndReuse(t *testing.T) {
	f := newPrincipalFixtureWith(t, enableRefresh)
	issued, _ := issueRefreshableToken(t, f, "refresh-rotate", "rotate-token", nil)
	if !strings.HasPrefix(issued.RefreshToken, "ee_rt_") || issued.RefreshExpiresIn <= 0 {
		t.Fatalf("开启后应返回 refresh token：%+v", issued)
	}
	anon := newClient(t, f.ts.URL)

	code, body := refreshCall(anon, issued.RefreshToken, "third_party")
	expect(t, "轮换刷新令牌", code, 200, body)
	pair := decode[refreshPair](t, body)
	if !strings.HasPrefix(pair.AccessToken, "ee_at_") || !strings.HasPrefix(pair.RefreshToken, "ee_rt_") {
		t.Fatalf("轮换返回的令牌格式不对：%s", body)
	}
	if pair.AccessToken == issued.Token || pair.RefreshToken == issued.RefreshToken {
		t.Fatalf("轮换必须换出全新的 access / refresh：%s", body)
	}
	if pair.TokenType != "Bearer" || pair.ExpiresIn <= 0 || pair.ExpiresIn > int64((30*time.Minute).Seconds())+5 ||
		pair.RefreshExpiresIn <= 0 || pair.ClientKind != store.ClientKindThirdParty {
		t.Fatalf("轮换响应字段不对：%s", body)
	}
	if len(pair.Scopes) != len(refreshScopes) {
		t.Fatalf("scope 应保持不变：%s", body)
	}
	// 响应里永远不能出现哈希/明文之外的敏感字段
	for _, forbidden := range []string{"secret_hash", "token_hash", "reused_refresh_token_id"} {
		if bytes.Contains(body, []byte(forbidden)) {
			t.Fatalf("刷新响应不应包含 %q：%s", forbidden, body)
		}
	}
	// 新 access 可用；旧 access 在轮换后仍然有效（等它自然过期或被复用检测吊销）
	code, body = anon.do("GET", "/v1/models", nil, bearer(pair.AccessToken))
	expect(t, "新 access token 可用", code, 200, body)
	code, body = anon.do("GET", "/v1/models", nil, bearer(issued.Token))
	expect(t, "轮换不吊销旧 access token", code, 200, body)
	// refresh token 不能当 Bearer 用
	code, body = anon.do("GET", "/v1/models", nil, bearer(pair.RefreshToken))
	expect(t, "refresh token 不能当 access token 用", code, 401, body)

	// 未知 refresh token 也是统一的 401 invalid_grant
	code, body = refreshCall(anon, "ee_rt_"+secret.Token(), "")
	expect(t, "未知 refresh token", code, 401, body)
	if !bytes.Contains(body, []byte("invalid_grant")) {
		t.Fatalf("未知令牌的错误码应为 invalid_grant：%s", body)
	}

	// 旧 refresh 复用：吊销整族
	code, body = refreshCall(anon, issued.RefreshToken, "")
	expect(t, "旧 refresh 复用", code, 401, body)
	if !bytes.Contains(body, []byte("invalid_grant")) {
		t.Fatalf("复用错误码应为 invalid_grant：%s", body)
	}
	code, body = anon.do("GET", "/v1/models", nil, bearer(pair.AccessToken))
	expect(t, "复用后新 access 也被吊销", code, 401, body)
	if !bytes.Contains(body, []byte("token_revoked")) {
		t.Fatalf("复用后应返回 token_revoked：%s", body)
	}
	code, body = anon.do("GET", "/v1/models", nil, bearer(issued.Token))
	expect(t, "复用后旧 access 也被吊销", code, 401, body)
	code, body = refreshCall(anon, pair.RefreshToken, "")
	expect(t, "复用后新 refresh 也失效", code, 401, body)

	// 审计：token.refresh 与 token.refresh_reuse 各一条；detail 里不能出现明文/哈希
	ctx := context.Background()
	refreshes, err := f.st.ListAudit(ctx, store.AuditFilter{Action: "token.refresh", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	reuses, err := f.st.ListAudit(ctx, store.AuditFilter{Action: "token.refresh_reuse", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshes) != 1 || len(reuses) != 1 {
		t.Fatalf("审计条数不对：refresh=%d reuse=%d", len(refreshes), len(reuses))
	}
	if reuses[0].ActorName != "refresh-rotate" || reuses[0].Via != "token" {
		t.Fatalf("复用审计归属不对：%+v", reuses[0])
	}
	for _, log := range []*store.AuditLog{refreshes[0], reuses[0]} {
		detail, _ := json.Marshal(log.Detail)
		for _, forbidden := range []string{issued.RefreshToken, pair.RefreshToken, issued.Token, pair.AccessToken, secret.Hash(issued.RefreshToken), secret.Hash(pair.RefreshToken)} {
			if strings.Contains(string(detail), forbidden) {
				t.Fatalf("审计 detail 不能包含令牌明文/哈希：%s", detail)
			}
		}
	}
}

func TestRefreshTokenConcurrent(t *testing.T) {
	f := newPrincipalFixtureWith(t, enableRefresh) // 严格轮换：并发只有一次成功
	issued, _ := issueRefreshableToken(t, f, "refresh-race", "race-token", nil)
	payload, _ := json.Marshal(map[string]string{"refresh_token": issued.RefreshToken})

	do := func() (int, []byte) {
		resp, err := http.Post(f.ts.URL+"/api/auth/refresh", "application/json", bytes.NewReader(payload))
		if err != nil {
			return 0, []byte(err.Error())
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	start := make(chan struct{})
	results := make([][2]any, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			code, body := do()
			results[i] = [2]any{code, body}
		}(i)
	}
	close(start)
	wg.Wait()

	var ok, failed int
	var winner refreshPair
	for _, r := range results {
		switch r[0].(int) {
		case 200:
			ok++
			winner = decode[refreshPair](t, r[1].([]byte))
		case 401:
			failed++
			if !bytes.Contains(r[1].([]byte), []byte("invalid_grant")) {
				t.Fatalf("并发落败方应返回 invalid_grant：%s", r[1])
			}
		default:
			t.Fatalf("并发刷新出现意外状态码 %v：%s", r[0], r[1])
		}
	}
	if ok != 1 || failed != 1 {
		t.Fatalf("并发轮换只能成功一次：ok=%d failed=%d", ok, failed)
	}
	// 严格语义下第二个请求按复用处理，整族被吊销：赢家的 refresh 也失效
	anon := newClient(t, f.ts.URL)
	code, body := refreshCall(anon, winner.RefreshToken, "")
	expect(t, "并发复用后整族吊销", code, 401, body)
}

func TestRefreshTokenGraceReplay(t *testing.T) {
	// 宽限期：客户端没收到轮换响应时的幂等重试原样返回上次结果，不吊销整族。
	f := newPrincipalFixtureWith(t, func(o *api.Options) {
		enableRefresh(o)
		o.RefreshGrace = 90 * time.Second
	})
	issued, _ := issueRefreshableToken(t, f, "refresh-grace", "grace-token", nil)
	anon := newClient(t, f.ts.URL)
	code, first := refreshCall(anon, issued.RefreshToken, "")
	expect(t, "首次轮换", code, 200, first)
	code, second := refreshCall(anon, issued.RefreshToken, "")
	expect(t, "宽限期内重试", code, 200, second)
	if !bytes.Equal(first, second) {
		t.Fatalf("宽限期内重试应原样返回上次响应：\nfirst=%s\nsecond=%s", first, second)
	}
	// 只记了一条轮换审计（重放不是一次新的轮换）
	logs, err := f.st.ListAudit(context.Background(), store.AuditFilter{Action: "token.refresh", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("重放不应额外记轮换审计：%d", len(logs))
	}
}

func TestRefreshTokenErrorsAndIdentity(t *testing.T) {
	f := newPrincipalFixtureWith(t, enableRefresh)
	anon := newClient(t, f.ts.URL)

	// 1) 过期：直接改库把 refresh 的到期时间放到过去
	expired, _ := issueRefreshableToken(t, f, "refresh-expired", "expired-token", nil)
	if _, err := f.st.DB().ExecContext(context.Background(),
		"UPDATE token_refresh_tokens SET expires_at = ? WHERE secret_hash = ?",
		time.Now().UTC().Add(-time.Hour), secret.Hash(expired.RefreshToken)); err != nil {
		t.Fatal(err)
	}
	code, body := refreshCall(anon, expired.RefreshToken, "")
	expect(t, "过期 refresh", code, 401, body)
	if !bytes.Contains(body, []byte("invalid_grant")) {
		t.Fatalf("过期 refresh 应返回 invalid_grant：%s", body)
	}

	// 2) client_kind 与令牌族不一致：400，不轮换
	thirdParty, _ := issueRefreshableToken(t, f, "refresh-kind", "kind-token", nil)
	code, body = refreshCall(anon, thirdParty.RefreshToken, "dsh")
	expect(t, "client_kind 不一致", code, 400, body)
	code, body = refreshCall(anon, thirdParty.RefreshToken, "third_party")
	expect(t, "client_kind 一致后正常轮换", code, 200, body)

	// 3) 吊销 access token 会连带吊销整族 refresh，不能用 refresh 绕过吊销
	revoked, c := issueRefreshableToken(t, f, "refresh-revoke", "revoke-token", nil)
	code, body = c.do("DELETE", "/api/me/tokens/"+revoked.Info.ID, nil, nil)
	expect(t, "吊销 access token", code, 204, body)
	code, body = refreshCall(anon, revoked.RefreshToken, "")
	expect(t, "吊销后 refresh 失效", code, 401, body)

	// 4) 账号停用后 refresh 立即失效
	disabled, _ := issueRefreshableToken(t, f, "refresh-disabled", "disabled-token", nil)
	users, err := f.st.ListUsers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var disabledUser *store.User
	for _, u := range users {
		if u.Username == "refresh-disabled" {
			disabledUser = u
		}
	}
	if disabledUser == nil {
		t.Fatal("找不到测试用户")
	}
	code, body = f.admin.do("PATCH", "/api/admin/users/"+itoa(disabledUser.ID), map[string]any{"disabled": true}, nil)
	expect(t, "停用账号", code, 200, body)
	code, body = refreshCall(anon, disabled.RefreshToken, "")
	expect(t, "停用账号后 refresh 失效", code, 401, body)

	// 5) 跨用户隔离：A 的 refresh 换出来仍是 A；A 的族被复用吊销不影响 B
	alice, _ := issueRefreshableToken(t, f, "refresh-alice", "alice-token", nil)
	bob, _ := issueRefreshableToken(t, f, "refresh-bob", "bob-token", nil)
	code, body = refreshCall(anon, alice.RefreshToken, "")
	expect(t, "A 轮换", code, 200, body)
	alicePair := decode[refreshPair](t, body)
	code, body = anon.do("GET", "/api/dsh/whoami", nil, bearer(alicePair.AccessToken))
	expect(t, "A 的新令牌 whoami", code, 200, body)
	if got := decodeAs[struct {
		Username string `json:"username"`
	}](t, body).Username; got != "refresh-alice" {
		t.Fatalf("A 的 refresh 应换出 A 的身份，实际 %q", got)
	}
	// A 的旧 refresh 复用 → A 的族吊销；B 不受影响
	code, body = refreshCall(anon, alice.RefreshToken, "")
	expect(t, "A 复用", code, 401, body)
	code, body = refreshCall(anon, bob.RefreshToken, "")
	expect(t, "B 的 refresh 不受 A 的族吊销影响", code, 200, body)

	// 6) 设备登录：开启 refresh 后，设备令牌响应里带可用的 refresh token
	devUser := f.user(t, "refresh-device", nil)
	devClient := loginClient(t, f, devUser.Username)
	device := newClient(t, f.ts.URL)
	code, body = device.do("POST", "/api/device/code", map[string]any{"client_name": "MacBook-refresh"}, nil)
	expect(t, "发起设备码", code, 200, body)
	dc := decodeAs[struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}](t, body)
	code, body = devClient.do("GET", "/api/device/"+dc.UserCode, nil, nil)
	expect(t, "设备信息", code, 200, body)
	code, body = devClient.do("POST", "/api/device/"+dc.UserCode, map[string]any{"approve": true}, nil)
	expect(t, "批准设备", code, 200, body)
	code, body = device.do("POST", "/api/device/token", map[string]string{"device_code": dc.DeviceCode}, nil)
	expect(t, "领取设备令牌", code, 200, body)
	granted := decodeAs[struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		RefreshExpiresIn int64  `json:"refresh_expires_in"`
	}](t, body)
	if !strings.HasPrefix(granted.RefreshToken, "ee_rt_") || granted.RefreshExpiresIn <= 0 {
		t.Fatalf("设备登录应返回 refresh token：%s", body)
	}
	code, body = refreshCall(anon, granted.RefreshToken, "dsh")
	expect(t, "设备 refresh 轮换", code, 200, body)
	devicePair := decode[refreshPair](t, body)
	if devicePair.ClientKind != store.ClientKindDSH {
		t.Fatalf("设备 refresh 应保持 dsh 客户端类型：%s", body)
	}
	code, body = anon.do("GET", "/v1/models", nil, bearer(devicePair.AccessToken))
	expect(t, "设备 refresh 换出的 access 可用", code, 200, body)
}
