package lease_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/lease"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// openTestStore 建一个随机库并跑迁移；没有数据库时跳过（CI 的 server job 有 TiDB）。
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	base := os.Getenv("DSH_EE_TEST_DSN")
	if base == "" {
		t.Skip("未设置 DSH_EE_TEST_DSN，跳过数据库集成测试")
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "dsh_ee_test_lease_" + hex.EncodeToString(suffix)
	ctx := context.Background()
	st, err := store.Open(ctx, strings.Replace(base, ")/", ")/"+name, 1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.DB().ExecContext(context.Background(), "DROP DATABASE `"+name+"`")
		st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return st
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// 两个 Runner 共用同一个真实数据库：同一任务同一时刻只有一个实例执行。
func TestStoreBackedTwoInstancesOnlyOneRuns(t *testing.T) {
	st := openTestStore(t)
	a := lease.New(st, "instance-a", true, 5*time.Second, quietLog())
	b := lease.New(st, "instance-b", true, 5*time.Second, quietLog())

	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var ran atomic.Int32
	var wg sync.WaitGroup
	results := make([]bool, 2)
	for i, r := range []*lease.Runner{a, b} {
		wg.Add(1)
		go func(i int, r *lease.Runner) {
			defer wg.Done()
			results[i] = r.Run(context.Background(), "kb.source-sync", func(context.Context) {
				ran.Add(1)
				startOnce.Do(func() { close(started) })
				<-release
			})
		}(i, r)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("没有实例抢到租约")
	}
	// 给另一个 Runner 一次尝试的机会（它应被跳过）。
	time.Sleep(100 * time.Millisecond)
	close(release)
	wg.Wait()
	if ran.Load() != 1 {
		t.Fatalf("两个实例应只有一个执行任务，实际执行 %d 次", ran.Load())
	}
	executed := 0
	for _, ok := range results {
		if ok {
			executed++
		}
	}
	if executed != 1 {
		t.Fatalf("应恰好一个 Runner 报告执行，得到 %d", executed)
	}
}

// 真实数据库上：旧实例崩溃后租约过期，新实例接管并执行。
func TestStoreBackedTakeoverAfterExpiry(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	// 模拟崩溃实例留下的、即将过期的租约。
	if ok, err := st.AcquireBackgroundLease(ctx, "app.row-purge", "crashed-instance", 100*time.Millisecond); err != nil || !ok {
		t.Fatalf("准备旧租约：ok=%v err=%v", ok, err)
	}
	time.Sleep(250 * time.Millisecond)
	r := lease.New(st, "instance-b", true, time.Minute, quietLog())
	var ran atomic.Int32
	if !r.Run(ctx, "app.row-purge", func(context.Context) { ran.Add(1) }) {
		t.Fatal("旧租约过期后应接管并执行")
	}
	if ran.Load() != 1 {
		t.Fatalf("应执行 1 次，得到 %d", ran.Load())
	}
	// 任务结束后释放，其他实例可以立刻抢到。
	if ok, err := st.AcquireBackgroundLease(ctx, "app.row-purge", "instance-c", time.Minute); err != nil || !ok {
		t.Fatalf("释放后应能重新抢到：ok=%v err=%v", ok, err)
	}
}
