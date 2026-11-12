package config

import (
	"encoding/base64"
	"testing"
	"time"
)

// setRequiredEnv 设好 Load 必需的两项配置；其余 DSH_EE_* 由各测试按需覆盖。
func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DSH_EE_DSN", "root:@tcp(127.0.0.1:4000)/dsh_ee")
	t.Setenv("DSH_EE_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func TestLoginIPLimitDefaults(t *testing.T) {
	setRequiredEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LoginIPLimit != 50 || c.LoginIPWindow != 15*time.Minute {
		t.Fatalf("默认应为 50 次 / 15m，得到 %d / %v", c.LoginIPLimit, c.LoginIPWindow)
	}
	if c.Metrics {
		t.Fatal("metrics 默认应关闭")
	}
}

func TestLoginIPLimitEnv(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("DSH_EE_LOGIN_IP_LIMIT", "7")
	t.Setenv("DSH_EE_LOGIN_IP_WINDOW", "2m")
	t.Setenv("DSH_EE_METRICS", "true")
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.LoginIPLimit != 7 || c.LoginIPWindow != 2*time.Minute {
		t.Fatalf("配置没生效：%d / %v", c.LoginIPLimit, c.LoginIPWindow)
	}
	if !c.Metrics {
		t.Fatal("DSH_EE_METRICS=true 应开启 metrics")
	}
}

func TestLoginIPLimitCanBeDisabled(t *testing.T) {
	for _, v := range []string{"0", "off", "false"} {
		setRequiredEnv(t)
		t.Setenv("DSH_EE_LOGIN_IP_LIMIT", v)
		c, err := Load()
		if err != nil {
			t.Fatalf("%q: %v", v, err)
		}
		if c.LoginIPLimit != -1 {
			t.Fatalf("%q 应关闭限流（-1），得到 %d", v, c.LoginIPLimit)
		}
	}
}

func TestLoginIPLimitInvalid(t *testing.T) {
	cases := []struct{ key, value string }{
		{"DSH_EE_LOGIN_IP_LIMIT", "-1"},
		{"DSH_EE_LOGIN_IP_LIMIT", "abc"},
		{"DSH_EE_LOGIN_IP_WINDOW", "30s"},
		{"DSH_EE_LOGIN_IP_WINDOW", "25h"},
		{"DSH_EE_LOGIN_IP_WINDOW", "nope"},
		{"DSH_EE_METRICS", "maybe"},
	}
	for _, c := range cases {
		setRequiredEnv(t)
		// 先设回合法值，确保是当前这一项触发的错误
		t.Setenv("DSH_EE_LOGIN_IP_LIMIT", "50")
		t.Setenv("DSH_EE_LOGIN_IP_WINDOW", "15m")
		t.Setenv("DSH_EE_METRICS", "false")
		t.Setenv(c.key, c.value)
		if _, err := Load(); err == nil {
			t.Errorf("%s=%q 应报错", c.key, c.value)
		}
	}
}
