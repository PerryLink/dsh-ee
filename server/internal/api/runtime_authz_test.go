package api_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// authzRuntimeProvider 只是让 runtimeAvailable 通过：下面的用例都在权限校验或参数校验
// 阶段就结束，真正创建沙箱的 Create 一次都不应被调用。
type authzRuntimeProvider struct{ calls int }

func (p *authzRuntimeProvider) Create(context.Context, runtime.SandboxSpec) (runtime.Sandbox, error) {
	p.calls++
	return nil, errors.New("test provider must not create sandboxes")
}

// 管理后台的云机器人 / 云电脑接口原先只认 settings.manage。runtime.manage 是这些接口
// 专用的新权限点：旧角色（settings.manage）行为不变，只给 runtime.manage 的角色也能用；
// 两者都没有仍然 403，而且 runtime.manage 不能顺带修改其它系统设置。
func TestRuntimeAdminAuthz(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", adminPassword); err != nil {
		t.Fatal(err)
	}
	provider := &authzRuntimeProvider{}
	box, _ := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	ts := httptest.NewServer(api.New(st, box, api.Options{
		DataDir: t.TempDir(),
		Runtime: runtime.Settings{Provider: provider},
	}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
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
	runtimeUser := createUser("rt-runtime", []int64{createRole("rt-runtime", rbac.RuntimeManage)})
	settingsUser := createUser("rt-settings", []int64{createRole("rt-settings", rbac.SettingsManage)})
	otherUser := createUser("rt-other", []int64{createRole("rt-other", rbac.UsersView)})

	// 8 个 admin 云接口的「读 / 写前置」矩阵。用不存在的资源 id 与非法参数，避免真的
	// 创建沙箱、运行机器人或回收电脑：期望值全部是处理函数能返回的结果（200 / 400 / 404），
	// 只有一个例外 —— 没有权限的人在路由层就被 403 挡住。
	cases := []struct {
		what, method, path string
		body               any
		want               int
	}{
		{"看机器人列表", "GET", "/api/admin/runtime/bots", nil, 200},
		{"新建机器人（非法参数）", "POST", "/api/admin/runtime/bots", map[string]any{}, 400},
		{"改不存在的机器人（非法参数）", "PUT", "/api/admin/runtime/bots/999999", map[string]any{}, 400},
		{"运行不存在的机器人", "POST", "/api/admin/runtime/bots/999999/run", nil, 404},
		{"共享不存在的机器人", "PUT", "/api/admin/runtime/bots/999999/share", map[string]any{"department": "研发部"}, 404},
		{"看云电脑列表", "GET", "/api/admin/runtime/computers", nil, 200},
		{"休眠不存在的云电脑", "POST", "/api/admin/runtime/computers/999999/hibernate", nil, 404},
		{"回收不存在的云电脑", "DELETE", "/api/admin/runtime/computers/999999", nil, 404},
	}
	for _, tc := range cases {
		for what, c := range map[string]*client{"runtime.manage": runtimeUser, "settings.manage": settingsUser} {
			status, body := c.do(tc.method, tc.path, tc.body, nil)
			if status != tc.want {
				t.Errorf("%s 调 %s：状态码 %d，期望 %d；响应 %s", what, tc.what, status, tc.want, body)
			}
		}
		status, body := otherUser.do(tc.method, tc.path, tc.body, nil)
		if status != 403 {
			t.Errorf("无相关权限调 %s：状态码 %d，期望 403；响应 %s", tc.what, status, body)
		} else if !bytes.Contains(body, []byte(`"code":"auth.forbidden"`)) {
			t.Errorf("无相关权限调 %s 应返回 auth.forbidden：%s", tc.what, body)
		}
	}

	// 作用域不放大：runtime.manage 不是「第二个 settings.manage」，不能改其它设置段，
	// 也不能读只给 settings.manage 的管理接口。
	status, body := runtimeUser.do("PUT", "/api/admin/settings", map[string]any{"orgName": "越权组织"}, nil)
	expect(t, "runtime.manage 不能改系统设置", status, 403, body)
	status, body = runtimeUser.do("GET", "/api/admin/write-confirm", nil, nil)
	expect(t, "runtime.manage 不能读写操作确认设置", status, 403, body)

	// 旧行为不变：settings.manage 仍然能看云接口（上面矩阵已覆盖），且真正的错误码不变。
	status, body = runtimeUser.do("GET", "/api/admin/runtime/bots", nil, nil)
	expect(t, "runtime.manage 看机器人列表", status, 200, body)
	if provider.calls != 0 {
		t.Fatalf("权限用例不应创建沙箱，实际调用 Create %d 次", provider.calls)
	}
}
