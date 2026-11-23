package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// 员工网页对话：每位员工和每个机器人之间只有一条长期线程（runtime_chat_conversations，
// owner_id + bot_id 唯一），只属于所有者本人。每一轮就是一条带 chat_conversation_id 的
// runtime_tasks 记录——员工原话存在 chat_content，状态和回复直接读任务的 status / summary，
// 不另存一份，也永远不因为上下文压缩而删改：线程上的 context_summary 只是给提示词用的有界摘录。

const (
	runtimeChatListLimit = 100
	// RuntimeChatHistoryPage / RuntimeChatHistoryMax 是历史分页的默认和最大页大小。
	RuntimeChatHistoryPage = 50
	RuntimeChatHistoryMax  = 200
	// 原样拼进提示词的最近成功轮次上限；字节预算由调用方的 Prompt 回调把关。
	runtimeChatContextTurns = 10
	// 补齐滚动记忆时每批读取的旧轮次数（一轮最多几十 KB，分批读免得一次占太多内存）。
	runtimeChatFoldBatch = 20
	// context_summary 是 TEXT 列：调用方的摘录预算远小于它，这里只是最后一道防线。
	runtimeChatMemoryMax = 60000
	// 机器人列表里最近一条消息预览的码点上限（含截断标记）。
	runtimeChatPreviewRunes = 160
	// 机器人资料页最多列出的产物数。
	runtimeChatArtifactLimit = 50
)

// runtimeChatBotEligible 是机器人可用于网页对话的条件（参数是当前员工 ID）：
// 所有者本人、以所有者身份运行、且管理员打开了网页对话。写操作是否放行由机器人的审批模式决定
// （只读模式与旧行为一致），不再要求机器人只读。
const runtimeChatBotEligible = `owner_id=? AND run_as_id=owner_id AND web_chat_enabled=1`

// RuntimeChatBot 是员工可对话的机器人。线程和最近一轮的字段只在存在时给出：
// 还没打开过线程的机器人只有 id / name。
type RuntimeChatBot struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// ThreadID 是这位员工与机器人的线程（对话）ID。
	ThreadID int64 `json:"thread_id,omitempty"`
	// LastMessage 是最近一轮的预览：成功且有回复时是回复，否则是员工原话；有界、压成一行。
	LastMessage string `json:"last_message,omitempty"`
	LastTaskID  int64  `json:"last_task_id,omitempty"`
	LastStatus  string `json:"last_status,omitempty"`
	// ApprovalMode 是机器人的写操作审批模式；ApprovalEditable 表示管理员允许本人在对话页切换。
	ApprovalMode     string `json:"approval_mode"`
	ApprovalEditable bool   `json:"approval_editable"`
}

