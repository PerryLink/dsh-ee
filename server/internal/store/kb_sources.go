package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// 知识源：挂在知识库上的扩展（docs/plugins.md §6.2）。

const (
	KBSourceSync      = "sync"
	KBSourceFederated = "federated"
)

// KBSourceNode 是管理员勾选的一个节点。
type KBSourceNode struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

type KBSource struct {
	ID         int64          `json:"id"`
	BaseID     int64          `json:"kb_id"`
	AssetID    int64          `json:"asset_id"`
	Mode       string         `json:"mode"`
	Title      string         `json:"title"`
	Scope      []KBSourceNode `json:"scope"`
	Folder     string         `json:"folder"`
	Cursor     string         `json:"-"`
	LastSyncAt *time.Time     `json:"last_sync_at"`
	LastError  string         `json:"last_error"`
	DocCount   int            `json:"doc_count"`
	CreatedBy  int64          `json:"created_by"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

const kbSourceCols = "id, kbase_id, asset_id, mode, title, scope, folder, sync_cursor, last_sync_at, last_error, doc_count, created_by, created_at, updated_at"

func scanKBSource(row interface{ Scan(...any) error }) (*KBSource, error) {
	var x KBSource
	var scope string
	var cursor sql.NullString
	var last sql.NullTime
	if err := row.Scan(&x.ID, &x.BaseID, &x.AssetID, &x.Mode, &x.Title, &scope, &x.Folder, &cursor, &last, &x.LastError, &x.DocCount, &x.CreatedBy, &x.CreatedAt, &x.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(scope), &x.Scope)
	if x.Scope == nil {
		x.Scope = []KBSourceNode{}
	}
	x.Cursor = cursor.String
	if last.Valid {
		t := last.Time
		x.LastSyncAt = &t
	}
	return &x, nil
}

func (s *Store) CreateKBSource(ctx context.Context, x *KBSource) error {
	scope, _ := json.Marshal(x.Scope)
	t := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO kb_sources (kbase_id, asset_id, mode, title, scope, folder, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		x.BaseID, x.AssetID, x.Mode, x.Title, string(scope), x.Folder, x.CreatedBy, t, t)
	if err != nil {
		return err
	}
	x.ID, err = res.LastInsertId()
	x.CreatedAt, x.UpdatedAt = t, t
	return err
}

func (s *Store) GetKBSource(ctx context.Context, id int64) (*KBSource, error) {
	return scanKBSource(s.db.QueryRowContext(ctx, "SELECT "+kbSourceCols+" FROM kb_sources WHERE id = ?", id))
}

// ListKBSources 列出知识源；baseID 为 0 列全部，mode 为空不限。
func (s *Store) ListKBSources(ctx context.Context, baseID int64, mode string) ([]*KBSource, error) {
	q, args := "SELECT "+kbSourceCols+" FROM kb_sources WHERE 1 = 1", []any{}
	if baseID > 0 {
		q, args = q+" AND kbase_id = ?", append(args, baseID)
	}
	if mode != "" {
		q, args = q+" AND mode = ?", append(args, mode)
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*KBSource{}
	for rows.Next() {
		x, err := scanKBSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SaveKBSourceSync 记下一轮同步走到哪了。
func (s *Store) SaveKBSourceSync(ctx context.Context, id int64, cursor, lastError string, docCount int) error {
	if len(lastError) > 900 {
		lastError = lastError[:900]
	}
	t := now()
	_, err := s.db.ExecContext(ctx, "UPDATE kb_sources SET sync_cursor = ?, last_error = ?, doc_count = ?, last_sync_at = ?, updated_at = ? WHERE id = ?",
		cursor, lastError, docCount, t, t, id)
	return err
}

func (s *Store) DeleteKBSource(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM kb_source_docs WHERE source_id = ?", id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM kb_sources WHERE id = ?", id)
	return err
}

// KBSourceDocs 返回一个知识源同步进来的文档：源系统里的 id → 文档 id。
func (s *Store) KBSourceDocs(ctx context.Context, sourceID int64) (map[string]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT external_id, doc_id FROM kb_source_docs WHERE source_id = ?", sourceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var ext string
		var doc int64
		if err := rows.Scan(&ext, &doc); err != nil {
			return nil, err
		}
		out[ext] = doc
	}
	return out, rows.Err()
}

func (s *Store) SetKBSourceDoc(ctx context.Context, sourceID int64, externalID string, docID int64) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kb_source_docs (source_id, external_id, doc_id, updated_at) VALUES (?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE doc_id = VALUES(doc_id), updated_at = VALUES(updated_at)`, sourceID, externalID, docID, now())
	return err
}

func (s *Store) DeleteKBSourceDoc(ctx context.Context, sourceID int64, externalID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM kb_source_docs WHERE source_id = ? AND external_id = ?", sourceID, externalID)
	return err
}

// KBSourceOfDocs 返回这些文档各自来自哪个知识源（不是同步来的不在结果里）。
func (s *Store) KBSourceOfDocs(ctx context.Context, sourceIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	for _, id := range sourceIDs {
		rows, err := s.db.QueryContext(ctx, "SELECT doc_id FROM kb_source_docs WHERE source_id = ?", id)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var doc int64
			if err := rows.Scan(&doc); err != nil {
				rows.Close()
				return nil, err
			}
			out[doc] = id
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
