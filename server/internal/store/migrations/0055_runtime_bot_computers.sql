-- 每个云机器人一台常驻云电脑（OpenSandbox 沙箱）：空闲后休眠，下一轮唤醒，工作目录里的文件保留。
-- 一行对应一个机器人；sandbox_id 为空表示还没有电脑（state='none'），下一轮再新建。
-- holder / lease_until 是独占租约：同一时间只有一个任务或一次手动操作能使用或改动这台电脑，跨实例生效。
-- 表里不存任何凭证：每轮的短期令牌只在执行时通过环境变量传入，这一轮结束即吊销。
CREATE TABLE IF NOT EXISTS runtime_bot_computers (
 bot_id BIGINT NOT NULL PRIMARY KEY,
 sandbox_id VARCHAR(255) NOT NULL DEFAULT '',
 state VARCHAR(16) NOT NULL,
 holder VARCHAR(64) NOT NULL DEFAULT '',
 lease_until DATETIME(3) NULL,
 disk_bytes BIGINT NOT NULL DEFAULT 0,
 last_used_at DATETIME(3) NOT NULL,
 hibernated_at DATETIME(3) NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 KEY idx_runtime_bot_computer_state (state, last_used_at)
);
