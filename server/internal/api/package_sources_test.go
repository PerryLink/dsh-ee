package api_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

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

// rawUploadTestServer 起一套带真实 registry 的测试服务：登录管理员、创建 hosted Raw 源、
// 签发一个 registry:publish 令牌。返回服务地址与令牌。
func rawUploadTestServer(t *testing.T) (base, token string) {
	t.Helper()
	st := openTestStore(t)
	if _, err := api.Bootstrap(context.Background(), st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	ts := newRegistryServer(t, st, newFakeRegistry(t))
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "log in", code, 200, body)
	code, body = admin.do("POST", "/api/admin/registry/sources", map[string]string{"protocol": "raw", "name": "files", "mode": "hosted", "upstream_url": ""}, nil)
	expect(t, "create raw source", code, 201, body)
	code, body = admin.do("POST", "/api/me/tokens", map[string]any{"name": "raw upload", "scopes": []string{"registry:publish"}}, nil)
	expect(t, "mint publish token", code, 201, body)
	return ts.URL, decodeAs[map[string]any](t, body)["token"].(string)
}

// rawUploadPut 发一次 Raw PUT，返回状态码与响应头；网络错误留给调用方决定怎么处理。
func rawUploadPut(base, token, path, contents string) (int, http.Header, error) {
	req, err := http.NewRequest(http.MethodPut, base+path, strings.NewReader(contents))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, resp.Header, nil
}

// TestRawUploadSlowBodyRejected 覆盖 P1：持发布令牌的人用慢速/半开的 PUT 占住连接和半写
// temp 文件时，服务端必须在读时限内失败（408 或断开连接），而不是一直挂着。
func TestRawUploadSlowBodyRejected(t *testing.T) {
	// 读时限压到 250 ms：测试最多等 5 s，就能区分「按时失败」和「一直挂住」。
	restore := api.SetPackageUploadReadDeadlineForTest(250 * time.Millisecond)
	t.Cleanup(restore)
	base, token := rawUploadTestServer(t)

	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	// 声明 4 KiB 却只发几个字节，然后不再发送：服务端会卡在读请求体上。
	if _, err := fmt.Fprintf(conn, "PUT /raw/files/slow.bin HTTP/1.1\r\nHost: raw-upload-test\r\nAuthorization: Bearer %s\r\nContent-Length: 4096\r\n\r\n", token); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "partial"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_ = conn.SetReadDeadline(start.Add(5 * time.Second))
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		// 修前没有读时限：这里会一直等到客户端 5 s 超时。
		t.Fatalf("慢速上传没有在读时限内结束：等了 %v（err=%v）", elapsed, err)
	}
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestTimeout {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("慢速上传应返回 408，得到 %d：%s", resp.StatusCode, body)
		}
	}
	// 超时/断开也必须把名额还回来：紧接着的正常 PUT 必须 201。
	if code, _, err := rawUploadPut(base, token, "/raw/files/after-timeout.bin", "abcdef"); err != nil || code != http.StatusCreated {
		t.Fatalf("超时后的正常上传应返回 201，得到 %d（err=%v）", code, err)
	}
}

// TestRawUploadConcurrentGate 覆盖 P1 的并发闸：2 个上传名额占满时，第三次 PUT 必须
// 立刻 429 + Retry-After（不排队读请求体）；释放名额后正常 PUT 恢复 201。
//
// 名额用测试钩子确定性地占住，而不是真开两条慢连接：两条真实请求谁先拿到名额本身
// 不确定，探针请求还可能插进去抢走一个，用它们写「占满」断言会偶发。真实慢连接占
// 名额/释放的行为由 TestRawUploadSlowBodyRejected 覆盖。
func TestRawUploadConcurrentGate(t *testing.T) {
	base, token := rawUploadTestServer(t)
	release := api.OccupyPackageUploadSlotsForTest(2)
	released := false
	defer func() {
		if !released {
			release()
		}
	}()

	code, header, err := rawUploadPut(base, token, "/raw/files/busy.bin", "x")
	if err != nil {
		t.Fatalf("名额占满时上传：%v", err)
	}
	if code != http.StatusTooManyRequests {
		t.Fatalf("名额占满时应返回 429，得到 %d", code)
	}
	retry := header.Get("Retry-After")
	if n, err := strconv.Atoi(retry); err != nil || n < 1 {
		t.Fatalf("429 应带不小于 1 秒的 Retry-After，得到 %q", retry)
	}

	// 释放名额后必须恢复上传：闸门没有泄漏名额。
	release()
	released = true
	code, _, err = rawUploadPut(base, token, "/raw/files/recovered.bin", "abcdef")
	if err != nil {
		t.Fatalf("释放名额后上传：%v", err)
	}
	if code != http.StatusCreated {
		t.Fatalf("释放名额后应恢复 201，得到 %d", code)
	}
}
