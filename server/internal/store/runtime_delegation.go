package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
)

// 机器人之间派任务（docs/dev/bot-delegation.md）。一次委派属于一位员工：发起方和目标都是他自己的、
// 以他本人身份运行、开着网页对话的机器人。委派先记成 pending，发起的那一轮成功结束、释放电脑之后，
// 才在目标机器人的线程里入队一轮（dispatched）；那一轮就是普通的网页对话轮次，同样的只读、额度和排队上限。

const (
	RuntimeDelegationPending    = "pending"
	RuntimeDelegationDispatched = "dispatched"
	RuntimeDelegationFailed     = "failed"
	RuntimeDelegationCanceled   = "canceled"

	// task：要结果，全部完成后结果回到发起方的线程，发起方接着处理；message：只是告知，不追加回合。
	RuntimeDelegationTask    = "task"
	RuntimeDelegationMessage = "message"

	// 防循环与防扇出的上限。
	// RuntimeDelegationMaxDepth：根（员工的一条消息或一次定时运行）往下最多派几层。
	RuntimeDelegationMaxDepth = 3
	// RuntimeDelegationMaxPerRun：一轮最多派出几个。
	RuntimeDelegationMaxPerRun = 3
	// RuntimeDelegationMaxPerRoot：一棵委派树（同一个根引出的所有轮次，包括结果回来后的追加轮）一共最多派几个。
	RuntimeDelegationMaxPerRoot = 8

	// RuntimeBotCreatedLimit：机器人替一位员工新建的机器人（仍然存在的）最多几个。
	RuntimeBotCreatedLimit = 5
)

var (
	ErrRuntimeDelegationCycle     = errors.New("delegation would loop back to a bot already in the chain")
	ErrRuntimeDelegationDepth     = errors.New("delegation chain is too deep")
	ErrRuntimeDelegationFanout    = errors.New("this run already delegated the maximum number of tasks")
	ErrRuntimeDelegationTotal     = errors.New("this conversation already delegated the maximum number of tasks")
	ErrRuntimeDelegationDuplicate = errors.New("this run already has a pending delegation to that bot")
	ErrRuntimeBotCreateLimit      = errors.New("the employee already has the maximum number of bot-created bots")
)

