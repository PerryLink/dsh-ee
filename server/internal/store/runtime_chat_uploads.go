package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// 网页对话的附件：员工把文档拖进对话框，先上传（CreateRuntimeChatUpload，task_id 为空），发消息时挂到这一轮
// （EnqueueRuntimeChatTurn 的 RuntimeChatMessage.Uploads），执行这一轮时由 worker 写进机器人的工作目录。
// 附件只属于上传它的员工：没发出去的附件只有本人能删，挂上去以后跟这一轮一起留在历史里（团队线程里成员都能看到文件名）。

// RuntimeChatUploadsPerMessage 是一条消息最多带几个附件。
const RuntimeChatUploadsPerMessage = 5

// ErrRuntimeChatUploadUnavailable 表示消息引用的附件不存在、不属于本人这条对话，或已经随别的消息发出。
var ErrRuntimeChatUploadUnavailable = errors.New("runtime chat upload unavailable")

// RuntimeChatUpload 是一个附件。StorageKey 是 DSH_EE_DATA_DIR/runtime-chat-uploads 下的文件名前缀：
// <key> 是原文件，<key>.md 是转出的正文。
type RuntimeChatUpload struct {
	ID             int64     `json:"id"`
	OwnerID        int64     `json:"-"`
	ConversationID int64     `json:"-"`
	TaskID         int64     `json:"-"`
	Name           string    `json:"name"`
	Size           int64     `json:"size"`
	Format         string    `json:"format"`
	Pages          int       `json:"pages,omitempty"`
	TextBytes      int64     `json:"text_bytes"`
	StorageKey     string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
}

const runtimeChatUploadColumns = `id, owner_id, conversation_id, COALESCE(task_id,0), name, size, format, pages, text_bytes, storage_key, created_at`

// runtimeChatUploadJoinColumns 是同一组列加上表别名 u：要 JOIN runtime_tasks 时两张表有同名列（id、owner_id、created_at），必须限定。
const runtimeChatUploadJoinColumns = `u.id, u.owner_id, u.conversation_id, COALESCE(u.task_id,0), u.name, u.size, u.format, u.pages, u.text_bytes, u.storage_key, u.created_at`

