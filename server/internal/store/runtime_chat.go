package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// 员工网页对话：一段对话绑定一个机器人，只属于所有者本人。每一轮就是一条带
// chat_conversation_id 的 runtime_tasks 记录——员工原话存在 chat_content，状态和回复直接读
// 任务的 status / summary，不另存一份。

const (
	runtimeChatListLimit    = 100
	runtimeChatHistoryLimit = 200
	// 拼进提示词的最近成功轮次上限；字节预算由调用方的 prompt 回调把关。
	runtimeChatContextTurns = 10
)

// runtimeChatBotEligible 是机器人可用于网页对话的条件（参数是当前员工 ID）：
// 所有者本人、以所有者身份运行、只读、且管理员打开了网页对话。
const runtimeChatBotEligible = `owner_id=? AND run_as_id=owner_id AND read_only=1 AND web_chat_enabled=1`

type RuntimeChatBot struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
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

// RuntimeChatBots 列出员工本人现在可以对话的机器人，只给 ID 和名称。
func (s *Store) RuntimeChatBots(ctx context.Context, ownerID int64) ([]RuntimeChatBot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name FROM runtime_bots WHERE `+runtimeChatBotEligible+` ORDER BY id`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RuntimeChatBot{}
	for rows.Next() {
		var b RuntimeChatBot
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateRuntimeChatConversation 新建一段对话；机器人不存在或对这位员工不可用时返回 ErrNotFound。
func (s *Store) CreateRuntimeChatConversation(ctx context.Context, ownerID, botID int64) (*RuntimeChatConversation, error) {
	t := now()
	res, err := s.db.ExecContext(ctx, `INSERT INTO runtime_chat_conversations (owner_id, bot_id, title, created_at, updated_at)
		SELECT owner_id, id, '', ?, ? FROM runtime_bots WHERE id=? AND `+runtimeChatBotEligible, t, t, botID, ownerID)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, ErrNotFound
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.RuntimeChatConversation(ctx, id, ownerID)
}

func (s *Store) RuntimeChatConversation(ctx context.Context, id, ownerID int64) (*RuntimeChatConversation, error) {
	return scanRuntimeChatConversation(s.db.QueryRowContext(ctx, runtimeChatConversationQuery+` WHERE c.id=? AND c.owner_id=?`, id, ownerID))
}

// RuntimeChatConversations 按最近活动倒序列出员工本人的对话。
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

// RuntimeChatTurns 按时间顺序返回对话最近的轮次。
func (s *Store) RuntimeChatTurns(ctx context.Context, conversationID, ownerID int64) ([]RuntimeChatTurn, error) {
	rows, err := s.db.QueryContext(ctx, runtimeChatRecentTurnsQuery(`chat_conversation_id=? AND owner_id=?`),
		conversationID, ownerID, runtimeChatHistoryLimit)
	if err != nil {
		return nil, err
	}
	return scanRuntimeChatTurns(rows)
}

// EnqueueRuntimeChatTurn 在一个事务里接收一条网页对话消息：锁住所有者串行化入队，先查幂等键
// （同内容重放返回原轮次，不同内容 ErrConflict），再复核机器人当前策略、对话内是否已有进行中
// 的轮次和所有者的队列上限，然后原子地写入任务、事件并更新对话的最近活动时间和首条消息标题。
// prompt 用机器人定义和最近成功的轮次（从旧到新）拼出任务提示词。
// 任务的 bot_id 留空：对话轮次不走机器人的定时互斥，也不触发飞书投递。
func (s *Store) EnqueueRuntimeChatTurn(ctx context.Context, m RuntimeChatMessage, prompt func(*RuntimeBot, []RuntimeChatTurn) string) (*RuntimeChatTurn, error) {
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
	var botID int64
	err = tx.QueryRowContext(ctx, `SELECT bot_id FROM runtime_chat_conversations WHERE id=? AND owner_id=? FOR UPDATE`, m.ConversationID, m.OwnerID).Scan(&botID)
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
	rows, err := tx.QueryContext(ctx, runtimeChatRecentTurnsQuery(`chat_conversation_id=? AND status=?`),
		m.ConversationID, RuntimeSucceeded, runtimeChatContextTurns)
	if err != nil {
		return nil, err
	}
	history, err := scanRuntimeChatTurns(rows)
	if err != nil {
		return nil, err
	}
	created := now()
	r, err := tx.ExecContext(ctx, `INSERT INTO runtime_tasks (owner_id, run_as_id, bot_id, prompt, status, idempotency_key, timeout_seconds, error, summary,
		created_at, updated_at, chat_conversation_id, chat_content) VALUES (?,?,NULL,?,?,?,?,'','',?,?,?,?)`,
		m.OwnerID, m.OwnerID, prompt(bot, history), RuntimeQueued, m.IdempotencyKey, m.TimeoutSeconds, created, created, m.ConversationID, m.Content)
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
	if _, err := tx.ExecContext(ctx, `UPDATE runtime_chat_conversations SET updated_at=?, title=IF(title='', ?, title) WHERE id=?`,
		created, m.Title, m.ConversationID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &RuntimeChatTurn{ID: id, Content: m.Content, Status: RuntimeQueued, CreatedAt: created}, nil
}
