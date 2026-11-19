package store

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// 新版本要不要审核取决于范围申请：调用方在锁外读到的资产是旧快照（还没有申请），
// AddAssetVersionWith 必须在资产行锁内重新读范围再交给 decide。
func TestAddAssetVersionDecidesUnderLock(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	decide := func(scope Scope, pending *Scope) (string, error) {
		if pending != nil {
			scope = *pending
		}
		if scope.OnlyUser(1) {
			return StatusPublished, nil
		}
		return StatusPending, nil
	}

	// 锁外快照里没有申请；之后申请全员落库，再提交新版本：锁内要看到申请
	a := scopeTestAsset(t, st, "version-scope-race", Scope{Users: []int64{1}})
	stale, err := st.GetAsset(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stale.PendingScope != nil {
		t.Fatalf("前置：不应有申请：%+v", stale.PendingScope)
	}
	if _, err := st.RequestAssetScope(ctx, a.ID, Scope{All: true}, nil); err != nil {
		t.Fatal(err)
	}
	v, status, err := st.AddAssetVersionWith(ctx, a.ID, json.RawMessage(`{"title":"v2","persona":"p2"}`), 0, decide)
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusPending {
		t.Fatalf("锁内应读到在审的全员申请，新版本应进审核，得到 %s", status)
	}
	if got, err := st.GetAssetVersion(ctx, a.ID, v); err != nil || got.Status != StatusPending {
		t.Fatalf("落库的版本状态应是 pending：%+v %v", got, err)
	}

	// 并发：另一事务锁住资产行、写入申请后才提交；新版本的判断必须等锁，看到提交后的申请
	b := scopeTestAsset(t, st, "version-scope-race-2", Scope{Users: []int64{1}})
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "SELECT id FROM assets WHERE id = ? FOR UPDATE", b.ID); err != nil {
		t.Fatal(err)
	}
	text, err := scopeText(Scope{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE assets SET pending_scope = ?, scope_requested_at = ? WHERE id = ?", text, now(), b.ID); err != nil {
		t.Fatal(err)
	}
	type result struct {
		status string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		_, s, err := st.AddAssetVersionWith(ctx, b.ID, json.RawMessage(`{"title":"v2","persona":"p2"}`), 0, decide)
		done <- result{s, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("资产行被锁住时不应完成提交：%+v", r)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.status != StatusPending {
			t.Fatalf("等到锁后应读到刚提交的申请，新版本应进审核，得到 %s", r.status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("提交新版本一直没有完成")
	}
}
