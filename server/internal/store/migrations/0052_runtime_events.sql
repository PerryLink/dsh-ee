ALTER TABLE runtime_bots ADD COLUMN delivery_target VARCHAR(32) NOT NULL DEFAULT 'feishu_owner';

CREATE TABLE IF NOT EXISTS runtime_task_events (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 task_id BIGINT NOT NULL,
 status VARCHAR(16) NOT NULL,
 attempt INT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 KEY idx_runtime_task_events (task_id, id)
);
