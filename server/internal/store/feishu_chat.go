package store

import (
	"context"
	"database/sql"
	"time"
)

const feishuChatRedelivery = 10 * time.Minute

// FeishuChatLifetime 是入站消息可被领取/回复的时间窗口，也是队列行的保留期：
// 更早的行客户端不会再领取，由后台周期任务通过 PurgeFeishuChats 整行删除。
const FeishuChatLifetime = time.Hour

type FeishuChatMessage struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
	MessageID string    `json:"-"`
}

type FeishuChatKey struct {
	AppScope string
	UserID   int64
	SenderID string
}

func (s *Store) EnqueueFeishuChat(ctx context.Context, key FeishuChatKey, messageID, chatID, text string) (bool, error) {
	result, err := s.db.ExecContext(ctx, `INSERT INTO feishu_chat_messages (app_scope,user_id,sender_user_id,message_id,chat_id,text,status,created_at)
		VALUES (?,?,?,?,?,?,'pending',?)`, key.AppScope, key.UserID, key.SenderID, messageID, chatID, text, now())
	if isDuplicate(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *Store) NextFeishuChat(ctx context.Context, key FeishuChatKey) (*FeishuChatMessage, error) {
	t := now()
	if _, err := s.db.ExecContext(ctx, `UPDATE feishu_chat_messages SET status='expired' WHERE app_scope=? AND user_id=? AND sender_user_id=?
		AND status IN ('pending','delivered') AND created_at < ?`, key.AppScope, key.UserID, key.SenderID, t.Add(-FeishuChatLifetime)); err != nil {
		return nil, err
	}
	for {
		var m FeishuChatMessage
		err := s.db.QueryRowContext(ctx, `SELECT id,text,created_at FROM feishu_chat_messages WHERE app_scope=? AND user_id=? AND sender_user_id=?
			AND created_at>=? AND (status='pending' OR (status='delivered' AND delivered_at<?)) ORDER BY id LIMIT 1`,
			key.AppScope, key.UserID, key.SenderID, t.Add(-FeishuChatLifetime), t.Add(-feishuChatRedelivery)).Scan(&m.ID, &m.Text, &m.CreatedAt)
		if err != nil {
			return nil, err
		}
		res, err := s.db.ExecContext(ctx, `UPDATE feishu_chat_messages SET status='delivered',delivered_at=? WHERE id=? AND (status='pending' OR (status='delivered' AND delivered_at<?))`, t, m.ID, t.Add(-feishuChatRedelivery))
		if err != nil {
			return nil, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return nil, err
		}
		if n == 1 {
			return &m, nil
		}
	}
}

func (s *Store) HasActiveFeishuChat(ctx context.Context, key FeishuChatKey) (bool, error) {
	t := now()
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM feishu_chat_messages WHERE app_scope=? AND user_id=? AND sender_user_id=?
		AND status='delivered' AND delivered_at>=? AND created_at>=?)`, key.AppScope, key.UserID, key.SenderID, t.Add(-feishuChatRedelivery), t.Add(-FeishuChatLifetime)).Scan(&active)
	return active, err
}

func (s *Store) FeishuChatForReply(ctx context.Context, key FeishuChatKey, id int64) (*FeishuChatMessage, error) {
	var m FeishuChatMessage
	err := s.db.QueryRowContext(ctx, `SELECT id,message_id,text,created_at FROM feishu_chat_messages WHERE id=? AND app_scope=? AND user_id=? AND sender_user_id=?
		AND status IN ('pending','delivered','replied')`, id, key.AppScope, key.UserID, key.SenderID).Scan(&m.ID, &m.MessageID, &m.Text, &m.CreatedAt)
	return &m, err
}

// PurgeFeishuChats 按创建时间整行删除超过保留期 keep 的队列行，返回删除行数。
// 注意是 DELETE 而不是把 status 标成 expired：过期标记不清空间，队列表会无界增长。
func (s *Store) PurgeFeishuChats(ctx context.Context, keep time.Duration) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM feishu_chat_messages WHERE created_at<?`, now().Add(-keep))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (s *Store) FinishFeishuChatReply(ctx context.Context, key FeishuChatKey, id int64, text string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE feishu_chat_messages SET status='replied',reply=CONCAT(COALESCE(reply,''),?),replied_at=?
		WHERE id=? AND app_scope=? AND user_id=? AND sender_user_id=?`, text, now(), id, key.AppScope, key.UserID, key.SenderID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return err
}
