package store

import (
	"context"
	"time"
)

// ---- 管理端嵌入运行概览（api/embed_stats.go）----
//
// 只做数量聚合：没有用户、IP、令牌，也只有应用标识这种低基数维度（最多 top 10）。
// 会话表里只有活跃会话与保留期内的终态行（见 DeleteExpiredEmbedSessions），规模有界；
// embed.deny 按 audit_logs(action, created_at) 索引计数。调用方给短超时，慢查询不会拖住管理页。

// EmbedAppStat 是一个应用的活跃嵌入会话数。
type EmbedAppStat struct {
	App      string `json:"app"`
	Sessions int    `json:"sessions"`
}

// EmbedStats 是一段时间内的嵌入运行概览。
type EmbedStats struct {
	// 当前有效（未过期、未吊销）的嵌入会话数。
	ActiveSessions int
	// [dayStart, now) 内新建的会话数。
	CreatedToday int
	// 今天到期、且没有先被吊销的会话数。
	ExpiredToday int
	// 今天被吊销的会话数（logout / 改密 / 禁用）。
	RevokedToday int
	// 最近 24 小时被拒绝的嵌入请求数（audit denyAction）。
	Denied24h int
	// 活跃会话最多的应用（最多 10 个，按活跃数倒序）。
	TopApps []EmbedAppStat
}

// EmbedStats 聚合嵌入运行概览。dayStart 是统计口径的「今天零点」（调用方按时区换算成 UTC 时刻），
// denyAction 是审计里的拒绝动作名（api 层的 embed.deny）。
//
// 过期/吊销的行会按保留期延后清理（见 DeleteExpiredEmbedSessions），所以当天的
// 过期/吊销数在当天不会被后台清理提前抹掉；活跃会话的判定不受保留期影响。
func (s *Store) EmbedStats(ctx context.Context, dayStart, now time.Time, denyAction string) (*EmbedStats, error) {
	st := &EmbedStats{TopApps: []EmbedAppStat{}}
	err := s.db.QueryRowContext(ctx,
		`SELECT
		    COUNT(CASE WHEN expires_at > ? AND revoked_at IS NULL THEN 1 END),
		    COUNT(CASE WHEN created_at >= ? AND created_at < ? THEN 1 END),
		    COUNT(CASE WHEN expires_at >= ? AND expires_at < ? AND revoked_at IS NULL THEN 1 END),
		    COUNT(CASE WHEN revoked_at >= ? AND revoked_at < ? THEN 1 END)
		 FROM embed_sessions`,
		now, dayStart, now, dayStart, now, dayStart, now,
	).Scan(&st.ActiveSessions, &st.CreatedToday, &st.ExpiredToday, &st.RevokedToday)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT app_slug, COUNT(*) AS active
		 FROM embed_sessions
		 WHERE expires_at > ? AND revoked_at IS NULL
		 GROUP BY app_slug
		 ORDER BY active DESC, app_slug ASC
		 LIMIT 10`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var a EmbedAppStat
		if err := rows.Scan(&a.App, &a.Sessions); err != nil {
			return nil, err
		}
		st.TopApps = append(st.TopApps, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_logs WHERE action = ? AND created_at >= ?`,
		denyAction, now.Add(-24*time.Hour)).Scan(&st.Denied24h); err != nil {
		return nil, err
	}
	return st, nil
}

// CountActiveEmbedSessionsGlobal 返回所有用户当前有效的嵌入会话数（/metrics 用）。
// 多实例部署时这是全库共用的值：Prometheus 里应该取 max() 而不是 sum()，否则会按实例数放大。
func (s *Store) CountActiveEmbedSessionsGlobal(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM embed_sessions WHERE expires_at > ? AND revoked_at IS NULL`, now()).Scan(&n)
	return n, err
}
