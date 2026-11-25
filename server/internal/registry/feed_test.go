package registry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// feedTestJSON 是一个最小的合法插件索引（DSH 索引 format 1）。
const feedTestJSON = `{"plugins":[{"name":"demo-plugin","title":"Demo","versions":[{"version":"1.0.0","publishTimestamp":1700000000}]}]}`

// 插件源是管理面发起的外连，重定向目标可能被第三方源控制。拨号层的 IP 守卫只在直连时生效
// （走 HTTP(S)_PROXY 时目标主机由代理解析、守卫被绕过），所以重定向必须逐跳预检：
// 这里用不带拨号守卫的 Upstream 模拟代理模式，修前会跟到 127.0.0.1 上的目标并成功解析。
func TestFeedRedirectToPrivateAddressRejected(t *testing.T) {
	t.Setenv("DSH_EE_UPSTREAM_ALLOW_PRIVATE", "")
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte(feedTestJSON))
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/feed.json", http.StatusFound)
	}))
	defer source.Close()
	u := &Upstream{hc: &http.Client{}}
	s := &Service{up: u}
	_, _, err := s.fetchFeed(context.Background(), source.URL+"/feed.json", true)
	if err == nil {
		t.Fatal("重定向到 127.0.0.1 的插件源应被拒绝")
	}
	if got := targetHits.Load(); got != 0 {
		t.Fatalf("重定向目标不应被访问，实际访问了 %d 次", got)
	}
	if !strings.Contains(err.Error(), "重定向被拒绝") || !strings.Contains(err.Error(), "internal address") {
		t.Fatalf("错误信息应说明重定向目标被 URL 校验拒绝：%v", err)
	}
}

// 服务端一直不回响应时，单次抓取必须在总超时内失败，不能把管理面 goroutine 挂住。
func TestFeedFetchTotalTimeout(t *testing.T) {
	t.Setenv("DSH_EE_UPSTREAM_ALLOW_PRIVATE", "1")
	old := feedFetchTimeout
	feedFetchTimeout = 100 * time.Millisecond
	t.Cleanup(func() { feedFetchTimeout = old })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done(): // 客户端超时断开
		case <-time.After(3 * time.Second):
			_, _ = w.Write([]byte(feedTestJSON))
		}
	}))
	defer server.Close()
	u, err := NewUpstream(nil, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{up: u}
	begin := time.Now()
	_, _, err = s.fetchFeed(context.Background(), server.URL+"/feed.json", true)
	elapsed := time.Since(begin)
	if err == nil {
		t.Fatal("服务端不回响应时应返回超时错误")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应因总超时失败：%v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("总超时没有生效，耗时 %s", elapsed)
	}
}

// 超过 16 MiB 的响应必须被拒绝（插件索引正常是 MB 级）。
func TestFeedRejectsOversizedResponse(t *testing.T) {
	t.Setenv("DSH_EE_UPSTREAM_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"plugins":[],"padding":"`)
		chunk := []byte(strings.Repeat("a", 1<<20))
		for i := 0; i < feedMaxBodyBytes>>20; i++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
		_, _ = io.WriteString(w, `"}`)
	}))
	defer server.Close()
	u, err := NewUpstream(nil, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{up: u}
	_, _, err = s.fetchFeed(context.Background(), server.URL+"/feed.json", true)
	if err == nil {
		t.Fatal("超过 16 MiB 的插件目录应被拒绝")
	}
	if !strings.Contains(err.Error(), "16 MB") {
		t.Fatalf("错误信息应说明超过响应上限：%v", err)
	}
}

// 合法的跳转（同主机换路径、跨主机 CDN）仍然要能跟随并解析，不能被加固拦死。
func TestFeedFollowsLegitRedirects(t *testing.T) {
	t.Setenv("DSH_EE_UPSTREAM_ALLOW_PRIVATE", "1")
	var finalHits atomic.Int32
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		finalHits.Add(1)
		_, _ = w.Write([]byte(feedTestJSON))
	}))
	defer final.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.json":
			http.Redirect(w, r, "/hop.json", http.StatusMovedPermanently) // 同主机换路径
		case "/hop.json":
			http.Redirect(w, r, final.URL+"/feed.json", http.StatusTemporaryRedirect) // 跨主机
		default:
			http.NotFound(w, r)
		}
	}))
	defer source.Close()
	u, err := NewUpstream(nil, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{up: u}
	plugins, at, err := s.fetchFeed(context.Background(), source.URL+"/feed.json", true)
	if err != nil {
		t.Fatalf("合法重定向应能跟随：%v", err)
	}
	if len(plugins) != 1 || plugins[0].Name != "demo-plugin" {
		t.Fatalf("解析结果不对：%+v", plugins)
	}
	if finalHits.Load() != 1 {
		t.Fatalf("跨主机跳转应只取一次，实际 %d 次", finalHits.Load())
	}
	if at.IsZero() {
		t.Fatal("成功抓取应记录 FetchedAt")
	}
}
