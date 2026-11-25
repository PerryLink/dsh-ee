-- 网页对话里员工拖进对话框上传的文档。上传时先存原文件和转出的 Markdown（DSH_EE_DATA_DIR/runtime-chat-uploads），
-- task_id 为空表示还没随消息发出；发消息时在同一个事务里挂到这一轮的云任务上，执行这一轮时写进机器人的工作目录。
CREATE TABLE IF NOT EXISTS runtime_chat_uploads (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_id BIGINT NOT NULL,
 conversation_id BIGINT NOT NULL,
 task_id BIGINT NULL,
 name VARCHAR(255) NOT NULL,
 size BIGINT NOT NULL,
 format VARCHAR(16) NOT NULL DEFAULT '',
 pages INT NOT NULL DEFAULT 0,
 text_bytes BIGINT NOT NULL DEFAULT 0,
 storage_key VARCHAR(64) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 KEY idx_runtime_chat_upload_owner (owner_id, conversation_id, task_id),
 KEY idx_runtime_chat_upload_task (task_id)
);
