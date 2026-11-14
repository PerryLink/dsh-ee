package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// openTestStorePair 在一个随机库里打开两个独立连接池，模拟两个 dsh-ee 实例共用同一个数据库。
func openTestStorePair(t *testing.T) (*store.Store, *store.Store) {
	t.Helper()
	base := os.Getenv("DSH_EE_TEST_DSN")
	if base == "" {
		t.Skip("未设置 DSH_EE_TEST_DSN，跳过数据库集成测试")
	}
	ctx := context.Background()
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	dbName := "dsh_ee_test_eph_" + hex.EncodeToString(suffix)
	dsn := strings.Replace(base, ")/", ")/"+dbName, 1)
	stA, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open A: %v", err)
	}
	t.Cleanup(func() {
		_, _ = stA.DB().ExecContext(context.Background(), "DROP DATABASE `"+dbName+"`")
		stA.Close()
	})
	if err := stA.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := stA.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	stB, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open B: %v", err)
	}
	t.Cleanup(func() { stB.Close() })
	return stA, stB
}

// ephemeralFixture 是两个服务端实例共享同一个数据库的测试环境（多实例部署的最小模拟）。
type ephemeralFixture struct {
	stA, stB *store.Store
	box      *secret.Box
	opts     api.Options
	srvA     *api.Server
	srvB     *api.Server
	ta, tb   *httptest.Server
	admin    *client
}

func newEphemeralFixture(t *testing.T) *ephemeralFixture {
	t.Helper()
	stA, stB := openTestStorePair(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, stA, "admin", "admin-password-123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	opts := api.Options{DataDir: t.TempDir()}
	f := &ephemeralFixture{stA: stA, stB: stB, box: box, opts: opts}
	f.srvA = api.New(stA, box, opts, quiet)
	f.srvB = api.New(stB, box, opts, quiet)
	f.ta = httptest.NewServer(f.srvA.Handler())
	f.tb = httptest.NewServer(f.srvB.Handler())
	t.Cleanup(f.ta.Close)
	t.Cleanup(f.tb.Close)
	f.admin = newClient(t, f.ta.URL)
	code, body := f.admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	return f
}

// user 建一个员工并返回；密码是 employee-password-1。
func (f *ephemeralFixture) user(t *testing.T, username string) store.User {
	t.Helper()
	code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
		"username": username, "password": "employee-password-1", "department": "研发效能部",
	}, nil)
	expect(t, "建员工 "+username, code, 201, body)
	return decode[store.User](t, body)
}

// token 直接往库里写一个 ee_at_ 令牌（明文用 plain）。
func (f *ephemeralFixture) token(t *testing.T, userID int64, plain string, scopes []string) {
	t.Helper()
	tok := &store.AccessToken{
		UserID: userID, Name: plain, Prefix: plain, Source: "manual", Scopes: scopes,
		ClientName: "test-client", CreatedBy: 1,
	}
	if err := f.stA.CreateAccessToken(context.Background(), tok, secret.Hash(plain)); err != nil {
		t.Fatal(err)
	}
}

// login 在 A 实例登录员工，返回带 cookie 的客户端（cookie 按主机共享，B 实例也认这个会话）。
func (f *ephemeralFixture) login(t *testing.T, username, password string) *client {
	t.Helper()
	c := newClient(t, f.ta.URL)
	code, body := c.do("POST", "/api/auth/login", map[string]string{"username": username, "password": password}, nil)
	expect(t, "员工登录", code, 200, body)
	return c
}

// at 复制一份客户端，把 base 换到另一个实例（共用 cookie jar）。
func (c *client) at(base string) *client {
	return &client{t: c.t, base: base, hc: c.hc}
}

// noFollow 复制一个不自动跟随跳转的客户端。
func noFollow(c *client) *client {
	hc := *c.hc
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client{t: c.t, base: c.base, hc: &hc}
}

