-- 多实例后台任务租约（docs/operations.md §9.2）：每个周期后台任务一条记录，
-- 只有抢到（或续租成功）的实例执行这一轮，避免多副本重复跑同一件事。
-- expires_at 是租约到期时间，持有者定期续约；进程崩溃后到期，别的实例可以接管。
-- name 是稳定的任务名（server/internal/api/background_leases.go 与
-- server/cmd/dsh-ee/main.go 里的 lease* 常量），滚动升级时新旧实例靠它互斥。
CREATE TABLE background_leases (
  name       VARCHAR(64)  NOT NULL,
  owner      VARCHAR(128) NOT NULL,
  expires_at DATETIME(3)  NOT NULL,
  renewed_at DATETIME(3)  NOT NULL,
  PRIMARY KEY (name)
);