type RuntimeDelegation struct {
	ID                   int64  `json:"id"`
	OwnerID              int64  `json:"-"`
	ParentTaskID         int64  `json:"parent_task_id"`
	ParentBotID          int64  `json:"parent_bot_id"`
	ParentBotName        string `json:"parent_bot_name"`
	ParentConversationID *int64 `json:"parent_conversation_id,omitempty"`
	RootTaskID           int64  `json:"-"`
	Depth                int    `json:"depth"`
	Chain                string `json:"-"`
	TargetBotID          int64  `json:"target_bot_id"`
	TargetBotName        string `json:"target_bot_name"`
	Kind                 string `json:"kind"`
	Content              string `json:"content"`
	// Status 是对外的状态：pending / failed / canceled，派出去之后就是目标那一轮任务的状态
	// （queued / running / succeeded / failed / canceled）。
	Status              string    `json:"status"`
	Error               string    `json:"error,omitempty"`
	ChildTaskID         *int64    `json:"child_task_id,omitempty"`
	ChildConversationID *int64    `json:"child_conversation_id,omitempty"`
	FollowupTaskID      *int64    `json:"followup_task_id,omitempty"`
	Result              string    `json:"result,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	// stored 是库里的状态（dispatched 时 Status 是子任务的状态）。
	stored string
}

// Dispatched 表示已经在目标线程里入队。
func (d *RuntimeDelegation) Dispatched() bool { return d.stored == RuntimeDelegationDispatched }

// Terminal 表示这次委派不会再变化（派不出去、被取消，或目标那一轮已结束）。
func (d *RuntimeDelegation) Terminal() bool {
	switch d.Status {
	case RuntimeDelegationFailed, RuntimeDelegationCanceled, RuntimeSucceeded:
		return true
	}
	return false
}

const runtimeDelegationQuery = `SELECT d.id, d.owner_id, d.parent_task_id, d.parent_bot_id, COALESCE(pb.name,''), d.parent_conversation_id, d.root_task_id,
	d.depth, d.chain, d.target_bot_id, COALESCE(tb.name,''), d.kind, d.content, d.status, d.error, d.child_task_id, d.child_conversation_id,
	d.followup_task_id, COALESCE(ct.status,''), COALESCE(ct.summary,''), d.created_at, d.updated_at
	FROM runtime_bot_delegations d
	LEFT JOIN runtime_bots pb ON pb.id=d.parent_bot_id
	LEFT JOIN runtime_bots tb ON tb.id=d.target_bot_id
	LEFT JOIN runtime_tasks ct ON ct.id=d.child_task_id`

func scanRuntimeDelegation(row interface{ Scan(...any) error }) (*RuntimeDelegation, error) {
	var d RuntimeDelegation
	var childStatus, childSummary string
	err := row.Scan(&d.ID, &d.OwnerID, &d.ParentTaskID, &d.ParentBotID, &d.ParentBotName, &d.ParentConversationID, &d.RootTaskID,
		&d.Depth, &d.Chain, &d.TargetBotID, &d.TargetBotName, &d.Kind, &d.Content, &d.stored, &d.Error, &d.ChildTaskID, &d.ChildConversationID,
		&d.FollowupTaskID, &childStatus, &childSummary, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Status = d.stored
	if d.stored == RuntimeDelegationDispatched && childStatus != "" {
		d.Status = childStatus
		if childStatus == RuntimeSucceeded {
			d.Result = childSummary
		}
	}
	return &d, nil
}

func (s *Store) queryRuntimeDelegations(ctx context.Context, where string, args ...any) ([]RuntimeDelegation, error) {
	rows, err := s.db.QueryContext(ctx, runtimeDelegationQuery+` WHERE `+where+` ORDER BY d.id LIMIT 200`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeDelegation{}
	for rows.Next() {
		d, err := scanRuntimeDelegation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// RuntimeTaskByCredential 按短期凭证找回正在运行的那一轮；任务不在运行时返回 ErrNotFound。
func (s *Store) RuntimeTaskByCredential(ctx context.Context, tokenID int64) (*RuntimeTask, error) {
	return scanRuntimeTask(s.db.QueryRowContext(ctx, `SELECT `+runtimeTaskColumns+` FROM runtime_tasks
		WHERE credential_token_id=? AND status=?`, tokenID, RuntimeRunning))
}

// RuntimeDelegationLineage 是一轮任务在委派树里的位置：根任务、层数和从根到它自己的机器人链。
// 目标线程里的委派轮次沿用那次委派的位置；结果回来后的追加轮是发起方继续工作，位置和发起方那一轮一样。
type RuntimeDelegationLineage struct {
	RootTaskID int64
	Depth      int
	Chain      []int64
}

func parseDelegationChain(chain string) []int64 {
	out := []int64{}
	for _, part := range strings.Split(strings.Trim(chain, ","), ",") {
		if id, err := strconv.ParseInt(part, 10, 64); err == nil {
			out = append(out, id)
		}
	}
	return out
}

func formatDelegationChain(ids []int64) string {
	var b strings.Builder
	b.WriteString(",")
	for _, id := range ids {
		b.WriteString(strconv.FormatInt(id, 10))
		b.WriteString(",")
	}
	return b.String()
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func runtimeDelegationLineage(ctx context.Context, q queryRower, taskID, botID int64) (RuntimeDelegationLineage, error) {
	var root int64
	var depth int
	var chain string
	err := q.QueryRowContext(ctx, `SELECT root_task_id, depth, chain FROM runtime_bot_delegations WHERE child_task_id=? LIMIT 1`, taskID).Scan(&root, &depth, &chain)
	if err == nil {
		return RuntimeDelegationLineage{RootTaskID: root, Depth: depth, Chain: parseDelegationChain(chain)}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RuntimeDelegationLineage{}, err
	}
	err = q.QueryRowContext(ctx, `SELECT root_task_id, depth, chain FROM runtime_bot_delegations WHERE followup_task_id=? ORDER BY id LIMIT 1`, taskID).Scan(&root, &depth, &chain)
	if err == nil {
		ids := parseDelegationChain(chain)
		if len(ids) > 0 {
			ids = ids[:len(ids)-1]
		}
		return RuntimeDelegationLineage{RootTaskID: root, Depth: depth - 1, Chain: ids}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return RuntimeDelegationLineage{}, err
	}
	return RuntimeDelegationLineage{RootTaskID: taskID, Depth: 0, Chain: []int64{botID}}, nil
}

// RuntimeDelegationLineage 读一轮任务在委派树里的位置（见 RuntimeDelegationLineage 类型）。
func (s *Store) RuntimeDelegationLineage(ctx context.Context, taskID, botID int64) (RuntimeDelegationLineage, error) {
	return runtimeDelegationLineage(ctx, s.db, taskID, botID)
}

// CreateRuntimeDelegation 记下一次委派（pending）。在所有者锁下检查：目标是所有者本人现在可对话的机器人
// （否则 ErrNotFound，不区分不存在和属于别人）；目标不在链上（ErrRuntimeDelegationCycle，包括派给自己）；
// 不超过层数、这一轮的数量和整棵树的数量上限；这一轮没有派给同一个机器人的待派委派。
// d 需要填 OwnerID、ParentTaskID、ParentBotID、ParentConversationID、TargetBotID、Kind、Content；
// 成功后 ID、RootTaskID、Depth、Chain、Status 被填上。
func (s *Store) CreateRuntimeDelegation(ctx context.Context, d *RuntimeDelegation) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? FOR UPDATE`, d.OwnerID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var target int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM runtime_bots WHERE id=? AND `+runtimeChatBotEligible, d.TargetBotID, d.OwnerID).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	lineage, err := runtimeDelegationLineage(ctx, tx, d.ParentTaskID, d.ParentBotID)
	if err != nil {
		return err
	}
	for _, id := range lineage.Chain {
		if id == d.TargetBotID {
			return ErrRuntimeDelegationCycle
		}
	}
	if d.TargetBotID == d.ParentBotID {
		return ErrRuntimeDelegationCycle
	}
	if lineage.Depth+1 > RuntimeDelegationMaxDepth {
		return ErrRuntimeDelegationDepth
	}
	var perRun, perRoot, duplicate int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(target_bot_id=? AND status=?),0) FROM runtime_bot_delegations WHERE parent_task_id=?`,
		d.TargetBotID, RuntimeDelegationPending, d.ParentTaskID).Scan(&perRun, &duplicate); err != nil {
		return err
	}
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_bot_delegations WHERE root_task_id=?`, lineage.RootTaskID).Scan(&perRoot); err != nil {
		return err
	}
	switch {
	case duplicate > 0:
		return ErrRuntimeDelegationDuplicate
	case perRun >= RuntimeDelegationMaxPerRun:
		return ErrRuntimeDelegationFanout
	case perRoot >= RuntimeDelegationMaxPerRoot:
		return ErrRuntimeDelegationTotal
	}
	d.RootTaskID, d.Depth, d.Chain = lineage.RootTaskID, lineage.Depth+1, formatDelegationChain(append(lineage.Chain, d.TargetBotID))
	d.Status, d.stored = RuntimeDelegationPending, RuntimeDelegationPending
	t := now()
	d.CreatedAt, d.UpdatedAt = t, t
	r, err := tx.ExecContext(ctx, `INSERT INTO runtime_bot_delegations (owner_id, parent_task_id, parent_bot_id, parent_conversation_id, root_task_id, depth, chain,
		target_bot_id, kind, content, status, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.OwnerID, d.ParentTaskID, d.ParentBotID, d.ParentConversationID, d.RootTaskID, d.Depth, d.Chain, d.TargetBotID, d.Kind, d.Content, d.Status, t, t)
	if err != nil {
		return err
	}
	if d.ID, err = r.LastInsertId(); err != nil {
		return err
	}
	return tx.Commit()
}

