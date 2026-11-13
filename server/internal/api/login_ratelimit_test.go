package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// newLoginRateLimitServer 起一个带管理员的完整服务端，登录限流的窗口 / 上限由 opts 指定。
func newLoginRateLimitServer(t *testing.T, opts api.Options) *httptest.Server {
	t.Helper()
	st := openTestStore(t)
	if _, err := api.Bootstrap(context.Background(), st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	box, _ := secret.NewBox(make([]byte, 32))
	if opts.DataDir == "" {
		opts.DataDir = t.TempDir()
	}
	ts := httptest.NewServer(api.New(st, box, opts, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	t.Cleanup(ts.Close)
	return ts
}

// loginAttempt 发一次网页登录；xff 非空时带上 X-Forwarded-For，模拟不同来源 IP。
// 返回状态码、响应体与响应头（测试要看 Retry-After）。
func loginAttempt(t *testing.T, base, xff, username, password string) (int, []byte, http.Header) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	req, err := http.NewRequest(http.MethodPost, base+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("登录请求失败：%v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

func loginWithForm(t *testing.T, base, xff string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, base+"/api/auth/login", strings.NewReader("username=admin"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if xff != "" {
		req.Header.Set("X-Forwarded-For", xff)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("表单登录请求失败：%v", err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func getBody(t *testing.T, base, path string) (int, string, http.Header) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Get(base + path)
	if err != nil {
		t.Fatalf("GET %s 失败：%v", path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data), resp.Header
}

// 同一出口 IP 连续失败达到上限后返回 429 + Retry-After，其它 IP 不受影响。
func TestLoginIPRateLimitIsolatesIPs(t *testing.T) {
	ts := newLoginRateLimitServer(t, api.Options{LoginIPLimit: 2, LoginIPWindow: 15 * time.Minute})
	a, b := "198.51.100.10", "198.51.100.11"

	for i := range 2 {
		code, body, _ := loginAttempt(t, ts.URL, a, "admin", "wrong-password")
		if code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应返回 401，得到 %d：%s", i+1, code, body)
		}
	}
	code, body, header := loginAttempt(t, ts.URL, a, "admin", "admin-password-123")
	if code != http.StatusTooManyRequests {
		t.Fatalf("达到上限后应返回 429，得到 %d：%s", code, body)
	}
	if !strings.Contains(string(body), "次数过多") {
		t.Fatalf("429 应说明失败次数过多：%s", body)
	}
	retry, err := strconv.Atoi(header.Get("Retry-After"))
	if err != nil || retry < 1 || retry > 15*60 {
		t.Fatalf("Retry-After 应是 1..900 的秒数，得到 %q (%v)", header.Get("Retry-After"), err)
	}
	// 被限流后即使密码正确也直接 429，不再做密码校验
	if code, body, _ := loginAttempt(t, ts.URL, a, "admin", "wrong-password"); code != http.StatusTooManyRequests {
		t.Fatalf("限流期间再次请求应仍为 429，得到 %d：%s", code, body)
	}
	// 另一个 IP 仍然可以正常登录
	if code, body, _ := loginAttempt(t, ts.URL, b, "admin", "admin-password-123"); code != http.StatusOK {
		t.Fatalf("其它 IP 不应被限流，得到 %d：%s", code, body)
	}
}

// 只有「用户名或密码错误」计数：登录成功、格式错误、用户名锁定都不增加 IP 失败次数。
func TestLoginIPRateLimitOnlyCountsBadCredentials(t *testing.T) {
	ts := newLoginRateLimitServer(t, api.Options{LoginIPLimit: 2, LoginIPWindow: 15 * time.Minute})
	ip := "198.51.100.20"

	// 成功登录不计失败
	if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "admin-password-123"); code != http.StatusOK {
		t.Fatalf("第一次登录应成功，得到 %d：%s", code, body)
	}
	// 一次密码错误：计数 1
	if code, _, _ := loginAttempt(t, ts.URL, ip, "admin", "wrong-password"); code != http.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401，得到 %d", code)
	}
	// 成功登录仍可进行，说明成功没有计数、也没有重置成负
	if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "admin-password-123"); code != http.StatusOK {
		t.Fatalf("一次失败后应还能登录，得到 %d：%s", code, body)
	}
	// 格式错误 / 非 JSON 请求在密码校验之前就返回，不应计数
	for range 3 {
		if code := loginWithForm(t, ts.URL, ip); code != http.StatusUnsupportedMediaType {
			t.Fatalf("表单登录应返回 415，得到 %d", code)
		}
	}
	// 第二次密码错误：计数达到上限
	if code, _, _ := loginAttempt(t, ts.URL, ip, "admin", "wrong-password"); code != http.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401，得到 %d", code)
	}
	if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "admin-password-123"); code != http.StatusTooManyRequests {
		t.Fatalf("两次失败后应返回 429，得到 %d：%s", code, body)
	}
}

// 固定窗口到期后自动解除限流，不需要重启服务。
func TestLoginIPRateLimitWindowResets(t *testing.T) {
	window := time.Second
	ts := newLoginRateLimitServer(t, api.Options{LoginIPLimit: 1, LoginIPWindow: window})
	ip := "198.51.100.30"

	if code, _, _ := loginAttempt(t, ts.URL, ip, "admin", "wrong-password"); code != http.StatusUnauthorized {
		t.Fatalf("错误密码应返回 401，得到 %d", code)
	}
	if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "admin-password-123"); code != http.StatusTooManyRequests {
		t.Fatalf("窗口内应返回 429，得到 %d：%s", code, body)
	}
	time.Sleep(window + 300*time.Millisecond)
	if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "admin-password-123"); code != http.StatusOK {
		t.Fatalf("窗口结束后应恢复登录，得到 %d：%s", code, body)
	}
}

// 用户名锁仍然生效；/metrics 统计密码错误、IP 限流与用户名锁定三种计数。
func TestLoginRateLimitMetrics(t *testing.T) {
	ts := newLoginRateLimitServer(t, api.Options{LoginIPLimit: 5, LoginIPWindow: 15 * time.Minute, Metrics: true, Version: "test"})
	ip := "198.51.100.40"

	for i := range 5 {
		code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "wrong-password")
		if code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应返回 401，得到 %d：%s", i+1, code, body)
		}
	}
	// 用户名锁（5 次）先于 IP 限流命中
	if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "admin-password-123"); code != http.StatusTooManyRequests {
		t.Fatalf("用户名应被锁定，得到 %d：%s", code, body)
	}
	// 换一个用户名，IP 失败次数达到上限
	if code, body, _ := loginAttempt(t, ts.URL, ip, "another", "whatever-password"); code != http.StatusTooManyRequests {
		t.Fatalf("IP 应被限流，得到 %d：%s", code, body)
	}

	code, body, header := getBody(t, ts.URL, "/metrics")
	if code != http.StatusOK {
		t.Fatalf("抓取 /metrics 应成功，得到 %d：%s", code, body)
	}
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "text/plain") {
		t.Fatalf("Content-Type 应为文本格式，得到 %q", ct)
	}
	for _, want := range []string{
		"dsh_ee_login_failures_total 5",
		"dsh_ee_login_ip_limited_total 1",
		"dsh_ee_login_username_limited_total 1",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("指标里缺少 %q：\n%s", want, body)
		}
	}
}
