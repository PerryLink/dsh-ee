-- 等数据迁移提交版本记录后再删旧表，让 0060 中断后仍能从旧表重试。
DROP TABLE IF EXISTS runtime_bot_computers;