// RuntimeDelegation 读所有者的一次委派。
func (s *Store) RuntimeDelegation(ctx context.Context, id, ownerID int64) (*RuntimeDelegation, error) {
	return scanRuntimeDelegation(s.db.QueryRowContext(ctx, runtimeDelegationQuery+` WHERE d.id=? AND d.owner_id=?`, id, ownerID))
}

// RuntimeDelegationsByParent 是一轮派出的所有委派（按派出顺序）。
func (s *Store) RuntimeDelegationsByParent(ctx context.Context, parentTaskID int64) ([]RuntimeDelegation, error) {
	return s.queryRuntimeDelegations(ctx, `d.parent_task_id=?`, parentTaskID)
}

// RuntimeDelegationByChild 是派到目标线程里的那一轮对应的委派；不是委派轮次时返回 ErrNotFound。
func (s *Store) RuntimeDelegationByChild(ctx context.Context, childTaskID int64) (*RuntimeDelegation, error) {
	return scanRuntimeDelegation(s.db.QueryRowContext(ctx, runtimeDelegationQuery+` WHERE d.child_task_id=? LIMIT 1`, childTaskID))
}

// RuntimeDelegationByFollowup 是以这一轮作为结果追加轮的委派（任取一条，它们的发起轮相同）；不是追加轮时返回 ErrNotFound。
func (s *Store) RuntimeDelegationByFollowup(ctx context.Context, followupTaskID int64) (*RuntimeDelegation, error) {
	return scanRuntimeDelegation(s.db.QueryRowContext(ctx, runtimeDelegationQuery+` WHERE d.followup_task_id=? ORDER BY d.id LIMIT 1`, followupTaskID))
}

