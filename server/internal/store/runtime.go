package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const (
	RuntimeQueued    = "queued"
	RuntimeRunning   = "running"
	RuntimeSucceeded = "succeeded"
	RuntimeFailed    = "failed"
	RuntimeCanceled  = "canceled"

	RuntimeTaskQueueLimit = 5
)

var (
	ErrRuntimeQueueFull = errors.New("runtime task queue is full")
	ErrRuntimeBotBusy   = errors.New("runtime bot already has an active task")
	// ErrRuntimeChatBusy：这段网页对话已有一轮在排队或运行。
	ErrRuntimeChatBusy = errors.New("runtime chat conversation already has an active turn")
	// ErrRuntimeChatBotUnavailable：对话绑定的机器人当前不满足网页对话条件。
	ErrRuntimeChatBotUnavailable = errors.New("runtime chat bot is unavailable")
	// ErrRuntimeChatTeamForbidden：这是一条团队线程（或团队机器人），当前员工此刻不在它共享给的部门里，
	// 或者机器人已经取消共享 / 改共享给了别的部门。
	ErrRuntimeChatTeamForbidden = errors.New("runtime chat team thread is not shared with this employee")
)

type RuntimeBot struct {
	ID             int64  `json:"id"`
	OwnerID        int64  `json:"owner_id"`
	RunAsID        int64  `json:"run_as_id"`
	Name           string `json:"name"`
	Prompt         string `json:"prompt"`
	Schedule       string `json:"schedule"`
	Enabled        bool   `json:"enabled"`
	ReadOnly       bool   `json:"read_only"`
	DeliveryTarget string `json:"delivery_target"`
	// WebChatEnabled 允许所有者本人在网页里与机器人对话；与 Enabled（定时开关）互不影响。
	WebChatEnabled bool `json:"web_chat_enabled"`
	// SharedDepartment 是机器人共享给的部门（users.department）；空串表示不共享（默认）。
	// 只由 SetRuntimeBotShare 写入，SaveRuntimeBot 不改它。
	SharedDepartment string `json:"shared_department"`
	// ApprovalMode 是写操作的放行方式（RuntimeApproval*）；ReadOnly 与它保持一致，只读时为 true。
	ApprovalMode string `json:"approval_mode"`
	// ApprovalRules 是自动审批的规则：匹配工具全名（<连接>__<工具>）的通配模式，只在 auto 模式下生效。
	ApprovalRules []string `json:"approval_rules"`
	// ApprovalEmployeeEditable 表示管理员允许所有者本人在对话页切换审批模式（规则仍只由管理员设置）。
	ApprovalEmployeeEditable bool      `json:"approval_employee_editable"`
	CreatedAt                time.Time `json:"created_at"`
	UpdatedAt                time.Time `json:"updated_at"`
}

// 云端机器人的写操作审批模式。
const (
	// RuntimeApprovalReadOnly：只读，写工具不下发、调用一律拦下（默认，与旧行为一致）。
	RuntimeApprovalReadOnly = "read_only"
	// RuntimeApprovalManual：每个写操作都在对话里出审批卡，员工批准后才执行。
	RuntimeApprovalManual = "manual"
	// RuntimeApprovalAuto：命中自动审批规则的直接执行，其余照逐个审批出卡。
	RuntimeApprovalAuto = "auto"
	// RuntimeApprovalFull：完全放行，不再审批（仍然留审计）。
	RuntimeApprovalFull = "full"
)

// ValidRuntimeApprovalMode 判断审批模式取值是否合法。
func ValidRuntimeApprovalMode(mode string) bool {
	switch mode {
	case RuntimeApprovalReadOnly, RuntimeApprovalManual, RuntimeApprovalAuto, RuntimeApprovalFull:
		return true
	}
	return false
}

const runtimeBotColumns = `id, owner_id, run_as_id, name, prompt, schedule, enabled, read_only, delivery_target, web_chat_enabled,
	approval_mode, COALESCE(approval_rules,''), approval_employee_editable, shared_department, created_at, updated_at`

