CREATE TABLE dingtalk_chat_messages (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 app_scope VARBINARY(64) NOT NULL,
 user_id BIGINT NOT NULL,
 message_id VARBINARY(255) NOT NULL,
 sender_user_id VARBINARY(512) NOT NULL,
 robot_code VARBINARY(255) NOT NULL,
 text MEDIUMTEXT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 delivered_at DATETIME(3) NULL,
 replied_at DATETIME(3) NULL,
 UNIQUE KEY uq_dingtalk_message (app_scope, message_id),
 KEY idx_dingtalk_queue (app_scope, user_id, id),
 KEY idx_dingtalk_expiry (created_at)
);
