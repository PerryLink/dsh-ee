// Package lease 是多实例 dsh-ee 的后台任务租约（选主）：
// 周期任务每轮先抢一条数据库租约，只有抢到（或续约成功）的实例执行这一轮，
// 多副本部署时同一个任务不会被重复执行。租约到期后别的实例可以接管；
// 优雅停机时释放，进程崩溃时靠 TTL 过期。
package lease

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultTTL 是租约默认有效期；持有者按 TTL/3 续约，所以进程被强杀后最多这么久别的实例才能接管。
const DefaultTTL = 90 * time.Second

// releaseTimeout 是停机时释放租约的超时：数据库不可用也不能把停机拖住。
const releaseTimeout = 3 * time.Second

// Store 是租约需要的持久化能力，由 *store.Store 实现；测试里用内存实现。
type Store interface {
	// AcquireBackgroundLease 抢占或续租租约，见 store.AcquireBackgroundLease。
	AcquireBackgroundLease(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
	// RenewBackgroundLease 续约；不是自己的租约返回 false。
	RenewBackgroundLease(ctx context.Context, name, owner string, ttl time.Duration) (bool, error)
	// ReleaseBackgroundLease 释放自己的租约（幂等）。
	ReleaseBackgroundLease(ctx context.Context, name, owner string) error
}

// Runner 是后台任务的租约执行器。nil Runner 和未启用租约的 Runner 都直接执行任务，
// 这样单实例部署、测试和服务端未接线时行为与原来一致。
type Runner struct {
	store   Store
	owner   string
	log     *slog.Logger
	ttl     time.Duration
	enabled bool

	mu     sync.Mutex
	held   map[string]struct{}
	closed bool
	wg     sync.WaitGroup
}

// New 创建执行器。enabled 为 false 或 store 为 nil 时退化为「每个实例都直接执行」。
// ttl <= 0 时用 DefaultTTL。
func New(st Store, owner string, enabled bool, ttl time.Duration, log *slog.Logger) *Runner {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{store: st, owner: owner, log: log, ttl: ttl, enabled: enabled, held: map[string]struct{}{}}
}

// OwnerID 返回本实例的租约持有者标识。主机名 + 监听地址 + PID + 随机串：
// 同一个进程内稳定；重启或另一个实例都不一样，因此重启后的旧租约要等 TTL 到期才能接管——
// 这也是进程被强杀时最长的重复执行空窗期。
func OwnerID(addr string) string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	if len(host) > 64 {
		host = host[:64]
	}
	if len(addr) > 40 {
		addr = addr[:40]
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%s/%s/%d/%s", host, addr, os.Getpid(), hex.EncodeToString(b[:]))
}

// Owner 返回本实例的租约持有者标识。
func (r *Runner) Owner() string {
	if r == nil {
		return ""
	}
	return r.owner
}

// TTL 返回租约有效期。nil Runner 返回 DefaultTTL。
func (r *Runner) TTL() time.Duration {
	if r == nil || r.ttl <= 0 {
		return DefaultTTL
	}
	return r.ttl
}

// Enabled 报告租约是否真的生效（未配置 store 时也返回 false）。
func (r *Runner) Enabled() bool {
	return r != nil && r.enabled && r.store != nil
}

// Run 在持有租约时执行 fn，返回 fn 是否执行了。
// 抢不到（别的实例正在跑）、数据库不可用、进程正在关闭或 ctx 已取消时跳过；
// 跳过时只记日志，不会退化成「每个实例都跑」，避免多实例重复执行。
//
// fn 收到的 ctx 在租约丢失（续约失败到期或被接管）时会取消，长任务应检查它。
func (r *Runner) Run(ctx context.Context, name string, fn func(context.Context)) bool {
	if fn == nil {
		return false
	}
	if !r.Enabled() {
		// 单实例/未启用：保持原有行为，直接执行。
		fn(ctx)
		return true
	}
	if ctx.Err() != nil {
		return false
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	r.wg.Add(1)
	r.mu.Unlock()
	defer r.wg.Done()

	ok, err := r.store.AcquireBackgroundLease(ctx, name, r.owner, r.ttl)
	if err != nil {
		// 数据库不可用：这轮跳过并记日志。宁可少跑一轮，也不要在多实例下重复执行。
		// 这些周期任务大多本来就要读写数据库，库不可用时单实例执行也做不成事。
		r.log.Warn("后台任务租约不可用，本轮跳过", "lease", name, "err", err)
		return false
	}
	if !ok {
		r.log.Debug("后台任务由其他实例执行，本轮跳过", "lease", name, "owner", r.owner)
		return false
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = r.release(name)
		return false
	}
	r.held[name] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.held, name)
		r.mu.Unlock()
		_ = r.release(name)
	}()

	leaseCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var lost atomic.Bool
	renewDone := make(chan struct{})
	go func() {
		defer close(renewDone)
		r.renew(leaseCtx, name, cancel, &lost, time.Now().Add(r.ttl))
	}()
	fn(leaseCtx)
	cancel()
	<-renewDone
	if lost.Load() {
		r.log.Warn("后台任务租约已被其他实例接管，已取消本轮任务", "lease", name)
	}
	return true
}

