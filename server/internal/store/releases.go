package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	ReleaseAvailable   = "available"
	ReleaseDownloading = "downloading"
	ReleaseMirrored    = "mirrored"
	ReleaseFailed      = "failed"
	// 被清理或手动删除的版本：保留记录，但自动镜像不再下载它，管理员仍可手动重新下载。
	ReleaseRemoved = "removed"
)

var (
	ErrReleaseRecommended = errors.New("推荐版本不能删除")
	ErrReleaseNotMirrored = errors.New("只能推荐已下载的版本")
)

type DshRelease struct {
	ID               int64      `json:"id"`
	Target           string     `json:"target"`
	Kind             string     `json:"kind"`
	Version          string     `json:"version"`
	ReleasedAt       time.Time  `json:"released_at"`
	File             string     `json:"file"`
	Size             int64      `json:"size"`
	SHA512           string     `json:"sha512"`
	ChecksumVerified bool       `json:"checksum_verified"`
	UpstreamURL      string     `json:"upstream_url"`
	State            string     `json:"state"`
	Recommended      bool       `json:"recommended"`
	LocalPath        string     `json:"local_path"`
	Error            string     `json:"error"`
	MirroredAt       *time.Time `json:"mirrored_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

const dshReleaseColumns = "id, target, kind, version, released_at, file, size, sha512, upstream_url, state, recommended, local_path, error, mirrored_at, created_at, checksum_verified"

func scanDshRelease(row interface{ Scan(...any) error }) (*DshRelease, error) {
	var r DshRelease
	var localPath, releaseErr sql.NullString
	var mirroredAt sql.NullTime
	err := row.Scan(&r.ID, &r.Target, &r.Kind, &r.Version, &r.ReleasedAt, &r.File, &r.Size, &r.SHA512,
		&r.UpstreamURL, &r.State, &r.Recommended, &localPath, &releaseErr, &mirroredAt, &r.CreatedAt, &r.ChecksumVerified)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.LocalPath, r.Error = localPath.String, releaseErr.String
	if mirroredAt.Valid {
		r.MirroredAt = &mirroredAt.Time
	}
	return &r, nil
}

func nullableReleaseText(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func (s *Store) CreateDshRelease(ctx context.Context, r *DshRelease) error {
	r.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO dsh_releases
		(target, kind, version, released_at, file, size, sha512, upstream_url, state, recommended, local_path, error, mirrored_at, created_at, checksum_verified)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, r.Target, r.Kind, r.Version, r.ReleasedAt, r.File, r.Size,
		r.SHA512, r.UpstreamURL, r.State, r.Recommended, nullableReleaseText(r.LocalPath), nullableReleaseText(r.Error), r.MirroredAt, r.CreatedAt, r.ChecksumVerified)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

func (s *Store) DshReleaseByID(ctx context.Context, id int64) (*DshRelease, error) {
	return scanDshRelease(s.db.QueryRowContext(ctx, "SELECT "+dshReleaseColumns+" FROM dsh_releases WHERE id = ?", id))
}

func (s *Store) DshReleaseByVersion(ctx context.Context, target, kind, version string) (*DshRelease, error) {
	return scanDshRelease(s.db.QueryRowContext(ctx,
		"SELECT "+dshReleaseColumns+" FROM dsh_releases WHERE target = ? AND kind = ? AND version = ?", target, kind, version))
}

func (s *Store) ListDshReleases(ctx context.Context, target string) ([]*DshRelease, error) {
	q := "SELECT " + dshReleaseColumns + " FROM dsh_releases"
	var args []any
	if target != "" {
		q += " WHERE target = ?"
		args = append(args, target)
	}
	q += " ORDER BY recommended DESC, released_at DESC, id DESC"
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*DshRelease
	for rows.Next() {
		r, err := scanDshRelease(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) UpdateDshReleaseState(ctx context.Context, id int64, state, localPath, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dsh_releases SET state = ?, local_path = ?, error = ?,
		mirrored_at = IF(? = 'mirrored', ?, NULL) WHERE id = ?`, state, nullableReleaseText(localPath), nullableReleaseText(message), state, now(), id)
	return err
}

func (s *Store) UpdateDshReleaseMetadata(ctx context.Context, r *DshRelease) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dsh_releases SET released_at = ?, file = ?, size = ?, sha512 = ?, upstream_url = ?
		WHERE id = ? AND state IN ('available', 'failed', 'removed')`, r.ReleasedAt, r.File, r.Size, r.SHA512, r.UpstreamURL, r.ID)
	return err
}

func (s *Store) FinishDshRelease(ctx context.Context, id, size int64, sha512, localPath string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dsh_releases SET state = 'mirrored', size = ?, sha512 = ?, local_path = ?, error = NULL, mirrored_at = ? WHERE id = ?`,
		size, sha512, localPath, now(), id)
	return err
}

func (s *Store) UpdateImportedDshReleaseMetadata(ctx context.Context, r *DshRelease) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dsh_releases SET released_at = ?, file = ?, size = ?, sha512 = ?, upstream_url = '', checksum_verified = ? WHERE id = ?`, r.ReleasedAt, r.File, r.Size, r.SHA512, r.ChecksumVerified, r.ID)
	return err
}

func (s *Store) SetDshReleaseRecommended(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var target string
	err = tx.QueryRowContext(ctx, "SELECT target FROM dsh_releases WHERE id = ?", id).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// 每个平台（target）只有一个推荐版本，不分安装包种类（docs/distribution-handoff.md
	// 「设推荐（同一事务里先把这个 target 的推荐全部清掉）」，管理页也写的是「每个芯片架构一个」）。
	// 先按 id 顺序一次锁住这个平台的所有行，再改：同一平台的两个推荐并发时按同样顺序加锁，
	// 后到的等前一个提交，不会各自先锁一行再去抢对方那行而死锁。
	rows, err := tx.QueryContext(ctx, "SELECT id, state FROM dsh_releases WHERE target = ? ORDER BY id FOR UPDATE", target)
	if err != nil {
		return err
	}
	state, found := "", false
	for rows.Next() {
		var rid int64
		var rstate string
		if err := rows.Scan(&rid, &rstate); err != nil {
			rows.Close()
			return err
		}
		if rid == id {
			state, found = rstate, true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		// 读 target 之后、加锁之前被删掉了
		return ErrNotFound
	}
	if state != ReleaseMirrored {
		return ErrReleaseNotMirrored
	}
	if _, err := tx.ExecContext(ctx, "UPDATE dsh_releases SET recommended = 0 WHERE target = ? AND recommended = 1", target); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE dsh_releases SET recommended = 1 WHERE id = ?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ResetDshRelease(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `UPDATE dsh_releases SET state = 'removed', local_path = NULL, error = NULL, mirrored_at = NULL
		WHERE id = ? AND recommended = 0`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrReleaseRecommended
	}
	return nil
}

func (s *Store) RecoverDshReleases(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dsh_releases SET state = 'available', local_path = NULL, error = NULL, mirrored_at = NULL WHERE state = 'downloading'`)
	return err
}
