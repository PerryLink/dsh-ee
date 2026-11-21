package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
)

// 用户名锁定是「全局」的（key 只有用户名），而且锁定期内原本在密码校验之前就返回 429。
// 于是任何未认证者只要每 15 分钟用 5 个错误密码（可轮换出口 IP 绕开按 IP 的限流），
// 就能让 admin 等已知用户名长期处于 locked —— 合法用户即使密码正确也登不进来。
//
// 这个测试卡住修复后的语义：锁定期间仍然完整校验密码，密码正确就放行并清零失败计数；
// 只有继续输错密码才 429（反爆破仍然有效）。
func TestLockedUsernameCorrectPasswordIsNotDenied(t *testing.T) {
	ts := newLoginRateLimitServer(t, api.Options{})
	correct := "admin-password-123"

	// 攻击者轮换出口 IP 连打 5 个错误密码，把 admin 打进锁定（IP 限流按 IP 计，换 IP 即可绕过）。
	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("203.0.113.%d", 10+i)
		if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "attacker-guess"); code != http.StatusUnauthorized {
			t.Fatalf("攻击者第 %d 次错误密码应 401，得到 %d：%s", i+1, code, body)
		}
	}

	// 锁定期内继续输错密码（再换一个干净 IP）：仍 429，说明反爆破没有被削弱。
	code, body, header := loginAttempt(t, ts.URL, "203.0.113.30", "admin", "attacker-guess")
	if code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内错误密码应 429，得到 %d：%s", code, body)
	}
	if got := header.Get("Retry-After"); got == "" {
		t.Fatalf("用户名锁定的 429 应带 Retry-After，实际没有")
	}

	// 合法用户用正确密码登录：即使用户名仍处于锁定窗口，也必须成功（修前这里是 429 DoS）。
	code, body, _ = loginAttempt(t, ts.URL, "198.51.100.200", "admin", correct)
	if code != http.StatusOK {
		t.Fatalf("锁定期内正确密码应登录成功，得到 %d：%s", code, body)
	}

	// 成功登录会清零该用户名的失败计数：之后一次错误密码应回到 401，而不是继续 429。
	code, body, _ = loginAttempt(t, ts.URL, "198.51.100.201", "admin", "attacker-guess")
	if code != http.StatusUnauthorized {
		t.Fatalf("登录成功后失败计数应清零（下一次错误密码 401），得到 %d：%s", code, body)
	}
}

// 多轮「锁定 → 正确密码登录」都必须成功：未认证者无法靠重复错误密码把已知用户名
// 变成永久不可登录。修前从第一轮的正确密码开始就 429。
func TestLoginLockoutCannotRenewIntoPermanentDenial(t *testing.T) {
	ts := newLoginRateLimitServer(t, api.Options{})
	correct := "admin-password-123"

	for round := 0; round < 3; round++ {
		for i := 0; i < 5; i++ {
			ip := fmt.Sprintf("203.0.%d.%d", 100+round, 10+i)
			if code, body, _ := loginAttempt(t, ts.URL, ip, "admin", "attacker-guess"); code != http.StatusUnauthorized {
				t.Fatalf("第 %d 轮第 %d 次错误密码应 401，得到 %d：%s", round+1, i+1, code, body)
			}
		}
		// 新一轮锁定后，合法用户仍然能登录；登录成功又把计数清零，进入下一轮。
		code, body, _ := loginAttempt(t, ts.URL, "198.51.100.210", "admin", correct)
		if code != http.StatusOK {
			t.Fatalf("第 %d 轮锁定后正确密码应登录成功，得到 %d：%s", round+1, code, body)
		}
	}
}

// 锁定按用户名生效：把不存在的用户名打锁，不影响其它用户名的正常登录；
// 从而攻击者无法用「打锁 admin」之外的错误密码波及无辜账号（同时又确认锁只能是
// 密码错误触发，不会因为别的用户名失败而锁定）。
func TestLoginLockoutIsScopedToTargetedUsername(t *testing.T) {
	ts := newLoginRateLimitServer(t, api.Options{})

	for i := 0; i < 5; i++ {
		ip := fmt.Sprintf("203.0.113.%d", 60+i)
		if code, body, _ := loginAttempt(t, ts.URL, ip, "ghost-user", "attacker-guess"); code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误密码应 401，得到 %d：%s", i+1, code, body)
		}
	}
	// ghost-user 已锁定：锁定期内输错密码 429。
	if code, body, _ := loginAttempt(t, ts.URL, "203.0.113.70", "ghost-user", "attacker-guess"); code != http.StatusTooManyRequests {
		t.Fatalf("被锁用户名的错误密码应 429，得到 %d：%s", code, body)
	}
	// 其它用户名完全不受影响。
	if code, body, _ := loginAttempt(t, ts.URL, "198.51.100.220", "admin", "admin-password-123"); code != http.StatusOK {
		t.Fatalf("其它用户名不应受锁定影响，得到 %d：%s", code, body)
	}
}
