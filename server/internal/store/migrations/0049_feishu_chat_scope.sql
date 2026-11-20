ALTER TABLE feishu_chat_messages
 ADD COLUMN app_scope VARBINARY(64) NOT NULL DEFAULT '',
 ADD COLUMN sender_user_id VARBINARY(512) NOT NULL DEFAULT '';

ALTER TABLE feishu_chat_messages ADD UNIQUE KEY uq_feishu_chat_app_message (app_scope, message_id);

ALTER TABLE feishu_chat_messages ADD KEY idx_feishu_chat_scoped_queue (app_scope, user_id, sender_user_id, status, id);

ALTER TABLE feishu_chat_messages DROP INDEX uq_feishu_chat_message, DROP INDEX idx_feishu_chat_queue;
