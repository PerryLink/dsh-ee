package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 身份的来历（user_identities.via）。
const (
	// IdentityHome 是账号主身份的镜像，以 users.external_dn 为准
	IdentityHome = "home"
	// IdentityExplicit 是员工登录后自己绑定的
	IdentityExplicit = "explicit"
	// IdentityMobile / IdentityEmail 是按目录里核实过的手机号 / 企业邮箱自动关联的
	IdentityMobile = "mobile"
	IdentityEmail  = "email"
)

// Identity 是账号带的一个飞书或钉钉身份。
type Identity struct {
	ID         int64     `json:"id"`
	UserID     int64     `json:"user_id"`
	Provider   string    `json:"provider"`
	Subject    string    `json:"subject"`
	Via        string    `json:"via"`
	MobileHash string    `json:"-"`
	EmailHash  string    `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
}

const identityColumns = "id, user_id, provider, subject, via, mobile_hash, email_hash, created_at"

func scanIdentity(row interface{ Scan(...any) error }) (*Identity, error) {
	var i Identity
	if err := row.Scan(&i.ID, &i.UserID, &i.Provider, &i.Subject, &i.Via, &i.MobileHash, &i.EmailHash, &i.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &i, nil
}

func (s *Store) listIdentities(ctx context.Context, where string, args ...any) ([]*Identity, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+identityColumns+" FROM user_identities WHERE "+where+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Identity
	for rows.Next() {
		i, err := scanIdentity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RecordHomeIdentity 记下账号主身份和这次从目录里核实到的联系方式摘要；摘要为空表示这次没拿到，保留原值。
// 主身份以 users.external_dn 为准，这一行只是为了能按联系方式找到它。
func (s *Store) RecordHomeIdentity(ctx context.Context, userID int64, provider, subject, mobileHash, emailHash string) error {
	t := now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_identities (user_id, provider, subject, via, mobile_hash, email_hash, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, userID, provider, subject, IdentityHome, mobileHash, emailHash, t, t)
	if !isDuplicate(err) {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE user_identities SET subject=?, via=?,
		mobile_hash=COALESCE(NULLIF(?, ''), mobile_hash), email_hash=COALESCE(NULLIF(?, ''), email_hash), updated_at=?
		WHERE user_id=? AND provider=?`, subject, IdentityHome, mobileHash, emailHash, t, userID, provider)
	if isDuplicate(err) {
		return ErrDuplicate
	}
	return err
}

// LinkIdentity 给账号关联一个身份。这个身份已经在别处、或账号已有同一来源的身份时返回 ErrDuplicate。
func (s *Store) LinkIdentity(ctx context.Context, userID int64, provider, subject, via, mobileHash, emailHash string) error {
	t := now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO user_identities (user_id, provider, subject, via, mobile_hash, email_hash, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)`, userID, provider, subject, via, mobileHash, emailHash, t, t)
	if isDuplicate(err) {
		return ErrDuplicate
	}
	return err
}

// GetLinkedIdentity 按身份找关联（不含主身份的镜像行：主身份按 users.external_dn 找）。
func (s *Store) GetLinkedIdentity(ctx context.Context, subject string) (*Identity, error) {
	return scanIdentity(s.db.QueryRowContext(ctx, "SELECT "+identityColumns+" FROM user_identities WHERE subject = ? AND via <> ?", subject, IdentityHome))
}

func (s *Store) ListUserIdentities(ctx context.Context, userID int64) ([]*Identity, error) {
	return s.listIdentities(ctx, "user_id = ?", userID)
}

// ListLinkedIdentities 列出某个来源的全部关联身份（不含主身份）。
func (s *Store) ListLinkedIdentities(ctx context.Context, provider string) ([]*Identity, error) {
	return s.listIdentities(ctx, "provider = ? AND via <> ?", provider, IdentityHome)
}

// TouchLinkedIdentity 更新关联身份的联系方式摘要；为空的保留原值。
func (s *Store) TouchLinkedIdentity(ctx context.Context, id int64, mobileHash, emailHash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE user_identities SET mobile_hash=COALESCE(NULLIF(?, ''), mobile_hash),
		email_hash=COALESCE(NULLIF(?, ''), email_hash), updated_at=? WHERE id=?`, mobileHash, emailHash, now(), id)
	return err
}

// DeleteLinkedIdentity 解除账号在某个来源上的关联身份，返回是否真的删了一行。
func (s *Store) DeleteLinkedIdentity(ctx context.Context, userID int64, provider string) (bool, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM user_identities WHERE user_id = ? AND provider = ? AND via <> ?", userID, provider, IdentityHome)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteUserIdentities 删掉账号的全部身份记录（管理员解除身份绑定时用）。
func (s *Store) DeleteUserIdentities(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM user_identities WHERE user_id = ?", userID)
	return err
}

// IdentityMatch 是按联系方式摘要找到的一个候选账号。
type IdentityMatch struct {
	UserID int64
	// 命中的是 mobile 还是 email
	By string
}

// MatchHomeIdentities 按核实过的联系方式摘要，在「另一个来源」的主身份里找候选账号。
// 只认仍然和 users.external_dn 对得上的主身份行；空摘要不参与匹配。
func (s *Store) MatchHomeIdentities(ctx context.Context, exceptProvider, mobileHash, emailHash string) ([]IdentityMatch, error) {
	if mobileHash == "" && emailHash == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT i.user_id, i.mobile_hash, i.email_hash FROM user_identities i
		JOIN users u ON u.id = i.user_id
		WHERE i.provider <> ? AND i.via = ? AND u.external_dn = i.subject
		AND ((? <> '' AND i.mobile_hash = ?) OR (? <> '' AND i.email_hash = ?)) ORDER BY i.id`,
		exceptProvider, IdentityHome, mobileHash, mobileHash, emailHash, emailHash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []IdentityMatch
	for rows.Next() {
		var m IdentityMatch
		var mobile, email string
		if err := rows.Scan(&m.UserID, &mobile, &email); err != nil {
			return nil, err
		}
		if mobileHash != "" && mobile == mobileHash {
			out = append(out, IdentityMatch{UserID: m.UserID, By: IdentityMobile})
		}
		if emailHash != "" && email == emailHash {
			out = append(out, IdentityMatch{UserID: m.UserID, By: IdentityEmail})
		}
	}
	return out, rows.Err()
}
