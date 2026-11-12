package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newMetricsTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	return New(nil, nil, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func getMetrics(t *testing.T, s *Server, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

func TestMetricsDisabledByDefault(t *testing.T) {
	s := newMetricsTestServer(t, Options{})
	rec := getMetrics(t, s, "127.0.0.1:5555")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("默认关闭时应返回 404，得到 %d：%s", rec.Code, rec.Body.String())
	}
}

func TestMetricsOnlyForInternalPeers(t *testing.T) {
	s := newMetricsTestServer(t, Options{Metrics: true})
	for _, addr := range []string{"203.0.113.9:5555", "198.51.100.9:5555"} {
		if rec := getMetrics(t, s, addr); rec.Code != http.StatusNotFound {
			t.Fatalf("公网来源 %s 不应拿到指标，得到 %d", addr, rec.Code)
		}
	}
	for _, addr := range []string{"127.0.0.1:5555", "[::1]:5555", "10.1.2.3:5555", "192.168.1.2:5555"} {
		if rec := getMetrics(t, s, addr); rec.Code != http.StatusOK {
			t.Fatalf("内网 / 本机来源 %s 应能抓取，得到 %d：%s", addr, rec.Code, rec.Body.String())
		}
	}
}

func TestMetricsContent(t *testing.T) {
	s := newMetricsTestServer(t, Options{Metrics: true, Version: "test-version"})
	s.loginFailures.Add(3)
	s.loginIPLimited.Add(2)
	s.loginUsernameLimited.Add(1)
	rec := getMetrics(t, s, "127.0.0.1:5555")
	if rec.Code != http.StatusOK {
		t.Fatalf("抓取失败：%d %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "version=0.0.4") {
		t.Fatalf("Content-Type 应为 Prometheus 文本格式，得到 %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		"dsh_ee_uptime_seconds",
		"dsh_ee_goroutines",
		`dsh_ee_build_info{version="test-version"} 1`,
		"dsh_ee_login_failures_total 3",
		"dsh_ee_login_ip_limited_total 2",
		"dsh_ee_login_username_limited_total 1",
		"# TYPE dsh_ee_login_failures_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("指标里缺少 %q：\n%s", want, body)
		}
	}
}

func TestMetricsVersionLabelEscaped(t *testing.T) {
	s := newMetricsTestServer(t, Options{Metrics: true, Version: `1.0"beta\test`})
	body := getMetrics(t, s, "127.0.0.1:5555").Body.String()
	if !strings.Contains(body, `dsh_ee_build_info{version="1.0\"beta\\test"} 1`) {
		t.Fatalf("版本号里的引号 / 反斜杠应转义：\n%s", body)
	}
}
