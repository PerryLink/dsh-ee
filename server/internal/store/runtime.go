package store

import (
	"context"
	"database/sql"
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
	WebChatEnabled bool      `json:"web_chat_enabled"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const runtimeBotColumns = `id, owner_id, run_as_id, name, prompt, schedule, enabled, read_only, delivery_target, web_chat_enabled, created_at, updated_at`

func scanRuntimeBot(row interface{ Scan(...any) error }) (*RuntimeBot, error) {
	var b RuntimeBot
	err := row.Scan(&b.ID, &b.OwnerID, &b.RunAsID, &b.Name, &b.Prompt, &b.Schedule, &b.Enabled, &b.ReadOnly, &b.DeliveryTarget, &b.WebChatEnabled, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

// SaveRuntimeBot 新建或更新机器人。webChat 是网页对话开关的显式取值：nil 表示调用方没带这个字段，
// 新建时按关闭写入，更新时由 UPDATE 语句原子地保留库里的现值（不先读后写，免得盖掉并发的关闭）。
// b.WebChatEnabled 只用于读取，不参与写入。
func (s *Store) SaveRuntimeBot(ctx context.Context, b *RuntimeBot, webChat *bool) error {
	t := now()
	if b.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO runtime_bots (owner_id, run_as_id, name, prompt, schedule, enabled, read_only, delivery_target, web_chat_enabled, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)`, b.OwnerID, b.RunAsID, b.Name, b.Prompt, b.Schedule, b.Enabled, b.ReadOnly, b.DeliveryTarget, webChat != nil && *webChat, t, t)
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
	res, err := s.db.ExecContext(ctx, `UPDATE runtime_bots SET name=?, prompt=?, schedule=?, enabled=?, read_only=?, delivery_target=?, web_chat_enabled=COALESCE(?, web_chat_enabled), updated_at=? WHERE id=? AND owner_id=? AND run_as_id=?`,
		b.Name, b.Prompt, b.Schedule, b.Enabled, b.ReadOnly, b.DeliveryTarget, webChatParam, t, b.ID, b.OwnerID, b.RunAsID)
	return runtimeAffected(res, err)
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
	PrivateResult bool `json:"-"`
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
		WHERE `+runtimeTaskOwned("")+` AND chat_conversation_id IS NULL ORDER BY id DESC LIMIT 100`, ownerID, ownerID)
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
