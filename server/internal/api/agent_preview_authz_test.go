package api_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 管理模式的 Preview 在 handler 细化授权之前执行，所以它自己也要做同一套可见性判断，
// 否则只读的预览本身就成了越权读取的通道：
//
//  1. ee_settings.put：不看 settings.manage 也能在 diff 里看到 embed 段的当前值；
//  2. ee_hub_admin.patch：portal.view 就能按自增 id 枚举任意能力的标题；
//  3. ee_connections_admin.tool：不看 roles.manage 也能在预览里看到角色名。
//
// 每一步先断言「修前会泄露」（红），再断言「修后拒绝或只回编号」（绿）。
//
//	DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4000)/' go test ./internal/api -run TestAgentPreviewAuthz -count=1 -v
func TestAgentPreviewAuthz(t *testing.T) {
	f := newPrincipalFixture(t)
	// 超级管理员开好管理模式的 Agent，用来验证「有权限的人照常能看到」。
	super := previewSuperAgent(t, f)

	t.Run("设置预览按段落权限", func(t *testing.T) {
		// 管理员把 embed 段写成只有 settings.manage 才看得到的样子
		code, body := f.admin.do("PUT", "/api/admin/settings", map[string]any{
			"embed": map[string]any{"allowedOrigins": []string{"https://embed.secret.example"}},
		}, nil)
		expect(t, "设置嵌入来源", code, 200, body)

		reg, regWeb := previewAdminAgent(t, f, "preview-registry-admin", "ee_at_prv_reg",
			previewRole(t, f, "preview-registry", "预览源管理员", rbac.RegistryManage, rbac.PortalView))
		// 前提：没有 settings.manage 时正常读设置会被清空 embed 段，这正是预览不该泄露的值
		code, body = regWeb.do("GET", "/api/settings", nil, nil)
		expect(t, "registry 管理员读设置", code, 200, body)
		if strings.Contains(string(body), "embed.secret.example") {
			t.Fatalf("前提不成立：没有 settings.manage 不应读到 embed 当前值：%s", body)
		}

		// ① 修前：预览的 diff 会打印 embed.allowedOrigins 的当前值（红）
		//    修后：预览直接报段落权限错误，且不带当前值（绿）
		text, isErr := reg.call("ee_settings", map[string]any{"action": "put", "changes": map[string]any{
			"embed": map[string]any{"allowedOrigins": []string{"https://attacker.example"}},
		}})
		if !isErr {
			t.Fatalf("没有 settings.manage 的人预览 embed 段应在预览阶段被拒绝，实际返回：%s", text)
		}
		if !strings.Contains(text, "没有权限修改") {
			t.Fatalf("预览应报段落权限错误：%s", text)
		}
		if strings.Contains(text, "embed.secret.example") {
			t.Fatalf("预览泄露了 embed 段的当前值：%s", text)
		}
		// 自己有权改的段照常预览（不要过度拦截）
		text, isErr = reg.call("ee_settings", map[string]any{"action": "put", "changes": map[string]any{
			"registry": map[string]any{"proxyMode": "preloaded"},
		}})
		if isErr {
			t.Fatalf("registry.manage 预览 registry 段应成功：%s", text)
		}
		if !strings.Contains(text, "registry.proxyMode") {
			t.Fatalf("预览应列出 registry 改动：%s", text)
		}
	})

	t.Run("能力预览不泄露看不到的标题", func(t *testing.T) {
		// 一位员工提交、还在待审核的能力：标题 / slug 只有负责人或 plugins.manage 看得到
		limit := int64(100000)
		author := f.user(t, "preview-asset-author", &limit)
		authorWeb := newClient(t, f.ts.URL)
		code, body := authorWeb.do("POST", "/api/auth/login", map[string]string{"username": author.Username, "password": "employee-password-1"}, nil)
		expect(t, "作者登录", code, 200, body)
		code, body = authorWeb.do("POST", "/api/hub", map[string]any{
			"kind": "prompt", "slug": "secret-asset",
			"spec": map[string]any{"title": "绝密能力标题", "description": "d", "body": "b"},
		}, nil)
		expect(t, "作者提交能力", code, 201, body)
		assetID := decodeAs[struct{ Asset struct{ ID int64 } }](t, body).Asset.ID

		// 只开 connections.manage 的管理员：PATCH /api/hub/{id} 路由只要 portal.view，
		// 但 handler 只允许负责人或 plugins.manage，所以预览也只能看到 #id
		conn, connWeb := previewAdminAgent(t, f, "preview-conn-admin", "ee_at_prv_conn",
			previewRole(t, f, "preview-conn", "预览连接管理员", rbac.ConnectionsManage, rbac.PortalView))
		// 前提：这位管理员正常读这个能力是 404，预览不能替他读出来
		code, body = connWeb.do("GET", "/api/hub/"+itoa(assetID), nil, nil)
		expect(t, "连接管理员看能力详情", code, 404, body)
		text, isErr := conn.call("ee_hub_admin", map[string]any{"action": "patch", "id": assetID, "featured": true})
		if isErr {
			t.Fatalf("预览应成功但只回编号，实际报错：%s", text)
		}
		if strings.Contains(text, "绝密能力标题") || strings.Contains(text, "secret-asset") {
			t.Fatalf("预览泄露了调用人看不到的能力标题 / slug：%s", text)
		}
		if !strings.Contains(text, fmt.Sprintf("#%d", assetID)) {
			t.Fatalf("预览应回退到 #id：%s", text)
		}

		// 同一类路径：连接工具预览也能对任意 id 取标题，同样只能回 #id
		text, isErr = conn.call("ee_connections_admin", map[string]any{"action": "tool", "id": assetID, "name": "lookup", "enabled": true})
		if isErr {
			t.Fatalf("连接工具预览应成功但只回编号：%s", text)
		}
		if strings.Contains(text, "绝密能力标题") || strings.Contains(text, "secret-asset") {
			t.Fatalf("连接工具预览泄露了看不到的能力标题：%s", text)
		}

		// 管理员（plugins.manage）照常看到标题，没有被误伤
		text, isErr = super.call("ee_hub_admin", map[string]any{"action": "patch", "id": assetID, "featured": true})
		if isErr || !strings.Contains(text, "绝密能力标题") {
			t.Fatalf("有管理权限的人预览应看到标题：%v %s", isErr, text)
		}
	})

	t.Run("连接工具预览不泄露角色名", func(t *testing.T) {
		// 角色名要 roles.manage / users.manage 才能从 /api/admin/roles 读到
		secretRole := previewRole(t, f, "preview-secret-role", "绝密角色名", rbac.PortalView)
		up := newFakeMCP(t)
		code, body := f.admin.do("POST", "/api/hub", map[string]any{
			"kind": "connection", "slug": "preview-crm", "delivery": "auto",
			"spec": map[string]any{"title": "预览 CRM", "type": "mcp", "url": up.URL, "auth": map[string]any{"type": "bearer"}}, "secret": "up-secret",
		}, nil)
		expect(t, "管理员接入连接", code, 201, body)
		connID := decodeAs[struct{ Asset struct{ ID int64 } }](t, body).Asset.ID

		conn, connWeb := previewAdminAgent(t, f, "preview-conn-tool-admin", "ee_at_prv_tool",
			previewRole(t, f, "preview-conn-tool", "预览连接工具管理员", rbac.ConnectionsManage, rbac.PortalView))
		// 前提：这位管理员读不到角色列表，所以预览不能把角色名读给他
		code, body = connWeb.do("GET", "/api/admin/roles", nil, nil)
		expect(t, "连接管理员读角色列表", code, 403, body)
		args := map[string]any{"action": "tool", "id": connID, "name": "lookup", "enabled": true, "roles": []int64{secretRole.ID}}
		text, isErr := conn.call("ee_connections_admin", args)
		if isErr {
			t.Fatalf("连接工具预览失败：%s", text)
		}
		if strings.Contains(text, "绝密角色名") {
			t.Fatalf("预览泄露了调用人读不到的角色名：%s", text)
		}
		if !strings.Contains(text, fmt.Sprintf("#%d", secretRole.ID)) {
			t.Fatalf("预览应回退到角色编号：%s", text)
		}

		// 有 roles.manage 的管理员照常看到角色名
		text, isErr = super.call("ee_connections_admin", args)
		if isErr || !strings.Contains(text, "绝密角色名") {
			t.Fatalf("有 roles.manage 的人预览应看到角色名：%v %s", isErr, text)
		}
	})
}

