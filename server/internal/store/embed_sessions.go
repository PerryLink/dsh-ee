package store

import (
	"context"
	"time"
)

// ---- 跨站嵌入 v1 的会话（embed_sessions）----
//
// 与网页会话（sessions）分开存：token 只存 sha256，明文只返回一次；会话固定是 limited
// （员工权限、无 keys.self），并且只能访问 api/embed.go 里的路由白名单。
//
// 会话本来就在数据库里，多实例共享读取；这里补上每用户会话上限与吊销（revoked_at）。

// CreateEmbedSession 保存一个嵌入会话。tokenHash 是 ee_emb_ 明文的 sha256。
// maxPerUser > 0 时执行每用户会话上限：超过就先删最旧的会话给新会话腾位置。
// 多实例并发 exchange 时上限是近似值（极端情况下可能短时多一条），但不会无限增长。
func (s *Store) CreateEmbedSession(ctx context.Context, tokenHash string, userID int64, appSlug, hostOrigin string, ttl time.Duration, maxPerUser int) error {
	t := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if maxPerUser > 0 {
		var active int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM embed_sessions WHERE user_id = ? AND expires_at > ? AND revoked_at IS NULL`,
			userID, t).Scan(&active); err != nil {
			return err
		}
		if active >= maxPerUser {
			rows, err := tx.QueryContext(ctx,
				`SELECT token_hash FROM embed_sessions
				 WHERE user_id = ? AND expires_at > ? AND revoked_at IS NULL
				 ORDER BY created_at ASC, token_hash ASC LIMIT ?`,
				userID, t, active-maxPerUser+1)
			if err != nil {
				return err
			}
			var evict []string
			for rows.Next() {
				var h string
				if err := rows.Scan(&h); err != nil {
					rows.Close()
					return err
				}
				evict = append(evict, h)
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return err
			}
			for _, h := range evict {
				if _, err := tx.ExecContext(ctx, "DELETE FROM embed_sessions WHERE token_hash = ?", h); err != nil {
					return err
				}
			}
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO embed_sessions (token_hash, user_id, app_slug, host_origin, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		tokenHash, userID, appSlug, hostOrigin, t.Add(ttl), t); err != nil {
		return err
	}
	return tx.Commit()
}

// EmbedSessionInfo 返回未过期、未吊销、账号可用的人类用户的嵌入会话。
func (s *Store) EmbedSessionInfo(ctx context.Context, tokenHash string) (*User, string, string, time.Time, error) {
	var appSlug, hostOrigin string
	var expiresAt time.Time
	u, err := scanUser(s.db.QueryRowContext(ctx,
		`SELECT `+userColumns("u.")+`, e.app_slug, e.host_origin, e.expires_at
		 FROM embed_sessions e JOIN users u ON u.id = e.user_id
		 WHERE e.token_hash = ? AND e.expires_at > ? AND e.revoked_at IS NULL
		   AND u.disabled = 0 AND u.account_type = 'human'`, tokenHash, now()), &appSlug, &hostOrigin, &expiresAt)
	return u, appSlug, hostOrigin, expiresAt, err
}

// CountActiveEmbedSessions 返回用户当前有效（未过期、未吊销）的嵌入会话数。
func (s *Store) CountActiveEmbedSessions(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM embed_sessions WHERE user_id = ? AND expires_at > ? AND revoked_at IS NULL`,
		userID, now()).Scan(&n)
	return n, err
}

// DeleteEmbedSession 撤销一个嵌入会话（logout 用）；返回是否真的撤销了。
// 用 revoked_at 软删除而不是物理删除：共享读取会立刻失效，审计/排查还能看到记录，
// 过期或被吊销的行由后台清理物理删除。
func (s *Store) DeleteEmbedSession(ctx context.Context, tokenHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE embed_sessions SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL", now(), tokenHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteUserEmbedSessions 撤销某个用户的全部嵌入会话（改密码、禁用、解绑身份时跟着失效）。
func (s *Store) DeleteUserEmbedSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE embed_sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL", now(), userID)
	return err
}

// embedSessionRetention 是过期/已吊销会话在表里保留的时长。
//
// 每小时清理如果立刻删掉终态行，管理端「运行概览」的今日新建/过期/吊销会在当天下午
// 就查不到上午的数据（默认会话 TTL 只有 2 小时，新建的行过期后马上被删）。保留 7 天
// 让按天聚合稳定，也超不过任何时区的一天；活跃会话的判定不受影响（过期/吊销本来就不算），
// 保留期外的行仍然物理删除，表不会无限增长。
const embedSessionRetention = 7 * 24 * time.Hour

// DeleteExpiredEmbedSessions 清理超过保留期的过期/已吊销嵌入会话（后台每小时跑一次）。
func (s *Store) DeleteExpiredEmbedSessions(ctx context.Context) (int64, error) {
	cutoff := now().Add(-embedSessionRetention)
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM embed_sessions WHERE expires_at <= ? OR (revoked_at IS NOT NULL AND revoked_at <= ?)", cutoff, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