func scanRuntimeBot(row interface{ Scan(...any) error }) (*RuntimeBot, error) {
	var b RuntimeBot
	var rules string
	err := row.Scan(&b.ID, &b.OwnerID, &b.RunAsID, &b.Name, &b.Prompt, &b.Schedule, &b.Enabled, &b.ReadOnly, &b.DeliveryTarget, &b.WebChatEnabled,
		&b.ApprovalMode, &rules, &b.ApprovalEmployeeEditable, &b.SharedDepartment, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	b.ApprovalRules = decodeRuntimeApprovalRules(rules)
	return &b, nil
}

func decodeRuntimeApprovalRules(raw string) []string {
	rules := []string{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &rules)
	}
	if rules == nil {
		rules = []string{}
	}
	return rules
}

func encodeRuntimeApprovalRules(rules []string) string {
	if len(rules) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(rules)
	return string(b)
}

// SaveRuntimeBot 新建或更新机器人。webChat 是网页对话开关的显式取值：nil 表示调用方没带这个字段，
// 新建时按关闭写入，更新时由 UPDATE 语句原子地保留库里的现值（不先读后写，免得盖掉并发的关闭）。
// b.WebChatEnabled 只用于读取，不参与写入。
func (s *Store) SaveRuntimeBot(ctx context.Context, b *RuntimeBot, webChat *bool) error {
	t := now()
	if !ValidRuntimeApprovalMode(b.ApprovalMode) {
		b.ApprovalMode = RuntimeApprovalReadOnly
	}
	b.ReadOnly = b.ApprovalMode == RuntimeApprovalReadOnly
	rules := encodeRuntimeApprovalRules(b.ApprovalRules)
	if b.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO runtime_bots (owner_id, run_as_id, name, prompt, schedule, enabled, read_only, delivery_target, web_chat_enabled,
			approval_mode, approval_rules, approval_employee_editable, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, b.OwnerID, b.RunAsID, b.Name, b.Prompt, b.Schedule, b.Enabled, b.ReadOnly, b.DeliveryTarget, webChat != nil && *webChat,
			b.ApprovalMode, rules, b.ApprovalEmployeeEditable, t, t)
		if err != nil {
			return err
		}
		b.ID, err = res.LastInsertId()
		return err
	}
	var webChatParam sql.NullBool
	if webChat != nil {
		webChatParam = sql.NullBool{Bool: *webChat, Valid: true}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE runtime_bots SET name=?, prompt=?, schedule=?, enabled=?, read_only=?, delivery_target=?, web_chat_enabled=COALESCE(?, web_chat_enabled),
		approval_mode=?, approval_rules=?, approval_employee_editable=?, updated_at=? WHERE id=? AND owner_id=? AND run_as_id=?`,
		b.Name, b.Prompt, b.Schedule, b.Enabled, b.ReadOnly, b.DeliveryTarget, webChatParam, b.ApprovalMode, rules, b.ApprovalEmployeeEditable, t, b.ID, b.OwnerID, b.RunAsID)
	return runtimeAffected(res, err)
}

// SetRuntimeBotApprovalMode 是所有者本人在对话页切换审批模式：只有管理员允许员工修改、且机器人属于本人时才改得动。
// 规则和「是否允许员工修改」不动。改不动（不存在、不属于本人、不允许修改）返回 ErrNotFound。
func (s *Store) SetRuntimeBotApprovalMode(ctx context.Context, botID, ownerID int64, mode string) error {
	if !ValidRuntimeApprovalMode(mode) {
		return ErrConflict
	}
	_, err := s.db.ExecContext(ctx, `UPDATE runtime_bots SET approval_mode=?, read_only=?, updated_at=?
		WHERE id=? AND owner_id=? AND run_as_id=owner_id AND approval_employee_editable=1`,
		mode, mode == RuntimeApprovalReadOnly, now(), botID, ownerID)
	if err != nil {
		return err
	}
	// 模式没变时 MySQL 报 0 行受影响；用一次读取区分「没变」和「改不动」。
	var ok int
	err = s.db.QueryRowContext(ctx, `SELECT 1 FROM runtime_bots WHERE id=? AND owner_id=? AND run_as_id=owner_id AND approval_employee_editable=1 AND approval_mode=?`,
		botID, ownerID, mode).Scan(&ok)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// RuntimeWriteContext 是一轮云任务做写操作时要用的上下文：任务、它所属的机器人和那个机器人此刻的审批设置。
// 没有机器人的临时任务 BotID 为 0、模式为只读。
type RuntimeWriteContext struct {
	TaskID        int64
	OwnerID       int64
	RunAsID       int64
	Status        string
	BotID         int64
	BotName       string
	ApprovalMode  string
	ApprovalRules []string
}

// SetRuntimeTaskCredential 记下这一轮云任务签发的短期凭证，/mcp 用它找回任务。
func (s *Store) SetRuntimeTaskCredential(ctx context.Context, taskID, tokenID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runtime_tasks SET credential_token_id=? WHERE id=?`, tokenID, taskID)
	return err
}

