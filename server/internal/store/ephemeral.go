package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ---- 多实例共享的一次性/短时状态（migrations/0036_ephemeral_states.sql）----
//
// 进程内 map 在多实例部署时会丢：A 实例生成的一次性 code/state，B 实例处理回调或轮询时
// 找不到。这张通用表把 web link handoff、开始使用接入码、OIDC state/PKCE 这类状态放到
// 所有实例共享的数据库里：
//
//   - state_hash 存随机值的 sha256（库里没有明文凭证，明文只在生成方的响应/URL 里出现一次）；
//   - payload 是调用方决定的 JSON，敏感内容由 API 层用主密钥加密后再放进来；
//   - 一次性由 ConsumeEphemeralState 的条件 UPDATE（consumed_at IS NULL → now()）保证，
//     两个实例并发消费同一个状态时只有一个能把 consumed_at 写进去；
//   - 过期判断在读取/消费时做（expires_at），后台清理只回收空间，不参与正确性；
//   - owner_key 给「同一个属主只保留一条」的状态用（如每人一个接入码），NULL 表示不去重。
//
// 数据库不可用时这些状态全部不可用：调用方应按失败处理（fail closed），不要回退到进程内存，
// 否则多实例下会出现「A 实例能用、B 实例用不了」的不一致。

var (
	// ErrEphemeralStateExpired 表示状态存在但已经过了有效期；与「无效/已被使用」区分开。
	ErrEphemeralStateExpired = errors.New("ephemeral state expired")
	// ErrEphemeralStateExists 表示 (kind, state_hash) 已经存在，调用方通常换一个随机值重试。
	ErrEphemeralStateExists = errors.New("ephemeral state already exists")
)

// 与表结构一致的字段上限。
const (
	ephemeralKindMax  = 32
	ephemeralHashLen  = 64
	ephemeralOwnerMax = 191
)

// EphemeralState 是一条短时状态（kind + state_hash 唯一）。
type EphemeralState struct {
	Kind       string
	StateHash  string
	OwnerKey   string
	Payload    json.RawMessage
	ExpiresAt  time.Time
	CreatedAt  time.Time
	ConsumedAt *time.Time
}

// validateEphemeralKey 校验主键与属主字段的长度。
func validateEphemeralKey(kind, stateHash, ownerKey string) error {
	if kind == "" || len(kind) > ephemeralKindMax {
		return fmt.Errorf("状态 kind 必须在 1–%d 字节之间", ephemeralKindMax)
	}
	if len(stateHash) != ephemeralHashLen {
		return fmt.Errorf("状态哈希必须是 %d 个字符的 sha256", ephemeralHashLen)
	}
	if len(ownerKey) > ephemeralOwnerMax {
		return fmt.Errorf("状态属主必须在 0–%d 字节之间", ephemeralOwnerMax)
	}
	return nil
}

// validateEphemeralState 校验字段长度/JSON 合法性；payload 必须是非空 JSON。
func validateEphemeralState(kind, stateHash, ownerKey string, payload json.RawMessage) error {
	if err := validateEphemeralKey(kind, stateHash, ownerKey); err != nil {
		return err
	}
	if len(payload) == 0 {
		return errors.New("状态 payload 不能为空")
	}
	if !json.Valid(payload) {
		return errors.New("状态 payload 不是合法 JSON")
	}
	return nil
}

// prepareEphemeralState 统一时间精度：created_at 由库层写当前时间，expires_at 按毫秒截断（与 DATETIME(3) 一致）。
func prepareEphemeralState(st EphemeralState) (EphemeralState, error) {
	if err := validateEphemeralState(st.Kind, st.StateHash, st.OwnerKey, st.Payload); err != nil {
		return EphemeralState{}, err
	}
	if st.ExpiresAt.IsZero() {
		return EphemeralState{}, errors.New("状态必须有 expires_at")
	}
	st.CreatedAt = now()
	st.ExpiresAt = st.ExpiresAt.UTC().Truncate(time.Millisecond)
	return st, nil
}

const ephemeralCols = "kind, state_hash, owner_key, payload, expires_at, created_at, consumed_at"

