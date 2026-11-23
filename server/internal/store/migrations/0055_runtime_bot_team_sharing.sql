-- 团队共享机器人：管理员或机器人的所有者把机器人共享给一个部门。部门沿用现有组织字段 users.department，不另建团队模型。
-- 默认不共享（空串）。同一时刻一个机器人只共享给一个部门。
ALTER TABLE runtime_bots ADD COLUMN shared_department VARCHAR(128) NOT NULL DEFAULT '';

-- 团队线程：owner_id=0、shared_department=部门，每个 (机器人, 部门) 一条，团队成员共用；员工自己的线程 shared_department 为空串。
-- 线程按部门区分：改共享到别的部门后，原部门的记录不会被新部门看到，重新共享回来时原线程接着用。
ALTER TABLE runtime_chat_conversations ADD COLUMN shared_department VARCHAR(128) NOT NULL DEFAULT '';

-- 先加新唯一索引再删旧的：员工线程的 (owner_id, bot_id) 本来就唯一，加上空串的部门列仍然唯一。
ALTER TABLE runtime_chat_conversations ADD UNIQUE KEY uq_runtime_chat_owner_bot_team (owner_id, bot_id, shared_department);

ALTER TABLE runtime_chat_conversations DROP INDEX uq_runtime_chat_owner_bot;
