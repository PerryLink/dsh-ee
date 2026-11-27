package api_test

import (
	"context"
	"crypto/rand"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// TestPackageSourceOwnerMismatchLeavesNoBlob：写他人命名空间被 403 拒绝的包源上传不能留下永久 blob。
// 修复前 putPackageFile 先 Blobs().Put 落盘、后 SavePackageSourceFile 才校验 owner，于是别的账号
// 的上传被拒后 blob 仍会留在数据目录里：管理端列表不可见、也没有审计记录，可以无痕吃满数据盘
// （服务级 DoS）。immutable / 重复内容的冲突取决于内容摘要，仍走事务内逻辑，语义不变（见文末断言）。
func TestPackageSourceOwnerMismatchLeavesNoBlob(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: dataDir}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "log in", code, 200, body)
	login := func(name string) *client {
		t.Helper()
		code, body := admin.do("POST", "/api/admin/users", map[string]any{"username": name, "password": name + "-password-1"}, nil)
		expect(t, "create "+name, code, 201, body)
		c := newClient(t, ts.URL)
		code, body = c.do("POST", "/api/auth/login", map[string]string{"username": name, "password": name + "-password-1"}, nil)
		expect(t, name+" login", code, 200, body)
		return c
	}
	mint := func(c *client) string {
		t.Helper()
		code, body := c.do("POST", "/api/me/tokens", map[string]any{"name": "registry", "scopes": []string{"registry:read", "registry:publish"}}, nil)
		expect(t, "mint registry token", code, 201, body)
		return decodeAs[map[string]any](t, body)["token"].(string)
	}
	alice, bob := login("alice"), login("bob")
	aliceToken, bobToken := mint(alice), mint(bob)

	const sources = "/api/admin/registry/sources"
	code, body = admin.do("POST", sources, map[string]string{"protocol": "raw", "name": "files", "mode": "hosted"}, nil)
	expect(t, "create raw source", code, 201, body)
	code, body = admin.do("POST", sources, map[string]string{"protocol": "conan", "name": "cpp", "mode": "hosted"}, nil)
	expect(t, "create conan source", code, 201, body)

	upload := func(path, contents, token string) int {
		t.Helper()
		req, err := http.NewRequest(http.MethodPut, ts.URL+path, strings.NewReader(contents))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("PUT %s: %v", path, err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}
	// blobs 下除了 tmp 暂存目录，每个普通文件都是一个已落盘的 blob。
	blobCount := func() int {
		t.Helper()
		n := 0
		err := filepath.WalkDir(filepath.Join(dataDir, "blobs"), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "tmp" {
					return fs.SkipDir
				}
				return nil
			}
			if d.Type().IsRegular() {
				n++
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	// A 先占住 Raw 命名空间（Raw 的 namespace 就是完整路径）后，B 再传同一路径 → 403 且不落盘。
	const rawPath = "/raw/files/releases/owned.bin"
	if got := upload(rawPath, "alice original payload", aliceToken); got != http.StatusCreated {
		t.Fatalf("owner first Raw PUT = %d, want 201", got)
	}
	before := blobCount()
	if got := upload(rawPath, "bob rejected payload", bobToken); got != http.StatusForbidden {
		t.Fatalf("other owner Raw PUT = %d, want 403", got)
	}
	if after := blobCount(); after != before {
		t.Errorf("403 拒绝的 Raw 上传仍落盘 %d 个 blob", after-before)
	}
	// 正常 owner 的覆盖上传（Raw 可变）仍然 201。
	if got := upload(rawPath, "alice replacement payload", aliceToken); got != http.StatusCreated {
		t.Fatalf("owner Raw overwrite = %d, want 201", got)
	}

	// 不可变路径的冲突是否成立取决于内容摘要，仍由 SavePackageSourceFile 事务内判断（回归保护）：
	// 相同内容重复上传保持 201 幂等，不同内容才 409。
	const conanFile = "/conan/cpp/v2/conans/lib/1.0/_/_/revisions/abc/files/conanfile.py"
	if got := upload(conanFile, "recipe v1", aliceToken); got != http.StatusCreated {
		t.Fatalf("owner first Conan PUT = %d, want 201", got)
	}
	if got := upload(conanFile, "recipe v1", aliceToken); got != http.StatusCreated {
		t.Fatalf("immutable Conan PUT with identical content = %d, want 201 (idempotent)", got)
	}
	if got := upload(conanFile, "recipe v2", aliceToken); got != http.StatusConflict {
		t.Fatalf("immutable Conan overwrite = %d, want 409", got)
	}
}

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
