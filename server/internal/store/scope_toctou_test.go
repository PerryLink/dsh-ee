package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

// 审批人评审的是申请 A，期间负责人又提交了 B 并覆盖 pending_scope（B 同样进审核）。
// 用看到 A 时记下的 scope_requested_at 审批：必须 ErrConflict，B 不被消费、范围不变；
// 只有用当前那份申请的标识才能通过。这条挡住「批的和看的不一致」的 TOCTOU。
func TestScopeResolveStaleRequestConflict(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	a := scopeTestAsset(t, st, "stale-scope", Scope{Users: []int64{1}})

	// 审批人在队列里看到的申请 A：分享给用户 1、2
	seenAt, err := st.RequestAssetScope(ctx, a.ID, Scope{Users: []int64{1, 2}}, nil)
	if err != nil {
		t.Fatalf("申请 A：%v", err)
	}
	seen, err := st.GetAsset(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if seen.ScopeRequestedAt == nil || !seen.ScopeRequestedAt.Equal(seenAt) {
		t.Fatalf("申请后应有快照标识 scope_requested_at：%+v", seen.ScopeRequestedAt)
	}

	// 管理员点通过前，负责人换成 B：全员
	sleepTurn()
	newAt, err := st.RequestAssetScope(ctx, a.ID, Scope{All: true}, nil)
	if err != nil {
		t.Fatalf("申请 B：%v", err)
	}
	if seenAt.Equal(newAt) {
		t.Fatalf("换个申请后 scope_requested_at 应更新：%v", seenAt)
	}

	// 拿旧快照审批：冲突，不消费 B，范围不变
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", seenAt); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("用旧快照审批应返回 ErrConflict，得到 ok=%v err=%v", ok, err)
	}
	cur, err := st.GetAsset(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !cur.Scope.OnlyUser(1) || cur.PendingScope == nil || !cur.PendingScope.All {
		t.Fatalf("冲突时范围和申请都不应变：scope=%+v pending=%+v", cur.Scope, cur.PendingScope)
	}
	if cur.ScopeRequestedAt == nil || !cur.ScopeRequestedAt.Equal(newAt) {
		t.Fatalf("冲突后当前申请的快照标识不应变：%+v", cur.ScopeRequestedAt)
	}

	// 旧快照驳回同样不能消费新申请
	if ok, err := st.ResolveAssetScope(ctx, a.ID, false, "先别放开", seenAt); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("用旧快照驳回应返回 ErrConflict，得到 ok=%v err=%v", ok, err)
	}
	cur, _ = st.GetAsset(ctx, a.ID)
	if cur.PendingScope == nil || cur.ScopeNote != "" {
		t.Fatalf("驳回冲突不应改动申请：pending=%+v note=%q", cur.PendingScope, cur.ScopeNote)
	}

	// 令牌缺失（零值）不能绕过校验
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", time.Time{}); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("缺令牌应返回 ErrConflict，得到 ok=%v err=%v", ok, err)
	}

	// 用当前那份申请的标识才通过，生效的是 B
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", newAt); err != nil || !ok {
		t.Fatalf("用当前快照审批：%v %v", ok, err)
	}
	cur, _ = st.GetAsset(ctx, a.ID)
	if !cur.Scope.All || cur.PendingScope != nil || cur.ScopeRequestedAt != nil {
		t.Fatalf("通过后应生效 B 并清空申请：scope=%+v pending=%+v at=%+v", cur.Scope, cur.PendingScope, cur.ScopeRequestedAt)
	}

	// 已经处理过：没有在审申请，返回 false 而不是冲突
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", newAt); err != nil || ok {
		t.Fatalf("没有在审申请：%v %v", ok, err)
	}
}

// 升级前遗留的范围申请没有 scope_requested_at：回填前带任何令牌都不能消费它（安全语义不变，
// 不是「没有标识就放宽」）；迁移用 updated_at 回填后，申请人当时那份申请的标识才能审批一次。
// 回填后的令牌仍按快照严格校验：差 1 毫秒或零值都冲突。
func TestScopeResolveLegacyRequestAfterBackfill(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	a := scopeTestAsset(t, st, "legacy-scope-approve", Scope{Users: []int64{1}})
	requestedAt := time.Date(2026, 11, 1, 9, 30, 0, 500_000_000, time.UTC)
	setLegacyScopeRequest(t, st, a.ID, `{"departments":["销售部"]}`, requestedAt)

	// 回填前：库里没有标识，任何令牌都冲突（不能借此绕过快照校验）
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", requestedAt); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("回填前应冲突，得到 ok=%v err=%v", ok, err)
	}

	replayMigration(t, st, legacyScopeBackfillMigration)
	cur, err := st.GetAsset(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.ScopeRequestedAt == nil || !cur.ScopeRequestedAt.Equal(requestedAt) {
		t.Fatalf("回填后应能用 updated_at 作为快照标识：got %v want %v", cur.ScopeRequestedAt, requestedAt)
	}

	// 伪造 / 旧令牌仍冲突：差 1 毫秒和零值都不能通过
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", cur.ScopeRequestedAt.Add(time.Millisecond)); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("回填后伪造令牌应冲突，得到 ok=%v err=%v", ok, err)
	}
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", time.Time{}); !errors.Is(err, ErrConflict) || ok {
		t.Fatalf("零值令牌应冲突，得到 ok=%v err=%v", ok, err)
	}

	// 用回填的令牌审批：成功，申请被消费、新范围生效
	if ok, err := st.ResolveAssetScope(ctx, a.ID, true, "", *cur.ScopeRequestedAt); err != nil || !ok {
		t.Fatalf("回填的令牌应能审批：%v %v", ok, err)
	}
	after, err := st.GetAsset(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PendingScope != nil || after.ScopeRequestedAt != nil {
		t.Fatalf("审批后应清空申请：pending=%+v at=%+v", after.PendingScope, after.ScopeRequestedAt)
	}
	if len(after.Scope.Departments) != 1 || after.Scope.Departments[0] != "销售部" {
		t.Fatalf("审批后新范围应生效：%+v", after.Scope)
	}
	// 申请提交后当前范围里已有的用户不能被旧快照覆盖掉（和正常审批同一套合并逻辑）
	if len(after.Scope.Users) != 1 || after.Scope.Users[0] != 1 {
		t.Fatalf("审批后应保留当前范围里已有的用户：%+v", after.Scope.Users)
	}
}
