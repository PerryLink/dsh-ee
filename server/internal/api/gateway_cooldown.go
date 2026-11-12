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

// providerCooldowns 记住每条线路（按上游 id）的冷却截止时间。
// 同名模型挂多条线路时，一条 429 / 5xx / 连不上后短时间内直接跳过它，
// 免得冷却期内每个请求都先撞一次墙才轮到下一条；全部冷却时仍会尝试最后一条兜底。
// 与登录限流一样按单实例设计，进程内存即可。
type providerCooldowns struct {
	mu    sync.Mutex
	until map[int64]time.Time
}

// mark 记一次失败；已有一个更晚的截止时间时不缩短。
func (c *providerCooldowns) mark(id int64, d time.Duration, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.until == nil {
		c.until = make(map[int64]time.Time)
	}
	if end := now.Add(d); end.After(c.until[id]) {
		c.until[id] = end
	}
}

// skip 报告这条线路现在是否仍在冷却期；期满即清除，下次不再跳过。
func (c *providerCooldowns) skip(id int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	end, ok := c.until[id]
	if !ok {
		return false
	}
	if now.After(end) {
		delete(c.until, id)
		return false
	}
	return true
}

func (c *providerCooldowns) clear(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.until, id)
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
