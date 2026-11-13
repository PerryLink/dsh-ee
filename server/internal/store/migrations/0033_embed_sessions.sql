-- 跨站嵌入 v1（docs/cross-site-embedding.md）：iframe 内的 cookie-free 会话。
-- 与网页会话 sessions 表分开，避免两种身份混在一张表里；token 只存 sha256，明文只返回一次。
CREATE TABLE embed_sessions (
  token_hash  CHAR(64)     NOT NULL PRIMARY KEY,
  user_id     BIGINT       NOT NULL,
  app_slug    VARCHAR(64)  NOT NULL,
  host_origin VARCHAR(255) NOT NULL,
  expires_at  DATETIME(3)  NOT NULL,
  created_at  DATETIME(3)  NOT NULL,
  revoked_at  DATETIME(3)  NULL,
  KEY idx_embed_sessions_user (user_id),
  KEY idx_embed_sessions_expires (expires_at)
);
