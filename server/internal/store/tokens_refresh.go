package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// 刷新令牌（docs/token-lifecycle.md §3）：OAuth 风格的一次性轮换 + 复用检测 + 族吊销。
//
// 约定：
//   - 只存 sha256（secret_hash），明文只在签发/轮换的响应里出现一次；
//   - family_id 串起同一次登录/签发出来的所有代际（access token 与 refresh token 都有）；
//   - 旧 refresh 再次出现 = 复用：吊销整个族（所有 access + refresh）并记审计；
//   - 开启宽限（DSH_EE_REFRESH_GRACE）时，轮换后短时间内的同一枚旧令牌重试会原样返回
//     上次的加密响应（successor_sealed），避免客户端丢响应时把整族吊销。

var (
	// ErrRefreshInvalid 表示这枚 refresh token 不可用（不存在 / 已吊销 / 账号停用）。
	// 调用方对外一律返回 invalid_grant，不区分原因，避免泄漏令牌是否存在。
	ErrRefreshInvalid = errors.New("refresh token 不可用")
	// ErrRefreshExpired 表示 refresh token 或它所属的令牌族已经过期。
	ErrRefreshExpired = errors.New("refresh token 已过期")
	// ErrRefreshClientKind 表示请求声明的 client_kind 与令牌族不一致。
	ErrRefreshClientKind = errors.New("client_kind 与 refresh token 不一致")
)

// RefreshToken 是一枚 refresh token 的库内记录。
type RefreshToken struct {
	ID              int64
	FamilyID        string
	AccessTokenID   int64
	UserID          int64
	ClientKind      string
	SecretHash      string
	IssuedAt        time.Time
	ExpiresAt       time.Time
	FamilyExpiresAt time.Time
	RotatedAt       *time.Time
	RevokedAt       *time.Time
	ReuseDetectedAt *time.Time
	CreatedIP       string
	SuccessorSealed []byte
}

const refreshTokenColumns = `id, family_id, access_token_id, user_id, client_kind, secret_hash,
	issued_at, expires_at, family_expires_at, rotated_at, revoked_at, reuse_detected_at, created_ip, successor_sealed`

func scanRefreshToken(row interface{ Scan(...any) error }) (*RefreshToken, error) {
	var rt RefreshToken
	var rotated, revoked, reuse sql.NullTime
	var sealed []byte
	err := row.Scan(&rt.ID, &rt.FamilyID, &rt.AccessTokenID, &rt.UserID, &rt.ClientKind, &rt.SecretHash,
		&rt.IssuedAt, &rt.ExpiresAt, &rt.FamilyExpiresAt, &rotated, &revoked, &reuse, &rt.CreatedIP, &sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rt.ClientKind = NormalizeClientKind(rt.ClientKind)
	if rotated.Valid {
		rt.RotatedAt = &rotated.Time
	}
	if revoked.Valid {
		rt.RevokedAt = &revoked.Time
	}
	if reuse.Valid {
		rt.ReuseDetectedAt = &reuse.Time
	}
	rt.SuccessorSealed = sealed
	return &rt, nil
}

// NewRefreshFamilyID 生成 32 个十六进制字符的令牌族 id（CHAR(32)）。
func NewRefreshFamilyID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 在受支持的平台上不会失败
	}
	return hex.EncodeToString(b)
}

