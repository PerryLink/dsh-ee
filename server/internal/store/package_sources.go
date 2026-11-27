package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/go-sql-driver/mysql"
)

type PackageSource struct {
	ID          int64  `json:"-"`
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	Mode        string `json:"mode"`
	UpstreamURL string `json:"upstream_url,omitempty"`
}

type PackageSourceFile struct {
	Path       string    `json:"path"`
	BlobKey    string    `json:"-"`
	Size       int64     `json:"size"`
	UploaderID int64     `json:"-"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type PackageSourceRevision struct {
	Path string
	Time time.Time
}

var ErrPackageSourceOwner = errors.New("artifact belongs to another publisher")
var ErrIncompletePackageRevision = errors.New("package revision is incomplete")

func packageSourceError(err error) error {
	var me *mysql.MySQLError
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrNotFound
	case errors.As(err, &me) && me.Number == 1062:
		return ErrConflict
	default:
		return err
	}
}

func (s *Store) CreatePackageSource(ctx context.Context, v PackageSource) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO package_sources (name,protocol,mode,upstream_url) VALUES (?,?,?,?)", v.Name, v.Protocol, v.Mode, v.UpstreamURL)
	return packageSourceError(err)
}

func (s *Store) UpdatePackageSourceUpstream(ctx context.Context, id int64, upstream string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE package_sources SET upstream_url=? WHERE id=? AND mode='proxy'", upstream, id)
	return err
}

func (s *Store) PackageSource(ctx context.Context, protocol, name string) (PackageSource, error) {
	var v PackageSource
	err := s.db.QueryRowContext(ctx, "SELECT id,name,protocol,mode,upstream_url FROM package_sources WHERE protocol=? AND name=?", protocol, name).
		Scan(&v.ID, &v.Name, &v.Protocol, &v.Mode, &v.UpstreamURL)
	return v, packageSourceError(err)
}

func (s *Store) ListPackageSources(ctx context.Context) ([]PackageSource, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id,name,protocol,mode,upstream_url FROM package_sources ORDER BY protocol,name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PackageSource{}
	for rows.Next() {
		var v PackageSource
		if err := rows.Scan(&v.ID, &v.Name, &v.Protocol, &v.Mode, &v.UpstreamURL); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

// CheckPackageSourceWrite 是 SavePackageSourceFile 的只读预检：只做与上传内容无关的 owner 校验，
// 不落盘、不开写事务、不写库。API 在读取请求体（写 blob）之前调用它，让注定被 403 拒绝的上传
// （写他人命名空间）不留下永远不被引用的 blob。immutable / 重复内容的冲突取决于内容摘要，
// 预检阶段还没有读 body，无法判断，仍交给 SavePackageSourceFile 的事务内逻辑（保持 201 幂等语义）；
// 并发下也以事务内结果为准，所以这里误放行不会写坏数据。
func (s *Store) CheckPackageSourceWrite(ctx context.Context, sourceID int64, namespace string, uploaderID int64) error {
	var owner int64
	err := s.db.QueryRowContext(ctx, "SELECT owner_id FROM package_source_owners WHERE source_id=? AND namespace=?", sourceID, namespace).Scan(&owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// 命名空间还没有主：本次上传会成为 owner（SavePackageSourceFile 负责写入这行）。
		return nil
	case err != nil:
		return err
	case owner != uploaderID:
		return ErrPackageSourceOwner
	default:
		return nil
	}
}

// SavePackageSourceFile serializes writes to a Conan recipe namespace, or to one Raw path.
func (s *Store) SavePackageSourceFile(ctx context.Context, sourceID int64, namespace, revision, parent string, f PackageSourceFile, mutable bool, archive string, required, archives []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO package_source_owners (source_id,namespace,owner_id) VALUES (?,?,?) ON DUPLICATE KEY UPDATE owner_id=owner_id",
		sourceID, namespace, f.UploaderID); err != nil {
		return err
	}
	var owner int64
	if err := tx.QueryRowContext(ctx, "SELECT owner_id FROM package_source_owners WHERE source_id=? AND namespace=? FOR UPDATE", sourceID, namespace).Scan(&owner); err != nil {
		return err
	}
	if owner != f.UploaderID {
		return ErrPackageSourceOwner
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT blob_key FROM package_source_files WHERE source_id=? AND path=? FOR UPDATE", sourceID, f.Path).Scan(&previous)
	switch {
	case err == nil && previous == f.BlobKey:
	case err == nil && !mutable:
		return ErrConflict
	case err == nil || errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO package_source_files (source_id,path,revision,blob_key,size,uploader_id,updated_at)
			VALUES (?,?,?,?,?,?,?) ON DUPLICATE KEY UPDATE blob_key=VALUES(blob_key),size=VALUES(size),updated_at=VALUES(updated_at)`,
			sourceID, f.Path, revision, f.BlobKey, f.Size, f.UploaderID, time.Now().UTC())
		if err != nil {
			return err
		}
	default:
		return err
	}
	if archive != "" {
		prefix := revision + "/files/" + archive
		if _, err := tx.ExecContext(ctx, "DELETE FROM package_source_files WHERE source_id=? AND path>=? AND path<? AND path<>?",
			sourceID, prefix+".", prefix+"/", f.Path); err != nil {
			return err
		}
	}
	if required != nil {
		for _, file := range required {
			var present int
			err := tx.QueryRowContext(ctx, "SELECT 1 FROM package_source_files WHERE source_id=? AND path=? FOR UPDATE", sourceID, revision+"/files/"+file).Scan(&present)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncompletePackageRevision
			}
			if err != nil {
				return err
			}
		}
		for _, archivePrefix := range archives {
			prefix := revision + "/files/" + archivePrefix
			var present int
			err := tx.QueryRowContext(ctx, "SELECT 1 FROM package_source_files WHERE source_id=? AND path>=? AND path<? LIMIT 1 FOR UPDATE",
				sourceID, prefix+".", prefix+"/").Scan(&present)
			if errors.Is(err, sql.ErrNoRows) {
				return ErrIncompletePackageRevision
			}
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO package_source_revisions (source_id,path,parent,published_at)
			VALUES (?,?,?,?) ON DUPLICATE KEY UPDATE published_at=published_at`, sourceID, revision, parent, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

const packageFileVisible = "(f.revision='' OR r.path IS NOT NULL)"
const packageFileJoin = " FROM package_source_files f LEFT JOIN package_source_revisions r ON r.source_id=f.source_id AND r.path=f.revision"

func (s *Store) PackageArtifact(ctx context.Context, sourceID int64, path string) (PackageSourceFile, error) {
	f := PackageSourceFile{Path: path}
	err := s.db.QueryRowContext(ctx, "SELECT f.blob_key,f.size,f.updated_at"+packageFileJoin+" WHERE f.source_id=? AND f.path=? AND "+packageFileVisible, sourceID, path).
		Scan(&f.BlobKey, &f.Size, &f.UpdatedAt)
	return f, packageSourceError(err)
}

func (s *Store) PackageArtifacts(ctx context.Context, sourceID int64, revision *string, after string, limit int) ([]PackageSourceFile, error) {
	q := "SELECT f.path,f.size,f.updated_at" + packageFileJoin + " WHERE f.source_id=? AND f.path>? AND " + packageFileVisible
	args := []any{sourceID, after}
	if revision != nil {
		q += " AND f.revision=?"
		args = append(args, *revision)
	}
	q += " ORDER BY f.path LIMIT ?"
	rows, err := s.db.QueryContext(ctx, q, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PackageSourceFile{}
	for rows.Next() {
		var f PackageSourceFile
		if err := rows.Scan(&f.Path, &f.Size, &f.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, f)
	}
	return items, rows.Err()
}

func (s *Store) PackageRevisions(ctx context.Context, sourceID int64, parent string, limit int) ([]PackageSourceRevision, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT path,published_at FROM package_source_revisions WHERE source_id=? AND parent=? ORDER BY published_at DESC,path DESC LIMIT ?", sourceID, parent, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []PackageSourceRevision{}
	for rows.Next() {
		var v PackageSourceRevision
		if err := rows.Scan(&v.Path, &v.Time); err != nil {
			return nil, err
		}
		items = append(items, v)
	}
	return items, rows.Err()
}

func (s *Store) PackageRevisionParents(ctx context.Context, sourceID int64, prefix, end string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT DISTINCT parent FROM package_source_revisions WHERE source_id=? AND parent>=? AND parent<? ORDER BY parent", sourceID, prefix, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		items = append(items, path)
	}
	return items, rows.Err()
}
