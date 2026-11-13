package config

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DSH_EE_DSN", "root:@tcp(127.0.0.1:4000)/dsh_ee")
	t.Setenv("DSH_EE_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func TestLoadEmbedConfig(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DSH_EE_EMBED_ORIGINS", "https://harness.acme.internal,http://localhost:5173")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load 报错：%v", err)
	}
	if strings.Join(cfg.EmbedOrigins, ",") != "https://harness.acme.internal,http://localhost:5173" {
		t.Fatalf("EmbedOrigins = %v", cfg.EmbedOrigins)
	}
	if cfg.EmbedCrossSite {
		t.Fatal("跨站嵌入默认应关闭")
	}
	if cfg.EmbedSessionTTL != 2*time.Hour {
		t.Fatalf("EmbedSessionTTL 默认 2h，实际 %v", cfg.EmbedSessionTTL)
	}
}

func TestLoadEmbedOriginsFailClosed(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DSH_EE_EMBED_ORIGINS", "https://harness.acme.internal/path")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DSH_EE_EMBED_ORIGINS") {
		t.Fatalf("非法来源应该让 Load 失败，实际 err=%v", err)
	}
}

func TestLoadEmbedCrossSite(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DSH_EE_EMBED_CROSS_SITE", "1")
	t.Setenv("DSH_EE_EMBED_ORIGINS", "https://harness.acme.internal:8443")
	t.Setenv("DSH_EE_EMBED_SESSION_TTL", "90m")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load 报错：%v", err)
	}
	if !cfg.EmbedCrossSite || cfg.EmbedSessionTTL != 90*time.Minute {
		t.Fatalf("cross-site=%v ttl=%v", cfg.EmbedCrossSite, cfg.EmbedSessionTTL)
	}

	// 开了跨站却没有来源：启动失败（不允许对任意来源放开）
	t.Setenv("DSH_EE_EMBED_ORIGINS", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "DSH_EE_EMBED_ORIGINS") {
		t.Fatalf("跨站开启但没配来源应失败，实际 err=%v", err)
	}

	// TTL 越界
	t.Setenv("DSH_EE_EMBED_ORIGINS", "https://harness.acme.internal")
	t.Setenv("DSH_EE_EMBED_SESSION_TTL", "9h")
	if _, err := Load(); err == nil {
		t.Fatal("超过 8h 的会话 TTL 应该失败")
	}
}

// loadWithRateLimit 只设置 Load 必需的变量，再按用例给 DSH_EE_RATE_LIMIT。
func loadWithRateLimit(t *testing.T, rateLimit string) (*Config, error) {
	t.Helper()
	t.Setenv("DSH_EE_DSN", "root:@tcp(127.0.0.1:4000)/dsh_ee")
	t.Setenv("DSH_EE_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("DSH_EE_RATE_LIMIT", rateLimit)
	return Load()
}

func TestRateLimitConfig(t *testing.T) {
	cases := []struct {
		name    string
		env     string
		want    int
		wantErr bool
	}{
		{name: "默认每分钟 60 次", env: "", want: 60},
		{name: "环境变量调大", env: "120", want: 120},
		{name: "0 关闭", env: "0", want: 0},
		{name: "off 关闭", env: "off", want: 0},
		{name: "false 关闭", env: "false", want: 0},
		{name: "带空格", env: "  45  ", want: 45},
		{name: "负数非法", env: "-1", wantErr: true},
		{name: "非数字非法", env: "abc", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg, err := loadWithRateLimit(t, c.env)
			if c.wantErr {
				if err == nil || !strings.Contains(err.Error(), "DSH_EE_RATE_LIMIT") {
					t.Fatalf("期望 DSH_EE_RATE_LIMIT 报错，得到 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.RateLimitPerMinute != c.want {
				t.Fatalf("RateLimitPerMinute = %d，期望 %d", cfg.RateLimitPerMinute, c.want)
			}
		})
	}
}
