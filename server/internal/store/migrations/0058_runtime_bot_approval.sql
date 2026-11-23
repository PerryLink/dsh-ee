-- 云端机器人的写操作审批模式（docs/dev/bot-approval.md）。
-- approval_mode：read_only（默认，与旧行为一致，只读）、manual（逐个审批）、auto（按规则自动审批，其余逐个审批）、full（完全放行）。
-- read_only 列保留并与 approval_mode 保持一致（read_only=1 当且仅当 approval_mode='read_only'），旧代码读它仍然正确。
ALTER TABLE runtime_bots ADD COLUMN approval_mode VARCHAR(16) NOT NULL DEFAULT 'read_only';

-- 自动审批规则：JSON 字符串数组，每条是匹配工具全名（<连接>__<工具>）的通配模式，如 "wiki__create_page"、"wiki__*"、"*__create_*"。
ALTER TABLE runtime_bots ADD COLUMN approval_rules TEXT NULL;

-- 管理员是否允许机器人所有者（员工）在对话页自己切换审批模式。
ALTER TABLE runtime_bots ADD COLUMN approval_employee_editable TINYINT(1) NOT NULL DEFAULT 0;

-- 每轮云任务的短期凭证（access_tokens.id）：/mcp 按它找回任务和机器人，再决定写操作怎么放行。
ALTER TABLE runtime_tasks ADD COLUMN credential_token_id BIGINT NULL;

ALTER TABLE runtime_tasks ADD KEY idx_runtime_task_credential (credential_token_id);

-- 云任务发起的写操作确认：对话页按任务把审批卡显示在那一轮里。网页 / 第三方 Harness 发起的确认为空。
ALTER TABLE write_confirmations ADD COLUMN runtime_task_id BIGINT NULL;
