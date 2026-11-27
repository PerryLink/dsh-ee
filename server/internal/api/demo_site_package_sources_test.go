package api_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 演示站的包源（Raw / Conan）只读：上传和源配置写入必须被拒，且拦在鉴权与写库之前；
// 读请求照常。上游 #334 为 /raw/、/conan/ 和源管理接口加了屏蔽，但当时没有回归测试，
// 这里把行为钉住，免得以后改路由、挪中间件顺序时又悄悄放开。
func TestDemoSiteBlocksPackageSourceWrites(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	// 演示站里源管理接口本身被拦，没法经接口建源，直接写库预置。
	for _, src := range []store.PackageSource{
		{Protocol: "raw", Name: "files", Mode: "hosted"},
		{Protocol: "raw", Name: "remote", Mode: "proxy", UpstreamURL: "http://127.0.0.1:1234"},
		{Protocol: "conan", Name: "cpp", Mode: "hosted"},
	} {
		if err := st.CreatePackageSource(ctx, src); err != nil {
			t.Fatalf("预置包源 %s/%s：%v", src.Protocol, src.Name, err)
		}
	}
	newServer := func(demo *api.DemoSite) *httptest.Server {
		key := make([]byte, 32)
		_, _ = rand.Read(key)
		box, _ := secret.NewBox(key)
		srv := api.New(st, box, api.Options{DataDir: t.TempDir(), DemoSite: demo}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		ts := httptest.NewServer(srv.Handler())
		t.Cleanup(ts.Close)
		return ts
	}
	demo := newServer(&api.DemoSite{Username: "admin", Password: "admin-password-123"})
	plain := newServer(nil)

	// 演示站：不带令牌的写请求也直接 403（屏蔽在鉴权之前），而不是 401。
	refused := func(what string, code int, body []byte) {
		t.Helper()
		if code != http.StatusForbidden || !strings.Contains(string(body), "演示站不开放这个操作") {
			t.Fatalf("%s：应返回 403 并说明是演示站限制，实际 %d %s", what, code, body)
		}
	}
	anon := newClient(t, demo.URL)
	for _, path := range []string{
		"/raw/files/releases/File.html",
		"/conan/cpp/v2/conans/lib/1.0/_/_/revisions/abc/files/conanfile.py",
	} {
		code, body := anon.do("PUT", path, nil, nil)
		refused("演示站上传 "+path, code, body)
	}

	// 源管理：即便有管理员会话也必须 403，且不能落库。
	demoAdmin := newClient(t, demo.URL)
	code, body := demoAdmin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "演示站管理员登录", code, 200, body)
	code, body = demoAdmin.do("POST", "/api/admin/registry/sources", map[string]string{"protocol": "raw", "name": "forbidden", "mode": "hosted"}, nil)
	refused("演示站新建包源", code, body)
	if _, err := st.PackageSource(ctx, "raw", "forbidden"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("演示站新建包源应被拦在写库之前，库里却查到了：%v", err)
	}
	code, body = demoAdmin.do("PATCH", "/api/admin/registry/sources/raw/remote", map[string]string{"upstream_url": "http://127.0.0.1:5678"}, nil)
	refused("演示站改包源上游", code, body)
	if src, err := st.PackageSource(ctx, "raw", "remote"); err != nil || src.UpstreamURL != "http://127.0.0.1:1234" {
		t.Fatalf("演示站改上游应被拦在写库之前：%+v %v", src, err)
	}

	// 只读不受影响：Conan ping 公开即可用；Raw 带只读令牌能走到查文件（404 表示没这个文件，不是 403）。
	code, body = newClient(t, demo.URL).do("GET", "/conan/cpp/v1/ping", nil, nil)
	expect(t, "演示站 Conan 只读", code, 200, body)
	code, body = demoAdmin.do("POST", "/api/me/tokens", map[string]any{"name": "demo read", "scopes": []string{"registry:read"}}, nil)
	expect(t, "演示站申请只读令牌", code, 201, body)
	readToken := decodeAs[map[string]any](t, body)["token"].(string)
	code, body = newClient(t, demo.URL).do("GET", "/raw/files/releases/missing.txt", nil, map[string]string{"Authorization": "Bearer " + readToken})
	if code != http.StatusNotFound {
		t.Fatalf("演示站 Raw 读应照常（没有文件即 404），实际 %d %s", code, body)
	}

	// 对照：同一份数据在非演示站上照常按鉴权处理、照常写库 —— 403 只来自演示站限制。
	for _, path := range []string{
		"/raw/files/releases/File.html",
		"/conan/cpp/v2/conans/lib/1.0/_/_/revisions/abc/files/conanfile.py",
	} {
		code, body := newClient(t, plain.URL).do("PUT", path, nil, nil)
		expect(t, "非演示站上传需鉴权 "+path, code, 401, body)
	}
	plainAdmin := newClient(t, plain.URL)
	code, body = plainAdmin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "非演示站管理员登录", code, 200, body)
	code, body = plainAdmin.do("POST", "/api/admin/registry/sources", map[string]string{"protocol": "raw", "name": "plain-created", "mode": "hosted"}, nil)
	expect(t, "非演示站新建包源", code, 201, body)
	code, body = plainAdmin.do("PATCH", "/api/admin/registry/sources/raw/remote", map[string]string{"upstream_url": "http://127.0.0.1:5678"}, nil)
	expect(t, "非演示站改包源上游", code, 200, body)
	if src, err := st.PackageSource(ctx, "raw", "remote"); err != nil || src.UpstreamURL != "http://127.0.0.1:5678" {
		t.Fatalf("非演示站改上游应写库：%+v %v", src, err)
	}
}
