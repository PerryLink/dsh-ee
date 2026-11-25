package api_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
)

func TestPackageSources(t *testing.T) {
	st := openTestStore(t)
	if _, err := api.Bootstrap(context.Background(), st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	ts := newRegistryServer(t, st, newFakeRegistry(t))
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "log in", code, 200, body)
	const sources = "/api/admin/registry/sources"
	create := func(protocol, name, mode, upstream string) {
		t.Helper()
		code, body := admin.do("POST", sources, map[string]string{"protocol": protocol, "name": name, "mode": mode, "upstream_url": upstream}, nil)
		expect(t, "create source", code, 201, body)
	}
	create("raw", "files", "hosted", "")
	create("raw", "remote", "proxy", "http://127.0.0.1:1234")
	create("conan", "cpp", "hosted", "")
	create("conan", "center", "proxy", "")
	code, body = admin.do("GET", sources, nil, nil)
	expect(t, "list sources", code, 200, body)
	if !strings.Contains(string(body), "https://center2.conan.io") {
		t.Fatalf("ConanCenter missing: %s", body)
	}
	mint := func(scopes ...string) string {
		t.Helper()
		code, body := admin.do("POST", "/api/me/tokens", map[string]any{"name": "registry integration", "scopes": scopes}, nil)
		expect(t, "mint registry token", code, 201, body)
		return decodeAs[map[string]any](t, body)["token"].(string)
	}
	writer, reader := mint("registry:read", "registry:publish"), mint("registry:read")
	publisher := mint("registry:publish")
	request := func(method, path, contents, token string, headers map[string]string, want int) (string, http.Header) {
		t.Helper()
		req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(contents))
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		expect(t, method+" "+path, resp.StatusCode, want, data)
		return string(data), resp.Header
	}
	const raw = "/raw/files/releases/File.html"
	request("GET", raw, "", "", nil, 401)
	request("PUT", raw, "abcdef", reader, nil, 403)
	request("PUT", raw, "abcdef", writer, nil, 201)
	content, h := request("GET", raw, "", reader, map[string]string{"Range": "bytes=1-3"}, 206)
	if content != "bcd" || h.Get("Content-Range") != "bytes 1-3/6" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Fatalf("unexpected Raw response: %q %v", content, h)
	}
	request("GET", "/raw/files/releases/file.html", "", reader, nil, 404)
	request("GET", "/raw/files/%2e%2e/private", "", reader, nil, 400)
	request("GET", "/raw/files/../private", "", reader, nil, 400)
	request("GET", "/raw/remote/file", "", reader, nil, 502)
	request("PUT", "/raw/remote/file", "abcdef", writer, nil, 405)
	request("PUT", "/conan/center/v2/conans/lib/1.0/_/_/revisions/abc/files/conanfile.py", "recipe", writer, nil, 405)
	_, h = request("GET", "/conan/cpp/v1/ping", "", "", nil, 200)
	if h.Get("X-Conan-Server-Capabilities") != "revisions" {
		t.Fatalf("unexpected Conan capabilities: %v", h)
	}
	const recipe = "/conan/cpp/v2/conans/lib/1.0/_/_"
	const files = recipe + "/revisions/abc/files"
	request("GET", "/conan/cpp/v2/users/check_credentials", "", publisher, nil, 200)
	request("GET", recipe+"/revisions", "", publisher, nil, 404)
	request("PUT", files+"/conanfile.py", "recipe", writer, map[string]string{"X-Checksum-Sha1": "bad"}, 400)
	request("PUT", files+"/conanfile.py", "recipe", writer, nil, 201)
	request("GET", recipe+"/latest", "", reader, nil, 404)
	request("GET", files+"/conanfile.py", "", reader, nil, 404)
	const manifest = "1\nconanfile.py: 0123456789abcdef0123456789abcdef\n"
	request("PUT", files+"/conanmanifest.txt", manifest, writer, nil, 201)
	content, _ = request("GET", recipe+"/latest", "", reader, nil, 200)
	if !strings.Contains(content, `"revision":"abc"`) {
		t.Fatalf("latest revision missing: %q", content)
	}
	request("GET", files, "", reader, nil, 200)
	request("GET", files+"/conanfile.py", "", reader, nil, 200)
	request("GET", recipe+"/revisions", "", publisher, nil, 200)
	request("GET", files+"/conanfile.py", "", publisher, nil, 403)
	request("GET", "/conan/center/v2/users/check_credentials", "", publisher, nil, 403)
	const binaryFiles = recipe + "/revisions/abc/packages/0123/revisions/def/files"
	request("PUT", binaryFiles+"/conan_package.tgz", "gzip", writer, nil, 201)
	request("PUT", binaryFiles+"/conaninfo.txt", "info", writer, nil, 201)
	request("PUT", binaryFiles+"/conanmanifest.txt", "1\n", writer, nil, 201)
	request("GET", recipe+"/revisions/abc/packages/0123/revisions", "", publisher, nil, 200)
	request("PUT", binaryFiles+"/conan_package.txz", "xz", writer, nil, 201)
	content, _ = request("GET", binaryFiles, "", reader, nil, 200)
	if strings.Contains(content, "conan_package.tgz") || !strings.Contains(content, "conan_package.txz") {
		t.Fatalf("expected only the replacement archive: %q", content)
	}
	request("GET", binaryFiles+"/conan_package.tgz", "", reader, nil, 404)
	request("GET", binaryFiles+"/conan_package.txz", "", reader, nil, 200)
	code, body = admin.do("GET", sources+"/raw/files/artifacts", nil, nil)
	expect(t, "list artifacts", code, 200, body)
	if !strings.Contains(string(body), "releases/File.html") {
		t.Fatalf("artifact missing: %s", body)
	}
}

