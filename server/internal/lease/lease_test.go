package lease

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeStore 是内存版租约表，行为与 store 的 CAS 一致，用来模拟多实例。
type fakeStore struct {
	mu     sync.Mutex
	leases map[string]fakeLease
	now    func() time.Time

	acquireCalls int
	releaseCalls int
	acquireErr   error
	renewErr     error
	renewOK      bool
}

type fakeLease struct {
	owner   string
	expires time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{leases: map[string]fakeLease{}, now: time.Now, renewOK: true}
}

func (f *fakeStore) AcquireBackgroundLease(_ context.Context, name, owner string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.acquireCalls++
	if f.acquireErr != nil {
		return false, f.acquireErr
	}
	now := f.now()
	cur, ok := f.leases[name]
	if ok && cur.owner != owner && cur.expires.After(now) {
		return false, nil
	}
	f.leases[name] = fakeLease{owner: owner, expires: now.Add(ttl)}
	return true, nil
}

func (f *fakeStore) RenewBackgroundLease(_ context.Context, name, owner string, ttl time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.renewErr != nil {
		return false, f.renewErr
	}
	now := f.now()
	cur, ok := f.leases[name]
	if !ok || cur.owner != owner || !cur.expires.After(now) {
		return false, nil
	}
	if !f.renewOK {
		return false, nil
	}
	f.leases[name] = fakeLease{owner: owner, expires: now.Add(ttl)}
	return true, nil
}

func (f *fakeStore) ReleaseBackgroundLease(_ context.Context, name, owner string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.releaseCalls++
	if cur, ok := f.leases[name]; ok && cur.owner == owner {
		delete(f.leases, name)
	}
	return nil
}

func (f *fakeStore) seed(name, owner string, expires time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leases[name] = fakeLease{owner: owner, expires: expires}
}

func (f *fakeStore) owner(name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.leases[name].owner
}

func (f *fakeStore) has(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.leases[name]
	return ok
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestNilAndDisabledRunnerRunsDirectly(t *testing.T) {
	var ran atomic.Int32
	fn := func(context.Context) { ran.Add(1) }

	var nilRunner *Runner
	if !nilRunner.Run(context.Background(), "task", fn) {
		t.Fatal("nil Runner 应直接执行任务")
	}
	if nilRunner.Enabled() {
		t.Fatal("nil Runner 不应认为租约生效")
	}
	st := newFakeStore()
	r := New(st, "instance-a", false, 0, quietLog())
	if !r.Run(context.Background(), "task", fn) {
		t.Fatal("关闭租约时应直接执行任务")
	}
	if ran.Load() != 2 {
		t.Fatalf("应执行 2 次，得到 %d", ran.Load())
	}
	if st.acquireCalls != 0 {
		t.Fatalf("关闭租约时不应访问租约表（调用 %d 次）", st.acquireCalls)
	}
}

func TestTwoInstancesOnlyOneRuns(t *testing.T) {
	st := newFakeStore()
	a := New(st, "instance-a", true, 5*time.Second, quietLog())
	b := New(st, "instance-b", true, 5*time.Second, quietLog())
	started := make(chan struct{})
	release := make(chan struct{})
	var ranA, ranB atomic.Int32
	doneA := make(chan bool, 1)
	go func() {
		doneA <- a.Run(context.Background(), "ldap.sync", func(context.Context) {
			ranA.Add(1)
			close(started)
			<-release
		})
	}()
	<-started

	var ranBFlag bool
	ranBFlag = b.Run(context.Background(), "ldap.sync", func(context.Context) {
		ranB.Add(1)
	})
	if ranBFlag {
		t.Fatal("第二个实例不应执行：租约由第一个实例持有")
	}
	if ranB.Load() != 0 {
		t.Fatal("第二个实例的任务不应执行")
	}
	close(release)
	if !<-doneA {
		t.Fatal("第一个实例应执行任务")
	}
	if ranA.Load() != 1 {
		t.Fatalf("第一个实例应执行 1 次，得到 %d", ranA.Load())
	}
	if st.has("ldap.sync") {
		t.Fatal("任务结束后应释放租约")
	}
}

func TestTTLExpiryTakeover(t *testing.T) {
	st := newFakeStore()
	// 模拟一个崩溃的实例：租约还在，但马上过期。
	st.seed("kb.source-sync", "crashed-instance", time.Now().Add(60*time.Millisecond))
	time.Sleep(100 * time.Millisecond)

	r := New(st, "instance-b", true, time.Minute, quietLog())
	var ran atomic.Int32
	ok := r.Run(context.Background(), "kb.source-sync", func(context.Context) {
		ran.Add(1)
		if got := st.owner("kb.source-sync"); got != "instance-b" {
			t.Fatalf("接管后执行期间租约应归新实例，得到 %q", got)
		}
	})
	if !ok || ran.Load() != 1 {
		t.Fatal("旧租约过期后应接管并执行")
	}
}

func TestRenewKeepsLeaseAlive(t *testing.T) {
	st := newFakeStore()
	a := New(st, "instance-a", true, 120*time.Millisecond, quietLog())
	b := New(st, "instance-b", true, 120*time.Millisecond, quietLog())
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		done <- a.Run(context.Background(), "feishu.sync", func(context.Context) {
			close(started)
			time.Sleep(400 * time.Millisecond)
		})
	}()
	<-started
	// 任务执行时间超过 TTL，续约应让别的实例抢不到。
	time.Sleep(250 * time.Millisecond)
	if ok := b.Run(context.Background(), "feishu.sync", func(context.Context) {
		t.Fatal("租约还在续约中，第二个实例不应执行")
	}); ok {
		t.Fatal("租约还在续约中，第二个实例不应抢到")
	}
	if !<-done {
		t.Fatal("第一个实例应执行")
	}
	// 释放后第二个实例立刻可以执行。
	if ok := b.Run(context.Background(), "feishu.sync", func(context.Context) {}); !ok {
		t.Fatal("任务结束释放租约后，第二个实例应能执行")
	}
}

