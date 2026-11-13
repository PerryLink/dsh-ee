package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// ---- 跨站嵌入 v1 的一次性 code（embed_codes）----
//
// 放数据库而不是进程内 map：多实例部署时 A 实例 mint、B 实例 exchange 也能成功。
// 一次性由 ConsumeEmbedCode 的条件 UPDATE（affected rows = 1）保证：两个 exchange
// 同时进来时只有一个能把 consumed_at 从 NULL 改掉。只存 sha256，明文只在 mint 响应里出现一次。

// ErrEmbedCodeExpired 表示 code 存在但已经过了有效期；错误码要和「无效/已被使用」区分开。
var ErrEmbedCodeExpired = errors.New("embed code expired")

// EmbedCode 是一个等待 exchange 的一次性登录码（已绑定用户 + 应用 + 宿主来源 + 落地路径）。
type EmbedCode struct {
	CodeHash   string
	UserID     int64
	AppSlug    string
	AppTitle   string
	HostOrigin string
	Next       string
	ExpiresAt  time.Time
	CreatedAt  time.Time
	ConsumedAt time.Time
}

// CreateEmbedCode 保存一个一次性 code。codeHash 是明文的 sha256，库里不存明文。
func (s *Store) CreateEmbedCode(ctx context.Context, c EmbedCode) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO embed_codes (code_hash, user_id, app_slug, app_title, host_origin, next_path, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		c.CodeHash, c.UserID, c.AppSlug, c.AppTitle, c.HostOrigin, c.Next,
		c.ExpiresAt.UTC().Truncate(time.Millisecond), now())
	return err
}

// ConsumeEmbedCode 原子地消费一个 code，并返回它绑定的用户/应用/来源/落地路径。
// 过期返回 ErrEmbedCodeExpired；不存在、已被使用、或并发时被别的请求抢走返回 ErrNotFound。
func (s *Store) ConsumeEmbedCode(ctx context.Context, codeHash string) (*EmbedCode, error) {
	c := &EmbedCode{CodeHash: codeHash}
	var consumed sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT user_id, app_slug, app_title, host_origin, next_path, expires_at, created_at, consumed_at
		 FROM embed_codes WHERE code_hash = ?`, codeHash).
		Scan(&c.UserID, &c.AppSlug, &c.AppTitle, &c.HostOrigin, &c.Next, &c.ExpiresAt, &c.CreatedAt, &consumed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// 先 SELECT 是为了区分 expired / invalid；一次性仍由下面的条件 UPDATE 裁决。
	if consumed.Valid {
		return nil, ErrNotFound
	}
	t := now()
	if !c.ExpiresAt.After(t) {
		return nil, ErrEmbedCodeExpired
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE embed_codes SET consumed_at = ? WHERE code_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
		t, codeHash, t)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		// 另一个实例/请求先消费了（或刚好在这两条 SQL 之间过期）。
		return nil, ErrNotFound
	}
	c.ConsumedAt = t
	return c, nil
}

// DeleteExpiredEmbedCodes 清理过期的一次性 code（已消费的码过了有效期也一起删）。
func (s *Store) DeleteExpiredEmbedCodes(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM embed_codes WHERE expires_at <= ?", now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
