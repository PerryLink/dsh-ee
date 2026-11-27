package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

// AuditLog 是一条操作记录。Action 形如 user.create、review.approve，前端按它翻译。
type AuditLog struct {
	ID        int64          `json:"id"`
	ActorID   *int64         `json:"actor_id"`
	ActorName string         `json:"actor_name"`
	Action    string         `json:"action"`
	Target    string         `json:"target"`
	Detail    map[string]any `json:"detail"`
	IP        string         `json:"ip"`
	Via       string         `json:"via"`
	TokenID   *int64         `json:"token_id"`
	Client    string         `json:"client"`
	CreatedAt time.Time      `json:"created_at"`
	// 操作人当前的显示名与部门，列表查询时填充。
	ActorDisplayName string `json:"actor_display_name"`
	ActorDepartment  string `json:"actor_department"`
}

func (s *Store) InsertAudit(ctx context.Context, a *AuditLog) error {
	var detail any
	if len(a.Detail) > 0 {
		b, err := json.Marshal(a.Detail)
		if err != nil {
			return err
		}
		detail = string(b)
	}
	if a.Via == "" {
		a.Via = "web"
	}
	a.CreatedAt = now()
	res, err := s.db.ExecContext(ctx,
		"INSERT INTO audit_logs (actor_id, actor_name, action, target, detail, ip, via, token_id, client, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		a.ActorID, truncateRunes(a.ActorName, 64), a.Action, auditTarget(a.Target), detail, truncateRunes(a.IP, 64), a.Via, a.TokenID, truncateRunes(a.Client, 128), a.CreatedAt)
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

// AuditFilter 按 id 倒序翻页：Before 为上一页最后一条的 id。
type AuditFilter struct {
	Before int64
	Limit  int
	// 动作前缀，如 "user." 或完整的 "review.approve"
	Action  string
	ActorID int64
	Via     string
	// 在操作人、对象里模糊搜索
	Query string
	Since time.Time
	Until time.Time
}

func (s *Store) ListAudit(ctx context.Context, f AuditFilter) ([]*AuditLog, error) {
	where := []string{"1 = 1"}
	var args []any
	if f.Before > 0 {
		where = append(where, "a.id < ?")
		args = append(args, f.Before)
	}
	if f.Action != "" {
		if strings.HasSuffix(f.Action, ".") {
			where = append(where, "a.action LIKE ?")
			args = append(args, escapeLike(f.Action)+"%")
		} else {
			where = append(where, "a.action = ?")
			args = append(args, f.Action)
		}
	}
	if f.ActorID > 0 {
		where = append(where, "a.actor_id = ?")
		args = append(args, f.ActorID)
	}
	if f.Via != "" {
		where = append(where, "a.via = ?")
		args = append(args, f.Via)
	}
	if f.Query != "" {
		q := "%" + escapeLike(f.Query) + "%"
		where = append(where, "(a.actor_name LIKE ? OR a.target LIKE ? OR u.display_name LIKE ?)")
		args = append(args, q, q, q)
	}
	if !f.Since.IsZero() {
		where = append(where, "a.created_at >= ?")
		args = append(args, f.Since)
	}
	if !f.Until.IsZero() {
		where = append(where, "a.created_at < ?")
		args = append(args, f.Until)
	}
	limit := f.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx,
		`SELECT a.id, a.actor_id, a.actor_name, a.action, a.target, a.detail, a.ip, a.via, a.token_id, a.client, a.created_at,
		        COALESCE(u.display_name, ''), COALESCE(u.department, '')
		 FROM audit_logs a LEFT JOIN users u ON u.id = a.actor_id
		 WHERE `+strings.Join(where, " AND ")+`
		 ORDER BY a.id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditLog{}
	for rows.Next() {
		var a AuditLog
		var actor, tokenID sql.NullInt64
		var detail sql.NullString
		if err := rows.Scan(&a.ID, &actor, &a.ActorName, &a.Action, &a.Target, &detail, &a.IP, &a.Via, &tokenID, &a.Client, &a.CreatedAt,
			&a.ActorDisplayName, &a.ActorDepartment); err != nil {
			return nil, err
		}
		if actor.Valid {
			v := actor.Int64
			a.ActorID = &v
		}
		if tokenID.Valid {
			a.TokenID = &tokenID.Int64
		}
		if detail.Valid && detail.String != "" {
			_ = json.Unmarshal([]byte(detail.String), &a.Detail)
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// escapeLike 转义 LIKE 里的通配符（MySQL 默认转义符是反斜杠）。
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// auditTargetMaxRunes 是 audit_logs.target 列（VARCHAR(255)）的字符上限。
const auditTargetMaxRunes = 255

// auditTargetHashHexLen 是保留的 sha256 十六进制前缀长度：12 位 = 48 bit。
const auditTargetHashHexLen = 12

// auditTarget 把审计对象压进 target 列的 VARCHAR(255)。
//
// 不能照其它短字段那样直接截断：Conan 等包源的对象路径合法长度可达约 459 字符
// （conans/<name>/<version>/<user>/<channel>/revisions/<40hex>/packages/<40hex>/…），
// 前 255 字符相同、只有尾部（revision、包 digest、文件名）不同的两条路径截断后
// 完全一样，事后无法区分是谁。这里保留前缀并追加完整 target 的 sha256 短哈希：
// 长度仍 ≤255，不同 target 可区分，同一 target 多次出现仍稳定；短 target 原样保存。
func auditTarget(s string) string {
	r := []rune(s)
	if len(r) <= auditTargetMaxRunes {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	suffix := "~" + hex.EncodeToString(sum[:])[:auditTargetHashHexLen]
	return string(r[:auditTargetMaxRunes-len(suffix)]) + suffix
}
