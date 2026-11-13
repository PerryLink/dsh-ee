package embed

import (
	"strings"
	"testing"
)

func TestParseOrigins(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"单个 https", "https://harness.acme.internal", []string{"https://harness.acme.internal"}},
		{"带端口与回环", "https://harness.acme.internal:8443,http://localhost:5173", []string{"https://harness.acme.internal:8443", "http://localhost:5173"}},
		{"去空白、末尾斜杠、大小写", " https://Harness.Acme.Internal , http://LOCALHOST/ ", []string{"https://harness.acme.internal", "http://localhost"}},
		{"去重", "https://a.example.com,https://A.EXAMPLE.COM", []string{"https://a.example.com"}},
		{"回环 IP", "http://127.0.0.1:5173,http://[::1]:5173", []string{"http://127.0.0.1:5173", "http://[::1]:5173"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseOrigins(c.raw)
			if err != nil {
				t.Fatalf("ParseOrigins(%q) 报错：%v", c.raw, err)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("ParseOrigins(%q) = %v，希望 %v", c.raw, got, c.want)
			}
		})
	}
}

func TestParseOriginsRejectsInvalid(t *testing.T) {
	for _, raw := range []string{
		"*",
		"null",
		"null,",
		"https://example.com/path",
		"https://example.com/path/",
		"https://example.com?x=1",
		"https://example.com#frag",
		"http://example.com",
		"ftp://example.com",
		"example.com",
		"https://*.example.com",
		"https://user:pass@example.com",
		"https://example.com:badport",
		"https://example.com,,https://b.example.com",
	} {
		t.Run(raw, func(t *testing.T) {
			if got, err := ParseOrigins(raw); err == nil {
				t.Fatalf("ParseOrigins(%q) = %v，应该报错", raw, got)
			}
		})
	}
}

func TestNormalizeOrigin(t *testing.T) {
	got, err := NormalizeOrigin("HTTPS://Harness.Acme.Internal:8443/")
	if err != nil || got != "https://harness.acme.internal:8443" {
		t.Fatalf("NormalizeOrigin = %q, %v", got, err)
	}
	if _, err := NormalizeOrigin("http://10.0.0.5:8080"); err == nil {
		t.Fatal("内网 http 来源应被拒绝")
	}
	if !Allowed([]string{"https://a.example.com"}, "https://a.example.com") {
		t.Fatal("Allowed 应命中")
	}
	if Allowed([]string{"https://a.example.com"}, "https://b.example.com") {
		t.Fatal("Allowed 不应命中")
	}
}
