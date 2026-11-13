package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// TrustedSigningKey 是受信任的扩展签名公钥（docs/plugins.md §9.3、docs/plugin-signing.md §3）。
// 撤销、换下都保留记录（不删行）：验签时据此区分 active / retired / revoked，
// 让历史签名还能核对，撤销之后用这个签名的新包不能再装。
type TrustedSigningKey struct {
	// 公钥的 sha256 前 16 位十六进制，和 extension.KeyID 一致
	KeyID string `json:"key_id"`
	// PEM：PUBLIC KEY 或 CERTIFICATE（不收私钥）
	PublicKey string `json:"public_key"`
	// 用途；现在只有 extension
	Purpose string `json:"purpose"`
	// 从什么时候开始信任
	ValidFrom time.Time `json:"valid_from"`
	// 换下：不再用于新签名，历史签名仍认
	RetiredAt *time.Time `json:"retired_at,omitempty"`
	// 撤销：等于不信任；历史签名显示 revoked，新装 / 升级拒绝
	RevokedAt *time.Time `json:"revoked_at,omitempty"`
	CreatedBy *int64     `json:"created_by,omitempty"`
	Note      string     `json:"note"`
	CreatedAt time.Time  `json:"created_at"`
}

const signingKeyCols = "key_id, public_key, purpose, valid_from, retired_at, revoked_at, created_by, note, created_at"

func scanSigningKey(row interface{ Scan(...any) error }) (*TrustedSigningKey, error) {
	var k TrustedSigningKey
	var retired, revoked sql.NullTime
	var by sql.NullInt64
	if err := row.Scan(&k.KeyID, &k.PublicKey, &k.Purpose, &k.ValidFrom, &retired, &revoked, &by, &k.Note, &k.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if retired.Valid {
		at := retired.Time
		k.RetiredAt = &at
	}
	if revoked.Valid {
		at := revoked.Time
		k.RevokedAt = &at
	}
	if by.Valid {
		id := by.Int64
		k.CreatedBy = &id
	}
	return &k, nil
}

// ListTrustedSigningKeys 列出所有受信任的签名公钥，包括已经换下、撤销的（保留历史）。
func (s *Store) ListTrustedSigningKeys(ctx context.Context) ([]TrustedSigningKey, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+signingKeyCols+" FROM trusted_signing_keys ORDER BY created_at DESC, key_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrustedSigningKey{}
	for rows.Next() {
		k, err := scanSigningKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

// TrustedSigningKey 按 key id 取一把。
func (s *Store) TrustedSigningKey(ctx context.Context, keyID string) (*TrustedSigningKey, error) {
	return scanSigningKey(s.db.QueryRowContext(ctx, "SELECT "+signingKeyCols+" FROM trusted_signing_keys WHERE key_id = ?", keyID))
}

// CreateTrustedSigningKey 新增一把受信任公钥；key id 已经存在时返回 ErrDuplicate。
// 撤销过的钥匙不能再加回来：撤销是信任根上的决定，要恢复信任请加一把新钥匙。
func (s *Store) CreateTrustedSigningKey(ctx context.Context, k *TrustedSigningKey) error {
	t := now()
	if k.Purpose == "" {
		k.Purpose = "extension"
	}
	if k.ValidFrom.IsZero() {
		k.ValidFrom = t
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO trusted_signing_keys
		(key_id, public_key, purpose, valid_from, retired_at, revoked_at, created_by, note, created_at)
		VALUES (?, ?, ?, ?, NULL, NULL, ?, ?, ?)`,
		k.KeyID, k.PublicKey, k.Purpose, k.ValidFrom, k.CreatedBy, k.Note, t)
	if isDuplicate(err) {
		return ErrDuplicate
	}
	if err != nil {
		return err
	}
	k.CreatedAt = t
	return nil
}

// RetireTrustedSigningKey 把一把公钥换下：不再用于新签名，历史签名仍认。
// 已经撤销的不再换下（返回 false）；已经换下的幂等（返回 false）。真正改了返回 true。
func (s *Store) RetireTrustedSigningKey(ctx context.Context, keyID string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE trusted_signing_keys SET retired_at = ? WHERE key_id = ? AND retired_at IS NULL AND revoked_at IS NULL", at, keyID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return true, nil
	}
	// 没改：要区分「不存在」「已换下」「已撤销」
	if _, err := s.TrustedSigningKey(ctx, keyID); err != nil {
		return false, err
	}
	return false, nil
}

// RevokeTrustedSigningKey 撤销一把公钥：等于不再信任，历史签名显示 revoked，新装 / 升级拒绝。
// 已经撤销的幂等（返回 false）。真正改了返回 true。
func (s *Store) RevokeTrustedSigningKey(ctx context.Context, keyID string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, "UPDATE trusted_signing_keys SET revoked_at = ? WHERE key_id = ? AND revoked_at IS NULL", at, keyID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		return true, nil
	}
	if _, err := s.TrustedSigningKey(ctx, keyID); err != nil {
		return false, err
	}
	return false, nil
}
