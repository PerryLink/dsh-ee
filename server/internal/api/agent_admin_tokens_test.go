package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 审计 C1：ee_admin_tokens.revoke 的预览绕过了执行侧的部门作用域。
// 只有 tokens.dept_manage 的部门令牌管理员开启管理模式后，就能让 Agent 预览到
// 其它部门令牌的用户名 / 令牌名 / 前缀（执行才 403，但元数据已进模型上下文），
// 过滤分支还能探出其它部门令牌的数量。预览必须与 tokens.go 的 handleRevokeTokens
// 用同一套规则：
//   - keys.manage 可看全员；否则要求 tokens.dept_manage 且归属某部门，只能看本部门机器人账号的令牌；
//   - ids 逐个校验持有者的部门与账号类型；过滤分支强制本部门 + 仅机器人。
func TestAgentTokenRevokePreviewDepartmentScope(t *testing.T) {
	f := newPrincipalFixture(t)
	limit := int64(100000)

	// 研发部的部门令牌管理员：只有 tokens.dept_manage，没有 keys.manage。
	code, body := f.admin.do("POST", "/api/admin/roles", map[string]any{
		"code": "dept-token-preview", "name": "部门令牌管理员", "permissions": []string{rbac.TokensDeptManage},
	}, nil)
	expect(t, "建部门角色", code, 201, body)
	role := decode[store.Role](t, body)
	var memberID int64
	code, body = f.admin.do("GET", "/api/admin/roles", nil, nil)
	expect(t, "角色列表", code, 200, body)
	for _, r := range decodeAs[struct{ Roles []store.Role }](t, body).Roles {
		if r.Code == rbac.RoleMember {
			memberID = r.ID
		}
	}
	mgr := f.user(t, "preview-dept-mgr", &limit) // 研发效能部
	code, body = f.admin.do("PUT", "/api/admin/users/"+itoa(mgr.ID)+"/roles", map[string]any{"role_ids": []int64{memberID, role.ID}}, nil)
	expect(t, "分配部门角色", code, 200, body)
	mc := newClient(t, f.ts.URL)
	code, body = mc.do("POST", "/api/auth/login", map[string]string{"username": mgr.Username, "password": "employee-password-1"}, nil)
	expect(t, "部门管理员登录", code, 200, body)
	mt := f.token(t, mgr.ID, "ee_at_preview_mgr", agentScopes, nil, nil)
	code, body = f.elevate(t, mc, mt.ID, "employee-password-1", 1)
	expect(t, "部门管理员开启管理模式", code, 200, body)
	a := agent{dshClient{t: t, base: f.ts.URL, token: "ee_at_preview_mgr"}}

	bot := func(username, department string) store.User {
		t.Helper()
		code, body := f.admin.do("POST", "/api/admin/users", map[string]any{
			"username": username, "department": department, "account_type": "bot", "owner_id": mgr.ID,
		}, nil)
		expect(t, "创建机器人 "+username, code, 201, body)
		return decode[store.User](t, body)
	}
	salesBot := bot("sales-bot", "销售部")
	salesTok := f.token(t, salesBot.ID, "ee_at_sales_secret", agentScopes, nil, nil)
	rdBot := bot("rd-bot", "研发效能部")
	rdTok := f.token(t, rdBot.ID, "ee_at_rd_bot", agentScopes, nil, nil)
	// 本部门真人：执行侧也要求机器人账号，预览同样不能看。
	peer := f.user(t, "preview-dept-peer", &limit)
	peerTok := f.token(t, peer.ID, "ee_at_peer_human", agentScopes, nil, nil)

	assertNoSalesMeta := func(what, text string) {
		t.Helper()
		for _, leak := range []string{"sales-bot", "ee_at_sales_secret"} {
			if strings.Contains(text, leak) {
				t.Errorf("%s 泄露了其它部门的令牌元数据（%s）：%s", what, leak, text)
			}
		}
	}

	// 1) 按 id 预览其它部门的机器人令牌：修前会回出「sales-bot 的「…」」。
	ids := map[string]any{"action": "revoke", "ids": []string{"at:" + itoa(salesTok.ID)}}
	if text, isErr := a.call("ee_admin_tokens", ids); !isErr {
		t.Errorf("按 id 预览其它部门的令牌应被拒绝，却拿到预览：%s", text)
		assertNoSalesMeta("按 id 预览", text)
	}

	// 2) 按账号 / 部门过滤：修前会回出销售部令牌数量（存在性与规模泄露）。
	for _, c := range []struct {
		what string
		args map[string]any
	}{
		{"按账号预览其它部门", map[string]any{"action": "revoke", "user_id": float64(salesBot.ID)}},
		{"按部门预览其它部门", map[string]any{"action": "revoke", "department": "销售部"}},
	} {
		if text, isErr := a.call("ee_admin_tokens", c.args); !isErr {
			t.Errorf("%s 应被拒绝，却拿到预览：%s", c.what, text)
			assertNoSalesMeta(c.what, text)
		}
	}

	// 3) 按 id 预览本部门真人令牌也要拒绝（执行侧只允许机器人账号）。
	ids = map[string]any{"action": "revoke", "ids": []string{"at:" + itoa(peerTok.ID)}}
	if text, isErr := a.call("ee_admin_tokens", ids); !isErr {
		t.Errorf("按 id 预览本部门真人的令牌应被拒绝，却拿到预览：%s", text)
	}

	// 4) 只有其它管理权限（users.manage，没有令牌管理权限）的人开启管理模式后，
	// 这个 action 能进到处理函数（执行才 403），预览同样不能先把元数据读出来。
	code, body = f.admin.do("POST", "/api/admin/roles", map[string]any{
		"code": "preview-user-mgr", "name": "用户管理员", "permissions": []string{rbac.UsersManage},
	}, nil)
	expect(t, "建用户管理员角色", code, 201, body)
	userRole := decode[store.Role](t, body)
	um := f.user(t, "preview-user-mgr", &limit)
	code, body = f.admin.do("PUT", "/api/admin/users/"+itoa(um.ID)+"/roles", map[string]any{"role_ids": []int64{memberID, userRole.ID}}, nil)
	expect(t, "分配用户管理员角色", code, 200, body)
	uc := newClient(t, f.ts.URL)
	code, body = uc.do("POST", "/api/auth/login", map[string]string{"username": um.Username, "password": "employee-password-1"}, nil)
	expect(t, "用户管理员登录", code, 200, body)
	ut := f.token(t, um.ID, "ee_at_preview_usermgr", agentScopes, nil, nil)
	code, body = f.elevate(t, uc, ut.ID, "employee-password-1", 1)
	expect(t, "用户管理员开启管理模式", code, 200, body)
	ua := agent{dshClient{t: t, base: f.ts.URL, token: "ee_at_preview_usermgr"}}
	salesArgs := map[string]any{"action": "revoke", "ids": []string{"at:" + itoa(salesTok.ID)}}
	if text, isErr := ua.call("ee_admin_tokens", salesArgs); !isErr {
		t.Errorf("没有令牌管理权限的人应拿不到预览，却拿到：%s", text)
		assertNoSalesMeta("无令牌权限的预览", text)
	}

	// 执行侧仍然 403：预览不是唯一防线，两边行为一致。
	code, body = mc.do("POST", "/api/admin/tokens/revoke", map[string]any{"ids": []string{"at:" + itoa(salesTok.ID)}}, nil)
	expect(t, "执行侧拒绝跨部门吊销", code, 403, body)

	// 回归 1：本部门机器人令牌的预览仍然可见（写明持有者与令牌名）。
	res := a.ok("ee_admin_tokens", map[string]any{"action": "revoke", "ids": []string{"at:" + itoa(rdTok.ID)}})
	preview := fmt.Sprint(res["preview"])
	if res["confirm_required"] != true || !strings.Contains(preview, "rd-bot") || !strings.Contains(preview, "ee_at_rd_bot") {
		t.Fatalf("本部门机器人令牌的预览应正常：%v", res)
	}
	// 回归 2：本部门过滤条件照旧能预览，且只统计本部门机器人（人类令牌不算）。
	res = a.ok("ee_admin_tokens", map[string]any{"action": "revoke", "department": "研发效能部"})
	if preview = fmt.Sprint(res["preview"]); !strings.Contains(preview, "1 个") {
		t.Fatalf("按本部门预览应只统计本部门机器人令牌（1 个）：%s", preview)
	}
	res = a.ok("ee_admin_tokens", map[string]any{"action": "revoke", "user_id": float64(rdBot.ID)})
	if preview = fmt.Sprint(res["preview"]); !strings.Contains(preview, "1 个") {
		t.Fatalf("按本部门机器人账号预览应正常：%s", preview)
	}

	// 回归 3：keys.manage 的管理员预览不受影响（本来就能吊销任何人的令牌）。
	code, body = f.admin.do("GET", "/api/auth/me", nil, nil)
	expect(t, "管理员自己", code, 200, body)
	super := decode[store.User](t, body)
	st := f.token(t, super.ID, "ee_at_preview_admin", agentScopes, nil, nil)
	code, body = f.elevate(t, f.admin, st.ID, adminPassword, 1)
	expect(t, "管理员开启管理模式", code, 200, body)
	sa := agent{dshClient{t: t, base: f.ts.URL, token: "ee_at_preview_admin"}}
	text, isErr := sa.call("ee_admin_tokens", map[string]any{"action": "revoke", "ids": []string{"at:" + itoa(salesTok.ID)}})
	if isErr || !strings.Contains(text, "sales-bot") || !strings.Contains(text, "ee_at_sales_secret") {
		t.Fatalf("keys.manage 管理员的预览应照旧显示跨部门令牌：isErr=%v %s", isErr, text)
	}
}
