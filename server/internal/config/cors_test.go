package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

// 合法写法：去空白、scheme/host 转小写、去尾部 /、去重；HTTP 只放行 localhost。
func TestParseCORSOrigins(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"单个 https", "https://harness.example.com", []string{"https://harness.example.com"}},
		{"多个来源带空白", " https://a.example.com , https://b.example.com:8443 ", []string{"https://a.example.com", "https://b.example.com:8443"}},
		{"http 只放行 localhost", "http://localhost:5173,http://localhost", []string{"http://localhost:5173", "http://localhost"}},
		{"大小写与尾部斜杠规范化", "HTTPS://Harness.Example.COM/", []string{"https://harness.example.com"}},
		{"去重", "https://a.example.com,HTTPS://A.EXAMPLE.COM", []string{"https://a.example.com"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseCORSOrigins(c.raw)
			if err != nil {
				t.Fatalf("parseCORSOrigins(%q) 报错：%v", c.raw, err)
			}
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Fatalf("parseCORSOrigins(%q) = %v，希望 %v", c.raw, got, c.want)
			}
		})
	}
}

// 13 类非法写法都必须在解析阶段报错：Load 里转成启动失败，而不是带着不可用的白名单跑起来。
func TestParseCORSOriginsRejectsInvalid(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"通配符 *", "*"},
		{"opaque origin null", "null"},
		{"http 非 localhost", "http://example.com"},
		{"http 的 IP 不是 localhost", "http://127.0.0.1:5173"},
		{"缺 scheme", "example.com"},
		{"非 http(s) scheme", "ftp://example.com"},
		{"带路径", "https://example.com/path"},
		{"带查询串", "https://example.com?x=1"},
		{"带片段", "https://example.com#frag"},
		{"带用户信息", "https://user:pass@example.com"},
		{"通配符主机", "https://*.example.com"},
		{"缺主机", "https://"},
		{"空列表项", "https://example.com,,https://b.example.com"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, err := parseCORSOrigins(c.raw); err == nil {
				t.Fatalf("parseCORSOrigins(%q) = %v，应该报错", c.raw, got)
			}
		})
	}
}

// 默认关闭：不设 DSH_EE_CORS_ORIGINS 时 CORSOrigins 为空，中间件不生效。
// 设置了非法值时 Load 直接失败（与其它配置项一致）。
func TestLoadCORSOrigins(t *testing.T) {
	t.Setenv("DSH_EE_DSN", "root:@tcp(127.0.0.1:4000)/dsh_ee")
	t.Setenv("DSH_EE_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("DSH_EE_CORS_ORIGINS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("默认为空时 Load 报错：%v", err)
	}
	if len(cfg.CORSOrigins) != 0 {
		t.Fatalf("未设置 DSH_EE_CORS_ORIGINS 时应该是关闭状态，实际 %v", cfg.CORSOrigins)
	}

	t.Setenv("DSH_EE_CORS_ORIGINS", "https://harness.example.com,http://localhost:5173")
	if cfg, err = Load(); err != nil {
		t.Fatalf("合法来源 Load 报错：%v", err)
	}
	if strings.Join(cfg.CORSOrigins, ",") != "https://harness.example.com,http://localhost:5173" {
		t.Fatalf("CORSOrigins = %v", cfg.CORSOrigins)
	}

	t.Setenv("DSH_EE_CORS_ORIGINS", "https://harness.example.com/path")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DSH_EE_CORS_ORIGINS") {
		t.Fatalf("非法来源应该让 Load 失败，实际 err=%v", err)
	}
}
