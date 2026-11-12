package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/access"
)

// 扩展字段和访问规则（docs/access-control.md）。

const fieldCols = "id, object, key_name, label, type, options, access, source"

func scanField(row interface{ Scan(...any) error }) (*access.Field, error) {
	var f access.Field
	var opts string
	if err := row.Scan(&f.ID, &f.Object, &f.Key, &f.Label, &f.Type, &opts, &f.Access, &f.Source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(opts), &f.Options)
	if f.Options == nil {
		f.Options = []string{}
	}
	return &f, nil
}

func (s *Store) ListFields(ctx context.Context) ([]access.Field, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+fieldCols+" FROM field_defs ORDER BY object, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []access.Field{}
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

func (s *Store) GetField(ctx context.Context, id int64) (*access.Field, error) {
	return scanField(s.db.QueryRowContext(ctx, "SELECT "+fieldCols+" FROM field_defs WHERE id = ?", id))
}

func (s *Store) CreateField(ctx context.Context, f *access.Field, by int64) error {
	opts, _ := json.Marshal(f.Options)
	t := now()
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO field_defs (object, key_name, label, type, options, access, source, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		f.Object, f.Key, f.Label, f.Type, string(opts), f.Access, f.Source, nullID(by), t, t)
	if err != nil {
		if isDuplicate(err) {
			return ErrDuplicate
		}
		return err
	}
	f.ID, err = res.LastInsertId()
	return err
}

// UpdateField 改字段的名字、选项、是否用于访问控制、来源。类型和键定了就不能改。
func (s *Store) UpdateField(ctx context.Context, f *access.Field) error {
	opts, _ := json.Marshal(f.Options)
	_, err := s.db.ExecContext(ctx, "UPDATE field_defs SET label = ?, options = ?, access = ?, source = ?, updated_at = ? WHERE id = ?",
		f.Label, string(opts), f.Access, f.Source, now(), f.ID)
	return err
}

func (s *Store) DeleteField(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM field_values WHERE field_id = ?", id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM field_defs WHERE id = ?", id)
	return err
}

// FieldValue 是一个对象上一个字段的值。
type FieldValue struct {
	FieldID int64
	Value   any
	Manual  bool
}

// ObjectFieldValues 取一个对象的全部字段值。
func (s *Store) ObjectFieldValues(ctx context.Context, object string, id int64) ([]FieldValue, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT field_id, value, manual FROM field_values WHERE object = ? AND object_id = ?", object, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FieldValue
	for rows.Next() {
		var fv FieldValue
		var raw string
		if err := rows.Scan(&fv.FieldID, &raw, &fv.Manual); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &fv.Value) == nil {
			out = append(out, fv)
		}
	}
	return out, rows.Err()
}

