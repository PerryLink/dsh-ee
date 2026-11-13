package store

import (
	"context"
	"time"
)

// ---- 嵌入限流的共享固定窗口（embed_rates）----
//
// mint / exchange 的限流从进程内 ipRate 改到数据库：N 个实例共用同一份额度，
// 不会因为多实例部署而成倍放水。窗口按分钟对齐，依赖各实例时钟同步（部署时用 NTP）。

// TakeEmbedRate 在一个固定窗口里消费一次额度，返回当前窗口计数与是否放行。
// 计数最多涨到 limit+1（到上限后不再增长），窗口结束后由后台清理。
// limit <= 0 视为不放行（配置层已经保证 >= 1，这里只是兜底）。
func (s *Store) TakeEmbedRate(ctx context.Context, key string, window time.Time, limit int) (int, bool, error) {
	if limit <= 0 {
		return 0, false, nil
	}
	window = window.UTC().Truncate(time.Millisecond)
	// 一条 upsert 同时完成「建窗口 / 计数 +1（封顶 limit+1）」：多实例并发时由行锁串行化，
	// 读回计数再判断是否放行。max 窗口结束前这一行一直存在。
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO embed_rates (rate_key, window_start, count) VALUES (?, ?, 1)
		 ON DUPLICATE KEY UPDATE count = IF(count <= ?, count + 1, count)`,
		key, window, limit); err != nil {
		return 0, false, err
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT count FROM embed_rates WHERE rate_key = ? AND window_start = ?`, key, window).Scan(&count); err != nil {
		return 0, false, err
	}
	return count, count <= limit, nil
}

// DeleteOldEmbedRates 清理已经结束的限流窗口（只保留当前及以后的窗口）。
func (s *Store) DeleteOldEmbedRates(ctx context.Context, keepFrom time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM embed_rates WHERE window_start < ?", keepFrom.UTC().Truncate(time.Millisecond))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
