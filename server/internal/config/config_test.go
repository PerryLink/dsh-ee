package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

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
