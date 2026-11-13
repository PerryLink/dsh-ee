package config

import (
	"net/netip"
	"strings"
	"testing"
)

func hasPrefix(prefixes []netip.Prefix, want string) bool {
	target := netip.MustParsePrefix(want)
	for _, p := range prefixes {
		if p == target {
			return true
		}
	}
	return false
}

func TestParseTrustedProxiesDefaultsToLoopback(t *testing.T) {
	got, err := ParseTrustedProxies("")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	if len(got) != 2 || !hasPrefix(got, "127.0.0.1/32") || !hasPrefix(got, "::1/128") {
		t.Fatalf("默认应只含回环地址，实际 %v", got)
	}
}

func TestParseTrustedProxiesAcceptsIPAndCIDR(t *testing.T) {
	got, err := ParseTrustedProxies(" 10.0.0.0/8 , 172.17.0.1,2001:db8::/32, ")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	for _, want := range []string{"127.0.0.1/32", "::1/128", "10.0.0.0/8", "172.17.0.1/32", "2001:db8::/32"} {
		if !hasPrefix(got, want) {
			t.Fatalf("缺少 %s，实际 %v", want, got)
		}
	}
}

func TestParseTrustedProxiesNormalizesCIDR(t *testing.T) {
	got, err := ParseTrustedProxies("10.1.2.3/8")
	if err != nil {
		t.Fatalf("ParseTrustedProxies: %v", err)
	}
	if !hasPrefix(got, "10.0.0.0/8") {
		t.Fatalf("网段应被规范化成网络地址，实际 %v", got)
	}
}

func TestParseTrustedProxiesRejectsInvalid(t *testing.T) {
	for _, s := range []string{"not-an-ip", "10.0.0.0/33", "10.0.0.0/"} {
		if _, err := ParseTrustedProxies(s); err == nil {
			t.Fatalf("%q 应报错", s)
		}
	}
}

// loadForProxyTest 在隔离的环境变量下调用 Load。
func loadForProxyTest(t *testing.T, env map[string]string) (*Config, error) {
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

func TestLoadTrustedProxiesFromEnv(t *testing.T) {
	cfg, err := loadForProxyTest(t, map[string]string{"DSH_EE_TRUSTED_PROXIES": "172.17.0.0/16,10.8.0.1"})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, want := range []string{"127.0.0.1/32", "172.17.0.0/16", "10.8.0.1/32"} {
		if !hasPrefix(cfg.TrustedProxies, want) {
			t.Fatalf("缺少 %s，实际 %v", want, cfg.TrustedProxies)
		}
	}
}

func TestLoadRejectsInvalidTrustedProxies(t *testing.T) {
	_, err := loadForProxyTest(t, map[string]string{"DSH_EE_TRUSTED_PROXIES": "proxy.local"})
	if err == nil || !strings.Contains(err.Error(), "DSH_EE_TRUSTED_PROXIES") {
		t.Fatalf("非法可信代理列表应报 DSH_EE_TRUSTED_PROXIES，实际 %v", err)
	}
}
