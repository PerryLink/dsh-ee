package config

import (
	"strings"
	"testing"
)

// loadForCookieTest 在隔离的环境变量下调用 Load，避免宿主机上的配置影响结果。
func loadForCookieTest(t *testing.T, env map[string]string) (*Config, error) {
	t.Helper()
	for _, k := range []string{"DSH_EE_PUBLIC_URL", "DSH_EE_COOKIE_SECURE", "DSH_EE_TRUSTED_PROXIES"} {
		t.Setenv(k, "")
	}
	t.Setenv("DSH_EE_DSN", "root:@tcp(127.0.0.1:4000)/dsh_ee")
	t.Setenv("DSH_EE_SECRET_KEY", "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=")
	for k, v := range env {
		t.Setenv(k, v)
	}
	return Load()
}

// 只填 PUBLIC_URL 时，会话 cookie 的 Secure 跟着对外地址走：https 默认开启，http/未设置默认关闭。
func TestCookieSecureDefaultsFromPublicURL(t *testing.T) {
	cases := []struct {
		name      string
		publicURL string
		want      bool
	}{
		{"未设置对外地址（本地 HTTP 部署）", "", false},
		{"HTTP 对外地址", "http://dsh.example.internal", false},
		{"HTTPS 对外地址", "https://dsh.example.internal", true},
		{"HTTPS 带端口与路径", "https://dsh.example.internal:8443/portal/", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadForCookieTest(t, map[string]string{"DSH_EE_PUBLIC_URL": c.publicURL})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.CookieSecure != c.want {
				t.Fatalf("PUBLIC_URL=%q 时 CookieSecure 应为 %v，实际 %v", c.publicURL, c.want, cfg.CookieSecure)
			}
		})
	}
}

// 显式 DSH_EE_COOKIE_SECURE 覆盖按 PUBLIC_URL 推出的默认值。
func TestCookieSecureExplicitOverridesDefault(t *testing.T) {
	cases := []struct {
		name      string
		publicURL string
		secure    string
		want      bool
	}{
		{"HTTPS 但显式关闭", "https://dsh.example.internal", "false", false},
		{"HTTP 但显式开启", "http://dsh.example.internal", "true", true},
		{"未设对外地址但显式开启", "", "true", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadForCookieTest(t, map[string]string{
				"DSH_EE_PUBLIC_URL":    c.publicURL,
				"DSH_EE_COOKIE_SECURE": c.secure,
			})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.CookieSecure != c.want {
				t.Fatalf("CookieSecure=%s 时应为 %v，实际 %v", c.secure, c.want, cfg.CookieSecure)
			}
		})
	}
}

func TestCookieSecureRejectsBadBool(t *testing.T) {
	_, err := loadForCookieTest(t, map[string]string{"DSH_EE_COOKIE_SECURE": "secure-please"})
	if err == nil || !strings.Contains(err.Error(), "DSH_EE_COOKIE_SECURE") {
		t.Fatalf("非法布尔值应报 DSH_EE_COOKIE_SECURE，实际 %v", err)
	}
}