// RuntimeWriteContextByToken 按云任务的短期凭证找回任务和机器人的审批设置；审批模式每次现读，管理员改了立即生效。
func (s *Store) RuntimeWriteContextByToken(ctx context.Context, tokenID int64) (*RuntimeWriteContext, error) {
	var w RuntimeWriteContext
	var botID sql.NullInt64
	var name, mode, rules sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT t.id, t.owner_id, t.run_as_id, t.status, b.id, b.name, b.approval_mode, b.approval_rules
		FROM runtime_tasks t
		LEFT JOIN runtime_chat_conversations c ON c.id = t.chat_conversation_id
		LEFT JOIN runtime_bots b ON b.id = COALESCE(t.bot_id, IF(c.owner_id=t.owner_id, c.bot_id, NULL))
			AND b.owner_id = t.owner_id AND b.run_as_id = t.run_as_id
		WHERE t.credential_token_id=? ORDER BY t.id DESC LIMIT 1`, tokenID).
		Scan(&w.TaskID, &w.OwnerID, &w.RunAsID, &w.Status, &botID, &name, &mode, &rules)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	w.ApprovalMode, w.ApprovalRules = RuntimeApprovalReadOnly, []string{}
	if botID.Valid {
		w.BotID, w.BotName = botID.Int64, name.String
		if ValidRuntimeApprovalMode(mode.String) {
			w.ApprovalMode = mode.String
		}
		w.ApprovalRules = decodeRuntimeApprovalRules(rules.String)
	}
	return &w, nil
}

func (s *Store) RuntimeBot(ctx context.Context, id int64) (*RuntimeBot, error) {
	return scanRuntimeBot(s.db.QueryRowContext(ctx, `SELECT `+runtimeBotColumns+` FROM runtime_bots WHERE id=?`, id))
}

func (s *Store) RuntimeBots(ctx context.Context, enabledOnly bool) ([]RuntimeBot, error) {
	q := `SELECT ` + runtimeBotColumns + ` FROM runtime_bots`
	if enabledOnly {
		q += ` WHERE enabled=1`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeBot{}
	for rows.Next() {
		b, err := scanRuntimeBot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

type RuntimeTask struct {
	ID             int64      `json:"id"`
	OwnerID        int64      `json:"owner_id"`
	RunAsID        int64      `json:"run_as_id"`
	BotID          *int64     `json:"bot_id,omitempty"`
	Prompt         string     `json:"-"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	IdempotencyKey string     `json:"-"`
	TimeoutSeconds int        `json:"timeout_seconds"`
	Error          string     `json:"error"`
	Summary        string     `json:"summary"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	StartedAt      *time.Time `json:"started_at,omitempty"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
	// 网页对话轮次才有；不进公开 DTO。对话轮次的 BotID 为空，机器人记在对话上。
	ChatConversationID *int64 `json:"-"`
	ChatContent        string `json:"-"`
	// 与常驻工作目录接触过的任务结果只对所有者开放。
	PrivateResult bool `json:"private_result,omitempty"`
	// A queued retry isn't claimable while its previous computer lease is active.
	RetryAfter *time.Time `json:"-"`
}