func scanEphemeralState(row interface{ Scan(...any) error }) (*EphemeralState, error) {
	var (
		owner    sql.NullString
		consumed sql.NullTime
	)
	st := &EphemeralState{}
	if err := row.Scan(&st.Kind, &st.StateHash, &owner, &st.Payload, &st.ExpiresAt, &st.CreatedAt, &consumed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if owner.Valid {
		st.OwnerKey = owner.String
	}
	if consumed.Valid {
		t := consumed.Time
		st.ConsumedAt = &t
	}
	return st, nil
}

// ownerValue 把空属主写成 SQL NULL：唯一索引允许多个 NULL，互不冲突。
func ownerValue(ownerKey string) any {
	if ownerKey == "" {
		return nil
	}
	return ownerKey
}

// PutEphemeralState 写入一条状态。同一个 (kind, state_hash) 已存在时返回
// ErrEphemeralStateExists，调用方换一个随机值重试；不会覆盖已有状态。
func (s *Store) PutEphemeralState(ctx context.Context, st EphemeralState) error {
	st, err := prepareEphemeralState(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO ephemeral_states (kind, state_hash, owner_key, payload, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		st.Kind, st.StateHash, ownerValue(st.OwnerKey), st.Payload, st.ExpiresAt, st.CreatedAt)
	if isDuplicate(err) {
		return ErrEphemeralStateExists
	}
	return err
}

// ReplaceEphemeralState 写入一条「按属主替换」的状态：先删掉同 kind 同 owner_key 的旧状态，
// 再插入新的，两步在同一个事务里。用于每人只保留一个有效接入码这类语义。
// 并发替换同一属主时，一个事务会因唯一索引冲突返回 ErrEphemeralStateExists，调用方重试即可。
func (s *Store) ReplaceEphemeralState(ctx context.Context, st EphemeralState) error {
	if st.OwnerKey == "" {
		return errors.New("按属主替换的状态必须有 owner_key")
	}
	st, err := prepareEphemeralState(st)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM ephemeral_states WHERE kind = ? AND owner_key = ?", st.Kind, st.OwnerKey); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO ephemeral_states (kind, state_hash, owner_key, payload, expires_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		st.Kind, st.StateHash, st.OwnerKey, st.Payload, st.ExpiresAt, st.CreatedAt); err != nil {
		if isDuplicate(err) {
			// 另一个实例刚给同一属主写入了新状态（或随机哈希撞了）；事务回滚，调用方重试。
			return ErrEphemeralStateExists
		}
		return err
	}
	return tx.Commit()
}

// GetEphemeralState 读取一条还没消费、还没过期的状态（不消费）。没有返回 ErrNotFound，
// 过期返回 ErrEphemeralStateExpired。
func (s *Store) GetEphemeralState(ctx context.Context, kind, stateHash string) (*EphemeralState, error) {
	if err := validateEphemeralKey(kind, stateHash, ""); err != nil {
		return nil, err
	}
	st, err := scanEphemeralState(s.db.QueryRowContext(ctx,
		"SELECT "+ephemeralCols+" FROM ephemeral_states WHERE kind = ? AND state_hash = ?", kind, stateHash))
	if err != nil {
		return nil, err
	}
	if st.ConsumedAt != nil {
		return nil, ErrNotFound
	}
	if !st.ExpiresAt.After(now()) {
		return nil, ErrEphemeralStateExpired
	}
	return st, nil
}

// ConsumeEphemeralState 原子地消费一条状态并返回它（含 payload）。过期返回
// ErrEphemeralStateExpired；不存在、已被消费、或并发时被别的实例抢走返回 ErrNotFound。
func (s *Store) ConsumeEphemeralState(ctx context.Context, kind, stateHash string) (*EphemeralState, error) {
	// 先读一次是为了把「过期」和「无效/已被用掉」区分开，也让调用方拿到 payload。
	// 一次性由下面的条件 UPDATE 裁决：两个实例同时消费，只有一个能把 consumed_at 从 NULL 改掉。
	st, err := s.GetEphemeralState(ctx, kind, stateHash)
	if err != nil {
		return nil, err
	}
	t := now()
	res, err := s.db.ExecContext(ctx,
		`UPDATE ephemeral_states SET consumed_at = ?
		 WHERE kind = ? AND state_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
		t, kind, stateHash, t)
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
	st.ConsumedAt = &t
	return st, nil
}

// UpdateEphemeralPayload 改写一条未消费、未过期状态的 payload（例如把设备登录的 user_code
// 记到接入码上）。状态不存在/已消费/已过期时返回 false，不返回错误。
func (s *Store) UpdateEphemeralPayload(ctx context.Context, kind, stateHash string, payload json.RawMessage) (bool, error) {
	if err := validateEphemeralState(kind, stateHash, "", payload); err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE ephemeral_states SET payload = ?
		 WHERE kind = ? AND state_hash = ? AND consumed_at IS NULL AND expires_at > ?`,
		payload, kind, stateHash, now())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		return true, nil
	}
	// affected rows 是「实际改动的行数」：payload 和原来完全一样时是 0，但状态仍然有效。
	if _, err := s.GetEphemeralState(ctx, kind, stateHash); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrEphemeralStateExpired) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// PurgeExpiredEphemeralStates 清理所有过期的短时状态（已消费但还没过期的留着随过期一起删）。
// 过期判断本身在 Get/Consume 里做，清理只回收空间；多实例重复执行是幂等的。
func (s *Store) PurgeExpiredEphemeralStates(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx, "DELETE FROM ephemeral_states WHERE expires_at <= ?", now())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
