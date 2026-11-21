package api

// 慢速上传的可用性回归：请求体是客户端可控的字节流，任何在「读完请求体」之前占住的
// 全局名额都可能被一个故意减速的客户端长期占住，让其他人的上传/发布/预检全部 429。
// 这里用只发一半 body 的 PUT 复现；名额只有 1 个，修前被慢请求占住，修后不该占。

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/registry"
)

// publishStarter 起一个只挂 npm publish 的 httptest 服务；started 在 handler 入口发信号，
// 避免测试靠调度延迟猜「请求已经进来了」。
func publishStarter(t *testing.T, s *Server) (ts *httptest.Server, started <-chan struct{}) {
	t.Helper()
	start := make(chan struct{}, 8)
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start <- struct{}{}
		s.handleNpmPublish(w, r, testNpmPublishCaller(), "@acme/slow")
	}))
	t.Cleanup(ts.Close)
	return ts, start
}

// startSlowPublish 发一个只写了一半 JSON 的 PUT，返回结束函数。
// 前面的合法前缀让服务端确定已经进到读 body 的阶段；之后不再发数据，模拟慢速客户端。
func startSlowPublish(t *testing.T, base string) (finish func()) {
	t.Helper()
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPut, base+"/npm/@acme%2fslow", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() {
		defer close(done)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			resp.Body.Close()
		}
	}()
	if _, err := pw.Write([]byte(`{"name":"@acme/slow",`)); err != nil {
		t.Fatalf("写慢速请求体: %v", err)
	}
	return func() {
		_ = pw.CloseWithError(errors.New("测试主动断开慢速请求"))
		<-done
	}
}

func TestNpmPublishSlowBodyDoesNotHoldUploadSlot(t *testing.T) {
	old := packageUploadSlots
	packageUploadSlots = make(chan struct{}, 1)
	t.Cleanup(func() { packageUploadSlots = old })

	s := testNpmPublishServer(1 << 20)
	ts, started := publishStarter(t, s)
	finish := startSlowPublish(t, ts.URL)
	defer finish()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("慢速 publish 没有进入处理函数")
	}
	// handler 入口之后就是读 body（修后）或先占名额（修前）；给调度留一点时间。
	time.Sleep(100 * time.Millisecond)

	release, ok := beginPackageUpload()
	if !ok {
		t.Fatal("只发了一半请求体的慢速 publish 仍占着上传名额：其他上传/预检会一直 429")
	}
	release()
}

func TestNpmPublishSlowBodyDoesNotBlockOtherUploads(t *testing.T) {
	old := packageUploadSlots
	packageUploadSlots = make(chan struct{}, 1)
	t.Cleanup(func() { packageUploadSlots = old })

	s := testNpmPublishServer(1 << 20)
	ts, started := publishStarter(t, s)
	finish := startSlowPublish(t, ts.URL)
	defer finish()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("慢速 publish 没有进入处理函数")
	}
	time.Sleep(100 * time.Millisecond)

	// 第二个客户端：body 是坏的 JSON，正常应该 400；修前会先撞上被慢请求占满的名额 → 429。
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/npm/@acme%2fother", strings.NewReader("{not-json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("第二个 publish: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		t.Fatalf("另一个人被慢速请求挤成了 429（名额被读 body 前的慢请求占住）")
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("坏 JSON 应返回 400，得到 %d", resp.StatusCode)
	}
}

// 慢速请求体必须被读超时打断：不设全局 ReadTimeout（会误伤流式接口），而是这几条
// 上传路由自己在 handler 里给请求体设总时限。测试注入 150ms 的短时限。
func TestNpmPublishSlowBodyReadDeadline(t *testing.T) {
	s := testNpmPublishServer(1 << 20)
	s.opts.UploadReadTimeout = 150 * time.Millisecond
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handleNpmPublish(w, r, testNpmPublishCaller(), "@acme/slow")
	}))
	t.Cleanup(ts.Close)

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/npm/@acme%2fslow", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	type result struct {
		status int
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		got <- result{status: resp.StatusCode}
	}()
	if _, err := pw.Write([]byte(`{"name":"@acme/slow",`)); err != nil {
		t.Fatalf("写慢速请求体: %v", err)
	}
	defer pw.CloseWithError(errors.New("测试结束"))

	select {
	case r := <-got:
		if r.err != nil {
			t.Fatalf("读超时后应在服务端回 408，而不是连接错误：%v", r.err)
		}
		if r.status != http.StatusRequestTimeout {
			t.Fatalf("读超时应返回 408，得到 %d", r.status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("慢速请求体没有被读超时打断：名额会被长期占住")
	}

	// 超时后名额必须还回来（即使请求体没读完）。
	release, ok := beginPackageUpload()
	if !ok {
		t.Fatal("读超时结束后上传名额没有释放")
	}
	release()
}

// 网页上传/预检走的是同一个读超时机制；这里覆盖预检入口。
func TestPackageCheckSlowBodyReadDeadline(t *testing.T) {
	s := &Server{registry: &registry.Service{MaxTarball: 1 << 20}}
	s.opts.UploadReadTimeout = 150 * time.Millisecond
	ts := httptest.NewServer(http.HandlerFunc(s.handleCheckPackage))
	t.Cleanup(ts.Close)

	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/me/packages/check", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	got := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			got <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusRequestTimeout {
			got <- fmt.Errorf("读超时应返回 408，得到 %d", resp.StatusCode)
			return
		}
		got <- nil
	}()
	if _, err := pw.Write([]byte("\x1f\x8b\x08")); err != nil {
		t.Fatalf("写慢速请求体: %v", err)
	}
	defer pw.CloseWithError(errors.New("测试结束"))
	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("慢速预检请求: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("慢速预检请求体没有被读超时打断")
	}
}
