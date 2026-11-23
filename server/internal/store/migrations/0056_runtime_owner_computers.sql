-- 云电脑从「每个机器人一台」改成「每个所有者一台」（docs/dev/bot-delegation.md）：
-- owner_kind='user' 是一位员工的电脑，他的所有个人机器人共用；owner_kind='team' 是一个团队（部门）的电脑，
-- 只给共享到这个部门的团队对话用，不放在任何个人的电脑上。同一所有者只有一行（唯一索引兜底）。
-- holder / lease_until 仍是跨实例的独占租约：同一时间只有一轮任务（或一次手动操作）在用这台电脑，
-- 所以电脑里任何时候最多只有一个人的短期凭证。holder_bot_id 记正在用（或最近一次用）电脑的机器人。
CREATE TABLE IF NOT EXISTS runtime_computers (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_kind VARCHAR(8) NOT NULL,
 owner_user_id BIGINT NOT NULL DEFAULT 0,
 owner_department VARCHAR(128) NOT NULL DEFAULT '',
 sandbox_id VARCHAR(255) NOT NULL DEFAULT '',
 state VARCHAR(16) NOT NULL,
 holder VARCHAR(64) NOT NULL DEFAULT '',
 holder_bot_id BIGINT NOT NULL DEFAULT 0,
 lease_until DATETIME(3) NULL,
 disk_bytes BIGINT NOT NULL DEFAULT 0,
 last_used_at DATETIME(3) NOT NULL,
 hibernated_at DATETIME(3) NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 UNIQUE KEY uq_runtime_computer_owner (owner_kind, owner_user_id, owner_department),
 KEY idx_runtime_computer_state (state, last_used_at)
);

-- 要删掉的旧沙箱：迁移时合并下来的多余电脑。回收器（reapCloudComputers）逐个调用 provider 删除，删完删行。
CREATE TABLE IF NOT EXISTS runtime_computer_orphans (
 sandbox_id VARCHAR(255) NOT NULL PRIMARY KEY,
 bot_id BIGINT NOT NULL DEFAULT 0,
 reason VARCHAR(32) NOT NULL,
 created_at DATETIME(3) NOT NULL
);

-- 已有数据：0055 时代每个机器人一台电脑都属于机器人的所有者本人（只有个人机器人能用常驻电脑）。
-- 每位所有者保留最近使用的一台（last_used_at 最大，并列取 bot_id 最大），它的文件原样留在工作目录根下，
-- 这位员工的所有机器人都能读到；其余几台记进 runtime_computer_orphans，由回收器删除（文件随之删除）。
-- 没有沙箱的记录（state='none'）直接丢弃。租约一律清空：迁移在新版本启动时执行，旧实例的任务不会再续租。
-- 两条数据语句都可重跑（INSERT IGNORE + 唯一键），不依赖执行次数。
INSERT IGNORE INTO runtime_computers (owner_kind, owner_user_id, owner_department, sandbox_id, state, holder_bot_id, disk_bytes,
  last_used_at, hibernated_at, created_at, updated_at)
 SELECT 'user', b.owner_id, '', c.sandbox_id, c.state, c.bot_id, c.disk_bytes, c.last_used_at, c.hibernated_at, c.created_at, c.updated_at
 FROM runtime_bot_computers c
 JOIN runtime_bots b ON b.id = c.bot_id
 WHERE c.sandbox_id <> '' AND NOT EXISTS (
  SELECT 1 FROM runtime_bot_computers c2 JOIN runtime_bots b2 ON b2.id = c2.bot_id
  WHERE b2.owner_id = b.owner_id AND c2.sandbox_id <> ''
   AND (c2.last_used_at > c.last_used_at OR (c2.last_used_at = c.last_used_at AND c2.bot_id > c.bot_id)));

-- 没保留下来的沙箱（包括机器人已被删除、找不到所有者的）都进回收队列。
INSERT IGNORE INTO runtime_computer_orphans (sandbox_id, bot_id, reason, created_at)
 SELECT c.sandbox_id, c.bot_id, 'merge', c.updated_at FROM runtime_bot_computers c
 WHERE c.sandbox_id <> '' AND NOT EXISTS (SELECT 1 FROM runtime_computers k WHERE k.sandbox_id = c.sandbox_id);

DROP TABLE IF EXISTS runtime_bot_computers;

-- 每轮云任务的短期凭证对应哪一轮：/mcp 上的机器人协作工具（bots__*）按令牌找回发起调用的任务和机器人。
-- 只存令牌 ID，不存令牌本身；令牌在这一轮结束时吊销，之后这条映射就没有用了。
CREATE TABLE IF NOT EXISTS runtime_task_credentials (
 token_id BIGINT NOT NULL PRIMARY KEY,
 task_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 KEY idx_runtime_task_credential_task (task_id)
);

-- 机器人之间派任务。owner_id 是这些机器人共同的所有者（也是运行身份），只能派给他自己的机器人。
-- 一次委派先是 pending（发起的那一轮还在跑，电脑被它占着），那一轮成功结束后才在目标机器人的线程里入队一轮
-- （dispatched，child_task_id 指向那一轮，状态和结果直接读那一轮任务）；派不出去是 failed，发起轮失败是 canceled。
-- 防循环：root_task_id 是整棵委派树的第一轮，depth 是第几层，chain 是从根到目标的机器人 ID（",1,2,"）。
-- followup_task_id 是结果回到发起方线程时追加的那一轮（kind='task' 才有）。
CREATE TABLE IF NOT EXISTS runtime_bot_delegations (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_id BIGINT NOT NULL,
 parent_task_id BIGINT NOT NULL,
 parent_bot_id BIGINT NOT NULL,
 parent_conversation_id BIGINT NULL,
 root_task_id BIGINT NOT NULL,
 depth INT NOT NULL,
 chain VARCHAR(255) NOT NULL,
 target_bot_id BIGINT NOT NULL,
 kind VARCHAR(16) NOT NULL,
 content TEXT NOT NULL,
 status VARCHAR(16) NOT NULL,
 error VARCHAR(512) NOT NULL DEFAULT '',
 child_task_id BIGINT NULL,
 child_conversation_id BIGINT NULL,
 followup_task_id BIGINT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 KEY idx_runtime_delegation_parent (parent_task_id, id),
 KEY idx_runtime_delegation_child (child_task_id),
 KEY idx_runtime_delegation_followup (followup_task_id),
 KEY idx_runtime_delegation_root (root_task_id)
);

-- 机器人新建的机器人：谁（哪个机器人、哪一轮）建的。单独一张表，不改 runtime_bots 的列。
CREATE TABLE IF NOT EXISTS runtime_bot_origins (
 bot_id BIGINT NOT NULL PRIMARY KEY,
 owner_id BIGINT NOT NULL,
 created_by_bot_id BIGINT NOT NULL,
 created_by_task_id BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 KEY idx_runtime_bot_origin_owner (owner_id)
);
