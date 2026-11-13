-- 扩展签名的信任根（docs/plugins.md §9.3、docs/plugin-signing.md §3）：受信任的签名公钥。
-- 撤销、换下都保留记录（不删行）：验签时据此区分 active / retired / revoked，历史签名还能核对，
-- 撤销之后用这个签名的新包不能再装。之前只能通过 DSH_EE_EXTENSION_TRUSTED_KEYS 配公钥，
-- 那张表是库里的信任根，环境变量仍然作为只读的兼容来源。

CREATE TABLE trusted_signing_keys (
  -- 公钥的 sha256 前 16 位十六进制（和 extension.KeyID 一致）
  key_id     VARCHAR(32)  NOT NULL,
  -- PEM：PUBLIC KEY 或 CERTIFICATE（不收私钥）
  public_key TEXT         NOT NULL,
  -- 用途；现在只有 extension（扩展签名），留给以后别的签名
  purpose    VARCHAR(32)  NOT NULL DEFAULT 'extension',
  -- 从什么时候开始信任
  valid_from DATETIME(3)  NOT NULL,
  -- 换下（不再用于新签名，但历史签名仍认）
  retired_at DATETIME(3)  NULL,
  -- 撤销（等于不信任；历史签名显示 revoked，新装 / 升级拒绝）
  revoked_at DATETIME(3)  NULL,
  created_by BIGINT       NULL,
  note       VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3)  NOT NULL,
  PRIMARY KEY (key_id),
  KEY idx_trusted_signing_keys_created (created_at)
);
