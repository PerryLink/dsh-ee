package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// fakeLoginClock 给 loginLimiter 测试用：锁定 15 分钟不用真等。
type fakeLoginClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeLoginClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeLoginClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// 锁定窗口不因继续失败而续期：锁定期间的 fail 既不累加计数、也不把 until 往后推，
// 否则攻击者每发一次错误密码就能把用户名永久锁死。锁定到期后计数清零，一次失败
// 不会立刻把用户名重新锁上。
func TestLoginLimiterLockDoesNotRenewAndClearsAfterExpiry(t *testing.T) {
	clock := &fakeLoginClock{t: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	l := newLoginLimiter()
	l.now = clock.Now

	for range maxFails {
		l.fail("admin")
	}
	if !l.locked("admin") {
		t.Fatal("连续失败 maxFails 次后应进入锁定")
	}
	clock.Advance(10 * time.Minute)
	// 锁定窗口内的失败尝试（即使有人绕过 handler 直接调 limiter）不能续期。
	for range 100 {
		l.fail("admin")
	}
	if got := l.lockRemaining("admin"); got != 5*time.Minute {
		t.Fatalf("锁定期内的失败不应续期：剩余 %v，期望 5m（若被续期会变成 15m）", got)
	}

	clock.Advance(5*time.Minute + time.Second)
	if l.locked("admin") {
		t.Fatal("到达锁定到期时间后应解除锁定")
	}
	// 到期后第一次失败重新从 1 开始计数，旧的 5 次失败不能让它立刻再锁。
	l.fail("admin")
	if l.locked("admin") {
		t.Fatal("锁定到期后的第一次失败不应立即重新锁定（计数应已清零）")
	}
}

// 端到端验证锁定续期语义：锁定期间输错密码只返回 429，不改动锁定到期时间。
// 如果 handler 在 locked 分支里仍然调用 limiter.fail（或 fail 没有防续期），
// 10 分钟时那次 429 会把 until 续到 t+25m，后面的正确密码就只能 429。
func TestLockedAttemptDoesNotExtendLock(t *testing.T) {
	st := openExtTestStore(t)
	ctx := context.Background()
	if _, err := Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new box: %v", err)
	}
	srv := New(st, box, Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	clock := &fakeLoginClock{t: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)}
	srv.limiter.now = clock.Now
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	const ip = "203.0.113.90"
	hit := func(password string) int {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"username": "admin", "password": password})
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/login", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		// 测试服务器的直连对端是回环地址（可信代理），XFF 会被采信；IP 限流默认 50，够用。
		req.Header.Set("X-Forwarded-For", ip)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("登录请求失败：%v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	for i := 0; i < maxFails; i++ {
		if code := hit("wrong-password"); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应 401，得到 %d", i+1, code)
		}
	}
	if code := hit("wrong-password"); code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内错误密码应 429，得到 %d", code)
	}
	// 锁定还剩 5 分钟时再输错一次：修法若在 locked 分支计数/续期，会把 until 推到 25 分钟。
	clock.Advance(10 * time.Minute)
	if code := hit("wrong-password"); code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内错误密码应 429，得到 %d", code)
	}
	clock.Advance(6 * time.Minute) // 已越过初次锁定的到期时间（t+15m）
	if code := hit("admin-password-123"); code != http.StatusOK {
		t.Fatalf("初次锁定到期后正确密码应登录成功（锁定被续期了才会 429），得到 %d", code)
	}
}
