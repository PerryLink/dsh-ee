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
)

type RuntimeBot struct {
	ID        int64     `json:"id"`
	OwnerID   int64     `json:"owner_id"`
	RunAsID   int64     `json:"run_as_id"`
	Name      string    `json:"name"`
	Prompt    string    `json:"prompt"`
	Schedule  string    `json:"schedule"`
	Enabled   bool      `json:"enabled"`
	ReadOnly  bool      `json:"read_only"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

const runtimeBotColumns = `id, owner_id, run_as_id, name, prompt, schedule, enabled, read_only, created_at, updated_at`

func scanRuntimeBot(row interface{ Scan(...any) error }) (*RuntimeBot, error) {
	var b RuntimeBot
	err := row.Scan(&b.ID, &b.OwnerID, &b.RunAsID, &b.Name, &b.Prompt, &b.Schedule, &b.Enabled, &b.ReadOnly, &b.CreatedAt, &b.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &b, err
}

func (s *Store) SaveRuntimeBot(ctx context.Context, b *RuntimeBot) error {
	t := now()
	if b.ID == 0 {
		res, err := s.db.ExecContext(ctx, `INSERT INTO runtime_bots (owner_id, run_as_id, name, prompt, schedule, enabled, read_only, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`, b.OwnerID, b.RunAsID, b.Name, b.Prompt, b.Schedule, b.Enabled, b.ReadOnly, t, t)
		if err != nil {
			return err
		}
		b.ID, err = res.LastInsertId()
		return err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE runtime_bots SET owner_id=?, run_as_id=?, name=?, prompt=?, schedule=?, enabled=?, read_only=?, updated_at=? WHERE id=?`,
		b.OwnerID, b.RunAsID, b.Name, b.Prompt, b.Schedule, b.Enabled, b.ReadOnly, t, b.ID)
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
}

const runtimeTaskColumns = `id, owner_id, run_as_id, bot_id, prompt, status, attempts, COALESCE(idempotency_key,''), timeout_seconds,
	error, summary, created_at, updated_at, started_at, finished_at`

func scanRuntimeTask(row interface{ Scan(...any) error }) (*RuntimeTask, error) {
	var t RuntimeTask
	err := row.Scan(&t.ID, &t.OwnerID, &t.RunAsID, &t.BotID, &t.Prompt, &t.Status, &t.Attempts, &t.IdempotencyKey, &t.TimeoutSeconds,
		&t.Error, &t.Summary, &t.CreatedAt, &t.UpdatedAt, &t.StartedAt, &t.FinishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

func (s *Store) EnqueueRuntimeTask(ctx context.Context, t RuntimeTask) (*RuntimeTask, error) {
	created := now()
	r, err := s.db.ExecContext(ctx, `INSERT INTO runtime_tasks (owner_id, run_as_id, bot_id, prompt, status, idempotency_key, timeout_seconds, error, summary, created_at, updated_at)
		VALUES (?,?,?,?,?,NULLIF(?,''),?,'','',?,?)`, t.OwnerID, t.RunAsID, t.BotID, t.Prompt, RuntimeQueued, t.IdempotencyKey, t.TimeoutSeconds, created, created)
	if isDuplicate(err) && t.IdempotencyKey != "" {
		existing, e := scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks WHERE owner_id=? AND idempotency_key=?`, t.OwnerID, t.IdempotencyKey))
		if e != nil {
			return nil, e
		}
		if !sameRuntimeTask(existing, &t) {
			return nil, ErrConflict
		}
		return existing, nil
	}
	if err != nil {
		return nil, err
	}
	id, err := r.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.RuntimeTask(ctx, id, t.OwnerID)
}

func sameRuntimeTask(a, b *RuntimeTask) bool {
	sameBot := a.BotID == nil && b.BotID == nil || a.BotID != nil && b.BotID != nil && *a.BotID == *b.BotID
	return sameBot && a.RunAsID == b.RunAsID && a.Prompt == b.Prompt && a.TimeoutSeconds == b.TimeoutSeconds
}

func (s *Store) RuntimeTask(ctx context.Context, id, ownerID int64) (*RuntimeTask, error) {
	return scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks WHERE id=? AND (?=0 OR owner_id=?)`, id, ownerID, ownerID))
}

func (s *Store) RuntimeTasks(ctx context.Context, ownerID int64) ([]RuntimeTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks WHERE (?=0 OR owner_id=?) ORDER BY id DESC LIMIT 100`, ownerID, ownerID)
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
	if _, err := s.db.ExecContext(ctx, `UPDATE runtime_tasks SET status=?, error='worker did not finish', finished_at=?, updated_at=?, lease_until=NULL
		WHERE status=? AND lease_until<=? AND attempts>=?`, RuntimeFailed, t, t, RuntimeRunning, t, attemptLimit); err != nil {
		return nil, err
	}
	for {
		task, err := scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks
			WHERE status=? OR (status=? AND lease_until<=? AND attempts<?) ORDER BY id LIMIT 1`, RuntimeQueued, RuntimeRunning, t, attemptLimit))
		if err != nil {
			return nil, err
		}
		lease := t.Add(time.Duration(task.TimeoutSeconds)*time.Second + grace)
		res, err := s.db.ExecContext(ctx, `UPDATE runtime_tasks SET status=?, attempts=attempts+1, started_at=?, updated_at=?, lease_until=?
			WHERE id=? AND attempts=? AND (status=? OR (status=? AND lease_until<=?))`,
			RuntimeRunning, t, t, lease, task.ID, task.Attempts, RuntimeQueued, RuntimeRunning, t)
		switch err := runtimeAffected(res, err); {
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
	res, err := s.db.ExecContext(ctx, `UPDATE runtime_tasks SET status=?, updated_at=?, finished_at=?, lease_until=NULL
		WHERE id=? AND (?=0 OR owner_id=?) AND status IN (?,?)`, RuntimeCanceled, t, t, id, ownerID, ownerID, RuntimeQueued, RuntimeRunning)
	return runtimeAffected(res, err)
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
	var finished *time.Time
	if status != RuntimeQueued {
		finished = &t
	}
	res, err := tx.ExecContext(ctx, `UPDATE runtime_tasks SET status=?, summary=?, error=?, updated_at=?, finished_at=?, lease_until=NULL
		WHERE id=? AND status=? AND attempts=? AND lease_until>?`, status, summary, message, t, finished, task.ID, RuntimeRunning, task.Attempts, t)
	if err := runtimeAffected(res, err); err != nil {
		return err
	}
	for _, a := range artifacts {
		if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_artifacts (task_id, name, storage_key, size_bytes, created_at) VALUES (?,?,?,?,?)`,
			task.ID, a.Name, a.StorageKey, a.Size, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RuntimeArtifacts(ctx context.Context, taskID, ownerID int64) ([]RuntimeArtifact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.task_id, a.name, a.storage_key, a.size_bytes, a.created_at FROM runtime_artifacts a
		JOIN runtime_tasks t ON t.id=a.task_id WHERE t.id=? AND (?=0 OR t.owner_id=?) ORDER BY a.id`, taskID, ownerID, ownerID)
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
