ALTER TABLE runtime_bot_computers ADD COLUMN creation_key VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE runtime_bot_computers ADD COLUMN gateway_host VARCHAR(255) NOT NULL DEFAULT '';