func insertRefreshToken(ctx context.Context, db execContext, rt *RefreshToken) error {
	rt.ClientKind = NormalizeClientKind(rt.ClientKind)
	if rt.IssuedAt.IsZero() {
		rt.IssuedAt = now()
	}
	res, err := db.ExecContext(ctx, `INSERT INTO token_refresh_tokens
		(family_id, access_token_id, user_id, client_kind, secret_hash, issued_at, expires_at, family_expires_at,
		 rotated_at, revoked_at, reuse_detected_at, created_ip, successor_sealed)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rt.FamilyID, rt.AccessTokenID, rt.UserID, rt.ClientKind, rt.SecretHash, rt.IssuedAt, rt.ExpiresAt,
		rt.FamilyExpiresAt, rt.RotatedAt, rt.RevokedAt, rt.ReuseDetectedAt, rt.CreatedIP, rt.SuccessorSealed)
	if err != nil {
		return err
	}
	rt.ID, err = res.LastInsertId()
	return err
}

// CreateAccessTokenWithRefresh 在同一个事务里签出 access token 与它的第一枚 refresh token。
// 只在开启 refresh（DSH_EE_REFRESH_TOKENS）时调用；rt 为 nil 时等价于 CreateAccessToken。
func (s *Store) CreateAccessTokenWithRefresh(ctx context.Context, t *AccessToken, tokenHash string, rt *RefreshToken) error {
	if rt == nil {
		return s.CreateAccessToken(ctx, t, tokenHash)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t.FamilyID = &rt.FamilyID
	if err := createAccessToken(ctx, tx, t, tokenHash); err != nil {
		return err
	}
	rt.AccessTokenID = t.ID
	rt.UserID = t.UserID
	rt.ClientKind = t.ClientKind
	if rt.IssuedAt.IsZero() {
		rt.IssuedAt = now()
	}
	if err := insertRefreshToken(ctx, tx, rt); err != nil {
		return err
	}
	return tx.Commit()
}

// CreateAccessTokenWithRefreshTx 供已经开了事务的路径（设备登录批准）使用。
func createAccessTokenWithRefreshTx(ctx context.Context, tx *sql.Tx, t *AccessToken, tokenHash string, rt *RefreshToken) error {
	if rt == nil {
		return createAccessToken(ctx, tx, t, tokenHash)
	}
	t.FamilyID = &rt.FamilyID
	if err := createAccessToken(ctx, tx, t, tokenHash); err != nil {
		return err
	}
	rt.AccessTokenID = t.ID
	rt.UserID = t.UserID
	rt.ClientKind = t.ClientKind
	if rt.IssuedAt.IsZero() {
		rt.IssuedAt = now()
	}
	return insertRefreshToken(ctx, tx, rt)
}

// RefreshGrant 是一次 refresh 轮换要写库的全部内容。明文令牌在 API 层生成，store 只接收哈希；
// SealSuccessor 在成功轮换后（同一事务里）被调用，用来把这次响应加密后存在旧行上供宽限期重试。
type RefreshGrant struct {
	// OldHash 是客户端提交的 refresh token 的 sha256
	OldHash string
	// DeclaredClientKind 是请求里声明的 client_kind；空表示不校验
	DeclaredClientKind string
	IP                 string
	// 新 access token 的哈希与展示前缀（明文只在 API 层出现）
	NewAccessHash   string
	NewAccessPrefix string
	// 新 refresh token 的 sha256
	NewRefreshHash string
	// 新 access token 的有效期；0 按 1 小时
	AccessTTL time.Duration
	// 新 refresh token 的有效期；受族绝对上限约束；0 按 30 天
	RefreshTTL time.Duration
	// 轮换宽限：>0 时允许旧令牌在轮换后这么久内重试并原样返回上次响应；0 表示严格（重试=复用）
	Grace time.Duration
	// SealSuccessor 在成功轮换的事务里调用；Grace 为 0 或为 nil 时不保存重放密文
	SealSuccessor func(RefreshRotation) ([]byte, error)
}

// RefreshRotation 是一次轮换的结果。
type RefreshRotation struct {
	// Replayed：宽限期内重试，直接把 ReplaySealed 解封后返回，没有写任何新记录
	Replayed     bool
	ReplaySealed []byte
	// SuccessorSealed 是这次轮换保存的重放密文（宽限期为 0 时为空）；
	// 首次成功的响应也用解封后的它，保证与宽限期重试逐字节一致。
	SuccessorSealed []byte
	// Reused：旧令牌被复用，已吊销整个族
	Reused         bool
	FamilyID       string
	OldRefreshID   int64
	UserID         int64
	ClientKind     string
	ClientName     string
	Scopes         []string
	AccessID       int64
	RefreshID      int64
	AccessExpires  time.Time
	RefreshExpires time.Time
	RevokedAccess  int64
	RevokedRefresh int64
}

// revokeRefreshFamilyTx 吊销一个令牌族的全部 refresh token 与 access token，返回吊销数量。
func revokeRefreshFamilyTx(ctx context.Context, tx *sql.Tx, familyID string, t time.Time) (accessN, refreshN int64, err error) {
	if familyID == "" {
		return 0, 0, nil
	}
	res, err := tx.ExecContext(ctx, "UPDATE token_refresh_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL", t, familyID)
	if err != nil {
		return 0, 0, err
	}
	refreshN, _ = res.RowsAffected()
	res, err = tx.ExecContext(ctx, "UPDATE access_tokens SET revoked_at = ? WHERE family_id = ? AND revoked_at IS NULL", t, familyID)
	if err != nil {
		return 0, 0, err
	}
	accessN, _ = res.RowsAffected()
	return accessN, refreshN, nil
}

// RotateRefreshToken 在一个事务里完成一次性轮换：
//
//   - 旧 refresh 不存在/已吊销/账号停用 → ErrRefreshInvalid；
//   - 旧 refresh 已过期 → ErrRefreshExpired；
//   - 旧 refresh 已轮换过：宽限期内重试 → 返回 Replayed（不写库）；否则按复用处理，
//     吊销整个族并返回 Reused；
//   - 正常：锁定旧行 → 标记 rotated_at → 新建 access + refresh → 可选保存重放密文。
//
// 开头的 SELECT … FOR UPDATE + rotated_at 的 CAS 保证并发轮换只有一次成功。
func (s *Store) RotateRefreshToken(ctx context.Context, g RefreshGrant) (*RefreshRotation, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	old, err := scanRefreshToken(tx.QueryRowContext(ctx,
		"SELECT "+refreshTokenColumns+" FROM token_refresh_tokens WHERE secret_hash = ? FOR UPDATE", g.OldHash))
	if err != nil {
		return nil, err
	}
	nowT := now()
	switch {
	case old.RevokedAt != nil:
		return nil, ErrRefreshInvalid
	case old.RotatedAt != nil:
		// 轮换宽限：刚换完、客户端可能没收到响应就重试了。解封上次响应原样返回；
		// 宽限期外仍然出现 = 旧令牌泄漏/重放，按复用吊销整族。
		if g.Grace > 0 && nowT.Sub(*old.RotatedAt) <= g.Grace && len(old.SuccessorSealed) > 0 {
			return &RefreshRotation{Replayed: true, ReplaySealed: old.SuccessorSealed, FamilyID: old.FamilyID}, nil
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE token_refresh_tokens SET reuse_detected_at = ? WHERE id = ? AND reuse_detected_at IS NULL", nowT, old.ID); err != nil {
			return nil, err
		}
		accessN, refreshN, err := revokeRefreshFamilyTx(ctx, tx, old.FamilyID, nowT)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &RefreshRotation{
			Reused: true, FamilyID: old.FamilyID, OldRefreshID: old.ID, UserID: old.UserID,
			ClientKind: old.ClientKind, RevokedAccess: accessN, RevokedRefresh: refreshN,
		}, nil
	}
	if !old.ExpiresAt.After(nowT) || !old.FamilyExpiresAt.After(nowT) {
		return nil, ErrRefreshExpired
	}
	kind := NormalizeClientKind(old.ClientKind)
	if declared := strings.TrimSpace(g.DeclaredClientKind); declared != "" && NormalizeClientKind(declared) != kind {
		return nil, ErrRefreshClientKind
	}
	// 账号停用后 refresh 立即失效（与 access token 的判定一致）
	var disabled bool
	err = tx.QueryRowContext(ctx, "SELECT disabled FROM users WHERE id = ?", old.UserID).Scan(&disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRefreshInvalid
	}
	if err != nil {
		return nil, err
	}
	if disabled {
		return nil, ErrRefreshInvalid
	}
	// 刷新出来的 access token 继承原令牌的属性（scope、额度、模型、客户端信息、来源），
	// 但管理模式不继承：admin范围要本人重新开启，避免一次续期把管理权限无限延长。
	base, err := scanAccessToken(tx.QueryRowContext(ctx,
		"SELECT "+accessTokenColumns+" FROM access_tokens t WHERE t.id = ?", old.AccessTokenID))
	if err != nil {
		return nil, err
	}
	if base.RevokedAt != nil {
		return nil, ErrRefreshInvalid
	}
	accessTTL := g.AccessTTL
	if accessTTL <= 0 {
		accessTTL = time.Hour
	}
	refreshTTL := g.RefreshTTL
	if refreshTTL <= 0 {
		refreshTTL = 30 * 24 * time.Hour
	}
	accessExpires := nowT.Add(accessTTL).Truncate(time.Millisecond)
	refreshExpires := nowT.Add(refreshTTL)
	if refreshExpires.After(old.FamilyExpiresAt) {
		refreshExpires = old.FamilyExpiresAt
	}
	if !refreshExpires.After(nowT) {
		return nil, ErrRefreshExpired
	}
	scopes := make([]string, 0, len(base.Scopes))
	for _, scope := range base.Scopes {
		if scope != "admin" {
			scopes = append(scopes, scope)
		}
	}
	familyID := old.FamilyID
	newAccess := &AccessToken{
		UserID: base.UserID, Name: base.Name, Prefix: g.NewAccessPrefix, Source: base.Source,
		ClientKind: kind, FamilyID: &familyID, Scopes: scopes,
		MonthlyTokenLimit: base.MonthlyTokenLimit, AllowedModels: base.AllowedModels,
		ClientName: base.ClientName, ClientOS: base.ClientOS, ClientVersion: base.ClientVersion,
		ExpiresAt: &accessExpires, CreatedBy: base.CreatedBy,
	}
	newRefresh := &RefreshToken{
		FamilyID: familyID, UserID: old.UserID, ClientKind: kind, SecretHash: g.NewRefreshHash,
		IssuedAt: nowT, ExpiresAt: refreshExpires, FamilyExpiresAt: old.FamilyExpiresAt, CreatedIP: g.IP,
	}
	result := &RefreshRotation{
		FamilyID: familyID, OldRefreshID: old.ID, UserID: base.UserID, ClientKind: kind,
		ClientName: base.ClientName, Scopes: scopes, AccessExpires: accessExpires, RefreshExpires: refreshExpires,
	}
	// 先占用旧行（CAS），并发请求即使绕过行锁也会在这里失败
	res, err := tx.ExecContext(ctx,
		"UPDATE token_refresh_tokens SET rotated_at = ? WHERE id = ? AND rotated_at IS NULL AND revoked_at IS NULL", nowT, old.ID)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return nil, ErrRefreshInvalid
	}
	if err := createAccessToken(ctx, tx, newAccess, g.NewAccessHash); err != nil {
		return nil, err
	}
	newRefresh.AccessTokenID = newAccess.ID
	if err := insertRefreshToken(ctx, tx, newRefresh); err != nil {
		return nil, err
	}
	result.AccessID, result.RefreshID = newAccess.ID, newRefresh.ID
	if g.Grace > 0 && g.SealSuccessor != nil {
		sealed, err := g.SealSuccessor(*result)
		if err != nil {
			return nil, err
		}
		if len(sealed) > 0 {
			if _, err := tx.ExecContext(ctx,
				"UPDATE token_refresh_tokens SET successor_sealed = ? WHERE id = ?", sealed, old.ID); err != nil {
				return nil, err
			}
			result.SuccessorSealed = sealed
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return result, nil
}

// RevokeRefreshFamily 吊销一个令牌族的所有 refresh token 与 access token，返回吊销数量。
// 手动吊销 access token 时（RevokeToken / 批量吊销）自动联动，不需要单独调用。
func (s *Store) RevokeRefreshFamily(ctx context.Context, familyID string) (accessN, refreshN int64, err error) {
	if familyID == "" {
		return 0, 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	accessN, refreshN, err = revokeRefreshFamilyTx(ctx, tx, familyID, now())
	if err != nil {
		return 0, 0, err
	}
	return accessN, refreshN, tx.Commit()
}

// ClearRefreshSuccessors 清掉轮换宽限已过的重放密文；后台定时任务调用。
func (s *Store) ClearRefreshSuccessors(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE token_refresh_tokens SET successor_sealed = NULL WHERE successor_sealed IS NOT NULL AND rotated_at IS NOT NULL AND rotated_at < ?", before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteExpiredRefreshTokens 清理过期且已经不可能再用到的 refresh 记录；后台定时任务调用。
// 保留 rotated/revoked 记录直到过期，之后删掉（access token 的记录不受影响）。
func (s *Store) DeleteExpiredRefreshTokens(ctx context.Context, before time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		"DELETE FROM token_refresh_tokens WHERE expires_at <= ? AND (rotated_at IS NOT NULL OR revoked_at IS NOT NULL)", before)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
