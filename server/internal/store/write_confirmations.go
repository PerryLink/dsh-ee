package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 写操作的服务端确认（docs/plugins.md §7.5）。

const (
	ConfirmPending  = "pending"
	ConfirmApproved = "approved"
	ConfirmRejected = "rejected"
	ConfirmUsed     = "used"
)

type WriteConfirmation struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"user_id"`
	AssetID   int64      `json:"asset_id"`
	Tool      string     `json:"tool"`
	ArgsHash  string     `json:"-"`
	Args      string     `json:"args"`
	Status    string     `json:"status"`
	Client    string     `json:"client"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`
	DecidedAt *time.Time `json:"decided_at"`
}

const confirmCols = "id, user_id, asset_id, tool, args_hash, args, status, client, created_at, expires_at, decided_at"

func scanConfirm(row interface{ Scan(...any) error }) (*WriteConfirmation, error) {
	var c WriteConfirmation
	var decided sql.NullTime
	if err := row.Scan(&c.ID, &c.UserID, &c.AssetID, &c.Tool, &c.ArgsHash, &c.Args, &c.Status, &c.Client, &c.CreatedAt, &c.ExpiresAt, &decided); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if decided.Valid {
		t := decided.Time
		c.DecidedAt = &t
	}
	return &c, nil
}

func (s *Store) CreateWriteConfirmation(ctx context.Context, c *WriteConfirmation, tokenHash string, ttl time.Duration) error {
	t := now()
	c.CreatedAt, c.ExpiresAt, c.Status = t, t.Add(ttl), ConfirmPending
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO write_confirmations (token_hash, user_id, asset_id, tool, args_hash, args, status, client, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tokenHash, c.UserID, c.AssetID, c.Tool, c.ArgsHash, c.Args, c.Status, c.Client, c.CreatedAt, c.ExpiresAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetWriteConfirmation(ctx context.Context, id int64) (*WriteConfirmation, error) {
	return scanConfirm(s.db.QueryRowContext(ctx, "SELECT "+confirmCols+" FROM write_confirmations WHERE id = ?", id))
}

// FindWriteConfirmation 找这个人对同一个工具、同样参数的一条还没过期的确认（状态是 status 的最新一条）。
func (s *Store) FindWriteConfirmation(ctx context.Context, userID, assetID int64, tool, argsHash, status string) (*WriteConfirmation, error) {
	return scanConfirm(s.db.QueryRowContext(ctx,
		"SELECT "+confirmCols+" FROM write_confirmations WHERE user_id = ? AND asset_id = ? AND tool = ? AND args_hash = ? AND status = ? AND expires_at > ? ORDER BY id DESC LIMIT 1",
		userID, assetID, tool, argsHash, status, now()))
}

// DecideWriteConfirmation 把一条待确认的改成同意或拒绝；同意后在 ttl 内可以执行一次。只有还在等、没过期的才改得动。
func (s *Store) DecideWriteConfirmation(ctx context.Context, id, userID int64, approve bool, ttl time.Duration) (bool, error) {
	status := ConfirmRejected
	if approve {
		status = ConfirmApproved
	}
	t := now()
	res, err := s.db.ExecContext(ctx,
		"UPDATE write_confirmations SET status = ?, decided_at = ?, expires_at = ? WHERE id = ? AND user_id = ? AND status = ? AND expires_at > ?",
		status, t, t.Add(ttl), id, userID, ConfirmPending, t)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UseWriteConfirmation 用掉一条已同意的确认。并发的两次调用只有一次能用掉。
func (s *Store) UseWriteConfirmation(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE write_confirmations SET status = ? WHERE id = ? AND status = ? AND expires_at > ?", ConfirmUsed, id, ConfirmApproved, now())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// PendingWriteConfirmations 列出这个人还在等确认的。
func (s *Store) PendingWriteConfirmations(ctx context.Context, userID int64) ([]*WriteConfirmation, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+confirmCols+" FROM write_confirmations WHERE user_id = ? AND status = ? AND expires_at > ? ORDER BY id DESC LIMIT 50", userID, ConfirmPending, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*WriteConfirmation{}
	for rows.Next() {
		c, err := scanConfirm(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// PurgeWriteConfirmations 删掉过期一天以上的记录（决定本身已经记在审计里）。
func (s *Store) PurgeWriteConfirmations(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM write_confirmations WHERE expires_at < ?", now().Add(-24*time.Hour))
	return err
}