func scanRuntimeChatUploads(rows *sql.Rows) ([]RuntimeChatUpload, error) {
	defer rows.Close()
	out := []RuntimeChatUpload{}
	for rows.Next() {
		var u RuntimeChatUpload
		if err := rows.Scan(&u.ID, &u.OwnerID, &u.ConversationID, &u.TaskID, &u.Name, &u.Size, &u.Format, &u.Pages, &u.TextBytes, &u.StorageKey, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CreateRuntimeChatUpload 记下一个还没发出的附件。调用方先确认对话对这位员工可见。
func (s *Store) CreateRuntimeChatUpload(ctx context.Context, u *RuntimeChatUpload) error {
	u.CreatedAt = now()
	r, err := s.db.ExecContext(ctx, `INSERT INTO runtime_chat_uploads (owner_id, conversation_id, task_id, name, size, format, pages, text_bytes, storage_key, created_at)
		VALUES (?,?,NULL,?,?,?,?,?,?,?)`, u.OwnerID, u.ConversationID, u.Name, u.Size, u.Format, u.Pages, u.TextBytes, u.StorageKey, u.CreatedAt)
	if err != nil {
		return err
	}
	u.ID, err = r.LastInsertId()
	return err
}

// RuntimeChatPendingUploadBytes 是本人所有还没发出的附件（原件 + 正文）合计的字节数。
func (s *Store) RuntimeChatPendingUploadBytes(ctx context.Context, ownerID int64) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(size + text_bytes), 0) FROM runtime_chat_uploads WHERE owner_id=? AND task_id IS NULL`, ownerID).Scan(&n)
	return n, err
}

// DeleteRuntimeChatUpload 删掉本人一个还没发出的附件，返回它（调用方删文件）。已发出或不属于本人时 ErrNotFound。
func (s *Store) DeleteRuntimeChatUpload(ctx context.Context, ownerID, id int64) (*RuntimeChatUpload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeChatUploadColumns+` FROM runtime_chat_uploads WHERE id=? AND owner_id=? AND task_id IS NULL`, id, ownerID)
	if err != nil {
		return nil, err
	}
	list, err := scanRuntimeChatUploads(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	r, err := s.db.ExecContext(ctx, `DELETE FROM runtime_chat_uploads WHERE id=? AND owner_id=? AND task_id IS NULL`, id, ownerID)
	if err != nil {
		return nil, err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// PruneRuntimeChatUploads 删掉早于 before 还没发出的附件（上传了却没发消息），返回被删的记录，调用方删文件。
func (s *Store) PruneRuntimeChatUploads(ctx context.Context, before time.Time) ([]RuntimeChatUpload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeChatUploadColumns+` FROM runtime_chat_uploads WHERE task_id IS NULL AND created_at<? LIMIT 200`, before)
	if err != nil {
		return nil, err
	}
	list, err := scanRuntimeChatUploads(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	removed := list[:0]
	for _, u := range list {
		r, err := s.db.ExecContext(ctx, `DELETE FROM runtime_chat_uploads WHERE id=? AND task_id IS NULL`, u.ID)
		if err != nil {
			return removed, err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			removed = append(removed, u)
		}
	}
	return removed, nil
}

// RuntimeChatUploadsFinishedBefore 列出挂在一轮已经结束（成功/失败/取消）且结束早于 before 的附件，
// 按 id 从小到大分页：返回 id 大于 afterID 的最多 limit 条。这一轮已经不会再读这些文件，调用方可以删原件和正文；
// DB 行要留着，历史里还要显示文件名、大小和格式。
func (s *Store) RuntimeChatUploadsFinishedBefore(ctx context.Context, before time.Time, afterID int64, limit int) ([]RuntimeChatUpload, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeChatUploadJoinColumns+` FROM runtime_chat_uploads u
		JOIN runtime_tasks t ON t.id=u.task_id
		WHERE u.id>? AND t.status IN (?,?,?) AND t.finished_at<?
		ORDER BY u.id LIMIT ?`,
		afterID, RuntimeSucceeded, RuntimeFailed, RuntimeCanceled, before, limit)
	if err != nil {
		return nil, err
	}
	return scanRuntimeChatUploads(rows)
}

// RuntimeChatUploadsForTasks 按轮次（任务 ID）列出挂在上面的附件，按上传顺序。
func (s *Store) RuntimeChatUploadsForTasks(ctx context.Context, taskIDs []int64) (map[int64][]RuntimeChatUpload, error) {
	out := map[int64][]RuntimeChatUpload{}
	if len(taskIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(taskIDs))
	for i, id := range taskIDs {
		args[i] = id
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+runtimeChatUploadColumns+` FROM runtime_chat_uploads WHERE task_id IN (?`+strings.Repeat(",?", len(taskIDs)-1)+`) ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	list, err := scanRuntimeChatUploads(rows)
	if err != nil {
		return nil, err
	}
	for _, u := range list {
		out[u.TaskID] = append(out[u.TaskID], u)
	}
	return out, nil
}

// attachRuntimeChatUploads 在入队事务里把本人这条对话里还没发出的附件挂到新的一轮上；少一个都整体失败。
func attachRuntimeChatUploads(ctx context.Context, tx *sql.Tx, m RuntimeChatMessage, taskID int64) error {
	if len(m.Uploads) == 0 {
		return nil
	}
	if len(m.Uploads) > RuntimeChatUploadsPerMessage {
		return ErrRuntimeChatUploadUnavailable
	}
	args := []any{taskID, m.OwnerID, m.ConversationID}
	seen := map[int64]bool{}
	for _, id := range m.Uploads {
		if seen[id] {
			return ErrRuntimeChatUploadUnavailable
		}
		seen[id] = true
		args = append(args, id)
	}
	r, err := tx.ExecContext(ctx, `UPDATE runtime_chat_uploads SET task_id=? WHERE owner_id=? AND conversation_id=? AND task_id IS NULL
		AND id IN (?`+strings.Repeat(",?", len(m.Uploads)-1)+`)`, args...)
	if err != nil {
		return err
	}
	if n, err := r.RowsAffected(); err != nil || n != int64(len(m.Uploads)) {
		return ErrRuntimeChatUploadUnavailable
	}
	return nil
}
