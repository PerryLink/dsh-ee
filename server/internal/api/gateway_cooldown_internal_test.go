package api

import (
	"testing"
	"time"
)

func TestProviderCooldowns(t *testing.T) {
	var c providerCooldowns
	now := time.Now()
	key := providerCooldownKey{providerID: 1, model: "chat"}
	if c.snapshot(key, now).cooling() {
		t.Fatal("没标记过的线路不应被跳过")
	}
	c.mark(key, 30*time.Second, now)
	if !c.snapshot(key, now.Add(time.Second)).cooling() {
		t.Fatal("冷却期内应跳过")
	}
	if c.snapshot(key, now.Add(31*time.Second)).cooling() {
		t.Fatal("冷却期满应恢复")
	}
	if c.snapshot(key, now.Add(32*time.Second)).cooling() {
		t.Fatal("期满清除后应保持不跳过")
	}
}

func TestProviderCooldownClearObserved(t *testing.T) {
	var c providerCooldowns
	now := time.Now()
	key := providerCooldownKey{providerID: 1, model: "chat"}
	connectionKey := providerCooldownKey{providerID: key.providerID}
	c.mark(connectionKey, 30*time.Second, now)
	c.mark(key, 30*time.Second, now)
	seen := c.snapshot(key, now.Add(time.Second))
	// 更新的失败即使没有延长截止时间，也应该保留。
	c.mark(key, 5*time.Second, now.Add(2*time.Second))
	c.clear(key, seen)
	seen = c.snapshot(key, now.Add(20*time.Second))
	if seen.connection != nil || seen.model == nil {
		t.Fatal("成功应清除已观察到的连接冷却，并保留较新的模型冷却和原截止时间")
	}
	c.clear(key, seen)
	if c.snapshot(key, now.Add(21*time.Second)).cooling() {
		t.Fatal("成功应解除已观察到的模型冷却")
	}
}

func TestCooldownFor(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"":                              providerCooldownDefault,
		"0":                             providerCooldownDefault,
		"-5":                            providerCooldownDefault,
		"abc":                           providerCooldownDefault,
		"Wed, 21 Oct 2015 07:28:00 GMT": providerCooldownDefault, // HTTP 日期形式不支持，走默认
		"120":                           2 * time.Minute,
		"3600":                          providerCooldownMax,
	} {
		if got := cooldownFor(in); got != want {
			t.Errorf("cooldownFor(%q) = %v，应为 %v", in, got, want)
		}
	}
}
