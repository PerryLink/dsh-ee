package api

import (
	"context"
	"time"
)

// 周期后台任务的租约名（docs/operations.md §9.2）。
//
// 这些名字是跨实例、跨版本的约定：滚动升级期间新旧实例靠同名租约互斥，
// 改名等于让两个版本各跑一份，不要改。main.go 里的清理任务用同样的命名方式。
const (
	leaseLDAPSync            = "ldap.sync"
	leaseFeishuSync          = "feishu.sync"
	leaseExtHealth           = "extension.health"
	leaseKBSourceSync        = "kb.source-sync"
	leaseElevationExpiry     = "admin.elevation-expiry"
	leaseRecoveryExpiry      = "admin.recovery-expiry"
	leaseAppRowPurge         = "app.row-purge"
	leaseDshReleases         = "dsh.releases"
	leaseConnectPublish      = "connect.publish"
	leaseRegistryCheckUpdate = "registry.check-updates"
)

// runLeased 在持有后台任务租约时执行 fn，返回 fn 是否执行。
// 没接租约（单实例/测试）时直接执行；抢不到、DB 不可用或进程正在关闭时跳过并记日志，
// 不会退化成「每个实例都跑」。
func (s *Server) runLeased(ctx context.Context, name string, fn func(context.Context)) bool {
	return s.leases.Run(ctx, name, fn)
}

// runLeasedRetry 用于启动阶段需要尽快跑一次的任务：第一次没抢到就隔一会儿重试，
// 覆盖进程被强杀后旧租约还没到期的空窗（最多 3 个 TTL/3）。
func (s *Server) runLeasedRetry(ctx context.Context, name string, fn func(context.Context)) bool {
	if s.runLeased(ctx, name, fn) {
		return true
	}
	wait := s.leases.TTL() / 3
	if wait <= 0 {
		wait = 30 * time.Second
	}
	deadline := time.Now().Add(3 * wait)
	for ctx.Err() == nil && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(wait):
		}
		if s.runLeased(ctx, name, fn) {
			return true
		}
	}
	return false
}
