package api_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// 管理后台「DSH 分发」页的菜单权限是 distribution.manage，但分发管理接口与分发设置段
// 原先只认 settings.manage：只被授予 distribution.manage 的角色能看到页面却处处 403。
// 这里锁定修复后的权限矩阵：settings.manage 与 distribution.manage 任一即可（旧角色不受影响），
// 两者都没有仍然 403，而且 distribution.manage 不能顺带修改其它设置段。
func TestDshReleaseDistributionAuthz(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", adminPassword); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	ts := httptest.NewServer(api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer ts.Close()

	login := func(user, pw string) *client {
		c := newClient(t, ts.URL)
		code, body := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": pw}, nil)
		expect(t, "登录 "+user, code, 200, body)
		return c
	}
	admin := login("admin", adminPassword)

	createRole := func(code string, perms ...string) int64 {
		t.Helper()
		status, body := admin.do("POST", "/api/admin/roles", map[string]any{"code": code, "name": code, "permissions": perms}, nil)
		expect(t, "建角色 "+code, status, 201, body)
		return decode[struct{ ID int64 }](t, body).ID
	}
	createUser := func(name string, roleIDs []int64) *client {
		t.Helper()
		pw := name + "-pass-1"
		status, body := admin.do("POST", "/api/admin/users", map[string]any{"username": name, "password": pw, "role_ids": roleIDs}, nil)
		expect(t, "建用户 "+name, status, 201, body)
		return login(name, pw)
	}
	distUser := createUser("dsh-dist", []int64{createRole("dsh-dist", rbac.DistributionManage)})
	settingsUser := createUser("dsh-settings", []int64{createRole("dsh-settings", rbac.SettingsManage)})
	otherUser := createUser("dsh-other", []int64{createRole("dsh-other", rbac.UsersView)})

	// 1. GET 列表：两种权限都通过，无关权限仍然 403。
	for what, c := range map[string]*client{"distribution.manage": distUser, "settings.manage": settingsUser} {
		status, body := c.do("GET", "/api/admin/dsh-releases", nil, nil)
		expect(t, what+" 看分发列表", status, 200, body)
	}
	status, body := otherUser.do("GET", "/api/admin/dsh-releases", nil, nil)
	expect(t, "无相关权限看分发列表", status, 403, body)
	if !bytes.Contains(body, []byte(`"code":"auth.forbidden"`)) {
		t.Fatalf("无权限看分发列表应返回 auth.forbidden：%s", body)
	}

	// 2. 其余分发管理接口：distribution.manage 都能进到处理函数（不是 403）。
	// 用不存在的版本与非法导入参数，避免触发真实下载。
	for _, tc := range []struct {
		what, method, path string
		want               int
	}{
		{"check", "POST", "/api/admin/dsh-releases/check", 202},
		{"mirror 不存在的版本", "POST", "/api/admin/dsh-releases/999999/mirror", 404},
		{"recommend 不存在的版本", "POST", "/api/admin/dsh-releases/999999/recommend", 404},
		{"delete 不存在的版本", "DELETE", "/api/admin/dsh-releases/999999", 404},
		{"import 非法参数", "POST", "/api/admin/dsh-releases/import?target=bogus&version=1.2.3&file=x.zip", 400},
	} {
		status, body := distUser.do(tc.method, tc.path, nil, nil)
		expect(t, "distribution.manage 调 "+tc.what, status, tc.want, body)
	}

	// 3. PUT 设置：Distribution 段 distribution.manage 也能改，并且确实落库。
	status, body = distUser.do("PUT", "/api/admin/settings", map[string]any{
		"distribution": map[string]any{"enabled": true},
	}, nil)
	expect(t, "distribution.manage 改分发段", status, 200, body)
	distributionEnabled := func(body []byte) bool {
		t.Helper()
		return decode[struct {
			Distribution struct {
				Enabled bool `json:"enabled"`
			} `json:"distribution"`
		}](t, body).Distribution.Enabled
	}
	if !distributionEnabled(body) {
		t.Fatalf("分发段没生效：%s", body)
	}
	status, body = distUser.do("GET", "/api/settings", nil, nil)
	expect(t, "读取设置", status, 200, body)
	if !distributionEnabled(body) {
		t.Fatalf("分发段没落库：%s", body)
	}

	// 只多给 Distribution 段：distribution.manage 改其它段仍然 403。
	status, body = distUser.do("PUT", "/api/admin/settings", map[string]any{"orgName": "越权组织"}, nil)
	expect(t, "distribution.manage 不能改其它设置段", status, 403, body)
	if !bytes.Contains(body, []byte(`"code":"settings.forbidden"`)) {
		t.Fatalf("越权改设置段应返回 settings.forbidden：%s", body)
	}

	// 4. settings.manage 的旧行为不变：仍能改分发段。
	status, body = settingsUser.do("PUT", "/api/admin/settings", map[string]any{
		"distribution": map[string]any{"enabled": false},
	}, nil)
	expect(t, "settings.manage 改分发段", status, 200, body)

	// 5. 无关权限：设置接口在路由层就 403。
	status, body = otherUser.do("PUT", "/api/admin/settings", map[string]any{
		"distribution": map[string]any{"enabled": true},
	}, nil)
	expect(t, "无相关权限改设置", status, 403, body)
}