// previewRole 建一个只带指定权限点的角色。
func previewRole(t *testing.T, f *principalFixture, code, name string, perms ...string) *store.Role {
	t.Helper()
	c, body := f.admin.do("POST", "/api/admin/roles", map[string]any{"code": code, "name": name, "permissions": perms}, nil)
	expect(t, "建角色 "+code, c, 201, body)
	role := decode[store.Role](t, body)
	return &role
}

// previewAdminAgent 建一个账号、分配默认员工角色与给定管理角色，登录后给一个 agent 令牌开启管理模式。
// 返回 Agent 视角和登录后的网页会话（网页会话用来验证「正常读」拿不到该值）。
func previewAdminAgent(t *testing.T, f *principalFixture, username, plain string, roles ...*store.Role) (agent, *client) {
	t.Helper()
	limit := int64(100000)
	u := f.user(t, username, &limit)
	c, body := f.admin.do("GET", "/api/admin/roles", nil, nil)
	expect(t, "角色列表", c, 200, body)
	ids := []int64{}
	for _, r := range decodeAs[struct{ Roles []store.Role }](t, body).Roles {
		if r.Code == rbac.RoleMember {
			ids = append(ids, r.ID)
		}
	}
	for _, r := range roles {
		ids = append(ids, r.ID)
	}
	c, body = f.admin.do("PUT", "/api/admin/users/"+itoa(u.ID)+"/roles", map[string]any{"role_ids": ids}, nil)
	expect(t, username+" 分配角色", c, 200, body)
	web := newClient(t, f.ts.URL)
	c, body = web.do("POST", "/api/auth/login", map[string]string{"username": username, "password": "employee-password-1"}, nil)
	expect(t, username+" 登录", c, 200, body)
	tk := f.token(t, u.ID, plain, agentScopes, nil, nil)
	c, body = f.elevate(t, web, tk.ID, "employee-password-1", 1)
	expect(t, username+" 开启管理模式", c, 200, body)
	return agent{dshClient{t: t, base: f.ts.URL, token: plain}}, web
}

// previewSuperAgent 给初始超级管理员一个开了管理模式的 agent 令牌。
func previewSuperAgent(t *testing.T, f *principalFixture) agent {
	t.Helper()
	c, body := f.admin.do("GET", "/api/auth/me", nil, nil)
	expect(t, "管理员", c, 200, body)
	super := decode[store.User](t, body)
	tk := f.token(t, super.ID, "ee_at_prv_super", agentScopes, nil, nil)
	c, body = f.elevate(t, f.admin, tk.ID, adminPassword, 1)
	expect(t, "管理员开启管理模式", c, 200, body)
	return agent{dshClient{t: t, base: f.ts.URL, token: "ee_at_prv_super"}}
}