// KBaseDocFieldValues 取一个知识库里所有文档在这几个字段上的值：文档 id → 字段 id → 值。
func (s *Store) KBaseDocFieldValues(ctx context.Context, baseID int64, fieldIDs []int64) (map[int64]map[int64]any, error) {
	out := map[int64]map[int64]any{}
	if len(fieldIDs) == 0 {
		return out, nil
	}
	args := []any{access.ObjectDocument, baseID}
	for _, id := range fieldIDs {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT v.object_id, v.field_id, v.value FROM field_values v JOIN kb_docs d ON d.id = v.object_id WHERE v.object = ? AND d.base_id = ? AND v.field_id IN (?"+
			strings.Repeat(", ?", len(fieldIDs)-1)+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var doc, field int64
		var raw string
		if err := rows.Scan(&doc, &field, &raw); err != nil {
			return nil, err
		}
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		if out[doc] == nil {
			out[doc] = map[int64]any{}
		}
		out[doc][field] = v
	}
	return out, rows.Err()
}

// SetFieldValue 写一个字段值；value 为 nil 时清空。manual 为 false 时（同步写的）不覆盖人手工改过的值，返回是否写了。
func (s *Store) SetFieldValue(ctx context.Context, object string, id, fieldID int64, value any, manual bool, by int64) (bool, error) {
	if !manual {
		var was bool
		err := s.db.QueryRowContext(ctx, "SELECT manual FROM field_values WHERE object = ? AND object_id = ? AND field_id = ?", object, id, fieldID).Scan(&was)
		if err == nil && was {
			return false, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	if value == nil {
		_, err := s.db.ExecContext(ctx, "DELETE FROM field_values WHERE object = ? AND object_id = ? AND field_id = ?", object, id, fieldID)
		return true, err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return false, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO field_values (object, object_id, field_id, value, manual, updated_by, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE value = VALUES(value), manual = VALUES(manual), updated_by = VALUES(updated_by), updated_at = VALUES(updated_at)`,
		object, id, fieldID, string(raw), manual, nullID(by), now())
	return true, err
}

// SetSyncedFieldValue 写一个同步来的值（manual=false）。override 为 false 时不覆盖人手工改过的值；
// 为 true 时（管理员选了「以同步为准」）照样覆盖，并把这个值标成同步来的。
func (s *Store) SetSyncedFieldValue(ctx context.Context, object string, id, fieldID int64, value any, override bool) (bool, error) {
	if override && value != nil {
		raw, err := json.Marshal(value)
		if err != nil {
			return false, err
		}
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO field_values (object, object_id, field_id, value, manual, updated_by, updated_at) VALUES (?, ?, ?, ?, 0, NULL, ?)
			 ON DUPLICATE KEY UPDATE value = VALUES(value), manual = 0, updated_by = NULL, updated_at = VALUES(updated_at)`,
			object, id, fieldID, string(raw), now())
		return err == nil, err
	}
	return s.SetFieldValue(ctx, object, id, fieldID, value, false, 0)
}

// DeleteObjectFieldValues 在对象被删掉时清掉它的字段值。
func (s *Store) DeleteObjectFieldValues(ctx context.Context, object string, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM field_values WHERE object = ? AND object_id = ?", object, id)
	return err
}

// CountFieldValues 数一个字段有多少个对象填了值。
func (s *Store) CountFieldValues(ctx context.Context, fieldID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM field_values WHERE field_id = ?", fieldID).Scan(&n)
	return n, err
}

const ruleCols = "id, name, enabled, op, user_field, doc_field, missing, kbases"

func scanRule(row interface{ Scan(...any) error }) (*access.Rule, error) {
	var r access.Rule
	var kbs string
	if err := row.Scan(&r.ID, &r.Name, &r.Enabled, &r.Op, &r.UserField, &r.DocField, &r.Missing, &kbs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	_ = json.Unmarshal([]byte(kbs), &r.KBases)
	if r.KBases == nil {
		r.KBases = []string{}
	}
	return &r, nil
}

func (s *Store) ListAccessRules(ctx context.Context) ([]access.Rule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+ruleCols+" FROM access_rules ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []access.Rule{}
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *Store) GetAccessRule(ctx context.Context, id int64) (*access.Rule, error) {
	return scanRule(s.db.QueryRowContext(ctx, "SELECT "+ruleCols+" FROM access_rules WHERE id = ?", id))
}

func (s *Store) CreateAccessRule(ctx context.Context, r *access.Rule, by int64) error {
	kbs, _ := json.Marshal(r.KBases)
	t := now()
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO access_rules (name, enabled, op, user_field, doc_field, missing, kbases, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		r.Name, r.Enabled, r.Op, r.UserField, r.DocField, r.Missing, string(kbs), nullID(by), t, t)
	if err != nil {
		if isDuplicate(err) {
			return ErrDuplicate
		}
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

func (s *Store) UpdateAccessRule(ctx context.Context, r *access.Rule) error {
	kbs, _ := json.Marshal(r.KBases)
	_, err := s.db.ExecContext(ctx, "UPDATE access_rules SET name = ?, enabled = ?, op = ?, user_field = ?, doc_field = ?, missing = ?, kbases = ?, updated_at = ? WHERE id = ?",
		r.Name, r.Enabled, r.Op, r.UserField, r.DocField, r.Missing, string(kbs), now(), r.ID)
	if isDuplicate(err) {
		return ErrDuplicate
	}
	return err
}

func (s *Store) DeleteAccessRule(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM access_rules WHERE id = ?", id)
	return err
}
