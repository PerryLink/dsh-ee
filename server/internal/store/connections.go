package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// ConnectionTool 是连接下的一个工具。
type ConnectionTool struct {
	AssetID     int64           `json:"asset_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// OpenAPI 导入的工具才有：调用时用来拼请求
	Operation json.RawMessage `json:"operation,omitempty"`
	ReadOnly  bool            `json:"read_only"`
	Enabled   bool            `json:"enabled"`
	// 额外限定的角色；空表示连接可用范围内的人都能用
	Roles     []int64   `json:"roles"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MCPToolOp 是 MCP 工具在 operation 里记的东西：上游的原名（和对外的名字不一样时才记）。
type MCPToolOp struct {
	Name string `json:"mcp_name"`
}

// mcpName 是 operation 里记的上游原名；没记（OpenAPI 工具、名字本来就合规的 MCP 工具、旧数据）时为空。
func (t *ConnectionTool) mcpName() string {
	var op MCPToolOp
	if len(t.Operation) > 0 && json.Unmarshal(t.Operation, &op) == nil {
		return op.Name
	}
	return ""
}

// UpstreamName 是这个工具在上游的身份：调上游 MCP 用它，重新同步时也按它把管理员的设置对回来。
func (t *ConnectionTool) UpstreamName() string {
	if n := t.mcpName(); n != "" {
		return n
	}
	return t.Name
}

const connToolCols = "asset_id, name, description, input_schema, operation, read_only, enabled, roles, updated_at"

func scanConnTool(row interface{ Scan(...any) error }) (*ConnectionTool, error) {
	var t ConnectionTool
	var schema string
	var op, roles sql.NullString
	if err := row.Scan(&t.AssetID, &t.Name, &t.Description, &schema, &op, &t.ReadOnly, &t.Enabled, &roles, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.InputSchema = json.RawMessage(schema)
	if op.Valid && op.String != "" {
		t.Operation = json.RawMessage(op.String)
	}
	t.Roles = []int64{}
	if roles.Valid && roles.String != "" {
		_ = json.Unmarshal([]byte(roles.String), &t.Roles)
	}
	return &t, nil
}

// ConnectionTools 列出连接的工具；assetIDs 为空表示全部连接。
func (s *Store) ConnectionTools(ctx context.Context, assetIDs ...int64) ([]*ConnectionTool, error) {
	q := "SELECT " + connToolCols + " FROM connection_tools"
	var args []any
	if len(assetIDs) > 0 {
		q += " WHERE asset_id IN (" + placeholders(len(assetIDs)) + ")"
		for _, id := range assetIDs {
			args = append(args, id)
		}
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY asset_id, name", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ConnectionTool{}
	for rows.Next() {
		t, err := scanConnTool(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ReplaceConnectionTools 用新发现的工具替换清单。已存在的工具保留管理员设置的启用状态与角色限定；
// 新出现的工具按 defaultEnabled 决定是否启用（OpenAPI 的写操作默认关闭）。
//
// 新旧工具按上游身份（UpstreamName）对上，不按对外的名字：对外的名字是规范、去重出来的，
// 上游多一个工具就可能换主人（get-issue 变成 get_issue，原来的 get_issue 成了 get_issue_2），
// 按名字对会把管理员给 get_issue 的角色限定挪到别的工具上，真正的 get_issue 反倒没了限制。
func (s *Store) ReplaceConnectionTools(ctx context.Context, assetID int64, tools []*ConnectionTool) error {
	existing, err := s.ConnectionTools(ctx, assetID)
	if err != nil {
		return err
	}
	byUpstream := map[string]*ConnectionTool{}
	// 没记上游原名的旧行：名字可能是规范过的（上游的 get-issue 存成了 get_issue）
	legacy := map[string]*ConnectionTool{}
	for _, t := range existing {
		byUpstream[t.UpstreamName()] = t
		if t.mcpName() == "" {
			legacy[t.Name] = t
		}
	}
	taken := map[*ConnectionTool]bool{}
	olds := make([]*ConnectionTool, len(tools))
	for i, t := range tools {
		if old := byUpstream[t.UpstreamName()]; old != nil {
			olds[i], taken[old] = old, true
		}
	}
	// 上游原名对不上的，再拿旧行的名字对一次规范后的名字：兼容还没记上游原名时存下的设置。
	// 放在后面，旧行已经被上游原名对上了就不再给别人。
	for i, t := range tools {
		if olds[i] != nil || t.mcpName() == "" {
			continue
		}
		if old := legacy[t.Name]; old != nil && !taken[old] {
			olds[i], taken[old] = old, true
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM connection_tools WHERE asset_id = ?", assetID); err != nil {
		return err
	}
	t0 := now()
	for i, t := range tools {
		if old := olds[i]; old != nil {
			t.Roles = old.Roles
			// 原来只读、现在成了写操作的工具不沿用「已启用」：等管理员确认后再打开（docs/plugins.md §9.4）
			if !old.ReadOnly || t.ReadOnly {
				t.Enabled = old.Enabled
			}
		}
		roles, _ := json.Marshal(nonNilIDs(t.Roles))
		var op any
		if len(t.Operation) > 0 {
			op = string(t.Operation)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO connection_tools (asset_id, name, description, input_schema, operation, read_only, enabled, roles, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			assetID, t.Name, t.Description, string(t.InputSchema), op, t.ReadOnly, t.Enabled, string(roles), t0); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func nonNilIDs(xs []int64) []int64 {
	if xs == nil {
		return []int64{}
	}
	return xs
}

// SetConnectionTool 修改一个工具的启用状态与角色限定。
func (s *Store) SetConnectionTool(ctx context.Context, assetID int64, name string, enabled bool, roles []int64) error {
	b, _ := json.Marshal(nonNilIDs(roles))
	res, err := s.db.ExecContext(ctx, "UPDATE connection_tools SET enabled = ?, roles = ?, updated_at = ? WHERE asset_id = ? AND name = ?",
		enabled, string(b), now(), assetID, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type ConnectionCall struct {
	AssetID   int64
	Tool      string
	UserID    int64
	OK        bool
	Error     string
	LatencyMS int
	// 这次调用用的接入令牌；0 表示网页会话/没有令牌
	TokenID int64
	// 这次调用带的员工身份：空（只有共享凭据）/ ee_jwt / user_secret
	AuthMode string
	// 扩展装的连接：扩展的版本和清单摘要（作者/名字@版本#摘要前 12 位）
	Extension string
	// 这次用的是哪一份命名共享凭据；空是原连接默认的那一份
	SharedName string
}

func (s *Store) RecordConnectionCall(ctx context.Context, c ConnectionCall) error {
	var tokenID any
	if c.TokenID > 0 {
		tokenID = c.TokenID
	}
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO connection_calls (asset_id, tool, user_id, ok, error, latency_ms, token_id, auth_mode, extension, shared_name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		c.AssetID, c.Tool, c.UserID, c.OK, truncateRunes(c.Error, 512), c.LatencyMS, tokenID, c.AuthMode, truncateRunes(c.Extension, 160), c.SharedName, now())
	return err
}

// RecentCall 是连接调用日志的一条，管理页展示。
type RecentCall struct {
	Tool       string    `json:"tool"`
	Username   string    `json:"username"`
	OK         bool      `json:"ok"`
	Error      string    `json:"error"`
	LatencyMS  int       `json:"latency_ms"`
	TokenID    int64     `json:"token_id,omitempty"`
	AuthMode   string    `json:"auth_mode,omitempty"`
	Extension  string    `json:"extension,omitempty"`
	SharedName string    `json:"shared_name,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) RecentConnectionCalls(ctx context.Context, assetID int64, limit int) ([]RecentCall, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.tool, COALESCE(u.username, ''), c.ok, c.error, c.latency_ms, c.token_id, c.auth_mode, c.extension, c.shared_name, c.created_at
		FROM connection_calls c LEFT JOIN users u ON u.id = c.user_id
		WHERE c.asset_id = ? ORDER BY c.id DESC LIMIT ?`, assetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecentCall{}
	for rows.Next() {
		var c RecentCall
		var tokenID sql.NullInt64
		if err := rows.Scan(&c.Tool, &c.Username, &c.OK, &c.Error, &c.LatencyMS, &tokenID, &c.AuthMode, &c.Extension, &c.SharedName, &c.CreatedAt); err != nil {
			return nil, err
		}
		if tokenID.Valid {
			c.TokenID = tokenID.Int64
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
