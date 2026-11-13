package store

import (
	"context"
	"time"
)

// ---- 跨站嵌入 v1 的会话（embed_sessions）----
//
// 与网页会话（sessions）分开存：token 只存 sha256，明文只返回一次；会话固定是 limited
// （员工权限、无 keys.self），并且只能访问 api/embed.go 里的路由白名单。

// CreateEmbedSession 保存一个嵌入会话。tokenHash 是 ee_emb_ 明文的 sha256。
func (s *Store) CreateEmbedSession(ctx context.Context, tokenHash string, userID int64, appSlug, hostOrigin string, ttl time.Duration) error {
	t := now()
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO embed_sessions (token_hash, user_id, app_slug, host_origin, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		tokenHash, userID, appSlug, hostOrigin, t.Add(ttl), t)
	return err
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

// DeleteEmbedSession 撤销一个嵌入会话；返回是否真的删掉了（logout 用）。
func (s *Store) DeleteEmbedSession(ctx context.Context, tokenHash string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM embed_sessions WHERE token_hash = ?", tokenHash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteUserEmbedSessions 删掉某个用户的全部嵌入会话（改密码、禁用、解绑身份时跟着失效）。
func (s *Store) DeleteUserEmbedSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM embed_sessions WHERE user_id = ?", userID)
	return err
}

// DeleteExpiredEmbedSessions 清理过期的嵌入会话（后台每小时跑一次）。
func (s *Store) DeleteExpiredEmbedSessions(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM embed_sessions WHERE expires_at <= ?", now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