func TestDBErrorSkipsInsteadOfDoubleRun(t *testing.T) {
	st := newFakeStore()
	st.acquireErr = errors.New("database is down")
	r := New(st, "instance-a", true, time.Minute, quietLog())
	var ran atomic.Int32
	if ok := r.Run(context.Background(), "connect.publish", func(context.Context) { ran.Add(1) }); ok {
		t.Fatal("数据库不可用时应跳过，而不是执行")
	}
	if ran.Load() != 0 {
		t.Fatal("数据库不可用时任务不应执行")
	}
	if st.acquireCalls != 1 {
		t.Fatalf("应尝试抢租约 1 次，得到 %d", st.acquireCalls)
	}
}

func TestRenewLostCancelsTask(t *testing.T) {
	st := newFakeStore()
	st.renewOK = false // 第一次续约就发现租约已被接管
	r := New(st, "instance-a", true, 200*time.Millisecond, quietLog())
	canceled := make(chan struct{})
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		done <- r.Run(context.Background(), "app.row-purge", func(ctx context.Context) {
			close(started)
			<-ctx.Done()
			close(canceled)
		})
	}()
	<-started
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("租约丢失后任务应被取消")
	}
	if !<-done {
		t.Fatal("任务执行过，Run 应返回 true")
	}
}

func TestRenewErrorCancelsAtExpiry(t *testing.T) {
	st := newFakeStore()
	st.renewErr = errors.New("database is down")
	r := New(st, "instance-a", true, 150*time.Millisecond, quietLog())
	started := make(chan struct{})
	done := make(chan bool, 1)
	go func() {
		done <- r.Run(context.Background(), "sessions.sweep", func(ctx context.Context) {
			close(started)
			<-ctx.Done()
		})
	}()
	<-started
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("续约一直失败时，到期后应取消任务")
	}
}

func TestContextCancelReleasesLease(t *testing.T) {
	st := newFakeStore()
	r := New(st, "instance-a", true, time.Minute, quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(ctx, "registry.check-updates", func(ctx context.Context) {
			close(started)
			<-ctx.Done()
		})
	}()
	<-started
	if !st.has("registry.check-updates") {
		t.Fatal("执行期间应持有租约")
	}
	cancel()
	<-done
	deadline := time.Now().Add(time.Second)
	for st.has("registry.check-updates") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if st.has("registry.check-updates") {
		t.Fatal("停机取消后应释放租约")
	}
}

func TestCloseReleasesHeldLease(t *testing.T) {
	st := newFakeStore()
	a := New(st, "instance-a", true, 5*time.Second, quietLog())
	started := make(chan struct{})
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.Run(context.Background(), "dsh.releases", func(context.Context) {
			close(started)
			<-stop
		})
	}()
	<-started

	// 任务不响应取消：Close 在宽限期结束后仍要释放租约。
	closeCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := a.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if st.has("dsh.releases") {
		t.Fatal("Close 后应释放租约")
	}
	b := New(st, "instance-b", true, 5*time.Second, quietLog())
	if ok := b.Run(context.Background(), "dsh.releases", func(context.Context) {}); !ok {
		t.Fatal("停机释放后另一个实例应能执行")
	}
	close(stop)
	<-done
	// 已经 close 的 Runner 不再接受新任务。
	if ok := a.Run(context.Background(), "dsh.releases", func(context.Context) {}); ok {
		t.Fatal("Close 之后不应再执行任务")
	}
}

func TestOwnerID(t *testing.T) {
	a := OwnerID("127.0.0.1:8080")
	b := OwnerID("127.0.0.1:8080")
	if a == "" || len(a) > 128 {
		t.Fatalf("持有者标识不合法：%q", a)
	}
	if a == b {
		t.Fatal("两个进程的持有者标识不应相同")
	}
}
