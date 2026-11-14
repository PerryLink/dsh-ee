package api

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// 管理端嵌入运行概览（系统设置 → 跨站嵌入 → 运行概览）。
//
// 只返回数量：没有用户、IP、令牌，也没有宿主 origin；应用维度最多 10 个低基数 slug。
// 数据全部由数据库聚合得出（多实例看到同一份），并用短超时保护管理页：
// 统计只是辅助信息，数据库慢的时候宁可 500 也不让设置页一直转圈。

// 一次统计的总预算；三个聚合查询都在这个 ctx 里。
const embedStatsTimeout = 2 * time.Second

// embedStatsDayStart 把「本地时区的今天零点」换算成 UTC 时刻。
// tz 是相对 UTC 的分钟数（东八区 480），与 /api/admin/stats 的口径一致；默认 0 即 UTC。
func embedStatsDayStart(now time.Time, tz int) time.Time {
	off := time.Duration(tz) * time.Minute
	local := now.Add(off)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(-off)
}

// GET /api/admin/embed/stats?tz=480：跨站嵌入运行概览（settings.manage）。
func (s *Server) handleAdminEmbedStats(w http.ResponseWriter, r *http.Request) {
	tz := 0
	if v := r.URL.Query().Get("tz"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < -14*60 || n > 14*60 {
			writeErr(w, http.StatusBadRequest, "tz 应是 -840 到 840 之间的分钟数")
			return
		}
		tz = n
	}
	now := time.Now().UTC()
	dayStart := embedStatsDayStart(now, tz)

	ctx, cancel := context.WithTimeout(r.Context(), embedStatsTimeout)
	defer cancel()
	stats, err := s.store.EmbedStats(ctx, dayStart, now, auditEmbedDeny)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"generated_at":    now,
		"day_start":       dayStart,
		"active_sessions": stats.ActiveSessions,
		"created_today":   stats.CreatedToday,
		"expired_today":   stats.ExpiredToday,
		"revoked_today":   stats.RevokedToday,
		"denied_24h":      stats.Denied24h,
		"top_apps":        stats.TopApps,
	})
}
