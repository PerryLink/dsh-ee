-- OAuth 风格的刷新令牌（docs/token-lifecycle.md §3）：一次性轮换 + 复用检测 + 族吊销。
-- 默认关闭（DSH_EE_REFRESH_TOKENS），关闭时 /api/auth/refresh 返回 404；旧客户端行为不变。
-- refresh token 只存 sha256，明文只在签发/轮换的响应里出现一次。
CREATE TABLE token_refresh_tokens (
  id                 BIGINT       NOT NULL AUTO_INCREMENT,
  family_id          CHAR(32)     NOT NULL,
  access_token_id    BIGINT       NOT NULL,
  user_id            BIGINT       NOT NULL,
  client_kind        VARCHAR(16)  NOT NULL DEFAULT 'third_party',
  secret_hash        CHAR(64)     NOT NULL,
  issued_at          DATETIME(3)  NOT NULL,
  expires_at         DATETIME(3)  NOT NULL,
  family_expires_at  DATETIME(3)  NOT NULL,
  rotated_at         DATETIME(3)  NULL,
  revoked_at         DATETIME(3)  NULL,
  reuse_detected_at  DATETIME(3)  NULL,
  created_ip         VARCHAR(64)  NOT NULL DEFAULT '',
  -- 轮换宽限（DSH_EE_REFRESH_GRACE）内同一枚旧 refresh 重试时原样返回上次响应；
  -- 只存主密钥加密后的响应，宽限期过后由后台任务清空。
  successor_sealed   VARBINARY(2048) NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_token_refresh_hash (secret_hash),
  KEY idx_token_refresh_family (family_id),
  KEY idx_token_refresh_access (access_token_id),
  KEY idx_token_refresh_user (user_id),
  KEY idx_token_refresh_expiry (expires_at)
);

-- 令牌族：同一次登录/签发出来的 access token 与后续刷新出来的代际共用 family_id。
-- 旧令牌、以及未开启 refresh 时签发的令牌为 NULL，行为不变。
ALTER TABLE access_tokens ADD COLUMN family_id CHAR(32) NULL;
ALTER TABLE access_tokens ADD KEY idx_access_tokens_family (family_id);
