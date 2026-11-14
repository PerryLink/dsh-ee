package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// 0035：多实例后台任务租约表。
func TestBackgroundLeasesMigration(t *testing.T) {
	body, err := migrationFS.ReadFile("migrations/0035_background_leases.sql")
	if err != nil {
		t.Fatalf("读 0035 迁移: %v", err)
	}
	stmts := splitStatements(string(body))
	if len(stmts) != 1 {
		t.Fatalf("0035 迁移应只有一条建表语句，得到 %d", len(stmts))
	}
	joined := stmts[0]
	for _, want := range []string{
		"CREATE TABLE background_leases",
		"name       VARCHAR(64)  NOT NULL",
		"owner      VARCHAR(128) NOT NULL",
		"expires_at DATETIME(3)  NOT NULL",
		"renewed_at DATETIME(3)  NOT NULL",
		"PRIMARY KEY (name)",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("0035 迁移缺少 %q：\n%s", want, joined)
		}
	}
}

func TestBackgroundLeaseStore(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	const name = "ldap.sync"

	// 第一次抢：成功。
	ok, err := st.AcquireBackgroundLease(ctx, name, "instance-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("首次抢租约：ok=%v err=%v", ok, err)
	}
	cur, err := st.GetBackgroundLease(ctx, name)
	if err != nil || cur.Owner != "instance-a" || !cur.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("抢到后的租约：%+v err=%v", cur, err)
	}
	if cur.RenewedAt.IsZero() {
		t.Fatal("renewed_at 不应为零值")
	}

	// 另一个实例在同一 TTL 内抢不到，也不能续约或释放别人的租约。
	if ok, err := st.AcquireBackgroundLease(ctx, name, "instance-b", time.Minute); err != nil || ok {
		t.Fatalf("未过期时第二个实例不应抢到：ok=%v err=%v", ok, err)
	}
	if ok, err := st.RenewBackgroundLease(ctx, name, "instance-b", time.Minute); err != nil || ok {
		t.Fatalf("非持有者不应续约：ok=%v err=%v", ok, err)
	}
	if err := st.ReleaseBackgroundLease(ctx, name, "instance-b"); err != nil {
		t.Fatalf("非持有者释放租约（幂等）：%v", err)
	}
	if cur, err := st.GetBackgroundLease(ctx, name); err != nil || cur.Owner != "instance-a" {
		t.Fatalf("非持有者释放后租约不应变化：%+v err=%v", cur, err)
	}

	// 持有者续约成功，到期时间往后推。
	before := cur.ExpiresAt
	time.Sleep(20 * time.Millisecond)
	if ok, err := st.RenewBackgroundLease(ctx, name, "instance-a", time.Minute); err != nil || !ok {
		t.Fatalf("持有者续约：ok=%v err=%v", ok, err)
	}
	renewed, err := st.GetBackgroundLease(ctx, name)
	if err != nil || !renewed.ExpiresAt.After(before) {
		t.Fatalf("续约后到期时间应更晚：before=%v after=%+v err=%v", before, renewed, err)
	}

	// 租约过期后，别的实例可以接管（用很短的 TTL 模拟崩溃）。
	if ok, err := st.AcquireBackgroundLease(ctx, name, "instance-a", 100*time.Millisecond); err != nil || !ok {
		t.Fatalf("短期续约：ok=%v err=%v", ok, err)
	}
	if ok, err := st.AcquireBackgroundLease(ctx, name, "instance-b", time.Minute); err != nil || ok {
		t.Fatalf("过期前第二个实例不应抢到：ok=%v err=%v", ok, err)
	}
	time.Sleep(200 * time.Millisecond)
	if ok, err := st.AcquireBackgroundLease(ctx, name, "instance-b", time.Minute); err != nil || !ok {
		t.Fatalf("过期后第二个实例应接管：ok=%v err=%v", ok, err)
	}
	if cur, err := st.GetBackgroundLease(ctx, name); err != nil || cur.Owner != "instance-b" {
		t.Fatalf("接管后的持有者应是 instance-b：%+v err=%v", cur, err)
	}
	// 被接管后旧实例不能再续约。
	if ok, err := st.RenewBackgroundLease(ctx, name, "instance-a", time.Minute); err != nil || ok {
		t.Fatalf("旧实例不应续约成功：ok=%v err=%v", ok, err)
	}

	// 释放后立刻可以被别人抢到。
	if err := st.ReleaseBackgroundLease(ctx, name, "instance-b"); err != nil {
		t.Fatalf("释放：%v", err)
	}
	if _, err := st.GetBackgroundLease(ctx, name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("释放后应查不到租约：%v", err)
	}
	if ok, err := st.AcquireBackgroundLease(ctx, name, "instance-a", time.Minute); err != nil || !ok {
		t.Fatalf("释放后应能重新抢到：ok=%v err=%v", ok, err)
	}

	// 参数校验与数据库错误传递。
	if _, err := st.AcquireBackgroundLease(ctx, "", "instance-a", time.Minute); err == nil {
		t.Fatal("空租约名应报错")
	}
	if _, err := st.AcquireBackgroundLease(ctx, name, "instance-a", 0); err == nil {
		t.Fatal("TTL 为 0 应报错")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := st.AcquireBackgroundLease(canceled, name, "instance-a", time.Minute); err == nil {
		t.Fatal("数据库不可用（ctx 已取消）时应返回错误，不能假装抢到")
	}
}
