package store

import (
	"context"
	"time"
)

const dingTalkChatLifetime = time.Hour
const dingTalkChatRedelivery = 10 * time.Minute

type DingTalkChat struct {
	ID        int64     `json:"id"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
	SenderID  string    `json:"-"`
	RobotCode string    `json:"-"`
}

func (s *Store) EnqueueDingTalkChat(ctx context.Context, scope string, userID int64, msgID, sender, robot, text string) (bool, error) {
	_, err := s.db.ExecContext(ctx, `INSERT INTO dingtalk_chat_messages
		(app_scope,user_id,message_id,sender_user_id,robot_code,text,created_at) VALUES (?,?,?,?,?,?,?)`,
		scope, userID, msgID, sender, robot, text, now())
	if isDuplicate(err) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) NextDingTalkChat(ctx context.Context, scope string, userID int64, sender string) (*DingTalkChat, error) {
	t := now()
	for {
		var m DingTalkChat
		err := s.db.QueryRowContext(ctx, `SELECT id,text,created_at FROM dingtalk_chat_messages
			WHERE app_scope=? AND user_id=? AND sender_user_id=? AND created_at>=? AND replied_at IS NULL
			AND (delivered_at IS NULL OR delivered_at<?) ORDER BY id LIMIT 1`,
			scope, userID, sender, t.Add(-dingTalkChatLifetime), t.Add(-dingTalkChatRedelivery)).Scan(&m.ID, &m.Text, &m.CreatedAt)
		if err != nil {
			return nil, err
		}
		res, err := s.db.ExecContext(ctx, `UPDATE dingtalk_chat_messages SET delivered_at=? WHERE id=?
			AND replied_at IS NULL AND (delivered_at IS NULL OR delivered_at<?)`, t, m.ID, t.Add(-dingTalkChatRedelivery))
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

func (s *Store) DingTalkChatForReply(ctx context.Context, scope string, userID, id int64, sender string) (*DingTalkChat, error) {
	m := &DingTalkChat{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT sender_user_id,robot_code FROM dingtalk_chat_messages
		WHERE app_scope=? AND user_id=? AND id=? AND sender_user_id=? AND delivered_at IS NOT NULL AND created_at>=?`,
		scope, userID, id, sender, now().Add(-dingTalkChatLifetime)).Scan(&m.SenderID, &m.RobotCode)
	return m, err
}

func (s *Store) FinishDingTalkChat(ctx context.Context, scope string, userID, id int64, sender string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE dingtalk_chat_messages SET replied_at=? WHERE id=? AND app_scope=? AND user_id=? AND sender_user_id=?`, now(), id, scope, userID, sender)
	return err
}

func (s *Store) DingTalkChatActive(ctx context.Context, scope string, userID int64, sender string) (bool, error) {
	t := now()
	var active bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM dingtalk_chat_messages WHERE app_scope=? AND user_id=?
		AND sender_user_id=? AND replied_at IS NULL AND delivered_at>=? AND created_at>=?)`,
		scope, userID, sender, t.Add(-dingTalkChatRedelivery), t.Add(-dingTalkChatLifetime)).Scan(&active)
	return active, err
}

func (s *Store) PurgeDingTalkChats(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM dingtalk_chat_messages WHERE created_at<?`, now().Add(-dingTalkChatLifetime))
	return err
}
