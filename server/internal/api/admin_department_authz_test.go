package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 部门变更的权限边界：只能调整权限严格小于自己的下级；自己、同级、上级都不行，
// 审计里要留下部门前后值。超级管理员正常调岗不受影响。
func TestUserDepartmentAuthz(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", adminPassword); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	ts := httptest.NewServer(api.New(st, box, api.Options{}, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer ts.Close()

	login := func(user, pw string) *client {
		c := newClient(t, ts.URL)
		code, body := c.do("POST", "/api/auth/login", map[string]string{"username": user, "password": pw}, nil)
		expect(t, "登录 "+user, code, 200, body)
		return c
	}
	admin := login("admin", adminPassword)

	code, body := admin.do("GET", "/api/admin/roles", nil, nil)
	expect(t, "角色列表", code, 200, body)
	var memberRole int64
	for _, r := range decode[struct {
		Roles []struct {
			ID   int64
			Code string
		}
	}](t, body).Roles {
		if r.Code == "member" {
			memberRole = r.ID
		}
	}
	if memberRole == 0 {
		t.Fatal("找不到内置的员工角色")
	}

	createRole := func(code string, perms ...string) int64 {
		t.Helper()
		code2, body := admin.do("POST", "/api/admin/roles", map[string]any{"code": code, "name": code, "permissions": perms}, nil)
		expect(t, "建角色 "+code, code2, 201, body)
		return decode[struct{ ID int64 }](t, body).ID
	}
	hrRole := createRole("hr", "users.manage")
	leadRole := createRole("hr-lead", "users.manage", "users.view", "audit.view")

	createUser := func(name, dept string, roleIDs ...int64) int64 {
		t.Helper()
		code, body := admin.do("POST", "/api/admin/users", map[string]any{
			"username": name, "password": name + "-password-1", "department": dept, "role_ids": roleIDs,
		}, nil)
		expect(t, "建用户 "+name, code, 201, body)
		return decode[struct{ ID int64 }](t, body).ID
	}
	// 管理员账号同时保留员工角色（正常用法），员工只有员工角色：这样 hr 的权限是员工的超集
	hrID := createUser("hr1", "人事部", memberRole, hrRole)
	peerID := createUser("hr2", "人事部", memberRole, hrRole)
	bossID := createUser("boss", "管理层", memberRole, leadRole)
	empID := createUser("emp", "研发部", memberRole)

	departmentOf := func(id int64) string {
		t.Helper()
		_, body := admin.do("GET", "/api/admin/users", nil, nil)
		for _, u := range decode[struct{ Users []store.User }](t, body).Users {
			if u.ID == id {
				return u.Department
			}
		}
		t.Fatalf("用户 %d 不存在", id)
		return ""
	}
	meID := func(c *client) int64 {
		t.Helper()
		_, body := c.do("GET", "/api/auth/me", nil, nil)
		return decode[meResp](t, body).ID
	}

	hr := login("hr1", "hr1-password-1")

	// 上级（权限比 hr 多）：不能改
	code, body = hr.do("PATCH", "/api/admin/users/"+itoa(bossID), map[string]any{"department": "人事部"}, nil)
	expect(t, "不能调上级的部门", code, 403, body)
	// 同级（同一角色）：不能改
	code, body = hr.do("PATCH", "/api/admin/users/"+itoa(peerID), map[string]any{"department": "财务部"}, nil)
	expect(t, "不能调同级的部门", code, 403, body)
	// 自己：不能改
	code, body = hr.do("PATCH", "/api/admin/users/"+itoa(hrID), map[string]any{"department": "财务部"}, nil)
	expect(t, "不能改自己的部门", code, 403, body)
	// 超级管理员：不能改
	code, body = hr.do("PATCH", "/api/admin/users/"+itoa(meID(admin)), map[string]any{"department": "财务部"}, nil)
	expect(t, "不能调超管的部门", code, 403, body)

	// 被拒的请求不能改动任何数据
	for _, c := range []struct {
		id   int64
		dept string
	}{
		{bossID, "管理层"},
		{peerID, "人事部"},
		{hrID, "人事部"},
	} {
		if got := departmentOf(c.id); got != c.dept {
			t.Fatalf("用户 %d 的部门被改动：%q，期望 %q", c.id, got, c.dept)
		}
	}

	// 下级（普通员工）：正常调岗
	code, body = hr.do("PATCH", "/api/admin/users/"+itoa(empID), map[string]any{"department": "财务部"}, nil)
	expect(t, "可以调下级的部门", code, 200, body)
	if got := decode[store.User](t, body).Department; got != "财务部" {
		t.Fatalf("调岗没生效：%q", body)
	}

	// 超级管理员正常调岗不受影响
	code, body = admin.do("PATCH", "/api/admin/users/"+itoa(bossID), map[string]any{"department": "财务部"}, nil)
	expect(t, "超管调岗", code, 200, body)
	if got := decode[store.User](t, body).Department; got != "财务部" {
		t.Fatalf("超管调岗没生效：%q", body)
	}

	// 审计记录部门前后值
	code, body = admin.do("GET", "/api/admin/audit?action=user.update&limit=100", nil, nil)
	expect(t, "审计列表", code, 200, body)
	logs := decode[struct{ Logs []store.AuditLog }](t, body).Logs
	found := false
	for _, l := range logs {
		if l.Target != "emp" {
			continue
		}
		found = true
		if fmt.Sprint(l.Detail["old_department"]) != "研发部" || fmt.Sprint(l.Detail["new_department"]) != "财务部" {
			t.Fatalf("部门变更审计的前后值不对：%v", l.Detail)
		}
	}
	if !found {
		t.Fatal("审计里没有 emp 的 user.update 记录")
	}
}
