package api

import (
	"testing"
	"time"
)

func TestProviderCooldowns(t *testing.T) {
	var c providerCooldowns
	now := time.Now()
	if c.skip(1, now) {
		t.Fatal("没标记过的线路不应被跳过")
	}
	c.mark(1, 30*time.Second, now)
	if !c.skip(1, now.Add(time.Second)) {
		t.Fatal("冷却期内应跳过")
	}
	if c.skip(1, now.Add(31*time.Second)) {
		t.Fatal("冷却期满应恢复")
	}
	if c.skip(1, now.Add(32*time.Second)) {
		t.Fatal("期满清除后应保持不跳过")
	}
	// 重复标记取更晚的截止时间，不会把冷却缩短
	c.mark(2, 30*time.Second, now)
	c.mark(2, 5*time.Second, now)
	if !c.skip(2, now.Add(20*time.Second)) {
		t.Fatal("重复标记应保留更长的冷却")
	}
	// clear 立即恢复
	c.clear(2)
	if c.skip(2, now.Add(time.Second)) {
		t.Fatal("clear 后应立即可用")
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