func (c *client) open(t *testing.T, raw string) *http.Response {
	t.Helper()
	resp, err := c.hc.Get(raw)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

// 从 DSH 打开网页免登录的一次性 code：A 实例生成，B 实例兑换成功；重放被拒；库里只存 sha256。
func TestWebLinkSharedAcrossInstances(t *testing.T) {
	f := newEphemeralFixture(t)
	emp := f.user(t, "handoff-multi")
	f.token(t, emp.ID, "ee_at_handoff_multi", []string{rbac.ScopeHub, rbac.ScopeConnections, rbac.ScopeAgent})
	dsh := newClient(t, f.ta.URL)

	code, body := dsh.do("POST", "/api/dsh/web-link", map[string]string{"next": "/knowledge"}, bearer("ee_at_handoff_multi"))
	expect(t, "A 实例生成网页登录链接", code, 200, body)
	link := decode[struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}](t, body)
	if !strings.Contains(link.URL, "/api/auth/handoff?code=") || link.ExpiresIn != 60 {
		t.Fatalf("链接不对：%s", body)
	}
	parsed, err := url.Parse(link.URL)
	if err != nil {
		t.Fatal(err)
	}
	plainCode := parsed.Query().Get("code")

	// 库里只存 sha256，不存明文 code；刚生成时还未消费。
	var (
		stateHash string
		payload   []byte
		consumed  int
	)
	if err := f.stA.DB().QueryRowContext(context.Background(),
		`SELECT state_hash, payload, consumed_at IS NOT NULL FROM ephemeral_states WHERE kind = 'web_link'`).
		Scan(&stateHash, &payload, &consumed); err != nil {
		t.Fatalf("读 ephemeral_states：%v", err)
	}
	if stateHash != secret.Hash(plainCode) || stateHash == plainCode {
		t.Fatalf("库里应该只存 code 的 sha256：%q", stateHash)
	}
	if consumed != 0 {
		t.Fatal("刚生成的一次性 code 不应已消费")
	}
	var bound struct {
		UserID int64  `json:"user_id"`
		Next   string `json:"next"`
	}
	if err := json.Unmarshal(payload, &bound); err != nil || bound.UserID != emp.ID || bound.Next != "/knowledge" {
		t.Fatalf("code 绑定的内容不对：%s (%v)", payload, err)
	}

	// A 实例生成、B 实例打开：换成 limited 网页会话并跳到目标页。
	browser := noFollow(newClient(t, f.tb.URL))
	resp := browser.open(t, link.URL)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/knowledge" {
		t.Fatalf("B 实例 handoff 应跳到目标页：%d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("handoff 应禁止 Referer")
	}
	code, body = browser.at(f.tb.URL).do("GET", "/api/auth/me", nil, nil)
	expect(t, "B 实例的 handoff 会话读 me", code, 200, body)
	me := decode[struct {
		Username string
		Limited  bool
	}](t, body)
	if me.Username != emp.Username || !me.Limited {
		t.Fatalf("handoff 会话不对：%s", body)
	}

	// 一次性：两个实例都不能再兑同一个 code。
	for name, base := range map[string]string{"A": f.ta.URL, "B": f.tb.URL} {
		resp = noFollow(newClient(t, base)).open(t, link.URL)
		loc := resp.Header.Get("Location")
		if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(loc, "/login?handoff=expired") {
			t.Fatalf("%s 实例重放应回到登录页：%d %q", name, resp.StatusCode, loc)
		}
	}
	// consumed_at 已落库。
	if err := f.stB.DB().QueryRowContext(context.Background(),
		`SELECT consumed_at IS NOT NULL FROM ephemeral_states WHERE kind = 'web_link' AND state_hash = ?`,
		secret.Hash(plainCode)).Scan(&consumed); err != nil || consumed != 1 {
		t.Fatalf("consumed_at 应该落库：%d %v", consumed, err)
	}
}

