package api

import (
	"strconv"
	"sync"
	"time"
)

// 上游线路失败后的冷却时长：默认参照常见限流窗口取 30 秒；
// 429 带了 Retry-After（秒）就按它来，封顶 5 分钟，防止上游写错一个数把线路封死。
const (
	providerCooldownDefault = 30 * time.Second
	providerCooldownMax     = 5 * time.Minute
)

type providerCooldownKey struct {
	providerID int64
	model      string // 上游实际接收的模型名。空值表示整条线路的连接故障。
}

type providerCooldown struct {
	until time.Time
}

type providerCooldownSnapshot struct {
	connection *providerCooldown
	model      *providerCooldown
}

func (s providerCooldownSnapshot) cooling() bool {
	return s.connection != nil || s.model != nil
}

// providerCooldowns 在进程内记录上游连接故障和各模型的 HTTP 失败。
type providerCooldowns struct {
	mu      sync.Mutex
	entries map[providerCooldownKey]*providerCooldown
}

// mark 记一次失败；已有一个更晚的截止时间时不缩短。
func (c *providerCooldowns) mark(key providerCooldownKey, d time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[providerCooldownKey]*providerCooldown)
	}
	end := now.Add(d)
	if previous := c.entries[key]; previous != nil && previous.until.After(end) {
		end = previous.until
	}
	// 每次失败都换一条记录，避免较早的成功清除这次失败。
	c.entries[key] = &providerCooldown{until: end}
}

func (c *providerCooldowns) snapshot(key providerCooldownKey, now time.Time) providerCooldownSnapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	return providerCooldownSnapshot{
		connection: c.active(providerCooldownKey{providerID: key.providerID}, now),
		model:      c.active(key, now),
	}
}

// active 由持锁的 snapshot 调用，期满即清除。
func (c *providerCooldowns) active(key providerCooldownKey, now time.Time) *providerCooldown {
	entry := c.entries[key]
	if entry != nil && now.After(entry.until) {
		delete(c.entries, key)
		return nil
	}
	return entry
}

// clear 只清除这次尝试开始时看到的记录。
func (c *providerCooldowns) clear(key providerCooldownKey, seen providerCooldownSnapshot) {
	if !seen.cooling() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	connectionKey := providerCooldownKey{providerID: key.providerID}
	if seen.connection != nil && c.entries[connectionKey] == seen.connection {
		delete(c.entries, connectionKey)
	}
	if seen.model != nil && c.entries[key] == seen.model {
		delete(c.entries, key)
	}
}

// cooldownFor 解析 429 响应的 Retry-After。只认整秒形式；HTTP 日期形式不常见，
// 认不出来就走默认值。0 或负数视为没给。
func cooldownFor(retryAfter string) time.Duration {
	if n, err := strconv.Atoi(retryAfter); err == nil && n > 0 {
		if d := time.Duration(n) * time.Second; d <= providerCooldownMax {
			return d
		}
		return providerCooldownMax
	}
	return providerCooldownDefault
}
