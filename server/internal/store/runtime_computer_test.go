package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 0056 把「每个机器人一台」合并成「每个员工一台」：每位员工保留最近使用的那台，其余进回收队列。
// 迁移的每条语句都可重跑，这里在已迁移的库上重建旧表、放进旧数据，再把 0056 整个执行一遍。
func TestMigrationMergesBotComputersPerOwner(t *testing.T) {
	s, _ := openTokenTestStoreDSN(t)
	ctx := context.Background()
	user := func(name string) int64 {
		u := &User{Username: name, DisplayName: name, AccountType: AccountHuman}
		if err := s.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	alice, bob := user("alice"), user("bob")
	bot := func(owner int64) int64 {
		b := &RuntimeBot{OwnerID: owner, RunAsID: owner, Name: "b", Prompt: "p", Schedule: "0 9 * * *", ReadOnly: true, DeliveryTarget: "feishu_owner"}
		if err := s.SaveRuntimeBot(ctx, b, nil); err != nil {
			t.Fatal(err)
		}
		return b.ID
	}
	a1, a2, a3, b1 := bot(alice), bot(alice), bot(alice), bot(bob)
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE runtime_bot_computers (
		bot_id BIGINT NOT NULL PRIMARY KEY, sandbox_id VARCHAR(255) NOT NULL DEFAULT '', state VARCHAR(16) NOT NULL,
		holder VARCHAR(64) NOT NULL DEFAULT '', lease_until DATETIME(3) NULL, disk_bytes BIGINT NOT NULL DEFAULT 0,
		last_used_at DATETIME(3) NOT NULL, hibernated_at DATETIME(3) NULL, created_at DATETIME(3) NOT NULL, updated_at DATETIME(3) NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		bot     int64
		sandbox string
		state   string
		used    time.Duration
	}{
		{a1, "sb-a1", RuntimeComputerHibernated, 1 * time.Hour},
		{a2, "sb-a2", RuntimeComputerHibernated, 3 * time.Hour}, // alice 最近用的
		{a3, "", RuntimeComputerNone, 5 * time.Hour},            // 没有电脑，丢弃
		{b1, "sb-b1", RuntimeComputerRunning, 2 * time.Hour},
		{999999, "sb-gone", RuntimeComputerHibernated, 4 * time.Hour}, // 机器人已删除
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO runtime_bot_computers (bot_id, sandbox_id, state, holder, lease_until, disk_bytes, last_used_at, created_at, updated_at)
			VALUES (?,?,?,'task:1',?,?,?,?,?)`, row.bot, row.sandbox, row.state, base.Add(time.Hour), 1024, base.Add(row.used), base, base); err != nil {
			t.Fatal(err)
		}
	}
	body, err := migrationFS.ReadFile("migrations/0056_runtime_owner_computers.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range splitStatements(string(body)) {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%v\n%s", err, stmt)
		}
	}
	got, err := s.RuntimeComputerOf(ctx, UserComputerOwner(alice))
	if err != nil || got.SandboxID != "sb-a2" || got.State != RuntimeComputerHibernated || got.Busy || got.BotID != a2 || got.DiskBytes != 1024 {
		t.Fatalf("alice keeps her most recently used computer: %+v %v", got, err)
	}
	if got, err := s.RuntimeComputerOf(ctx, UserComputerOwner(bob)); err != nil || got.SandboxID != "sb-b1" {
		t.Fatalf("bob: %+v %v", got, err)
	}
	orphans, err := s.RuntimeComputerOrphans(ctx, 10)
	if err != nil || len(orphans) != 2 || orphans[0] == orphans[1] {
		t.Fatalf("orphans: %v %v", orphans, err)
	}
	for _, id := range orphans {
		if id != "sb-a1" && id != "sb-gone" {
			t.Fatalf("unexpected orphan %q", id)
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='runtime_bot_computers'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("old table should be dropped: %d %v", n, err)
	}
	// 租约：同一所有者同一时间只有一个持有者，团队电脑与员工电脑各自独立。
	held, err := s.AcquireRuntimeComputer(ctx, UserComputerOwner(alice), "task:7", a1, time.Now().Add(time.Minute))
	if err != nil || held.ID != got.ID || held.SandboxID != "sb-a2" || held.BotID != a1 {
		t.Fatalf("acquire: %+v %v", held, err)
	}
	if _, err := s.AcquireRuntimeComputer(ctx, UserComputerOwner(alice), "task:8", a2, time.Now().Add(time.Minute)); !errors.Is(err, ErrRuntimeComputerBusy) {
		t.Fatalf("second holder: %v", err)
	}
	team, err := s.AcquireRuntimeComputer(ctx, TeamComputerOwner("研发部"), "task:9", a1, time.Now().Add(time.Minute))
	if err != nil || team.ID == held.ID || team.OwnerKind != RuntimeComputerOwnerTeam || team.Department != "研发部" || team.State != RuntimeComputerNone {
		t.Fatalf("team computer: %+v %v", team, err)
	}
	if _, err := s.AcquireRuntimeComputer(ctx, RuntimeComputerOwner{Kind: RuntimeComputerOwnerTeam}, "x", 0, time.Now()); err == nil {
		t.Fatal("a team computer needs a department")
	}
}
