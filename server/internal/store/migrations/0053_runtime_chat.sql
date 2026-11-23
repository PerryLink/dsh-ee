ALTER TABLE runtime_bots ADD COLUMN web_chat_enabled TINYINT(1) NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS runtime_chat_conversations (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_id BIGINT NOT NULL,
 bot_id BIGINT NOT NULL,
 title VARCHAR(255) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 KEY idx_runtime_chat_owner (owner_id, updated_at, id)
);

ALTER TABLE runtime_tasks ADD COLUMN chat_conversation_id BIGINT NULL;

ALTER TABLE runtime_tasks ADD COLUMN chat_content TEXT NULL;

ALTER TABLE runtime_tasks ADD KEY idx_runtime_task_chat (chat_conversation_id, id);
