-- API 客户端注册（client_credentials，docs/token-lifecycle.md §6）。
--
-- 给第三方 Harness / CI 这类无人值守的集成方发一对 client_id / client_secret，
-- 由它们自己换 access token，而不是把一枚管理员令牌交给对方。
-- client_secret 只存 sha256，明文只在创建响应里出现一次（和会话、设备令牌一致）。
CREATE TABLE api_clients (
  id            BIGINT        NOT NULL AUTO_INCREMENT,
  -- 对外可见的随机标识（dshc_…），不是自增 id；管理 API / 客户端凭据里用这个
  client_id     VARCHAR(64)   NOT NULL,
  -- client_secret 的 sha256（十六进制），不存明文
  secret_hash   CHAR(64)      NOT NULL,
  name          VARCHAR(128)  NOT NULL,
  -- 令牌签给哪个账号：机器人账号（owner_id 指向负责人）或员工本人都可以
  owner_user_id BIGINT        NOT NULL,
  -- JSON 数组，如 ["models","hub"]；签发时与 owner 当前权限求交集
  scopes        VARCHAR(1024) NOT NULL DEFAULT '',
  -- 签发出来的 access token 属于哪类客户端：目前只发 third_party（写操作始终人工确认）
  client_kind   VARCHAR(16)   NOT NULL DEFAULT 'third_party',
  created_by    BIGINT        NOT NULL DEFAULT 0,
  -- 手工禁用（DELETE /api/admin/clients/{client_id}）：禁用后不能再换 token，已发出的令牌按自身有效期自然过期
  disabled_at   DATETIME(3)   NULL,
  last_used_at  DATETIME(3)   NULL,
  created_at    DATETIME(3)   NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_api_clients_client_id (client_id),
  KEY idx_api_clients_owner (owner_user_id),
  KEY idx_api_clients_disabled (disabled_at)
);

-- 审计来源 via 原来是 VARCHAR(8)，放不下 client_credentials（18 个字符）；
-- 只加宽不改变已有值，旧代码写 web/token/agent/system 仍然兼容。
ALTER TABLE audit_logs MODIFY COLUMN via VARCHAR(32) NOT NULL DEFAULT 'web';
