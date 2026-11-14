package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// BackgroundLease 是一段时间内由某个实例独占的后台任务租约（migrations/0035_background_leases.sql）。
// 周期任务（LDAP/飞书同步、上游新版本检查、清理等）每轮先抢租约，只有持租实例执行，
// 多实例部署时同一任务不会重复跑；崩溃后 expires_at 到期，别的实例可以接管。
type BackgroundLease struct {
	Name      string
	Owner     string
	ExpiresAt time.Time
	RenewedAt time.Time
}

// 租约字段上限与表结构一致。
const (
	leaseNameMax  = 64
	leaseOwnerMax = 128
)

func validLeaseArgs(name, owner string) error {
	if name == "" || len(name) > leaseNameMax {
		return fmt.Errorf("租约名必须在 1–%d 字节之间", leaseNameMax)
	}
	if owner == "" || len(owner) > leaseOwnerMax {
		return fmt.Errorf("租约持有者必须在 1–%d 字节之间", leaseOwnerMax)
	}
	return nil
}

// AcquireBackgroundLease 抢占后台任务租约：租约空闲、已过期或本来就是自己持有时返回 true。
// CAS 由 UPDATE 的 WHERE 条件保证：并发抢占时只有一个实例能把 owner 改成自己。
// 拿到租约后要用 RenewBackgroundLease 在到期前续约，任务结束（或进程停机）用 ReleaseBackgroundLease 释放。
func (s *Store) AcquireBackgroundLease(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	if err := validLeaseArgs(name, owner); err != nil {
		return false, err
	}
	if ttl <= 0 {
		return false, errors.New("租约有效期必须大于 0")
	}
	now := now()
	expires := now.Add(ttl)
	// 先确保行存在：两个实例同时首次抢时都执行 INSERT，ON DUPLICATE KEY 保证只有一行。
	// 已有行时这里不改任何值，真正的抢占交给下面带条件的 UPDATE。
	if _, err := s.db.ExecContext(ctx, `INSERT INTO background_leases (name, owner, expires_at, renewed_at)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE name = name`, name, owner, expires, now); err != nil {
		return false, err
	}
	// CAS：只接管空闲/已过期的租约，或本来自己持有的（重复抢 = 续约）。
	res, err := s.db.ExecContext(ctx, `UPDATE background_leases SET owner = ?, expires_at = ?, renewed_at = ?
		WHERE name = ? AND (owner = ? OR expires_at <= ?)`, owner, expires, now, name, owner, now)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return true, nil
	}
	// 同一毫秒重复抢自己的租约时值可能完全没变，affected rows 是 0；读回来确认一次。
	return s.ownsBackgroundLease(ctx, name, owner, now)
}

// RenewBackgroundLease 续约：只有当前持有者是自己时才延长到期时间。
// 租约已过期但还没被别人接管时仍可续；已经被接管就返回 false。
func (s *Store) RenewBackgroundLease(ctx context.Context, name, owner string, ttl time.Duration) (bool, error) {
	if err := validLeaseArgs(name, owner); err != nil {
		return false, err
	}
	if ttl <= 0 {
		return false, errors.New("租约有效期必须大于 0")
	}
	now := now()
	expires := now.Add(ttl)
	res, err := s.db.ExecContext(ctx, `UPDATE background_leases SET expires_at = ?, renewed_at = ?
		WHERE name = ? AND owner = ?`, expires, now, name, owner)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err == nil && n > 0 {
		return true, nil
	}
	return s.ownsBackgroundLease(ctx, name, owner, now)
}

// ownsBackgroundLease 读回租约确认是不是 owner 的（且还没过期）。
func (s *Store) ownsBackgroundLease(ctx context.Context, name, owner string, now time.Time) (bool, error) {
	var got BackgroundLease
	err := s.db.QueryRowContext(ctx, `SELECT name, owner, expires_at, renewed_at FROM background_leases WHERE name = ?`, name).
		Scan(&got.Name, &got.Owner, &got.ExpiresAt, &got.RenewedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return got.Owner == owner && got.ExpiresAt.After(now), nil
}

// ReleaseBackgroundLease 释放自己持有的租约；不是自己持有的行不动（幂等）。
func (s *Store) ReleaseBackgroundLease(ctx context.Context, name, owner string) error {
	if err := validLeaseArgs(name, owner); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM background_leases WHERE name = ? AND owner = ?`, name, owner)
	return err
}

// GetBackgroundLease 读一条租约（测试与排查用）；没有返回 ErrNotFound。
func (s *Store) GetBackgroundLease(ctx context.Context, name string) (*BackgroundLease, error) {
	if name == "" || len(name) > leaseNameMax {
		return nil, fmt.Errorf("租约名必须在 1–%d 字节之间", leaseNameMax)
	}
	var got BackgroundLease
	err := s.db.QueryRowContext(ctx, `SELECT name, owner, expires_at, renewed_at FROM background_leases WHERE name = ?`, name).
		Scan(&got.Name, &got.Owner, &got.ExpiresAt, &got.RenewedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &got, nil
}
