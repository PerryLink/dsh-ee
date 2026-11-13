package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// API 客户端（client_credentials，docs/token-lifecycle.md §6）：
// 管理端创建 / 列表 / 禁用；客户端用 client_id + client_secret 换 access token。
// 需要数据库（DSH_EE_TEST_DSN）。

type apiClientViewBody struct {
	ID              int64      `json:"id"`
	ClientID        string     `json:"client_id"`
	Name            string     `json:"name"`
	OwnerUserID     int64      `json:"owner_user_id"`
	OwnerUsername   string     `json:"owner_username"`
	OwnerDepartment string     `json:"owner_department"`
	Scopes          []string   `json:"scopes"`
	ClientKind      string     `json:"client_kind"`
	CreatedBy       int64      `json:"created_by"`
	DisabledAt      *time.Time `json:"disabled_at"`
	LastUsedAt      *time.Time `json:"last_used_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

type apiClientCreateBody struct {
	Client       apiClientViewBody `json:"client"`
	ClientSecret string            `json:"client_secret"`
}

type apiClientListBody struct {
	Clients []apiClientViewBody `json:"clients"`
}

type clientTokenBody struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int64    `json:"expires_in"`
	Scopes      []string `json:"scopes"`
	ClientID    string   `json:"client_id"`
	ClientName  string   `json:"client_name"`
	ClientKind  string   `json:"client_kind"`
	TokenID     string   `json:"token_id"`
}

func createAPIClient(t *testing.T, f *principalFixture, ownerID int64, name string, scopes []string) apiClientCreateBody {
	t.Helper()
	code, body := f.admin.do("POST", "/api/admin/clients", map[string]any{
		"name": name, "owner_user_id": ownerID, "scopes": scopes,
	}, nil)
	expect(t, "创建 API 客户端", code, 201, body)
	out := decodeAs[apiClientCreateBody](t, body)
	if out.Client.ClientID == "" || !strings.HasPrefix(out.Client.ClientID, "dshc_") {
		t.Fatalf("client_id 格式不对：%s", body)
	}
	if !strings.HasPrefix(out.ClientSecret, "dshs_") {
		t.Fatalf("client_secret 只在创建响应里返回，且应有 dshs_ 前缀：%s", body)
	}
	return out
}

func clientTokenRequest(t *testing.T, c *client, clientID, clientSecret string) (int, []byte) {
	t.Helper()
	return c.do("POST", "/api/auth/client-token", map[string]any{"client_id": clientID, "client_secret": clientSecret}, nil)
}

// 创建 / 列表（不回显 secret）/ 换 token / 核对归属与审计。
func TestAPIClientCreateAndClientToken(t *testing.T) {
	f := newPrincipalFixture(t)
	owner := f.user(t, "client-owner", nil)
	scopes := []string{rbac.ScopeModels, rbac.ScopeHub}
	created := createAPIClient(t, f, owner.ID, "ci-runner", scopes)

	// 列表带 owner 信息，且永远不回显 secret / 哈希
	code, body := f.admin.do("GET", "/api/admin/clients", nil, nil)
	expect(t, "列出 API 客户端", code, 200, body)
	list := decodeAs[apiClientListBody](t, body)
	if len(list.Clients) != 1 || list.Clients[0].ClientID != created.Client.ClientID {
		t.Fatalf("列表内容不对：%s", body)
	}
	if got := list.Clients[0]; got.OwnerUsername != owner.Username || got.Name != "ci-runner" ||
		got.OwnerUserID != owner.ID || got.ClientKind != store.ClientKindThirdParty || got.CreatedBy == 0 {
		t.Fatalf("列表字段不对：%+v", got)
	}
	if strings.Contains(string(body), created.ClientSecret) || strings.Contains(string(body), secret.Hash(created.ClientSecret)) {
		t.Fatalf("列表不能回显 secret 或哈希：%s", body)
	}

	// 公开的 client_credentials 换 token
	anon := newClient(t, f.ts.URL)
	code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "client_credentials 换 token", code, 200, body)
	token := decodeAs[clientTokenBody](t, body)
	if !strings.HasPrefix(token.AccessToken, "ee_at_") || token.TokenType != "Bearer" || token.ExpiresIn <= 0 {
		t.Fatalf("access token 形状不对：%s", body)
	}
	if token.ClientID != created.Client.ClientID || token.ClientName != "ci-runner" ||
		token.ClientKind != store.ClientKindThirdParty || token.TokenID == "" {
		t.Fatalf("换 token 响应字段不对：%s", body)
	}
	if len(token.Scopes) != 2 || token.Scopes[0] != rbac.ScopeModels || token.Scopes[1] != rbac.ScopeHub {
		t.Fatalf("签出的 scope 应等于客户端申请范围：%s", body)
	}

	// 换出来的令牌能用（hub → 能力目录），归属 owner 且 client_kind=third_party
	code, body = anon.do("GET", "/api/dsh/catalog", nil, bearer(token.AccessToken))
	expect(t, "client token 读能力目录", code, 200, body)
	code, body = anon.do("POST", "/api/auth/introspect", map[string]any{"token": token.AccessToken}, bearer(token.AccessToken))
	expect(t, "自省 client token", code, 200, body)
	info := decodeIntrospect(t, body)
	if !info.Active || info.ClientKind != store.ClientKindThirdParty || info.ClientName != "ci-runner" || info.User == nil || info.User.ID != owner.ID {
		t.Fatalf("client token 自省结果不对：%s", body)
	}

	// 最近使用时间被记录
	code, body = f.admin.do("GET", "/api/admin/clients", nil, nil)
	expect(t, "再列客户端", code, 200, body)
	if got := decodeAs[apiClientListBody](t, body).Clients[0]; got.LastUsedAt == nil {
		t.Fatalf("成功换 token 后应更新 last_used_at：%s", body)
	}

	// 审计：client.create + token.issue（via=client_credentials）
	code, body = f.admin.do("GET", "/api/admin/audit?action=client.create", nil, nil)
	expect(t, "查 client.create 审计", code, 200, body)
	if !strings.Contains(string(body), "client.create") || !strings.Contains(string(body), created.Client.ClientID) {
		t.Fatalf("缺 client.create 审计：%s", body)
	}
	code, body = f.admin.do("GET", "/api/admin/audit?action=token.issue&via=client_credentials", nil, nil)
	expect(t, "查 client_credentials 审计", code, 200, body)
	if !strings.Contains(string(body), `"via":"client_credentials"`) || !strings.Contains(string(body), created.Client.ClientID) {
		t.Fatalf("token.issue 审计应记录 client_credentials 与 client_id：%s", body)
	}
	if strings.Contains(string(body), created.ClientSecret) || strings.Contains(string(body), token.AccessToken) {
		t.Fatalf("审计里不能有明文 secret / access token：%s", body)
	}
}

// 错误凭据统一 invalid_client；禁用客户端 / 停用 owner 立即失效；DELETE 幂等。
func TestAPIClientInvalidCredentials(t *testing.T) {
	f := newPrincipalFixture(t)
	owner := f.user(t, "client-invalid-owner", nil)
	created := createAPIClient(t, f, owner.ID, "ci-invalid", []string{rbac.ScopeModels})
	anon := newClient(t, f.ts.URL)

	code, wrongSecret := clientTokenRequest(t, anon, created.Client.ClientID, "dshs_not-the-secret")
	expect(t, "错误的 client_secret", code, 401, wrongSecret)
	if !strings.Contains(string(wrongSecret), "invalid_client") {
		t.Fatalf("错误 secret 应回 invalid_client：%s", wrongSecret)
	}
	code, unknown := clientTokenRequest(t, anon, "dshc_does-not-exist", created.ClientSecret)
	expect(t, "未知 client_id", code, 401, unknown)
	// 两种失败对外不能可区分，避免探测 client_id 是否存在
	if !strings.Contains(string(unknown), "invalid_client") || string(unknown) != string(wrongSecret) {
		t.Fatalf("未知 client 与错误 secret 的响应应一致：\n%s\n%s", wrongSecret, unknown)
	}
	code, body := anon.do("POST", "/api/auth/client-token", map[string]any{"client_id": created.Client.ClientID}, nil)
	expect(t, "缺 client_secret", code, 401, body)
	if !strings.Contains(string(body), "invalid_client") {
		t.Fatalf("缺字段也应 invalid_client：%s", body)
	}

	// DELETE 禁用：之后换 token invalid_client；重复 DELETE 幂等
	code, body = f.admin.do("DELETE", "/api/admin/clients/"+created.Client.ClientID, nil, nil)
	expect(t, "禁用 API 客户端", code, 200, body)
	if got := decodeAs[struct {
		Client apiClientViewBody `json:"client"`
	}](t, body).Client; got.DisabledAt == nil {
		t.Fatalf("禁用后应返回 disabled_at：%s", body)
	}
	code, body = f.admin.do("DELETE", "/api/admin/clients/"+created.Client.ClientID, nil, nil)
	expect(t, "重复禁用幂等", code, 200, body)
	code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "禁用后换 token", code, 401, body)
	if !strings.Contains(string(body), "invalid_client") {
		t.Fatalf("禁用后应回 invalid_client：%s", body)
	}
	code, body = f.admin.do("DELETE", "/api/admin/clients/dshc_missing", nil, nil)
	expect(t, "禁用不存在的客户端", code, 404, body)

	// owner 停用后立即停止签发
	other := createAPIClient(t, f, owner.ID, "ci-owner-disabled", []string{rbac.ScopeModels})
	disabled := true
	if err := f.st.UpdateUser(context.Background(), owner.ID, store.UserPatch{Disabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	code, body = clientTokenRequest(t, anon, other.Client.ClientID, other.ClientSecret)
	expect(t, "owner 停用后换 token", code, 401, body)
	if !strings.Contains(string(body), "invalid_client") {
		t.Fatalf("owner 停用后应回 invalid_client：%s", body)
	}
}

// scope 受 owner 权限限制：创建时校验；owner 权限被收回后，换到的 token 取交集。
func TestAPIClientScopeRules(t *testing.T) {
	f := newPrincipalFixture(t)
	owner := f.user(t, "client-scope-owner", nil)
	ctx := context.Background()

	// 员工没有 fields.manage / roles.manage，config 不能给；admin 永远不能通过签发接口下发
	code, body := f.admin.do("POST", "/api/admin/clients", map[string]any{
		"name": "over-scope", "owner_user_id": owner.ID, "scopes": []string{rbac.ScopeModels, rbac.ScopeConfig},
	}, nil)
	expect(t, "创建时 scope 超权", code, 400, body)
	code, body = f.admin.do("POST", "/api/admin/clients", map[string]any{
		"name": "admin-scope", "owner_user_id": owner.ID, "scopes": []string{rbac.ScopeAdmin},
	}, nil)
	expect(t, "创建时 admin scope", code, 400, body)
	code, body = f.admin.do("GET", "/api/admin/clients", nil, nil)
	expect(t, "超权创建不应落库", code, 200, body)
	if n := len(decodeAs[apiClientListBody](t, body).Clients); n != 0 {
		t.Fatalf("超权请求不应创建客户端，实际 %d 条：%s", n, body)
	}

	created := createAPIClient(t, f, owner.ID, "ci-scope", []string{rbac.ScopeModels, rbac.ScopeHub})
	anon := newClient(t, f.ts.URL)

	// 改成一个只有 portal.view 的自建角色：models 被收回，只剩 hub
	limited := &store.Role{Code: "client-limited", Name: "受限", Permissions: []string{rbac.PortalView}}
	if err := f.st.CreateRole(ctx, limited); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserRoles(ctx, owner.ID, []int64{limited.ID}); err != nil {
		t.Fatal(err)
	}
	code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "权限收回后换 token", code, 200, body)
	if got := decodeAs[clientTokenBody](t, body).Scopes; len(got) != 1 || got[0] != rbac.ScopeHub {
		t.Fatalf("换到的 scope 应是 owner 当前权限的交集（只剩 hub）：%s", body)
	}

	// 权限再收成空：没有可签发的 scope，拒绝而不是签一枚空令牌
	none := &store.Role{Code: "client-none", Name: "无权限", Permissions: []string{}}
	if err := f.st.CreateRole(ctx, none); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserRoles(ctx, owner.ID, []int64{none.ID}); err != nil {
		t.Fatal(err)
	}
	code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "owner 无权限", code, 403, body)
	if !strings.Contains(string(body), "insufficient_scope") {
		t.Fatalf("owner 无权限时应回 insufficient_scope：%s", body)
	}
}

// 管理接口需要 keys.manage；client_credentials 按 IP 限流。
func TestAPIClientPermissionsAndRateLimit(t *testing.T) {
	f := newPrincipalFixtureWith(t, func(o *api.Options) { o.ClientTokenPerMinute = 2 })
	owner := f.user(t, "client-perm-owner", nil)
	created := createAPIClient(t, f, owner.ID, "ci-limits", []string{rbac.ScopeModels})

	// 员工网页会话不能管理 API 客户端
	employee := f.user(t, "client-employee", nil)
	emp := newClient(t, f.ts.URL)
	code, body := emp.do("POST", "/api/auth/login", map[string]string{"username": employee.Username, "password": "employee-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)
	code, body = emp.do("POST", "/api/admin/clients", map[string]any{"name": "nope", "owner_user_id": employee.ID}, nil)
	expect(t, "员工创建 API 客户端", code, 403, body)
	code, body = emp.do("GET", "/api/admin/clients", nil, nil)
	expect(t, "员工列出 API 客户端", code, 403, body)

	// 每 IP 每分钟 2 次：前两次成功后 429，带 Retry-After
	anon := newClient(t, f.ts.URL)
	for i := 0; i < 2; i++ {
		code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
		expect(t, "限流窗口内换 token", code, 200, body)
	}
	code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "超过每 IP 限流", code, 429, body)
	if !strings.Contains(string(body), "rate_limited") {
		t.Fatalf("限流响应应是 rate_limited：%s", body)
	}
}

// 用创建响应里的 secret 换到的 token 有固定 1h 有效期（过期后客户端自己再换，无需人工）。
func TestAPIClientTokenTTL(t *testing.T) {
	f := newPrincipalFixture(t)
	owner := f.user(t, "client-ttl-owner", nil)
	created := createAPIClient(t, f, owner.ID, "ci-ttl", []string{rbac.ScopeModels})
	anon := newClient(t, f.ts.URL)
	code, body := clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "换 token", code, 200, body)
	token := decodeAs[clientTokenBody](t, body)
	if token.ExpiresIn != int64(time.Hour.Seconds()) {
		t.Fatalf("access token 有效期应为 1h：%s", body)
	}
	// 令牌本身带 same 到期时间（自省可读）
	code, body = anon.do("POST", "/api/auth/introspect", map[string]any{"token": token.AccessToken}, bearer(token.AccessToken))
	expect(t, "自省换到的 token", code, 200, body)
	info := decodeIntrospect(t, body)
	if info.ExpiresAt == nil || time.Until(*info.ExpiresAt) > time.Hour+time.Minute || time.Until(*info.ExpiresAt) < 55*time.Minute {
		t.Fatalf("自省到期时间应是 1h 左右：%s", body)
	}
}

// owner 可以是机器人账号：管理端建 bot 并指派负责人，再由 bot 注册 API 客户端。
func TestAPIClientBotOwner(t *testing.T) {
	f := newPrincipalFixture(t)
	owner := f.user(t, "client-bot-owner", nil)
	code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
		"username": "client-ci-bot", "department": "研发效能部", "account_type": "bot", "owner_id": owner.ID,
	}, nil)
	expect(t, "创建机器人账号", code, 201, body)
	bot := decode[store.User](t, body)
	if bot.AccountType != store.AccountBot || bot.OwnerID == nil || *bot.OwnerID != owner.ID {
		t.Fatalf("机器人账号字段不对：%s", body)
	}

	created := createAPIClient(t, f, bot.ID, "ci-bot-client", []string{rbac.ScopeModels})
	anon := newClient(t, f.ts.URL)
	code, body = clientTokenRequest(t, anon, created.Client.ClientID, created.ClientSecret)
	expect(t, "机器人 owner 的客户端换 token", code, 200, body)
	token := decodeAs[clientTokenBody](t, body)
	code, body = anon.do("POST", "/api/auth/introspect", map[string]any{"token": token.AccessToken}, bearer(token.AccessToken))
	expect(t, "自省机器人 client token", code, 200, body)
	if info := decodeIntrospect(t, body); info.User == nil || info.User.ID != bot.ID || info.User.AccountType != store.AccountBot {
		t.Fatalf("client token 应签给机器人 owner：%s", body)
	}
}
