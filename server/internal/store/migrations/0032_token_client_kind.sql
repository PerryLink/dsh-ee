-- 令牌的客户端属性（roadmap/audit-thirdparty）：client_kind 表示这个令牌签发给哪类客户端。
-- dsh 的令牌沿用「写操作由 DSH 审批卡确认」的快捷语义；third_party 的令牌不认请求头，
-- 写操作始终要员工在网页上人工确认（write_confirm.go）。
-- 旧令牌默认 dsh，保持现有接入插件的行为不变；新令牌的值由签发入口决定。
ALTER TABLE access_tokens ADD COLUMN client_kind VARCHAR(16) NOT NULL DEFAULT 'dsh';

-- 连接调用记录这次调用用的是哪个令牌，便于按令牌追责。旧记录、网页会话与没有令牌的调用为 NULL。
ALTER TABLE connection_calls ADD COLUMN token_id BIGINT NULL;
ALTER TABLE connection_calls ADD KEY idx_connection_calls_token (token_id, created_at);

-- 写确认记录发起这次确认的令牌，审批后审计里能带出 token id。
ALTER TABLE write_confirmations ADD COLUMN token_id BIGINT NULL;
