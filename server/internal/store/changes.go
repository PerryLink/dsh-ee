package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	ChangeConfig    = "config"
	ChangeUserRoles = "user_roles"

	ChangePending  = "pending"
	ChangeApplied  = "applied"
	ChangeRejected = "rejected"
	ChangeFailed   = "failed"
)

type ChangeRequest struct {
	ID        int64      `json:"id"`
	Kind      string     `json:"kind"`
	Title     string     `json:"title"`
	Payload   string     `json:"payload"`
	Before    string     `json:"-"`
	Status    string     `json:"status"`
	Note      string     `json:"note"`
	Error     string     `json:"error"`
	CreatedBy int64      `json:"created_by"`
	ViaToken  bool       `json:"via_token"`
	DecidedBy *int64     `json:"decided_by"`
	CreatedAt time.Time  `json:"created_at"`
	DecidedAt *time.Time `json:"decided_at"`
}

const changeCols = "id, kind, title, payload, before_snap, status, note, error, created_by, via_token, decided_by, created_at, decided_at"

func scanChange(row interface{ Scan(...any) error }) (*ChangeRequest, error) {
	var c ChangeRequest
	var before sql.NullString
	var by sql.NullInt64
	var at sql.NullTime
	if err := row.Scan(&c.ID, &c.Kind, &c.Title, &c.Payload, &before, &c.Status, &c.Note, &c.Error, &c.CreatedBy, &c.ViaToken, &by, &c.CreatedAt, &at); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	c.Before = before.String
	if by.Valid {
		c.DecidedBy = &by.Int64
	}
	if at.Valid {
		c.DecidedAt = &at.Time
	}
	return &c, nil
}

func (s *Store) CreateChangeRequest(ctx context.Context, c *ChangeRequest) error {
	c.CreatedAt, c.Status = now(), ChangePending
	res, err := s.db.ExecContext(ctx, "INSERT INTO change_requests (kind, title, payload, status, note, created_by, via_token, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		c.Kind, c.Title, c.Payload, c.Status, c.Note, c.CreatedBy, c.ViaToken, c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

func (s *Store) GetChangeRequest(ctx context.Context, id int64) (*ChangeRequest, error) {
	return scanChange(s.db.QueryRowContext(ctx, "SELECT "+changeCols+" FROM change_requests WHERE id = ?", id))
}

func (s *Store) ListChangeRequests(ctx context.Context, limit int) ([]*ChangeRequest, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+changeCols+" FROM change_requests ORDER BY id DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ChangeRequest{}
	for rows.Next() {
		c, err := scanChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ClaimChangeRequest 把一条待批的占住（改成 status），并发的两次批准只有一次能成功。
func (s *Store) ClaimChangeRequest(ctx context.Context, id, by int64, status string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE change_requests SET status = ?, decided_by = ?, decided_at = ? WHERE id = ? AND status = ?", status, by, now(), id, ChangePending)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// FinishChangeRequest 记下执行的结果和执行前的样子。
func (s *Store) FinishChangeRequest(ctx context.Context, id int64, status, before, errMsg string) error {
	if len(errMsg) > 900 {
		errMsg = errMsg[:900]
	}
	_, err := s.db.ExecContext(ctx, "UPDATE change_requests SET status = ?, before_snap = ?, error = ? WHERE id = ?", status, nullStr(before), errMsg, id)
	return err
}

// ---- 恢复账号 ----

func (s *Store) AddRecoveryAccount(ctx context.Context, userID int64, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO recovery_accounts (user_id, expires_at, created_at) VALUES (?, ?, ?)", userID, expires, now())
	return err
}

// ExpiredRecoveryAccounts 列出过期了的恢复账号并从表里删掉。
func (s *Store) ExpiredRecoveryAccounts(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT user_id FROM recovery_accounts WHERE expires_at < ?", now())
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM recovery_accounts WHERE user_id = ?", id); err != nil {
			return nil, err
		}
	}
	return ids, rows.Err()
}

// IsRecoveryAccount 报告这个人是不是还没过期的恢复账号。
func (s *Store) IsRecoveryAccount(ctx context.Context, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM recovery_accounts WHERE user_id = ? AND expires_at > ?", userID, now()).Scan(&n)
	return n > 0, err
}