// RuntimeChatArtifact 是机器人资料页里的一个产物；下载走任务产物接口
// （/api/runtime/tasks/{task_id}/artifacts/{id}）。Source 是 chat（对话轮次）或 task（定时 / 手动任务）。
type RuntimeChatArtifact struct {
	ID        int64     `json:"id"`
	TaskID    int64     `json:"task_id"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	CreatedAt time.Time `json:"created_at"`
	Source    string    `json:"source"`
}

// RuntimeChatBotProfile 是所有者本人看到的机器人资料：定时设置只读，Running 只看有没有正在运行的任务。
type RuntimeChatBotProfile struct {
	Schedule        string                `json:"schedule"`
	ScheduleEnabled bool                  `json:"schedule_enabled"`
	Running         bool                  `json:"running"`
	Artifacts       []RuntimeChatArtifact `json:"artifacts"`
}

type RuntimeChatConversation struct {
	ID        int64     `json:"id"`
	OwnerID   int64     `json:"-"`
	BotID     int64     `json:"bot_id"`
	BotName   string    `json:"bot_name"`
	Title     string    `json:"title"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RuntimeChatTurn 是对话里的一轮：ID 是云任务 ID，Content 是员工原话，Summary 是回复。
// 故意不带任务的 error 诊断信息。
type RuntimeChatTurn struct {
	ID        int64     `json:"id"`
	Content   string    `json:"content"`
	Status    string    `json:"status"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

type RuntimeChatMessage struct {
	OwnerID        int64
	ConversationID int64
	Content        string
	// Title 在对话还没有标题时写入（第一条消息）。
	Title          string
	IdempotencyKey string
	TimeoutSeconds int
}

// RuntimeChatPlanner 由调用方提供，在入队事务里决定这一轮的上下文。两个函数都必须是确定性的纯函数。
type RuntimeChatPlanner struct {
	// Fold 把一轮成功的问答并入滚动记忆，返回新的记忆（有界）。
	Fold func(memory string, turn RuntimeChatTurn) string
	// Prompt 用机器人定义、滚动记忆和记忆之后的成功轮次（从旧到新）拼出任务提示词。
	// kept 是原样放进提示词的轮次数（recent 的后缀）；放不下的前缀必须由它自己并入返回的 updated，
	// 存储层据此推进水位，保证每一轮要么在记忆里、要么在原样历史里，不重不漏。
	Prompt func(bot *RuntimeBot, memory string, recent []RuntimeChatTurn) (prompt, updated string, kept int)
}

const runtimeChatConversationQuery = `SELECT c.id, c.owner_id, c.bot_id, COALESCE(b.name,''), c.title, c.updated_at
	FROM runtime_chat_conversations c LEFT JOIN runtime_bots b ON b.id=c.bot_id`

// runtimeChatTurnColumns 直接从 runtime_tasks 读一轮；chat_content 带别名，外层查询才能按列名引用。
const runtimeChatTurnColumns = `id, COALESCE(chat_content,'') AS chat_content, status, summary, created_at`

// runtimeChatRecentTurnsQuery 取满足 where 的最近 N 轮（最后一个参数是 LIMIT），再按时间正序返回。
// 外层只引用派生表 recent 导出的列名，不重复内层的表达式。
func runtimeChatRecentTurnsQuery(where string) string {
	return `SELECT id, chat_content, status, summary, created_at FROM (SELECT ` + runtimeChatTurnColumns + ` FROM runtime_tasks
		WHERE ` + where + ` ORDER BY id DESC LIMIT ?) recent ORDER BY id`
}

func scanRuntimeChatConversation(row interface{ Scan(...any) error }) (*RuntimeChatConversation, error) {
	var c RuntimeChatConversation
	err := row.Scan(&c.ID, &c.OwnerID, &c.BotID, &c.BotName, &c.Title, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &c, err
}

func scanRuntimeChatTurns(rows *sql.Rows) ([]RuntimeChatTurn, error) {
	defer rows.Close()
	out := []RuntimeChatTurn{}
	for rows.Next() {
		var t RuntimeChatTurn
		if err := rows.Scan(&t.ID, &t.Content, &t.Status, &t.Summary, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RuntimeChatBots 列出员工本人现在可以对话的机器人，带上已有线程的 ID 和最近一轮的预览。
func (s *Store) RuntimeChatBots(ctx context.Context, ownerID int64) ([]RuntimeChatBot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, approval_mode, approval_employee_editable FROM runtime_bots WHERE `+runtimeChatBotEligible+` ORDER BY id`, ownerID)
	if err != nil {
		return nil, err
	}
	out := []RuntimeChatBot{}
	for rows.Next() {
		var b RuntimeChatBot
		if err := rows.Scan(&b.ID, &b.Name, &b.ApprovalMode, &b.ApprovalEditable); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(out) == 0 {
		return out, err
	}
	threads := map[int64]int64{}
	rows, err = s.db.QueryContext(ctx, `SELECT bot_id, id FROM runtime_chat_conversations WHERE owner_id=?`, ownerID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var bot, thread int64
		if err := rows.Scan(&bot, &thread); err != nil {
			rows.Close()
			return nil, err
		}
		threads[bot] = thread
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		b := &out[i]
		if b.ThreadID = threads[b.ID]; b.ThreadID == 0 {
			continue
		}
		// 每个机器人一条线程，按 (chat_conversation_id, id) 索引取最后一轮；LEFT 按字符截，只取预览用得到的部分。
		var content, reply string
		err := s.db.QueryRowContext(ctx, `SELECT id, status, LEFT(COALESCE(chat_content,''), 400), LEFT(summary, 400) FROM runtime_tasks
			WHERE chat_conversation_id=? AND owner_id=? ORDER BY id DESC LIMIT 1`, b.ThreadID, ownerID).
			Scan(&b.LastTaskID, &b.LastStatus, &content, &reply)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if b.LastStatus != RuntimeSucceeded || strings.TrimSpace(reply) == "" {
			reply = content
		}
		b.LastMessage = runtimeChatPreview(reply)
	}
	return out, nil
}

// runtimeChatPreview 把文本压成一行并截到 runtimeChatPreviewRunes 个码点（超出时以 "…" 结尾）。
func runtimeChatPreview(text string) string {
	runes := []rune(strings.Join(strings.Fields(text), " "))
	if len(runes) > runtimeChatPreviewRunes {
		return string(runes[:runtimeChatPreviewRunes-1]) + "…"
	}
	return string(runes)
}

// RuntimeChatBotProfile 返回所有者本人的机器人资料。机器人必须属于本人并以本人身份运行，且要么已有线程
// （历史仍可读），要么 employee 为真且机器人当前可对话；其余一律 ErrNotFound。
// 产物包括线程里的对话轮次和这个机器人的定时 / 手动任务，都限定 owner_id，最新的在前，
// 最多 runtimeChatArtifactLimit 个。
func (s *Store) RuntimeChatBotProfile(ctx context.Context, ownerID, botID int64, employee bool) (*RuntimeChatBotProfile, error) {
	var p RuntimeChatBotProfile
	var readOnly, webChat bool
	err := s.db.QueryRowContext(ctx, `SELECT schedule, enabled, read_only, web_chat_enabled FROM runtime_bots
		WHERE id=? AND owner_id=? AND run_as_id=owner_id`, botID, ownerID).Scan(&p.Schedule, &p.ScheduleEnabled, &readOnly, &webChat)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var thread int64
	err = s.db.QueryRowContext(ctx, `SELECT id FROM runtime_chat_conversations WHERE owner_id=? AND bot_id=?`, ownerID, botID).Scan(&thread)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if thread == 0 && !(employee && readOnly && webChat) {
		return nil, ErrNotFound
	}
	// 只认已经在运行的任务：排队中的不算，也不编造沙箱的任何遥测。没有线程时 chat_conversation_id 比较的是 NULL，不命中。
	var running int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_tasks WHERE owner_id=? AND status=? AND (bot_id=? OR chat_conversation_id=?)`,
		ownerID, RuntimeRunning, botID, sql.NullInt64{Int64: thread, Valid: thread != 0}).Scan(&running); err != nil {
		return nil, err
	}
	p.Running = running > 0
	p.Artifacts = []RuntimeChatArtifact{}
	// 两类任务各走各的索引，各取最新的一页再合并。
	sources := []struct {
		name, where string
		id          int64
	}{{"task", "t.bot_id=?", botID}, {"chat", "t.chat_conversation_id=?", thread}}
	for _, src := range sources {
		if src.id == 0 {
			continue
		}
		rows, err := s.db.QueryContext(ctx, `SELECT a.id, a.task_id, a.name, a.size_bytes, a.created_at FROM runtime_artifacts a
			JOIN runtime_tasks t ON t.id=a.task_id WHERE t.owner_id=? AND `+src.where+` ORDER BY a.id DESC LIMIT ?`,
			ownerID, src.id, runtimeChatArtifactLimit)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			a := RuntimeChatArtifact{Source: src.name}
			if err := rows.Scan(&a.ID, &a.TaskID, &a.Name, &a.Size, &a.CreatedAt); err != nil {
				rows.Close()
				return nil, err
			}
			p.Artifacts = append(p.Artifacts, a)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	sort.Slice(p.Artifacts, func(i, j int) bool { return p.Artifacts[i].ID > p.Artifacts[j].ID })
	if len(p.Artifacts) > runtimeChatArtifactLimit {
		p.Artifacts = p.Artifacts[:runtimeChatArtifactLimit]
	}
	return &p, nil
}

// EnsureRuntimeChatConversation 返回员工与机器人的线程，没有就建一条：同一对 (owner, bot) 无论调用多少次、
// 是否并发，都得到同一条线程（唯一索引 uq_runtime_chat_owner_bot 兜底）。
// 机器人不存在或对这位员工不可用时返回 ErrNotFound，已有线程也不例外。
func (s *Store) EnsureRuntimeChatConversation(ctx context.Context, ownerID, botID int64) (*RuntimeChatConversation, error) {
	existing := func() (*RuntimeChatConversation, error) {
		return scanRuntimeChatConversation(s.db.QueryRowContext(ctx, runtimeChatConversationQuery+` WHERE c.owner_id=? AND c.bot_id=?`, ownerID, botID))
	}
	var eligible int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM runtime_bots WHERE id=? AND `+runtimeChatBotEligible, botID, ownerID).Scan(&eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if c, err := existing(); !errors.Is(err, ErrNotFound) {
		return c, err
	}
	t := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO runtime_chat_conversations (owner_id, bot_id, title, created_at, updated_at)
		SELECT owner_id, id, '', ?, ? FROM runtime_bots WHERE id=? AND `+runtimeChatBotEligible, t, t, botID, ownerID)
	switch {
	case isMySQLErr(err, mysqlErrDuplicateKey):
		// 并发的另一个请求先建好了。
	case err != nil:
		return nil, err
	default:
		if n, err := res.RowsAffected(); err != nil {
			return nil, err
		} else if n == 0 {
			return nil, ErrNotFound
		}
	}
	return existing()
}

// RuntimeChatConversation 返回员工本人的线程。id 直接命中时就是它；否则按 0054 留下的合并存档
// （runtime_chat_thread_merges）把本人被合并的旧对话 ID 解析到保留线程，旧链接和旧接口 ID 继续可用。
// 两步都限定所有者：别人的 ID（无论新旧）一律 ErrNotFound。返回的 ID 永远是保留线程的。
func (s *Store) RuntimeChatConversation(ctx context.Context, id, ownerID int64) (*RuntimeChatConversation, error) {
	c, err := scanRuntimeChatConversation(s.db.QueryRowContext(ctx, runtimeChatConversationQuery+` WHERE c.id=? AND c.owner_id=?`, id, ownerID))
	if !errors.Is(err, ErrNotFound) {
		return c, err
	}
	return scanRuntimeChatConversation(s.db.QueryRowContext(ctx, runtimeChatConversationQuery+`
		JOIN runtime_chat_thread_merges m ON m.canonical_id=c.id
		WHERE m.old_id=? AND m.owner_id=? AND c.owner_id=?`, id, ownerID, ownerID))
}

// RuntimeChatConversations 按最近活动倒序列出员工本人的线程。
func (s *Store) RuntimeChatConversations(ctx context.Context, ownerID int64) ([]RuntimeChatConversation, error) {
	rows, err := s.db.QueryContext(ctx, runtimeChatConversationQuery+` WHERE c.owner_id=? ORDER BY c.updated_at DESC, c.id DESC LIMIT ?`,
		ownerID, runtimeChatListLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeChatConversation{}
	for rows.Next() {
		c, err := scanRuntimeChatConversation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// RuntimeChatTurns 按时间顺序返回线程的一页轮次：before 不大于 0 时是最新的一页，否则是任务 ID 小于 before
// 的最近一页。还有更早的轮次时 nextBefore 是下一页要传的 before，否则为 0。存档不设总量上限，翻页能走到第一轮。
func (s *Store) RuntimeChatTurns(ctx context.Context, conversationID, ownerID, before int64, limit int) (turns []RuntimeChatTurn, nextBefore int64, err error) {
	if limit <= 0 || limit > RuntimeChatHistoryMax {
		limit = RuntimeChatHistoryPage
	}
	// 多取一条只为知道前面还有没有。
	rows, err := s.db.QueryContext(ctx, runtimeChatRecentTurnsQuery(`chat_conversation_id=? AND owner_id=? AND (?<=0 OR id<?)`),
		conversationID, ownerID, before, before, limit+1)
	if err != nil {
		return nil, 0, err
	}
	turns, err = scanRuntimeChatTurns(rows)
	if err != nil {
		return nil, 0, err
	}
	if len(turns) > limit {
		turns = turns[1:]
		nextBefore = turns[0].ID
	}
	return turns, nextBefore, nil
}

// EnqueueRuntimeChatTurn 在一个事务里接收一条网页对话消息：锁住所有者串行化入队，先查幂等键
// （同内容重放返回原轮次，不同内容 ErrConflict），再复核机器人当前策略、对话内是否已有进行中
// 的轮次和所有者的队列上限，然后原子地写入任务、事件并更新对话的最近活动时间和首条消息标题。
//
// 上下文在同一个事务、同一把锁下算好再建任务：水位（context_task_id）之后的成功轮次里，最近
// runtimeChatContextTurns 轮交给 plan.Prompt 原样引用，更早的（包括合并旧线程后从未处理过的）按任务 ID
// 顺序逐轮交给 plan.Fold 并入滚动记忆；Prompt 放不下的前缀也并入记忆。水位随之推进，所以每一轮恰好
// 处理一次：重放幂等键在这之前就返回了，不会重复并入。存档（runtime_tasks 里的原话和回复）不受影响。
// 任务的 bot_id 留空：对话轮次不走机器人的定时互斥，也不触发飞书投递。
func (s *Store) EnqueueRuntimeChatTurn(ctx context.Context, m RuntimeChatMessage, plan RuntimeChatPlanner) (*RuntimeChatTurn, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=? FOR UPDATE`, m.OwnerID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var botID, watermark int64
	var memory string
	err = tx.QueryRowContext(ctx, `SELECT bot_id, COALESCE(context_summary,''), context_task_id FROM runtime_chat_conversations
		WHERE id=? AND owner_id=? FOR UPDATE`, m.ConversationID, m.OwnerID).Scan(&botID, &memory, &watermark)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var existing RuntimeChatTurn
	var existingConversation sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT `+runtimeChatTurnColumns+`, chat_conversation_id FROM runtime_tasks
		WHERE owner_id=? AND idempotency_key=? FOR UPDATE`, m.OwnerID, m.IdempotencyKey).
		Scan(&existing.ID, &existing.Content, &existing.Status, &existing.Summary, &existing.CreatedAt, &existingConversation)
	switch {
	case err == nil:
		if !existingConversation.Valid || existingConversation.Int64 != m.ConversationID || existing.Content != m.Content {
			return nil, ErrConflict
		}
		return &existing, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	}
	bot, err := scanRuntimeBot(tx.QueryRowContext(ctx, `SELECT `+runtimeBotColumns+` FROM runtime_bots WHERE id=? AND `+runtimeChatBotEligible+` FOR UPDATE`,
		botID, m.OwnerID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrRuntimeChatBotUnavailable
	}
	if err != nil {
		return nil, err
	}
	var active int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_tasks WHERE chat_conversation_id=? AND status IN (?,?)`,
		m.ConversationID, RuntimeQueued, RuntimeRunning).Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, ErrRuntimeChatBusy
	}
	if err := checkRuntimeQueue(ctx, tx, RuntimeTask{OwnerID: m.OwnerID}, false); err != nil {
		return nil, err
	}
	// 线程里此刻没有进行中的轮次（上面刚查过，且整段都在所有者锁下），所以水位之前不会再冒出新的成功轮次。
	rows, err := tx.QueryContext(ctx, runtimeChatRecentTurnsQuery(`chat_conversation_id=? AND status=? AND id>?`),
		m.ConversationID, RuntimeSucceeded, watermark, runtimeChatContextTurns)
	if err != nil {
		return nil, err
	}
	recent, err := scanRuntimeChatTurns(rows)
	if err != nil {
		return nil, err
	}
	// 最近窗口满了才可能有更早的未处理轮次：从水位起按任务 ID 顺序分批并入记忆。
	for len(recent) == runtimeChatContextTurns {
		rows, err := tx.QueryContext(ctx, `SELECT `+runtimeChatTurnColumns+` FROM runtime_tasks
			WHERE chat_conversation_id=? AND status=? AND id>? AND id<? ORDER BY id LIMIT ?`,
			m.ConversationID, RuntimeSucceeded, watermark, recent[0].ID, runtimeChatFoldBatch)
		if err != nil {
			return nil, err
		}
		older, err := scanRuntimeChatTurns(rows)
		if err != nil {
			return nil, err
		}
		for _, turn := range older {
			memory, watermark = plan.Fold(memory, turn), turn.ID
		}
		if len(older) < runtimeChatFoldBatch {
			break
		}
	}
	prompt, memory, kept := plan.Prompt(bot, memory, recent)
	if kept < 0 || kept > len(recent) {
		return nil, errors.New("runtime chat planner kept an invalid number of turns")
	}
	if kept < len(recent) {
		watermark = recent[len(recent)-kept-1].ID
	}
	if len(memory) > runtimeChatMemoryMax {
		return nil, errors.New("runtime chat memory exceeds its storage bound")
	}
	created := now()
	r, err := tx.ExecContext(ctx, `INSERT INTO runtime_tasks (owner_id, run_as_id, bot_id, prompt, status, idempotency_key, timeout_seconds, error, summary,
		created_at, updated_at, chat_conversation_id, chat_content) VALUES (?,?,NULL,?,?,?,?,'','',?,?,?,?)`,
		m.OwnerID, m.OwnerID, prompt, RuntimeQueued, m.IdempotencyKey, m.TimeoutSeconds, created, created, m.ConversationID, m.Content)
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
	if _, err := tx.ExecContext(ctx, `UPDATE runtime_chat_conversations SET updated_at=?, title=IF(title='', ?, title),
		context_summary=?, context_task_id=? WHERE id=?`, created, m.Title, memory, watermark, m.ConversationID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RuntimeChatTurn{ID: id, Content: m.Content, Status: RuntimeQueued, CreatedAt: created}, nil
}
