package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

const (
	FeishuActionHubVersion = "hub_version"
	FeishuActionScope      = "scope_request"
	FeishuActionAccess     = "access_request"

	FeishuActionPending    = "pending"
	FeishuActionProcessing = "processing"
	FeishuActionDone       = "done"
	FeishuActionFailed     = "failed"
	FeishuActionSuperseded = "superseded"
)

// FeishuCardAction 是一次审批动作。ActionHash 是随机 action id 的摘要，回调按它唯一确定对象和权限状态。
type FeishuCardAction struct {
	ID          int64      `json:"id"`
	Kind        string     `json:"kind"`
	ObjectKey   string     `json:"object_key"`
	AssetID     int64      `json:"asset_id"`
	SubID       int64      `json:"sub_id"`
	MessageID   string     `json:"message_id"`
	Status      string     `json:"status"`
	EventID     string     `json:"event_id"`
	Result      string     `json:"result"`
	ExpiresAt   time.Time  `json:"expires_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

var ErrFeishuActionConflict = errors.New("feishu action conflict")

type FeishuCardActionInput struct {
	ActionHash string
	MessageID  string
	ExpiresAt  time.Time
}

// SendFeishuCardAction 把同一个待审批对象的旧动作作废后记录新动作；旧回调即使到达也不能操作。
func (s *Store) SendFeishuCardAction(ctx context.Context, hash, kind, objectKey string, assetID, subID int64, messageID string, expiresAt time.Time) error {
	return s.SendFeishuCardActions(ctx, kind, objectKey, assetID, subID, []FeishuCardActionInput{{ActionHash: hash, MessageID: messageID, ExpiresAt: expiresAt}})
}

// SendFeishuCardActions 一次登记同一对象的多个审批人卡片。
// 不能逐条调用 SendFeishuCardAction：后一条会把前一条刚插入的 pending 动作置为 superseded，
// 导致多审批人时只有最后一张卡能用。旧动作只作废一次，然后在同一事务里插入全部新动作。
func (s *Store) SendFeishuCardActions(ctx context.Context, kind, objectKey string, assetID, subID int64, actions []FeishuCardActionInput) error {
	if len(actions) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	t := now()
	if _, err := tx.ExecContext(ctx, `UPDATE feishu_card_actions SET status = 'superseded', updated_at = ?
		WHERE kind = ? AND object_key = ? AND status IN ('pending','processing')`, t, kind, objectKey); err != nil {
		return err
	}
	for _, a := range actions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO feishu_card_actions
			(action_hash, kind, object_key, asset_id, sub_id, message_id, status, expires_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`, a.ActionHash, kind, objectKey, assetID, subID, a.MessageID, a.ExpiresAt, t, t); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) FeishuCardAction(ctx context.Context, hash string) (*FeishuCardAction, error) {
	var a FeishuCardAction
	var completed sql.NullTime
	err := s.db.QueryRowContext(ctx, `SELECT id, kind, object_key, asset_id, sub_id, message_id, status, event_id, result, expires_at, completed_at
		FROM feishu_card_actions WHERE action_hash = ?`, hash).
		Scan(&a.ID, &a.Kind, &a.ObjectKey, &a.AssetID, &a.SubID, &a.MessageID, &a.Status, &a.EventID, &a.Result, &a.ExpiresAt, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if completed.Valid {
		t := completed.Time
		a.CompletedAt = &t
	}
	return &a, nil
}

// BeginFeishuCardAction 把 pending 原子改为 processing，阻止不同浏览器/回调并发重复审批。
func (s *Store) BeginFeishuCardAction(ctx context.Context, hash, eventID string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE feishu_card_actions SET status='processing', event_id=?, updated_at=?
		WHERE action_hash=? AND status='pending' AND expires_at > ?`, eventID, now(), hash, now())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeishuActionConflict
	}
	return nil
}

// ReleaseFeishuCardAction 把占用中的动作放回待处理：点按钮的人没有权限时用，
// 不能让一个无权的人把审批人的卡片点废。
func (s *Store) ReleaseFeishuCardAction(ctx context.Context, hash string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE feishu_card_actions SET status='pending', event_id='', updated_at=?
		WHERE action_hash=? AND status='processing'`, now(), hash)
	return err
}

// FinishFeishuCardAction 终态只允许从 processing 进入，失败也保留可观测结果，不伪装成成功。
func (s *Store) FinishFeishuCardAction(ctx context.Context, hash, status, result string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE feishu_card_actions SET status=?, result=?, completed_at=?, updated_at=?
		WHERE action_hash=? AND status='processing'`, status, result, now(), now(), hash)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeishuActionConflict
	}
	return nil
}