// RuntimeDelegationsForTasks 是和这些轮次有关的委派（它们派出的、它们执行的、它们是结果追加轮的），只限所有者本人。
func (s *Store) RuntimeDelegationsForTasks(ctx context.Context, ownerID int64, taskIDs []int64) ([]RuntimeDelegation, error) {
	if len(taskIDs) == 0 {
		return []RuntimeDelegation{}, nil
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(taskIDs)), ",")
	args := []any{ownerID}
	for range 3 {
		for _, id := range taskIDs {
			args = append(args, id)
		}
	}
	return s.queryRuntimeDelegations(ctx, `d.owner_id=? AND (d.parent_task_id IN (`+in+`) OR d.child_task_id IN (`+in+`) OR d.followup_task_id IN (`+in+`))`, args...)
}

// MarkRuntimeDelegationDispatched 记下委派已在目标线程入队。只改 pending 的；改不动返回 ErrConflict。
func (s *Store) MarkRuntimeDelegationDispatched(ctx context.Context, id, childTaskID, childConversationID int64) error {
	return runtimeAffected(s.db.ExecContext(ctx, `UPDATE runtime_bot_delegations SET status=?, child_task_id=?, child_conversation_id=?, updated_at=?
		WHERE id=? AND status=?`, RuntimeDelegationDispatched, childTaskID, childConversationID, now(), id, RuntimeDelegationPending))
}

// FinishRuntimeDelegation 把一次没能派出去的委派记成 failed 或 canceled（只改 pending 的）。
func (s *Store) FinishRuntimeDelegation(ctx context.Context, id int64, status, message string) error {
	if len(message) > 500 {
		message = message[:500]
	}
	return runtimeAffected(s.db.ExecContext(ctx, `UPDATE runtime_bot_delegations SET status=?, error=?, updated_at=? WHERE id=? AND status=?`,
		status, message, now(), id, RuntimeDelegationPending))
}

// SetRuntimeDelegationFollowup 把结果追加轮记到这一轮派出的所有 task 委派上。已经记过时返回 ErrConflict。
func (s *Store) SetRuntimeDelegationFollowup(ctx context.Context, parentTaskID, followupTaskID int64) error {
	return runtimeAffected(s.db.ExecContext(ctx, `UPDATE runtime_bot_delegations SET followup_task_id=?, updated_at=?
		WHERE parent_task_id=? AND kind=? AND followup_task_id IS NULL`, followupTaskID, now(), parentTaskID, RuntimeDelegationTask))
}

// CreateRuntimeBotByBot 替员工新建一个机器人（由他的另一个机器人发起，员工已经同意）：
// 在所有者锁下检查机器人建的、仍然存在的机器人不超过 RuntimeBotCreatedLimit，然后写入机器人和来源记录。
// 新机器人以所有者身份运行、只读、开着网页对话、不开定时。
func (s *Store) CreateRuntimeBotByBot(ctx context.Context, b *RuntimeBot, createdByBot, createdByTask int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? FOR UPDATE`, b.OwnerID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	var created int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_bot_origins o JOIN runtime_bots b ON b.id=o.bot_id WHERE o.owner_id=?`, b.OwnerID).Scan(&created); err != nil {
		return err
	}
	if created >= RuntimeBotCreatedLimit {
		return ErrRuntimeBotCreateLimit
	}
	t := now()
	b.RunAsID, b.ReadOnly, b.Enabled, b.WebChatEnabled = b.OwnerID, true, false, true
	res, err := tx.ExecContext(ctx, `INSERT INTO runtime_bots (owner_id, run_as_id, name, prompt, schedule, enabled, read_only, delivery_target, web_chat_enabled, created_at, updated_at)
		VALUES (?,?,?,?,?,0,1,?,1,?,?)`, b.OwnerID, b.RunAsID, b.Name, b.Prompt, b.Schedule, b.DeliveryTarget, t, t)
	if err != nil {
		return err
	}
	if b.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO runtime_bot_origins (bot_id, owner_id, created_by_bot_id, created_by_task_id, created_at) VALUES (?,?,?,?,?)`,
		b.ID, b.OwnerID, createdByBot, createdByTask, t); err != nil {
		return err
	}
	b.CreatedAt, b.UpdatedAt = t, t
	return tx.Commit()
}

// RuntimeBotsCreatedByBots 是机器人替这位员工建的、仍然存在的机器人数。
func (s *Store) RuntimeBotsCreatedByBots(ctx context.Context, ownerID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_bot_origins o JOIN runtime_bots b ON b.id=o.bot_id WHERE o.owner_id=?`, ownerID).Scan(&n)
	return n, err
}

// RuntimeColleagueBots 是所有者本人现在可以对话（也就可以派任务）的机器人，按 ID 排序。
func (s *Store) RuntimeColleagueBots(ctx context.Context, ownerID int64) ([]RuntimeBot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeBotColumns+` FROM runtime_bots WHERE `+runtimeChatBotEligible+` ORDER BY id LIMIT 100`, ownerID)
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
