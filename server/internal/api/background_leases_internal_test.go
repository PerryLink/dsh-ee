package api

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/lease"
)

// fakeLeaseStore 只实现租约需要的三个方法，用来验证后台任务的租约接线。
type fakeLeaseStore struct {
	mu           sync.Mutex
	lastAcquired string
	acquires     int
	failNext     int // 前 N 次抢租约返回失败，模拟别的实例持有/数据库还没就绪
}

func (f *fakeLeaseStore) AcquireBackgroundLease(_ context.Context, name, owner string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquires++
	if f.failNext > 0 {
		f.failNext--
		return false, nil
	}
	f.lastAcquired = name
	return true, nil
}

func (f *fakeLeaseStore) RenewBackgroundLease(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}

func (f *fakeLeaseStore) ReleaseBackgroundLease(context.Context, string, string) error { return nil }

func (f *fakeLeaseStore) acquireCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.acquires
}

func quietServer() *Server {
	return &Server{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// 没有接租约执行器的 Server（单实例、测试）保持原有行为：直接执行。
func TestRunLeasedWithoutRunner(t *testing.T) {
	s := quietServer()
	var ran atomic.Int32
	if !s.runLeased(context.Background(), leaseLDAPSync, func(context.Context) { ran.Add(1) }) {
		t.Fatal("没接租约时应直接执行")
	}
	if ran.Load() != 1 {
		t.Fatalf("应执行 1 次，得到 %d", ran.Load())
	}
}

// 租约由别的实例持有时跳过本轮，不执行任务。
func TestRunLeasedSkipsWhenHeld(t *testing.T) {
	st := &fakeLeaseStore{failNext: 1}
	s := &Server{log: quietServer().log, leases: lease.New(st, "instance-b", true, time.Minute, quietServer().log)}
	var ran atomic.Int32
	if s.runLeased(context.Background(), leaseFeishuSync, func(context.Context) { ran.Add(1) }) {
		t.Fatal("租约被其他实例持有时不应执行")
	}
	if ran.Load() != 0 {
		t.Fatal("租约被其他实例持有时任务不应执行")
	}
}

// 启动阶段的重试：第一次抢不到，隔一会儿抢到后执行一次。
func TestRunLeasedRetryAcquiresLater(t *testing.T) {
	st := &fakeLeaseStore{failNext: 1}
	s := &Server{log: quietServer().log, leases: lease.New(st, "instance-b", true, 30*time.Millisecond, quietServer().log)}
	var ran atomic.Int32
	if !s.runLeasedRetry(context.Background(), leaseConnectPublish, func(context.Context) { ran.Add(1) }) {
		t.Fatal("重试后应执行")
	}
	if ran.Load() != 1 {
		t.Fatalf("应只执行 1 次，得到 %d", ran.Load())
	}
	if st.acquireCount() < 2 {
		t.Fatalf("应至少抢 2 次租约，得到 %d", st.acquireCount())
	}
	st.mu.Lock()
	got := st.lastAcquired
	st.mu.Unlock()
	if got != leaseConnectPublish {
		t.Fatalf("启动检查应使用 %q 租约，得到 %q", leaseConnectPublish, got)
	}
}

// ctx 取消后重试立即结束，不执行任务。
func TestRunLeasedRetryStopsOnCancel(t *testing.T) {
	st := &fakeLeaseStore{failNext: 100}
	s := &Server{log: quietServer().log, leases: lease.New(st, "instance-b", true, 30*time.Millisecond, quietServer().log)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Int32
	if s.runLeasedRetry(ctx, leaseDshReleases, func(context.Context) { ran.Add(1) }) {
		t.Fatal("ctx 已取消时不应执行")
	}
	if ran.Load() != 0 {
		t.Fatal("ctx 已取消时任务不应执行")
	}
}
