package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// multipartUploadRequest 造一个带 1 MiB 文件字段的 multipart 请求。文件大于内存阈值，
// 标准库会把它落到 os.TempDir() 下的临时文件里。
func multipartUploadRequest(t *testing.T) *http.Request {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "a.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("x"), 1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r
}

// multipartTempFiles 返回 dir 下标准库 multipart 落盘的临时文件。
func multipartTempFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "multipart-") {
			names = append(names, e.Name())
		}
	}
	return names
}

// 认证中间件用 r.WithContext 造请求副本交给 handler，net/http 只清理原请求上的
// MultipartForm；这里验证 route.inner 收口的那层清理确实删掉了副本上的临时文件。
func TestRouteInnerCleansMultipartTempFiles(t *testing.T) {
	tmp := t.TempDir()
	// 标准库用 os.TempDir() 落盘，把它指到测试目录才能断言文件真的被删了。
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	if got := os.TempDir(); got != tmp {
		t.Skipf("无法把 os.TempDir() 重定向到测试目录（得到 %q），跳过", got)
	}

	r := multipartUploadRequest(t)
	// 模拟认证中间件：handler 拿到的是 r.WithContext 造出来的副本，不是原请求。
	rp := r.WithContext(context.WithValue(r.Context(), struct{ key string }{}, "auth"))
	var filesDuringHandler int
	rt := route{pattern: "POST /api/upload", h: func(w http.ResponseWriter, req *http.Request) {
		if req != rp {
			t.Fatal("handler 应拿到认证中间件传下来的请求副本")
		}
		if err := req.ParseMultipartForm(32 << 10); err != nil {
			t.Fatalf("解析 multipart: %v", err)
		}
		if got := len(req.MultipartForm.File["file"]); got != 1 {
			t.Fatalf("解析后应有一个 file 字段，得到 %d 个", got)
		}
		filesDuringHandler = len(multipartTempFiles(t, tmp))
	}}
	rt.inner(&Server{}).ServeHTTP(httptest.NewRecorder(), rp)

	if filesDuringHandler == 0 {
		t.Fatal("测试前提不成立：handler 期间应存在标准库落盘的临时文件")
	}
	if left := multipartTempFiles(t, tmp); len(left) != 0 {
		t.Fatalf("handler 返回后临时文件应被清理，还剩 %v", left)
	}
	if r.MultipartForm != nil {
		t.Fatal("解析发生在请求副本上，原请求的 MultipartForm 不该被赋值")
	}
}

func TestCleanupMultipartFormNil(t *testing.T) {
	// 没解析过 multipart 的请求（以及 nil）不能 panic。
	cleanupMultipartForm(nil)
	cleanupMultipartForm(httptest.NewRequest(http.MethodGet, "/", nil))
	// RemoveAll 幂等：别的清理点先删过一次时再清也不报错、不 panic。
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.MultipartForm = &multipart.Form{}
	cleanupMultipartForm(r)
	cleanupMultipartForm(r)
}
