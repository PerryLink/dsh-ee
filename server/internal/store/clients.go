package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// APIClient 是第三方 Harness / CI 的程序化客户端（docs/token-lifecycle.md §6）。
// 它不直接持有权限：签发 access token 时按 owner 账号当前 RBAC 与这里的 scopes 求交集，
// 所以 owner 停用、权限收紧会立即反映到之后换到的令牌上。
type APIClient struct {
	ID          int64  `json:"id"`
	ClientID    string `json:"client_id"`
	SecretHash  string `json:"-"`
	Name        string `json:"name"`
	OwnerUserID int64  `json:"owner_user_id"`
	// 客户端申请的能力范围；签发时还要和 owner 当前权限求交集
	Scopes     []string   `json:"scopes"`
	ClientKind string     `json:"client_kind"`
	CreatedBy  int64      `json:"created_by"`
	DisabledAt *time.Time `json:"disabled_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	// 列表 / 单个查询时带出 owner 的展示信息，管理页排查用
	OwnerUsername   string `json:"owner_username,omitempty"`
	OwnerDepartment string `json:"owner_department,omitempty"`
}

const apiClientColumns = `c.id, c.client_id, c.secret_hash, c.name, c.owner_user_id, c.scopes, c.client_kind,
	c.created_by, c.disabled_at, c.last_used_at, c.created_at`

func scanAPIClient(row interface{ Scan(...any) error }, extra ...any) (*APIClient, error) {
	var c APIClient
	var scopes sql.NullString
	var disabled, used sql.NullTime
	err := row.Scan(append([]any{
		&c.ID, &c.ClientID, &c.SecretHash, &c.Name, &c.OwnerUserID, &scopes, &c.ClientKind,
		&c.CreatedBy, &disabled, &used, &c.CreatedAt,
	}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ClientKind = NormalizeClientKind(c.ClientKind)
	if scopes.Valid && scopes.String != "" {
		if err := json.Unmarshal([]byte(scopes.String), &c.Scopes); err != nil {
			return nil, err
		}
	}
	if disabled.Valid {
		c.DisabledAt = &disabled.Time
	}
	if used.Valid {
		c.LastUsedAt = &used.Time
	}
	if len(extra) == 2 {
		c.OwnerUsername = *extra[0].(*string)
		c.OwnerDepartment = *extra[1].(*string)
	}
	return &c, nil
}

// CreateAPIClient 落库一个新客户端；调用方只把明文 secret 返回给创建人一次。
func (s *Store) CreateAPIClient(ctx context.Context, c *APIClient) error {
	c.ClientKind = NormalizeClientKind(c.ClientKind)
	scopes, err := json.Marshal(nonNilStrings(c.Scopes))
	if err != nil {
		return err
	}
	c.CreatedAt = now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO api_clients
		(client_id, secret_hash, name, owner_user_id, scopes, client_kind, created_by, disabled_at, last_used_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ClientID, c.SecretHash, c.Name, c.OwnerUserID, string(scopes), c.ClientKind, c.CreatedBy,
		c.DisabledAt, c.LastUsedAt, c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

const apiClientJoin = ` FROM api_clients c LEFT JOIN users u ON u.id = c.owner_user_id`

// APIClientByClientID 按对外 client_id 取（含已禁用的：签发端点要能区分并统一回 invalid_client）。
func (s *Store) APIClientByClientID(ctx context.Context, clientID string) (*APIClient, error) {
	return scanAPIClient(s.db.QueryRowContext(ctx,
		`SELECT `+apiClientColumns+`, COALESCE(u.username, ''), COALESCE(u.department, '')`+apiClientJoin+`
		 WHERE c.client_id = ?`, clientID), new(string), new(string))
}

// ListAPIClients 返回全部客户端（含已禁用），新的在前。
func (s *Store) ListAPIClients(ctx context.Context) ([]*APIClient, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+apiClientColumns+`, COALESCE(u.username, ''), COALESCE(u.department, '')`+apiClientJoin+`
		 ORDER BY c.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIClient{}
	for rows.Next() {
		c, err := scanAPIClient(rows, new(string), new(string))
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DisableAPIClient 禁用客户端；已经禁用时返回 changed=false（幂等）。
func (s *Store) DisableAPIClient(ctx context.Context, clientID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		"UPDATE api_clients SET disabled_at = ? WHERE client_id = ? AND disabled_at IS NULL", now(), clientID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TouchAPIClient 记一次成功签发时的最近使用时间；禁用后不再更新。
func (s *Store) TouchAPIClient(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE api_clients SET last_used_at = ? WHERE id = ? AND disabled_at IS NULL", now(), id)
	return err
}

// nonNilStrings 保证 JSON 列落一个数组而不是 null。
func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
