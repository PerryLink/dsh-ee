package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

func proxyTestServer(extra ...netip.Prefix) *Server {
	trusted := defaultTrustedProxies()
	trusted = append(trusted, extra...)
	return &Server{trustedProxies: trusted}
}

func proxyTestRequest(remoteAddr, host string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://dsh.internal/api/test", nil)
	r.RemoteAddr = remoteAddr
	r.Host = host
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

// 直连对端不是可信代理时，X-Forwarded-For 一律忽略；是可信代理时从右往左取客户端地址。
func TestClientIPTrustsForwardedForOnlyFromProxy(t *testing.T) {
	cases := []struct {
		name   string
		extra  []netip.Prefix
		remote string
		xff    string
		want   string
	}{
		{"直连公网对端不信伪造的 XFF", nil, "198.51.100.7:5555", "192.0.2.1", "198.51.100.7"},
		{"直连内网对端不信伪造的 XFF", nil, "192.168.10.5:4000", "10.9.9.9", "192.168.10.5"},
		{"同机代理取最后一跳", nil, "127.0.0.1:5555", "203.0.113.9, 198.51.100.7", "198.51.100.7"},
		{"同机代理单跳", nil, "127.0.0.1:5555", "203.0.113.9", "203.0.113.9"},
		{"多级可信代理从右往左跳过", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, "10.1.1.1:443", "203.0.113.9, 10.2.2.2", "203.0.113.9"},
		{"代理前面伪造的跳不采信", nil, "127.0.0.1:5555", "6.6.6.6, 203.0.113.9", "203.0.113.9"},
		{"非法 XFF 回落到对端地址", nil, "127.0.0.1:5555", "not-an-ip", "127.0.0.1"},
		{"RemoteAddr 没有端口也能解析", nil, "127.0.0.1", "203.0.113.9", "203.0.113.9"},
		{"IPv6 回环代理", nil, "[::1]:5555", "203.0.113.9", "203.0.113.9"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := proxyTestServer(c.extra...)
			r := proxyTestRequest(c.remote, "dsh.internal", map[string]string{"X-Forwarded-For": c.xff})
			if got := s.clientIP(r); got != c.want {
				t.Fatalf("clientIP = %q，期望 %q", got, c.want)
			}
		})
	}
}

// publicBase：转发头只在对端是可信代理时生效；配置了 PUBLIC_URL 时永远以配置为准。
func TestPublicBaseTrustsForwardedHeadersOnlyFromProxy(t *testing.T) {
	t.Run("直连内网对端忽略伪造的转发头", func(t *testing.T) {
		s := proxyTestServer()
		r := proxyTestRequest("192.168.10.5:4000", "dsh.internal", map[string]string{
			"X-Forwarded-Host":  "evil.example",
			"X-Forwarded-Proto": "https",
		})
		if got := s.publicBase(r); got != "http://dsh.internal" {
			t.Fatalf("publicBase = %q，期望 http://dsh.internal", got)
		}
	})

	t.Run("同机代理采信转发头", func(t *testing.T) {
		s := proxyTestServer()
		r := proxyTestRequest("127.0.0.1:5555", "dsh.internal", map[string]string{
			"X-Forwarded-Host":  "dsh.example.internal",
			"X-Forwarded-Proto": "https",
		})
		if got := s.publicBase(r); got != "https://dsh.example.internal" {
			t.Fatalf("publicBase = %q，期望 https://dsh.example.internal", got)
		}
	})

	t.Run("显式配置的可信代理也采信", func(t *testing.T) {
		s := proxyTestServer(netip.MustParsePrefix("10.0.0.0/8"))
		r := proxyTestRequest("10.1.1.1:443", "dsh.internal", map[string]string{
			"X-Forwarded-Host":  "dsh.example.internal",
			"X-Forwarded-Proto": "https",
		})
		if got := s.publicBase(r); got != "https://dsh.example.internal" {
			t.Fatalf("publicBase = %q，期望 https://dsh.example.internal", got)
		}
	})

	t.Run("多值转发头取最近一跳", func(t *testing.T) {
		s := proxyTestServer()
		r := proxyTestRequest("127.0.0.1:5555", "dsh.internal", map[string]string{
			"X-Forwarded-Host":  "first.example, second.example",
			"X-Forwarded-Proto": "http, https",
		})
		if got := s.publicBase(r); got != "https://second.example" {
			t.Fatalf("publicBase = %q，期望 https://second.example", got)
		}
	})

	t.Run("TLS 直连按 https 生成", func(t *testing.T) {
		s := proxyTestServer()
		r := proxyTestRequest("198.51.100.7:5555", "dsh.internal", nil)
		r.TLS = &tls.ConnectionState{}
		if got := s.publicBase(r); got != "https://dsh.internal" {
			t.Fatalf("publicBase = %q，期望 https://dsh.internal", got)
		}
	})

	t.Run("PUBLIC_URL 优先于所有转发头", func(t *testing.T) {
		s := proxyTestServer()
		s.opts.PublicURL = "https://fixed.example"
		r := proxyTestRequest("127.0.0.1:5555", "dsh.internal", map[string]string{
			"X-Forwarded-Host": "evil.example",
		})
		if got := s.publicBase(r); got != "https://fixed.example" {
			t.Fatalf("publicBase = %q，期望 https://fixed.example", got)
		}
	})
}
