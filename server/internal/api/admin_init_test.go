package api_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// 首次初始化：没有用户时凭日志里的初始化码在网页上建第一个管理员；建好后入口关闭。
func TestInitAdmin(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if created, err := api.Bootstrap(ctx, st, "admin", ""); err != nil || created {
		t.Fatalf("没给密码不应建管理员：created=%v err=%v", created, err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	code := api.NewInitCode()
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), InitCode: code}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	c := newClient(t, ts.URL)
	initOpen := func() bool {
		t.Helper()
		status, body := c.do("GET", "/api/auth/methods", nil, nil)
		expect(t, "登录方式", status, 200, body)
		var m struct {
			InitAdmin bool `json:"init_admin"`
		}
		_ = json.Unmarshal(body, &m)
		return m.InitAdmin
	}
	if !initOpen() {
		t.Fatal("没有用户时应开放网页初始化")
	}

	status, body := c.do("POST", "/api/auth/init-admin", map[string]string{"code": "WRONG-CODE-0000", "username": "root", "password": "root-password-123"}, nil)
	expect(t, "初始化码错误", status, 403, body)
	status, body = c.do("POST", "/api/auth/init-admin", map[string]string{"code": code, "username": "root", "password": "short"}, nil)
	expect(t, "弱密码", status, 400, body)

	// 初始化码不区分大小写、可以不带连字符。
	loose := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	status, body = c.do("POST", "/api/auth/init-admin", map[string]string{"code": loose, "username": "Root", "password": "root-password-123"}, nil)
	expect(t, "创建管理员", status, 200, body)
	status, body = c.do("GET", "/api/auth/me", nil, nil)
	expect(t, "创建后已登录", status, 200, body)
	if !strings.Contains(string(body), `"username":"root"`) {
		t.Fatalf("应以 root 登录：%s", body)
	}

	if initOpen() {
		t.Fatal("建好管理员后不应再开放网页初始化")
	}
	other := newClient(t, ts.URL)
	status, body = other.do("POST", "/api/auth/init-admin", map[string]string{"code": code, "username": "evil", "password": "evil-password-123"}, nil)
	expect(t, "重复初始化", status, 404, body)
	status, body = other.do("POST", "/api/auth/login", map[string]string{"username": "root", "password": "root-password-123"}, nil)
	expect(t, "新管理员登录", status, 200, body)
}

func TestInitAdminDisabledWithoutCode(t *testing.T) {
	st := openTestStore(t)
	if _, err := api.Bootstrap(context.Background(), st, "admin", ""); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := newClient(t, ts.URL)
	status, body := c.do("POST", "/api/auth/init-admin", map[string]string{"code": "", "username": "root", "password": "root-password-123"}, nil)
	expect(t, "没有初始化码", status, 404, body)
}
