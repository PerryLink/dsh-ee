package api_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 程序化签发（docs/token-lifecycle.md §6）：管理员用带 keys.manage 的 Bearer 令牌
// 调用 POST /api/admin/tokens，为指定员工 / 机器人签发 ee_at_，不需要网页会话。
// 需要数据库（DSH_EE_TEST_DSN）。

type programmaticIssue struct {
	Token            string `json:"token"`
	RefreshToken     string `json:"refresh_token"`
	RefreshExpiresIn int64  `json:"refresh_expires_in"`
	Info             struct {
		ID           string     `json:"id"`
		UserID       int64      `json:"user_id"`
		Source       string     `json:"source"`
		ClientKind   string     `json:"client_kind"`
		ClientName   string     `json:"client_name"`
		ExpiresAt    *time.Time `json:"expires_at"`
		MonthlyToken *int64     `json:"monthly_token_limit"`
	} `json:"info"`
}

func TestProgrammaticTokenIssue(t *testing.T) {
	// 同时开启 refresh：验证通过 Bearer 签发的令牌也能正常拿 refresh token
	f := newPrincipalFixtureWith(t, enableRefresh)
	ctx := context.Background()

	_, meBody := f.admin.do("GET", "/api/auth/me", nil, nil)
	admin := decode[store.User](t, meBody)

	// 管理员令牌：先建一枚普通令牌，再开启管理模式（等价网页上「开启管理模式」）。
	// 只有管理模式下的令牌才带 keys.manage 管理权限（docs/token-lifecycle.md §2）。
	adminToken := f.token(t, admin.ID, "ee_at_prog_admin", refreshScopes, nil, nil)
	if err := f.st.ElevateToken(ctx, adminToken.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	adminBearer := bearer("ee_at_prog_admin")

	// 目标：员工负责人 + 机器人账号
	owner := f.user(t, "ci-owner", nil)
	code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
		"username": "ci-bot-programmatic", "department": "研发效能部", "account_type": "bot", "owner_id": owner.ID,
	}, nil)
	expect(t, "创建机器人", code, 201, body)
	bot := decode[store.User](t, body)

	anonymous := newClient(t, f.ts.URL)
	code, body = anonymous.do("POST", "/api/admin/tokens", map[string]any{
		"user_id": bot.ID, "name": "CI bot token", "scopes": refreshScopes,
		"client_kind": "third_party", "client_name": "ci-runner", "expires_in_days": 7,
	}, adminBearer)
	expect(t, "Bearer 签发令牌", code, 201, body)
	issued := decode[programmaticIssue](t, body)
	if !strings.HasPrefix(issued.Token, "ee_at_") || issued.Info.ID == "" {
		t.Fatalf("Bearer 签发的令牌格式不对：%s", body)
	}
	if issued.Info.UserID != bot.ID || issued.Info.Source != "issued" || issued.Info.ClientKind != store.ClientKindThirdParty ||
		issued.Info.ClientName != "ci-runner" || issued.Info.ExpiresAt == nil {
		t.Fatalf("Bearer 签发的令牌属性不对：%s", body)
	}
	if !strings.HasPrefix(issued.RefreshToken, "ee_rt_") || issued.RefreshExpiresIn <= 0 {
		t.Fatalf("开启 refresh 时 Bearer 签发也应带 refresh token：%s", body)
	}
	// 签出来的令牌本身可用
	code, body = anonymous.do("GET", "/api/dsh/catalog", nil, bearer(issued.Token))
	expect(t, "Bearer 签发的令牌可读目录", code, 200, body)
	// 且 refresh 流程没有被破坏
	code, body = anonymous.do("POST", "/api/auth/refresh", map[string]any{
		"refresh_token": issued.RefreshToken, "client_kind": "third_party",
	}, nil)
	expect(t, "Bearer 签发的 refresh 可轮换", code, 200, body)

	// 审计 token.issue：归属管理员、via=token、带上调用方令牌 id，detail 有目标用户
	logs, err := f.st.ListAudit(ctx, store.AuditFilter{Action: "token.issue", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var audit *store.AuditLog
	for _, log := range logs {
		if log.Target == issued.Token[:12] {
			audit = log
		}
	}
	if audit == nil {
		t.Fatalf("没有找到 token.issue 审计：%+v", logs)
	}
	if audit.Via != "token" || audit.TokenID == nil || *audit.TokenID != adminToken.ID || audit.ActorName != admin.Username {
		t.Fatalf("程序化签发的审计归属不对：%+v", audit)
	}
	if got, _ := audit.Detail["user_id"].(float64); int64(got) != bot.ID {
		t.Fatalf("审计 detail.user_id 不对：%+v", audit.Detail)
	}

	// 负例：普通员工的 Bearer（没有管理模式 → 没有 keys.manage）不能签发
	employee := f.user(t, "prog-employee", nil)
	f.token(t, employee.ID, "ee_at_prog_employee", refreshScopes, nil, nil)
	code, body = anonymous.do("POST", "/api/admin/tokens", map[string]any{
		"user_id": bot.ID, "name": "should fail", "scopes": refreshScopes,
	}, bearer("ee_at_prog_employee"))
	expect(t, "普通令牌不能签发", code, 403, body)

	// 负例：管理模式已过期的令牌同样不能签发（admin 范围即时失效）
	expiredToken := f.token(t, admin.ID, "ee_at_prog_expired", refreshScopes, nil, nil)
	if err := f.st.ElevateToken(ctx, expiredToken.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	code, body = anonymous.do("POST", "/api/admin/tokens", map[string]any{
		"user_id": bot.ID, "name": "expired admin", "scopes": refreshScopes,
	}, bearer("ee_at_prog_expired"))
	expect(t, "管理模式过期后不能签发", code, 403, body)

	// 负例：无效 Bearer → 401；目标账号不存在 → 400
	code, body = anonymous.do("POST", "/api/admin/tokens", map[string]any{
		"user_id": bot.ID, "name": "bad token", "scopes": refreshScopes,
	}, bearer("ee_at_prog_missing"))
	expect(t, "无效 Bearer", code, 401, body)
	code, body = anonymous.do("POST", "/api/admin/tokens", map[string]any{
		"user_id": 99999, "name": "no user", "scopes": refreshScopes,
	}, adminBearer)
	expect(t, "目标账号不存在", code, 400, body)
}
