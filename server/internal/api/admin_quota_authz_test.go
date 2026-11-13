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

// 建账号时显式设置 / 清空月度额度需要「分配额度」权限，审计要记下额度。
func TestCreateUserQuotaAuthz(t *testing.T) {
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

	createRole := func(code string, perms ...string) int64 {
		t.Helper()
		code2, body := admin.do("POST", "/api/admin/roles", map[string]any{"code": code, "name": code, "permissions": perms}, nil)
		expect(t, "建角色 "+code, code2, 201, body)
		return decode[struct{ ID int64 }](t, body).ID
	}
	hrRole := createRole("hr", "users.manage")
	quotaRole := createRole("hr-quota", "users.manage", "quotas.manage")
	for _, u := range []struct {
		name string
		role int64
	}{{"hr1", hrRole}, {"hrq1", quotaRole}} {
		code, body := admin.do("POST", "/api/admin/users", map[string]any{
			"username": u.name, "password": u.name + "-password-1", "role_ids": []int64{u.role},
		}, nil)
		expect(t, "建管理员 "+u.name, code, 201, body)
	}
	hr := login("hr1", "hr1-password-1")
	hrq := login("hrq1", "hrq1-password-1")

	// 只有 users.manage：显式设置（含 0）或清空额度都拒绝
	for _, limit := range []any{nil, 0, 1000} {
		code, body := hr.do("POST", "/api/admin/users", map[string]any{
			"username": "blocked", "password": "blocked-password-1", "monthly_token_limit": limit,
		}, nil)
		expect(t, fmt.Sprintf("users.manage 建号设额度 %v", limit), code, 403, body)
	}
	// 不传额度字段：按默认额度 0 建号，正常
	code, body := hr.do("POST", "/api/admin/users", map[string]any{"username": "default-quota", "password": "default-quota-pass-1"}, nil)
	expect(t, "默认额度建号", code, 201, body)
	if u := decode[store.User](t, body); u.MonthlyTokenLimit == nil || *u.MonthlyTokenLimit != 0 {
		t.Fatalf("新账号默认额度应为 0：%s", body)
	}

	// 有 quotas.manage：可以显式不限额，也可以设具体额度
	code, body = hrq.do("POST", "/api/admin/users", map[string]any{
		"username": "unlimited", "password": "unlimited-password-1", "monthly_token_limit": nil,
	}, nil)
	expect(t, "有额度权限建不限额账号", code, 201, body)
	if u := decode[store.User](t, body); u.MonthlyTokenLimit != nil {
		t.Fatalf("显式 null 应是不限额：%s", body)
	}
	code, body = hrq.do("POST", "/api/admin/users", map[string]any{
		"username": "limited", "password": "limited-password-1", "monthly_token_limit": 2000,
	}, nil)
	expect(t, "有额度权限建限额账号", code, 201, body)
	if u := decode[store.User](t, body); u.MonthlyTokenLimit == nil || *u.MonthlyTokenLimit != 2000 {
		t.Fatalf("额度没生效：%s", body)
	}

	// 审计记录建号时的额度
	code, body = admin.do("GET", "/api/admin/audit?action=user.create&limit=100", nil, nil)
	expect(t, "审计列表", code, 200, body)
	logs := decode[struct{ Logs []store.AuditLog }](t, body).Logs
	quotaOf := func(username string) (any, bool) {
		for _, l := range logs {
			if l.Target == username {
				v, ok := l.Detail["monthly_token_limit"]
				return v, ok
			}
		}
		return nil, false
	}
	if v, ok := quotaOf("default-quota"); !ok || fmt.Sprint(v) != "0" {
		t.Fatalf("默认额度建号的审计额度不对：%v %v", v, ok)
	}
	if v, ok := quotaOf("unlimited"); !ok || v != nil {
		t.Fatalf("不限额建号的审计额度应为 null：%v %v", v, ok)
	}
	if v, ok := quotaOf("limited"); !ok || fmt.Sprint(v) != "2000" {
		t.Fatalf("限额建号的审计额度不对：%v %v", v, ok)
	}
}
