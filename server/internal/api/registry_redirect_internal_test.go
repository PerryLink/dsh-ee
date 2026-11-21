package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// registryRedirectTestServer 建一对 server：origin 按 path 决定是否重定向到 dest。
// dest 记录被请求次数，用来证明跨主机重定向的目标一次都没被访问。
func registryRedirectTestServer(t *testing.T, crossPath string) (origin, dest *httptest.Server, destHits *atomic.Int32) {
	t.Helper()
	hits := new(atomic.Int32)
	dest = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(dest.Close)
	origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case crossPath:
			http.Redirect(w, r, dest.URL+"/internal"+crossPath, http.StatusFound)
		case "/samehost/-/ping":
			// 同主机换路径是反向代理后面的正常情况，仍要跟随。
			http.Redirect(w, r, "/samehost2/-/ping", http.StatusFound)
		case "/samehost2/-/ping":
			_, _ = io.WriteString(w, "ok")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(origin.Close)
	return origin, dest, hits
}

func newRegistryRedirectServer(t *testing.T, npmUpstream *http.Client) *Server {
	t.Helper()
	return New(nil, nil, Options{DataDir: t.TempDir(), NpmUpstream: npmUpstream},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// TestRegistryClientRejectsCrossHostRedirect 覆盖管理面 npm 源检查的跳板风险：
// 管理员填的上游（或中间人）302 到另一台主机时，registryClient 不能跟过去。
func TestRegistryClientRejectsCrossHostRedirect(t *testing.T) {
	origin, _, destHits := registryRedirectTestServer(t, "/-/ping")
	s := newRegistryRedirectServer(t, nil)

	resp, err := s.registryClient().Get(origin.URL + "/-/ping")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("跨主机 302 被跟随后拿到了 %d 响应", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "重定向") || !strings.Contains(err.Error(), "另一个地址") {
		t.Fatalf("错误应说明跨主机重定向被拦下：%v", err)
	}
	if n := destHits.Load(); n != 0 {
		t.Fatalf("跨主机重定向目标被请求了 %d 次", n)
	}

	// 同主机换路径仍要放行，避免把正常 registry 的跳转一刀切拦掉。
	resp, err = s.registryClient().Get(origin.URL + "/samehost/-/ping")
	if err != nil {
		t.Fatalf("同主机重定向不应被拦：%v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Fatalf("同主机重定向结果：%d %q", resp.StatusCode, body)
	}
}

// TestRegistryClientInjectedClientRejectsCrossHostRedirect：测试或嵌入部署通过
// NpmUpstream 注入的 client 走的是同一条管理面检查路径，也要补上跨主机限制；
// 注入 client 自己的 Transport/超时保留。
func TestRegistryClientInjectedClientRejectsCrossHostRedirect(t *testing.T) {
	origin, _, destHits := registryRedirectTestServer(t, "/-/ping")
	injected := &http.Client{Transport: &http.Transport{}}
	s := newRegistryRedirectServer(t, injected)

	hc := s.registryClient()
	if hc.Transport != injected.Transport || hc.Timeout != injected.Timeout {
		t.Fatal("注入 client 的 Transport/Timeout 应保留")
	}
	resp, err := hc.Get(origin.URL + "/-/ping")
	if err == nil {
		resp.Body.Close()
		t.Fatalf("跨主机 302 被跟随后拿到了 %d 响应", resp.StatusCode)
	}
	if !strings.Contains(err.Error(), "重定向") {
		t.Fatalf("错误应说明重定向被拦下：%v", err)
	}
	if n := destHits.Load(); n != 0 {
		t.Fatalf("跨主机重定向目标被请求了 %d 次", n)
	}
}
