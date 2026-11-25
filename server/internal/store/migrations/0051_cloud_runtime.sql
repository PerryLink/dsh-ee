CREATE TABLE IF NOT EXISTS runtime_bots (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_id BIGINT NOT NULL,
 run_as_id BIGINT NOT NULL,
 name VARCHAR(128) NOT NULL,
 prompt TEXT NOT NULL,
 schedule VARCHAR(128) NOT NULL,
 enabled TINYINT(1) NOT NULL,
 read_only TINYINT(1) NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 KEY idx_runtime_bots_enabled (enabled, id)
);

CREATE TABLE IF NOT EXISTS runtime_tasks (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 owner_id BIGINT NOT NULL,
 run_as_id BIGINT NOT NULL,
 bot_id BIGINT NULL,
 prompt TEXT NOT NULL,
 status VARCHAR(16) NOT NULL,
 attempts INT NOT NULL DEFAULT 0,
 idempotency_key VARBINARY(128) NULL,
 timeout_seconds INT NOT NULL,
 error TEXT NOT NULL,
 summary TEXT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 updated_at DATETIME(3) NOT NULL,
 started_at DATETIME(3) NULL,
 finished_at DATETIME(3) NULL,
 lease_until DATETIME(3) NULL,
 UNIQUE KEY uq_runtime_task_idempotency (owner_id, idempotency_key),
 KEY idx_runtime_task_claim (status, lease_until, id),
 KEY idx_runtime_task_owner (owner_id, id)
);

CREATE TABLE IF NOT EXISTS runtime_artifacts (
 id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
 task_id BIGINT NOT NULL,
 name VARCHAR(255) NOT NULL,
 storage_key VARBINARY(128) NOT NULL,
 size_bytes BIGINT NOT NULL,
 created_at DATETIME(3) NOT NULL,
 KEY idx_runtime_artifacts_task (task_id, id)
);
