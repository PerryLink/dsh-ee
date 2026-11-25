-- 员工网页对话改为「每位员工与每个机器人只有一条长期线程」。
-- 旧版允许同一位员工对同一个机器人开多段对话：这里把它们并进 id 最小的那一段（确定性的保留线程），
-- 轮次（runtime_tasks）改挂到保留线程上，任务本身、回复、事件、产物和幂等键都不动，一轮不丢。
-- 数据语句全部可重跑（保留线程永远是组内最小 id，不会被删），DDL 一条语句只做一件事。

-- 被合并线程的存档：旧 id 到保留线程的映射，以及旧线程原来的标题和时间。合并后不再写入，只留作追溯。
CREATE TABLE IF NOT EXISTS runtime_chat_thread_merges (
 old_id BIGINT NOT NULL PRIMARY KEY,
 canonical_id BIGINT NOT NULL,
 owner_id BIGINT NOT NULL,
 bot_id BIGINT NOT NULL,
 title VARCHAR(255) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 KEY idx_runtime_chat_merge_canonical (canonical_id, old_id)
);

INSERT IGNORE INTO runtime_chat_thread_merges (old_id, canonical_id, owner_id, bot_id, title, created_at, updated_at)
 SELECT c.id, k.canonical_id, c.owner_id, c.bot_id, c.title, c.created_at, c.updated_at
 FROM runtime_chat_conversations c
 JOIN (SELECT owner_id, bot_id, MIN(id) AS canonical_id FROM runtime_chat_conversations GROUP BY owner_id, bot_id) k
  ON k.owner_id = c.owner_id AND k.bot_id = c.bot_id
 WHERE c.id <> k.canonical_id;

-- 轮次改挂到保留线程；任务 id 全局递增，合并后的线程按任务 id 排就是真实的先后顺序。
UPDATE runtime_tasks t
 JOIN runtime_chat_thread_merges m ON m.old_id = t.chat_conversation_id
 SET t.chat_conversation_id = m.canonical_id;

-- 保留线程的时间取整组的最早创建和最近活动。
UPDATE runtime_chat_conversations c
 JOIN (SELECT canonical_id, MIN(created_at) AS created_at, MAX(updated_at) AS updated_at
  FROM runtime_chat_thread_merges GROUP BY canonical_id) m ON m.canonical_id = c.id
 SET c.created_at = LEAST(c.created_at, m.created_at), c.updated_at = GREATEST(c.updated_at, m.updated_at);

-- 保留线程自己还没有标题（没发过消息）时，沿用组内最早一段有标题的对话的标题。
UPDATE runtime_chat_conversations c
 JOIN (SELECT canonical_id, MIN(old_id) AS old_id FROM runtime_chat_thread_merges WHERE title <> '' GROUP BY canonical_id) f
  ON f.canonical_id = c.id
 JOIN runtime_chat_thread_merges m ON m.old_id = f.old_id
 SET c.title = m.title
 WHERE c.title = '';

DELETE c FROM runtime_chat_conversations c
 JOIN runtime_chat_thread_merges m ON m.old_id = c.id;

ALTER TABLE runtime_chat_conversations ADD UNIQUE KEY uq_runtime_chat_owner_bot (owner_id, bot_id);

-- 线程的滚动记忆：context_summary 是更早轮次的有界摘录（JSON），context_task_id 是水位——
-- 任务 id 不大于它的成功轮次都已经并入摘录。合并来的旧线程水位为 0，下一次发消息时从头补齐。
ALTER TABLE runtime_chat_conversations ADD COLUMN context_summary TEXT NULL;

ALTER TABLE runtime_chat_conversations ADD COLUMN context_task_id BIGINT NOT NULL DEFAULT 0;

-- 机器人资料页按机器人列出定时 / 手动任务的产物和运行状态。
ALTER TABLE runtime_tasks ADD KEY idx_runtime_task_owner_bot (owner_id, bot_id, id);
