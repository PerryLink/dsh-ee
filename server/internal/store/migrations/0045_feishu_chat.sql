CREATE TABLE feishu_chat_messages (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 user_id BIGINT NOT NULL,
 message_id VARCHAR(255) NOT NULL,
 chat_id VARCHAR(255) NOT NULL,
 text MEDIUMTEXT NOT NULL,
 status VARCHAR(16) NOT NULL DEFAULT 'pending',
 reply MEDIUMTEXT NULL,
 created_at DATETIME(3) NOT NULL,
 delivered_at DATETIME(3) NULL,
 replied_at DATETIME(3) NULL,
 UNIQUE KEY uq_feishu_chat_message (message_id),
 KEY idx_feishu_chat_queue (user_id, status, id)
);
