package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeClock 给限流器测试用：窗口重置不用真等 15 分钟。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

func newTestLoginIPLimiter(limit int, window time.Duration) (*loginIPLimiter, *fakeClock) {
	l := newLoginIPLimiter(limit, window)
	c := &fakeClock{t: time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)}
	l.now = c.now
	return l, c
}

func TestLoginIPLimiterIsolatesIPs(t *testing.T) {
	l, _ := newTestLoginIPLimiter(2, 15*time.Minute)
	for range 2 {
		l.fail("198.51.100.10")
	}
	if _, blocked := l.blocked("198.51.100.10"); !blocked {
		t.Fatal("同一 IP 达到上限后应被限流")
	}
	if _, blocked := l.blocked("198.51.100.11"); blocked {
		t.Fatal("另一个 IP 不应受影响")
	}
}

func TestLoginIPLimiterCountsFailuresOnly(t *testing.T) {
	l, _ := newTestLoginIPLimiter(2, 15*time.Minute)
	// 一次失败后允许再试一次
	l.fail("203.0.113.7")
	if _, blocked := l.blocked("203.0.113.7"); blocked {
		t.Fatal("第一次失败不应立即限流")
	}
	l.fail("203.0.113.7")
	if _, blocked := l.blocked("203.0.113.7"); !blocked {
		t.Fatal("第二次失败后应达到上限")
	}
}

func TestLoginIPLimiterWindowReset(t *testing.T) {
	l, c := newTestLoginIPLimiter(1, 15*time.Minute)
	l.fail("203.0.113.8")
	if _, blocked := l.blocked("203.0.113.8"); !blocked {
		t.Fatal("达到上限后应被限流")
	}
	retry, blocked := l.blocked("203.0.113.8")
	if !blocked || retry != 15*time.Minute {
		t.Fatalf("刚失败时 Retry-After 应接近整个窗口，得到 %v / %v", retry, blocked)
	}
	c.t = c.t.Add(5 * time.Minute)
	if retry, blocked := l.blocked("203.0.113.8"); !blocked || retry != 10*time.Minute {
		t.Fatalf("窗口过半后剩余时间应为 10m，得到 %v / %v", retry, blocked)
	}
	c.t = c.t.Add(10 * time.Minute)
	if _, blocked := l.blocked("203.0.113.8"); blocked {
		t.Fatal("窗口结束后应解除限流")
	}
	// 新窗口从下一次失败重新开始
	l.fail("203.0.113.8")
	if _, blocked := l.blocked("203.0.113.8"); !blocked {
		t.Fatal("新窗口里的失败应重新计数")
	}
}

func TestLoginIPLimiterDisabled(t *testing.T) {
	l := newLoginIPLimiter(-1, 0)
	for range 100 {
		l.fail("203.0.113.9")
	}
	if _, blocked := l.blocked("203.0.113.9"); blocked {
		t.Fatal("关闭后不应限流")
	}
	if len(l.items) != 0 {
		t.Fatalf("关闭后不应保留条目：%d", len(l.items))
	}
}

func TestLoginIPLimiterDefaults(t *testing.T) {
	l := newLoginIPLimiter(0, 0)
	if l.limit != defaultLoginIPLimit || l.window != defaultLoginIPWindow {
		t.Fatalf("默认值不对：limit=%d window=%v", l.limit, l.window)
	}
}

func TestLoginIPLimiterBoundedMemory(t *testing.T) {
	l, _ := newTestLoginIPLimiter(50, 15*time.Minute)
	for i := range maxLoginIPEntries + 100 {
		l.fail(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
	}
	if got := len(l.items); got > maxLoginIPEntries {
		t.Fatalf("条目数应有上限 %d，实际 %d", maxLoginIPEntries, got)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want int
	}{
		{0, 1},
		{time.Millisecond, 1},
		{time.Second, 1},
		{time.Second + time.Millisecond, 2},
		{10 * time.Minute, 600},
	}
	for _, c := range cases {
		if got := retryAfterSeconds(c.in); got != c.want {
			t.Errorf("retryAfterSeconds(%v) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

// 按 IP 限流依赖 s.clientIP：X-Forwarded-For 只从可信代理（默认只有本机回环）采信，
// 其余直连对端（含内网地址）伪造的 X-Forwarded-For 一律忽略，换 IP 绕不过限流、也嫁祸不了别人。
func TestClientIPForRateLimit(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		xff        string
		want       string
	}{
		{"公网直连忽略 X-Forwarded-For", "203.0.113.9:5555", "198.51.100.7", "203.0.113.9"},
		{"本机代理取 X-Forwarded-For 最后一跳", "127.0.0.1:5555", "198.51.100.1, 198.51.100.7", "198.51.100.7"},
		{"内网直连不是可信代理，忽略伪造的 X-Forwarded-For", "10.0.0.5:5555", "10.0.0.99", "10.0.0.5"},
	}
	s := &Server{trustedProxies: defaultTrustedProxies()}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
			r.RemoteAddr = c.remoteAddr
			r.Header.Set("X-Forwarded-For", c.xff)
			if got := s.clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q，期望 %q", got, c.want)
			}
		})
	}
}