// renew 按 TTL/3 续约；续约失败时在到期前重试，确认过期/被接管就取消本轮任务。
func (r *Runner) renew(ctx context.Context, name string, cancel context.CancelFunc, lost *atomic.Bool, expires time.Time) {
	interval := r.ttl / 3
	if interval < 10*time.Millisecond {
		// 只给自动化测试用很短的 TTL；生产 TTL 远大于这个下限。
		interval = 10 * time.Millisecond
	}
	timer := time.NewTimer(interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		ok, err := r.store.RenewBackgroundLease(ctx, name, r.owner, r.ttl)
		now := time.Now()
		if err != nil {
			// 续约失败先重试；到期的瞬间还联系不上库就视为租约丢失，取消任务，避免双跑。
			if !now.Before(expires) {
				lost.Store(true)
				cancel()
				return
			}
			r.log.Warn("后台任务续约失败，到期前仍会重试", "lease", name, "err", err)
			wait := time.Until(expires) / 2
			if wait < 10*time.Millisecond {
				wait = 10 * time.Millisecond
			}
			if wait > interval {
				wait = interval
			}
			timer.Reset(wait)
			continue
		}
		if !ok {
			lost.Store(true)
			r.log.Warn("后台任务租约已被其他实例接管，取消本轮任务", "lease", name)
			cancel()
			return
		}
		expires = now.Add(r.ttl)
		timer.Reset(interval)
	}
}

// release 用独立上下文释放租约：停机时调用方 ctx 已经取消，仍然要把租约放掉。
func (r *Runner) release(name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), releaseTimeout)
	defer cancel()
	if err := r.store.ReleaseBackgroundLease(ctx, name, r.owner); err != nil {
		r.log.Warn("释放后台任务租约失败，等 TTL 到期后其他实例可接管", "lease", name, "err", err)
		return err
	}
	return nil
}

// Close 优雅停机：不再接受新任务，等在跑的任务结束（最多等到 ctx 超时），
// 然后释放还持有的租约，让其他实例立刻可以接管。
func (r *Runner) Close(ctx context.Context) error {
	if !r.Enabled() {
		return nil
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()

	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// 有任务没在宽限期内退出：仍然把租约放掉，交给其他实例；
		// 任务本身的 ctx 已经由停机取消，重启窗口里由 TTL 与幂等操作兜底。
	}

	r.mu.Lock()
	names := make([]string, 0, len(r.held))
	for name := range r.held {
		names = append(names, name)
	}
	r.mu.Unlock()
	var errs []error
	for _, name := range names {
		if err := r.release(name); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}
	if len(names) > 0 {
		r.log.Info("已释放后台任务租约", "count", len(names))
	}
	return errors.Join(errs...)
}
