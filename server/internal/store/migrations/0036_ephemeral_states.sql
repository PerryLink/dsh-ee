-- 多实例共享的一次性/短时状态（web link handoff、开始使用接入码、OIDC state/PKCE）。
--
-- 这些状态原来放在进程内 map：A 实例生成、B 实例回调/轮询时找不到，多实例部署会失败。
-- 放到数据库后由所有实例共享；一次性由 ConsumeEphemeralState 的条件 UPDATE
-- （consumed_at IS NULL → now()）保证，跨实例并发消费只有一个赢家。
-- state_hash 只存随机值的 sha256，明文只在生成方的响应/URL 里出现一次；
-- payload 是 JSON，敏感内容（OIDC nonce / PKCE verifier）由 API 层用主密钥加密后再存。
CREATE TABLE ephemeral_states (
  kind        VARCHAR(32)  NOT NULL,
  state_hash  CHAR(64)     NOT NULL,
  -- 按属主替换用（例如每人只保留一个「开始使用」接入码）；NULL 表示不按属主去重。
  owner_key   VARCHAR(191) NULL,
  payload     JSON         NOT NULL,
  expires_at  DATETIME(3)  NOT NULL,
  created_at  DATETIME(3)  NOT NULL,
  consumed_at DATETIME(3)  NULL,
  PRIMARY KEY (kind, state_hash),
  -- 同一个 kind 下同一个属主最多一条；MySQL 唯一索引允许多个 NULL，所以无属主的状态不受影响。
  UNIQUE KEY uk_ephemeral_states_owner (kind, owner_key),
  -- 过期清理按 expires_at 扫描。
  KEY idx_ephemeral_states_expires (expires_at)
);