// TestPackageSourcesRequireJSONContentType：包源管理接口和其他写接口一样必须走
// readJSON，只接受 application/json。text/plain 是浏览器的「简单请求」——不发
// CORS 预检就能带着 SameSite=Lax 的会话 cookie 打过来，所以入口处必须 415 拒绝；
// 否则跨站页面可以静默创建包源（CSRF）。
func TestPackageSourcesRequireJSONContentType(t *testing.T) {
	st := openTestStore(t)
	if _, err := api.Bootstrap(context.Background(), st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	ts := newRegistryServer(t, st, newFakeRegistry(t))
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "log in", code, 200, body)
	const sources = "/api/admin/registry/sources"
	code, body = admin.do("POST", sources, map[string]string{"protocol": "raw", "name": "remote", "mode": "proxy", "upstream_url": "http://127.0.0.1:1234"}, nil)
	expect(t, "application/json 创建包源", code, http.StatusCreated, body)
	code, body = admin.do("POST", sources, map[string]string{"protocol": "raw", "name": "csrf", "mode": "hosted"}, map[string]string{"Content-Type": "text/plain"})
	expect(t, "text/plain 创建包源应被拒（CSRF 防护）", code, http.StatusUnsupportedMediaType, body)
	code, body = admin.do("GET", sources, nil, nil)
	expect(t, "list sources", code, 200, body)
	if strings.Contains(string(body), "\"csrf\"") {
		t.Fatalf("text/plain 请求不应创建包源：%s", body)
	}
	code, body = admin.do("PATCH", sources+"/raw/remote", map[string]string{"upstream_url": "http://127.0.0.1:5678"}, map[string]string{"Content-Type": "text/plain"})
	expect(t, "text/plain 更新包源应被拒（CSRF 防护）", code, http.StatusUnsupportedMediaType, body)
	code, body = admin.do("PATCH", sources+"/raw/remote", map[string]string{"upstream_url": "http://127.0.0.1:5678"}, nil)
	expect(t, "application/json 更新包源", code, http.StatusOK, body)
}