// 开始使用接入码：A 实例生成、B 实例发起设备登录并轮询；换码后旧码在另一个实例立即失效。
func TestSetupCodeSharedAcrossInstances(t *testing.T) {
	f := newEphemeralFixture(t)
	emp := f.user(t, "starter-multi")
	empA := f.login(t, emp.Username, "employee-password-1")
	empB := empA.at(f.tb.URL)

	// 先生成接入码需要管理员已发布接入插件。
	code, body := f.admin.do("POST", "/api/admin/connect/publish", map[string]any{"force": true}, nil)
	expect(t, "发布接入插件", code, 200, body)

	code, body = empA.do("POST", "/api/me/get-started", map[string]any{}, nil)
	expect(t, "A 实例生成接入码", code, 200, body)
	issued := decode[struct {
		Code string `json:"code"`
	}](t, body)
	if len(issued.Code) != 9 {
		t.Fatalf("接入码格式不对：%s", body)
	}
	normalized := store.NormalizeUserCode(issued.Code)

	// 库里只存接入码的 sha256，payload 不含明文接入码。
	var stateHash string
	var payload []byte
	if err := f.stA.DB().QueryRowContext(context.Background(),
		`SELECT state_hash, payload FROM ephemeral_states WHERE kind = 'setup_code' AND owner_key = ?`,
		"user:"+itoa(emp.ID)).Scan(&stateHash, &payload); err != nil {
		t.Fatalf("读接入码状态：%v", err)
	}
	if stateHash != secret.Hash(normalized) || stateHash == normalized {
		t.Fatalf("库里应该只存接入码的 sha256：%q", stateHash)
	}
	if bytes.Contains(payload, []byte(issued.Code)) || bytes.Contains(payload, []byte(normalized)) {
		t.Fatalf("payload 不应含明文接入码：%s", payload)
	}

	// B 实例轮询：应该能看到这个接入码（跨实例共享）。
	code, body = empB.do("GET", "/api/me/get-started/"+issued.Code, nil, nil)
	expect(t, "B 实例读取接入进度", code, 200, body)
	if stage := decode[struct {
		Stage string `json:"stage"`
	}](t, body).Stage; stage != "waiting" {
		t.Fatalf("还没登录时应是 waiting：%s", body)
	}

	// B 实例用接入码发起设备登录；A 实例轮询到 pending 与设备信息（attach 跨实例生效）。
	anonymous := newClient(t, f.tb.URL)
	code, body = anonymous.do("POST", "/api/device/code", map[string]any{"client_name": "MacBook", "setup_code": issued.Code}, nil)
	expect(t, "B 实例用接入码发起登录", code, 200, body)
	dev := decode[struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}](t, body)
	code, body = empA.do("GET", "/api/me/get-started/"+issued.Code, nil, nil)
	expect(t, "A 实例读取接入进度", code, 200, body)
	pending := decode[struct {
		Stage      string `json:"stage"`
		UserCode   string `json:"user_code"`
		ClientName string `json:"client_name"`
	}](t, body)
	if pending.Stage != "pending" || pending.UserCode != dev.UserCode || pending.ClientName != "MacBook" {
		t.Fatalf("A 实例应看到 B 实例发起的设备登录：%s", body)
	}

	// A 实例网页确认，B 实例轮询到 approved。
	code, body = empA.do("POST", "/api/device/"+dev.UserCode, map[string]any{"approve": true}, nil)
	expect(t, "网页确认授权", code, 200, body)
	code, body = empB.do("GET", "/api/me/get-started/"+issued.Code, nil, nil)
	expect(t, "B 实例读取确认后进度", code, 200, body)
	if stage := decode[struct {
		Stage string `json:"stage"`
	}](t, body).Stage; stage != "approved" {
		t.Fatalf("确认后应是 approved：%s", body)
	}

	// 同一员工换新接入码：旧码在另一个实例立即失效（按属主替换）。
	code, body = empB.do("POST", "/api/me/get-started", map[string]any{}, nil)
	expect(t, "B 实例重新生成接入码", code, 200, body)
	again := decode[struct {
		Code string `json:"code"`
	}](t, body)
	code, body = empA.do("GET", "/api/me/get-started/"+issued.Code, nil, nil)
	expect(t, "A 实例读旧接入码", code, 404, body)
	code, body = empA.do("GET", "/api/me/get-started/"+again.Code, nil, nil)
	expect(t, "A 实例读新接入码", code, 200, body)
	var rows int
	if err := f.stA.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM ephemeral_states WHERE kind = 'setup_code' AND owner_key = ?`,
		"user:"+itoa(emp.ID)).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("同一员工只能有一个有效接入码：%d %v", rows, err)
	}
}

// OIDC state/PKCE：A 实例发起登录、B 实例收回调成功；state 只用一次；payload 里没有明文
// nonce/verifier（用主密钥加密）；state 过期/已消费后回调回到登录页。
func TestOIDCStateSharedAcrossInstances(t *testing.T) {
	f := newEphemeralFixture(t)
	idp := newFakeIdP(t)
	// 让两个实例都通过假 IdP 的客户端访问 IdP。
	f.opts.OIDCClient = idp.Client()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.srvA = api.New(f.stA, f.box, f.opts, quiet)
	f.srvB = api.New(f.stB, f.box, f.opts, quiet)
	f.ta.Config.Handler = f.srvA.Handler()
	f.tb.Config.Handler = f.srvB.Handler()

	adminA := f.login(t, "admin", "admin-password-123")
	cfg := map[string]any{
		"enabled": true, "preset": "keycloak", "name": "多实例 SSO", "issuer": idp.URL,
		"clientId": "dsh-ee", "clientSecret": "idp-secret", "scopes": "profile email",
		"usernameClaim": "preferred_username", "nameClaim": "name", "emailClaim": "email",
		"departmentClaim": "department", "groupsClaim": "groups", "autoCreate": true,
	}
	code, body := adminA.do("PUT", "/api/admin/auth/oidc", cfg, nil)
	expect(t, "保存 OIDC 配置", code, 200, body)

	// start 在 A 实例发起登录，返回带 state cookie 的客户端、state/nonce/PKCE challenge。
	start := func(t *testing.T) (*client, string, string, string) {
		t.Helper()
		c := noFollow(newClient(t, f.ta.URL))
		resp := c.open(t, f.ta.URL+"/api/auth/oidc/start?next=/build")
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("start 应跳到 IdP：%d", resp.StatusCode)
		}
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		q := loc.Query()
		state, nonce, challenge := q.Get("state"), q.Get("nonce"), q.Get("code_challenge")
		if state == "" || nonce == "" || challenge == "" {
			t.Fatalf("授权请求参数不全：state=%q nonce=%q challenge=%q", state, nonce, challenge)
		}
		return c, state, nonce, challenge
	}
	// grant 模拟 IdP：登记这次授权码要签发的声明与 PKCE challenge。
	grant := func(nonce, challenge, sub, username string) string {
		claims := map[string]any{
			"iss": idp.URL, "aud": "dsh-ee", "exp": time.Now().Add(5 * time.Minute).Unix(),
			"iat": time.Now().Unix(), "nonce": nonce,
			"sub": sub, "preferred_username": username, "name": "多实例员工",
		}
		grantCode := secret.Token()
		idp.mu.Lock()
		idp.codes[grantCode] = fakeGrant{claims: claims, challenge: challenge}
		idp.mu.Unlock()
		return grantCode
	}
	// callback 用带 state cookie 的客户端在指定实例上收回调，返回跳转地址。
	callback := func(c *client, base, state, grantCode string) string {
		resp := c.open(t, base+"/api/auth/oidc/callback?"+url.Values{"code": {grantCode}, "state": {state}}.Encode())
		return resp.Header.Get("Location")
	}
	// callbackWithCookie 给没有 cookie jar 的场景手工带上 state cookie（重放/过期用例）。
	callbackWithCookie := func(t *testing.T, base, state, grantCode string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest("GET", base+"/api/auth/oidc/callback?"+
			url.Values{"code": {grantCode}, "state": {state}}.Encode(), nil)
		req.AddCookie(&http.Cookie{Name: "dsh_ee_oidc", Value: state, Path: "/api/auth/oidc"})
		hc := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// ---- A 实例发起、B 实例收回调：跨实例登录成功 ----
	c, state, nonce, challenge := start(t)

	// 库里 state 只存 sha256；payload 里只有加密后的密文，没有明文 nonce/PKCE verifier/next。
	var stateHash string
	var payload []byte
	if err := f.stA.DB().QueryRowContext(context.Background(),
		`SELECT state_hash, payload FROM ephemeral_states WHERE kind = 'oidc_state' AND state_hash = ?`,
		secret.Hash(state)).Scan(&stateHash, &payload); err != nil {
		t.Fatalf("读 OIDC state：%v", err)
	}
	if stateHash != secret.Hash(state) || stateHash == state {
		t.Fatalf("库里应该只存 state 的 sha256：%q", stateHash)
	}
	var sealed map[string]any
	if err := json.Unmarshal(payload, &sealed); err != nil {
		t.Fatalf("payload 不是 JSON：%s", payload)
	}
	if len(sealed) != 2 || sealed["enc"] == nil {
		t.Fatalf("payload 应该只有版本与密文：%s", payload)
	}
	if bytes.Contains(payload, []byte(nonce)) || bytes.Contains(payload, []byte("verifier")) || bytes.Contains(payload, []byte("/build")) {
		t.Fatalf("payload 不应含明文 nonce/verifier/next：%s", payload)
	}

	grantA := grant(nonce, challenge, "u-multi", "multi.user")
	if loc := callback(c, f.tb.URL, state, grantA); loc != "/build" {
		t.Fatalf("B 实例回调应登录成功并回到 next：%q", loc)
	}
	code, body = c.at(f.tb.URL).do("GET", "/api/auth/me", nil, nil)
	expect(t, "B 实例上登录后的 me", code, 200, body)
	if got := decode[struct {
		Username string
	}](t, body).Username; got != "multi.user" {
		t.Fatalf("登录用户不对：%s", body)
	}

	// ---- state 一次性：已消费的 state 再回调回登录页 ----
	replay := callbackWithCookie(t, f.tb.URL, state, grantA)
	if loc := replay.Header.Get("Location"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("已消费的 state 再回调应回到登录页：%d %q", replay.StatusCode, loc)
	}

	// ---- 过期：把 state 的过期时间改到过去，回调同样回登录页 ----
	_, state2, nonce2, challenge2 := start(t)
	grant2 := grant(nonce2, challenge2, "u-multi-2", "multi.user2")
	if _, err := f.stA.DB().ExecContext(context.Background(),
		"UPDATE ephemeral_states SET expires_at = ? WHERE kind = 'oidc_state' AND state_hash = ?",
		time.Now().UTC().Add(-time.Second), secret.Hash(state2)); err != nil {
		t.Fatal(err)
	}
	expired := callbackWithCookie(t, f.tb.URL, state2, grant2)
	if loc := expired.Header.Get("Location"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("过期 state 的回调应回到登录页：%d %q", expired.StatusCode, loc)
	}
	if code, _ := c.at(f.tb.URL).do("GET", "/api/auth/me", nil, nil); code != 200 {
		t.Fatal("过期 state 不应影响已有会话")
	}
}

// 数据库不可用时一次性状态功能必须直接失败（不留进程内副本、不假装成功）。
func TestEphemeralStateDBUnavailableFailsClosed(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("POST", "/api/admin/users", map[string]any{
		"username": "db-down", "password": "employee-password-1", "department": "研发效能部",
	}, nil)
	expect(t, "建员工", code, 201, body)
	emp := decode[store.User](t, body)
	empToken := &store.AccessToken{
		UserID: emp.ID, Name: "db-down", Prefix: "db-down", Source: "manual",
		Scopes: []string{rbac.ScopeHub, rbac.ScopeConnections, rbac.ScopeAgent}, ClientName: "test-client", CreatedBy: 1,
	}
	if err := st.CreateAccessToken(ctx, empToken, secret.Hash("ee_at_db_down")); err != nil {
		t.Fatal(err)
	}

	// 数据库正常时能拿到链接。
	code, body = newClient(t, ts.URL).do("POST", "/api/dsh/web-link", map[string]string{"next": "/knowledge"}, bearer("ee_at_db_down"))
	expect(t, "数据库正常时生成链接", code, 200, body)
	link := decode[struct {
		URL string `json:"url"`
	}](t, body)

	// 关掉数据库连接：已生成的一次性 code 也不能凭进程内副本继续兑换，必须直接失败。
	// （迁移前 code 在进程内，这一步会成功；现在数据库是唯一事实源，fail closed。）
	st.Close()
	browser := noFollow(newClient(t, ts.URL))
	resp := browser.open(t, link.URL)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("数据库不可用时 handoff 应直接 500，得到 %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// 新的 handoff code 同样生成不了。
	code, body = newClient(t, ts.URL).do("POST", "/api/dsh/web-link", map[string]string{"next": "/knowledge"}, bearer("ee_at_db_down"))
	if code == 200 {
		t.Fatalf("数据库不可用时不应生成新的网页登录链接：%s", body)
	}
}
