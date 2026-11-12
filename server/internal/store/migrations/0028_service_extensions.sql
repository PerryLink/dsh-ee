-- 扩展第 2 批（docs/plugins.md §12）：oauth 按人存令牌、写操作的服务端确认、知识源。

-- oauth 的令牌（access + refresh，有的系统给的是很长的 JWT）和 user_secret 存在同一张表里，放宽长度
ALTER TABLE user_credentials MODIFY COLUMN secret_enc VARBINARY(16384) NOT NULL;

-- 写操作的服务端确认（§7.5）：调用方不是 DSH 时，写操作先停在这里，员工在网页上确认后，
-- 同一个人用同样的参数再调一次才真正执行，而且只执行一次。
CREATE TABLE write_confirmations (
  id          BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  token_hash  CHAR(64)     NOT NULL,
  user_id     BIGINT       NOT NULL,
  asset_id    BIGINT       NOT NULL,
  tool        VARCHAR(128) NOT NULL,
  args_hash   CHAR(64)     NOT NULL,
  args        MEDIUMTEXT   NOT NULL,
  -- pending / approved / rejected / used
  status      VARCHAR(16)  NOT NULL DEFAULT 'pending',
  client      VARCHAR(128) NOT NULL DEFAULT '',
  created_at  DATETIME(3)  NOT NULL,
  expires_at  DATETIME(3)  NOT NULL,
  decided_at  DATETIME(3)  NULL,
  UNIQUE KEY uk_write_confirmations_token (token_hash),
  KEY idx_write_confirmations_lookup (user_id, asset_id, tool, args_hash, status),
  KEY idx_write_confirmations_expires (expires_at)
);

-- 知识源（§6.2）：把一个扩展挂到知识库上。mode=sync 定时把勾选的范围同步进知识库；
-- mode=federated 不复制，员工检索时用他自己的身份实时去查。
CREATE TABLE kb_sources (
  id           BIGINT       NOT NULL AUTO_INCREMENT PRIMARY KEY,
  kbase_id     BIGINT       NOT NULL,
  asset_id     BIGINT       NOT NULL,
  mode         VARCHAR(16)  NOT NULL,
  title        VARCHAR(128) NOT NULL DEFAULT '',
  -- 勾选的节点：[{id, title}]
  scope        TEXT         NOT NULL,
  folder       VARCHAR(255) NOT NULL DEFAULT '',
  sync_cursor  TEXT         NULL,
  last_sync_at DATETIME(3)  NULL,
  last_error   VARCHAR(1000) NOT NULL DEFAULT '',
  doc_count    INT          NOT NULL DEFAULT 0,
  created_by   BIGINT       NOT NULL,
  created_at   DATETIME(3)  NOT NULL,
  updated_at   DATETIME(3)  NOT NULL,
  KEY idx_kb_sources_kbase (kbase_id),
  KEY idx_kb_sources_asset (asset_id)
);

-- 同步进来的文档和源系统里的 id 的对应关系
CREATE TABLE kb_source_docs (
  source_id   BIGINT       NOT NULL,
  external_id VARCHAR(255) NOT NULL,
  doc_id      BIGINT       NOT NULL,
  updated_at  DATETIME(3)  NOT NULL,
  PRIMARY KEY (source_id, external_id),
  KEY idx_kb_source_docs_doc (doc_id)
);
