-- 跨站嵌入多实例共享（docs/embedding.md「多实例部署」）。
--
-- 一次性 code 从进程内存搬到 embed_codes：A 实例 mint、B 实例 exchange 也能成功；
-- 一次性由 exchange 的条件 UPDATE（consumed_at IS NULL → now）保证，跨实例只有一个赢家。
-- 只存 sha256，明文只在 mint 响应里出现一次。
CREATE TABLE embed_codes (
  code_hash   CHAR(64)     NOT NULL PRIMARY KEY,
  user_id     BIGINT       NOT NULL,
  app_slug    VARCHAR(64)  NOT NULL,
  app_title   VARCHAR(255) NOT NULL DEFAULT '',
  host_origin VARCHAR(255) NOT NULL,
  -- mint 时绑定的站内落地路径（v1 的 next），exchange 原样带回给 iframe。
  next_path   VARCHAR(512) NOT NULL DEFAULT '',
  expires_at  DATETIME(3)  NOT NULL,
  created_at  DATETIME(3)  NOT NULL,
  consumed_at DATETIME(3)  NULL,
  KEY idx_embed_codes_expires (expires_at)
);

-- mint / exchange 的限流窗口：固定窗口（每分钟一行），按 IP / 用户分键。
-- 多实例共用同一张表，N 个实例不会把额度放大 N 倍；旧窗口由后台清理。
CREATE TABLE embed_rates (
  rate_key     VARCHAR(191) NOT NULL,
  window_start DATETIME(3)  NOT NULL,
  count        INT          NOT NULL DEFAULT 0,
  PRIMARY KEY (rate_key, window_start),
  KEY idx_embed_rates_window (window_start)
);