const runtimeTaskColumns = `id, owner_id, run_as_id, bot_id, prompt, status, attempts, COALESCE(idempotency_key,''), timeout_seconds,
	error, summary, created_at, updated_at, started_at, finished_at, chat_conversation_id, COALESCE(chat_content,''), private_result`

// runtimeTaskOwned 是任务读取的归属条件（两个参数都是 ownerID）：所有者能读自己的全部任务；
// ownerID=0 的管理员通配不包含网页对话和使用过常驻工作目录的任务。
func runtimeTaskOwned(prefix string) string {
	return `(` + prefix + `owner_id=? OR (?=0 AND ` + prefix + `chat_conversation_id IS NULL AND NOT ` + prefix + `private_result))`
}

func scanRuntimeTask(row interface{ Scan(...any) error }) (*RuntimeTask, error) {
	var t RuntimeTask
	err := row.Scan(&t.ID, &t.OwnerID, &t.RunAsID, &t.BotID, &t.Prompt, &t.Status, &t.Attempts, &t.IdempotencyKey, &t.TimeoutSeconds,
		&t.Error, &t.Summary, &t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.FinishedAt, &t.ChatConversationID, &t.ChatContent, &t.PrivateResult)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (s *Store) EnqueueRuntimeTask(ctx context.Context, t RuntimeTask) (*RuntimeTask, error) {
	return s.enqueueRuntimeTask(ctx, t, false)
}

func (s *Store) EnqueueScheduledRuntimeTask(ctx context.Context, t RuntimeTask) (*RuntimeTask, error) {
	return s.enqueueRuntimeTask(ctx, t, true)
}

func (s *Store) enqueueRuntimeTask(ctx context.Context, t RuntimeTask, scheduled bool) (*RuntimeTask, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Serialize admissions for the same owner across instances.
	var owner int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? FOR UPDATE`, t.OwnerID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if t.IdempotencyKey != "" {
		existing, err := scanRuntimeTask(tx.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+`
			FROM runtime_tasks WHERE owner_id=? AND idempotency_key=? FOR UPDATE`, t.OwnerID, t.IdempotencyKey))
		switch {
		case err == nil:
			if !sameRuntimeTask(existing, &t) {
				return nil, ErrConflict
			}
			return existing, nil
		case !errors.Is(err, ErrNotFound):
			return nil, err
		}
	}
	if err := checkRuntimeQueue(ctx, tx, t, scheduled); err != nil {
		return nil, err
	}
	created := now()
	r, err := tx.ExecContext(ctx, `INSERT INTO runtime_tasks (owner_id, run_as_id, bot_id, prompt, status, idempotency_key, timeout_seconds, error, summary, created_at, updated_at)
		VALUES (?,?,?,?,?,NULLIF(?,''),?,'','',?,?)`, t.OwnerID, t.RunAsID, t.BotID, t.Prompt, RuntimeQueued, t.IdempotencyKey, t.TimeoutSeconds, created, created)
	if err != nil {
		return nil, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return nil, err
	}
	if err := appendRuntimeEvent(ctx, tx, id, RuntimeQueued, 0, created); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.RuntimeTask(ctx, id, t.OwnerID)
}

func checkRuntimeQueue(ctx context.Context, tx *sql.Tx, t RuntimeTask, scheduled bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT bot_id FROM runtime_tasks
		WHERE owner_id=? AND status IN (?,?) LIMIT ? FOR UPDATE`, t.OwnerID, RuntimeQueued, RuntimeRunning, RuntimeTaskQueueLimit)
	if err != nil {
		return err
	}
	defer rows.Close()
	active := 0
	for rows.Next() {
		var botID sql.NullInt64
		if err := rows.Scan(&botID); err != nil {
			return err
		}
		if scheduled && t.BotID != nil && botID.Valid && botID.Int64 == *t.BotID {
			return ErrRuntimeBotBusy
		}
		active++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if active >= RuntimeTaskQueueLimit {
		return ErrRuntimeQueueFull
	}
	return nil
}

func sameRuntimeTask(a, b *RuntimeTask) bool {
	sameBot := a.BotID == nil && b.BotID == nil || a.BotID != nil && b.BotID != nil && *a.BotID == *b.BotID
	return sameBot && a.RunAsID == b.RunAsID && a.Prompt == b.Prompt && a.TimeoutSeconds == b.TimeoutSeconds
}

func (s *Store) RuntimeTask(ctx context.Context, id, ownerID int64) (*RuntimeTask, error) {
	return scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks WHERE id=? AND `+runtimeTaskOwned(""), id, ownerID, ownerID))
}

func (s *Store) RuntimeTasks(ctx context.Context, ownerID int64) ([]RuntimeTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks
		WHERE (owner_id=? OR ?=0) AND chat_conversation_id IS NULL ORDER BY id DESC LIMIT 100`, ownerID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeTask{}
	for rows.Next() {
		t, err := scanRuntimeTask(rows)
		if err != nil {
			return nil, err
		}
		if ownerID == 0 && t.PrivateResult {
			t.Summary, t.Error = "", ""
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Store) ClaimRuntimeTask(ctx context.Context, attemptLimit int, grace time.Duration) (*RuntimeTask, error) {
	t := now()
	if err := s.expireRuntimeTasks(ctx, t, attemptLimit); err != nil {
		return nil, err
	}
	for {
		task, err := scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks
			WHERE (status=? AND (lease_until IS NULL OR lease_until<=?)) OR (status=? AND lease_until<=? AND attempts<?)
			ORDER BY id LIMIT 1`, RuntimeQueued, t, RuntimeRunning, t, attemptLimit))
		if err != nil {
			return nil, err
		}
		lease := t.Add(time.Duration(task.TimeoutSeconds)*time.Second + grace)
		err = s.recordRuntimeTransition(ctx, task.ID, RuntimeRunning, t, `UPDATE runtime_tasks SET status=?, attempts=attempts+1, started_at=?, updated_at=?, lease_until=?
			WHERE id=? AND attempts=? AND ((status=? AND (lease_until IS NULL OR lease_until<=?)) OR (status=? AND lease_until<=?))`,
			RuntimeRunning, t, t, lease, task.ID, task.Attempts, RuntimeQueued, t, RuntimeRunning, t)
		switch {
		case errors.Is(err, ErrConflict):
			continue
		case err != nil:
			return nil, err
		}
		task.Status, task.Attempts, task.StartedAt, task.UpdatedAt = RuntimeRunning, task.Attempts+1, &t, t
		return task, nil
	}
}

func (s *Store) CancelRuntimeTask(ctx context.Context, id, ownerID int64) error {
	t := now()
	return s.recordRuntimeTransition(ctx, id, RuntimeCanceled, t, `UPDATE runtime_tasks SET status=?, updated_at=?, finished_at=?, lease_until=NULL
		WHERE id=? AND `+runtimeTaskOwned("")+` AND status IN (?,?)`, RuntimeCanceled, t, t, id, ownerID, ownerID, RuntimeQueued, RuntimeRunning)
}

func (s *Store) expireRuntimeTasks(ctx context.Context, t time.Time, attemptLimit int) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM runtime_tasks WHERE status=? AND lease_until<=? AND attempts>=?`, RuntimeRunning, t, attemptLimit)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		err := s.recordRuntimeTransition(ctx, id, RuntimeFailed, t, `UPDATE runtime_tasks SET status=?, error='worker did not finish', finished_at=?, updated_at=?, lease_until=NULL
			WHERE id=? AND status=? AND lease_until<=? AND attempts>=?`, RuntimeFailed, t, t, id, RuntimeRunning, t, attemptLimit)
		if err != nil && !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return nil
}

func (s *Store) recordRuntimeTransition(ctx context.Context, id int64, status string, t time.Time, query string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := runtimeAffected(tx.ExecContext(ctx, query, args...)); err != nil {
		return err
	}
	var attempt int
	if err := tx.QueryRowContext(ctx, `SELECT attempts FROM runtime_tasks WHERE id=?`, id).Scan(&attempt); err != nil {
		return err
	}
	if err := appendRuntimeEvent(ctx, tx, id, status, attempt, t); err != nil {
		return err
	}
	return tx.Commit()
}

func appendRuntimeEvent(ctx context.Context, tx *sql.Tx, id int64, status string, attempt int, t time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO runtime_task_events (task_id, status, attempt, created_at) VALUES (?,?,?,?)`, id, status, attempt, t)
	return err
}

type RuntimeTaskEvent struct {
	ID        int64     `json:"id"`
	Status    string    `json:"status"`
	Attempt   int       `json:"attempt"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) RuntimeTaskEvents(ctx context.Context, id, owner int64) ([]RuntimeTaskEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.status,e.attempt,e.created_at FROM runtime_task_events e
		JOIN runtime_tasks t ON t.id=e.task_id WHERE t.id=? AND `+runtimeTaskOwned("t.")+` ORDER BY e.id`, id, owner, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeTaskEvent{}
	for rows.Next() {
		var event RuntimeTaskEvent
		if err := rows.Scan(&event.ID, &event.Status, &event.Attempt, &event.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

type RuntimeArtifact struct {
	ID         int64     `json:"id"`
	TaskID     int64     `json:"task_id"`
	Name       string    `json:"name"`
	StorageKey string    `json:"-"`
	Size       int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) FinishRuntimeTask(ctx context.Context, task *RuntimeTask, status, summary, message string, artifacts []RuntimeArtifact) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	var finished, retryAfter *time.Time
	if status == RuntimeQueued {
		retryAfter = task.RetryAfter
	} else {
		finished = &t
	}
	res, err := tx.ExecContext(ctx, `UPDATE runtime_tasks SET status=?, summary=?, error=?, updated_at=?, finished_at=?, lease_until=?, private_result=private_result OR ?
		WHERE id=? AND status=? AND attempts=? AND lease_until>?`, status, summary, message, t, finished, retryAfter, task.PrivateResult, task.ID, RuntimeRunning, task.Attempts, t)
	if err := runtimeAffected(res, err); err != nil {
		return err
	}
	for _, a := range artifacts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_artifacts (task_id, name, storage_key, size_bytes, created_at) VALUES (?,?,?,?,?)`,
			task.ID, a.Name, a.StorageKey, a.Size, t); err != nil {
			return err
		}
	}
	if err := appendRuntimeEvent(ctx, tx, task.ID, status, task.Attempts, t); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) RuntimeArtifacts(ctx context.Context, taskID, ownerID int64) ([]RuntimeArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.task_id, a.name, a.storage_key, a.size_bytes, a.created_at FROM runtime_artifacts a
		JOIN runtime_tasks t ON t.id=a.task_id WHERE t.id=? AND `+runtimeTaskOwned("t.")+` ORDER BY a.id`, taskID, ownerID, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeArtifact{}
	for rows.Next() {
		var a RuntimeArtifact
		if err := rows.Scan(&a.ID, &a.TaskID, &a.Name, &a.StorageKey, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func runtimeAffected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return ErrConflict
	}
	return err
}

// SetRuntimeBotShare 把机器人共享给 department（users.department 的取值；空串表示取消共享），返回原来共享给的部门。
// ownerID>0 时只能改这位员工本人、以本人身份运行的机器人（所有者自己操作），共享时还要求只读且开着网页对话；
// ownerID=0 是管理员，不限所有者。
// 取消共享或改到别的部门时，原团队线程里排队 / 运行中的轮次在同一个事务里取消：原部门的成员已经看不到这条线程，
// 结果不该再写进去；正在运行的轮次由 worker 的状态巡检停下沙箱（见 runCloudClaim）。
func (s *Store) SetRuntimeBotShare(ctx context.Context, botID, ownerID int64, department string) (string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var previous string
	// 所有者取消共享不看网页对话开关（管理员关掉网页对话后所有者仍能收回共享）；共享时才要求机器人此刻可对话。
	err = tx.QueryRowContext(ctx, `SELECT shared_department FROM runtime_bots
		WHERE id=? AND (?=0 OR (owner_id=? AND run_as_id=owner_id AND (?='' OR (read_only=1 AND web_chat_enabled=1)))) FOR UPDATE`,
		botID, ownerID, ownerID, department).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	t := now()
	if previous != department {
		if _, err := tx.ExecContext(ctx, `UPDATE runtime_bots SET shared_department=?, updated_at=? WHERE id=?`, department, t, botID); err != nil {
			return "", err
		}
	}
	if previous != "" && previous != department {
		rows, err := tx.QueryContext(ctx, `SELECT t.id, t.attempts FROM runtime_tasks t
			JOIN runtime_chat_conversations c ON c.id=t.chat_conversation_id
			WHERE c.owner_id=0 AND c.bot_id=? AND c.shared_department=? AND t.status IN (?,?) FOR UPDATE`,
			botID, previous, RuntimeQueued, RuntimeRunning)
		if err != nil {
			return "", err
		}
		type active struct {
			id      int64
			attempt int
		}
		var tasks []active
		for rows.Next() {
			var a active
			if err := rows.Scan(&a.id, &a.attempt); err != nil {
				rows.Close()
				return "", err
			}
			tasks = append(tasks, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return "", err
		}
		for _, a := range tasks {
			if _, err := tx.ExecContext(ctx, `UPDATE runtime_tasks SET status=?, updated_at=?, finished_at=?, lease_until=NULL WHERE id=? AND status IN (?,?)`,
				RuntimeCanceled, t, t, a.id, RuntimeQueued, RuntimeRunning); err != nil {
				return "", err
			}
			if err := appendRuntimeEvent(ctx, tx, a.id, RuntimeCanceled, a.attempt, t); err != nil {
				return "", err
			}
		}
	}
	return previous, tx.Commit()
}

// RuntimeTeamChatTask 返回团队线程里的一轮（云任务），条件是这条线程此刻对 userID 可见（见 runtimeChatTeamVisible）。
// 任务存在且属于团队线程、但员工看不到时是 ErrRuntimeChatTeamForbidden；不是团队线程的任务一律 ErrNotFound。
// 只给产物查看和下载用：事件、取消仍然只认触发这一轮的员工本人。
func (s *Store) RuntimeTeamChatTask(ctx context.Context, taskID, userID int64) (*RuntimeTask, error) {
	task, err := scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks WHERE id=? AND chat_conversation_id IN
		(SELECT c.id FROM runtime_chat_conversations c JOIN runtime_bots b ON b.id=c.bot_id WHERE `+runtimeChatTeamVisible+`)`, taskID, userID))
	if !errors.Is(err, ErrNotFound) {
		return task, err
	}
	var team int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_tasks t JOIN runtime_chat_conversations c ON c.id=t.chat_conversation_id
		WHERE t.id=? AND c.owner_id=0`, taskID).Scan(&team); err != nil {
		return nil, err
	}
	if team > 0 {
		return nil, ErrRuntimeChatTeamForbidden
	}
	return nil, ErrNotFound
}
