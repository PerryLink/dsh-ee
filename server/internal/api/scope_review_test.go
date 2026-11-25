package api_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 审批人评审的是申请 A，点通过前负责人换成了更大的申请 B（同样进审核）。
// 用 A 的 scope_requested_at 审批必须 409 且不生效；用审核队列里的当前标识才能通过。
// 不带 scope_requested_at 的旧客户端按契约收紧返回 400。
func TestHubScopeReviewStaleToken(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alice-password-1", "department": "销售部"}, nil)
	expect(t, "建用户", code, 201, body)
	aliceID := decodeAs[struct{ ID int64 }](t, body).ID
	alice := newClient(t, ts.URL)
	code, body = alice.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alice-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)

	// 自己用的应用直接发布
	code, body = alice.do("POST", "/api/hub", map[string]any{"kind": "app", "slug": "toctou-app", "scope": map[string]any{"users": []int64{aliceID}},
		"spec": map[string]any{"title": "TOCTOU 应用", "description": "复现范围审批 TOCTOU", "persona": "你是销售助理。"}}, nil)
	expect(t, "自己用的应用", code, 201, body)
	appID := decodeAs[struct{ Asset store.Asset }](t, body).Asset.ID
	path := fmt.Sprintf("/api/hub/%d", appID)

	// 申请 A：分享给销售部 —— 审核人看到的就是这份
	code, body = alice.do("PATCH", path, map[string]any{"scope": map[string]any{"departments": []string{"销售部"}}, "updated_at": scopeToken(t, alice, path)}, nil)
	expect(t, "申请 A", code, 200, body)
	tokenA := decodeAs[store.Asset](t, body).ScopeRequestedAt

	// 点通过前，负责人换成 B：全员（同样进审核）
	code, body = alice.do("PATCH", path, map[string]any{"scope": map[string]any{"all": true}, "updated_at": scopeToken(t, alice, path)}, nil)
	expect(t, "申请 B", code, 200, body)
	tokenB := decodeAs[store.Asset](t, body).ScopeRequestedAt
	if tokenA == nil || tokenB == nil || tokenA.Equal(*tokenB) {
		t.Fatalf("两次申请的 scope_requested_at 应不同：A=%v B=%v", tokenA, tokenB)
	}

	// 旧客户端不带令牌：400，明确要求刷新
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve"}, nil)
	expect(t, "不带快照标识", code, 400, body)
	if text := string(body); !strings.Contains(text, "scope_requested_at") || !strings.Contains(text, "刷新") {
		t.Fatalf("400 文案应说明要带 scope_requested_at 并刷新：%s", body)
	}

	// 用 A 的标识审批：409，B 不被消费、范围不变
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve", "scope_requested_at": tokenA}, nil)
	expect(t, "旧快照审批", code, 409, body)
	if text := decodeAs[struct{ Error string }](t, body).Error; !strings.Contains(text, "已被更新") || !strings.Contains(text, "刷新") {
		t.Fatalf("409 文案应提示申请已更新、刷新后重审：%s", body)
	}
	det := decodeAs[struct{ Asset store.Asset }](t, mustGet(t, admin, path))
	if det.Asset.Scope.All || det.Asset.PendingScope == nil || !det.Asset.PendingScope.All {
		t.Fatalf("旧标识不能消费新申请：scope=%+v pending=%+v", det.Asset.Scope, det.Asset.PendingScope)
	}
	if det.Asset.ScopeRequestedAt == nil || !det.Asset.ScopeRequestedAt.Equal(*tokenB) {
		t.Fatalf("当前在审申请的标识应还是 B：%+v tokenB=%v", det.Asset.ScopeRequestedAt, tokenB)
	}

	// 用审核队列里当前那份申请的标识：204，B 生效
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve", "scope_requested_at": tokenB}, nil)
	expect(t, "当前快照审批", code, 204, body)
	det = decodeAs[struct{ Asset store.Asset }](t, mustGet(t, admin, path))
	if !det.Asset.Scope.All || det.Asset.PendingScope != nil {
		t.Fatalf("通过后应生效 B 并清空申请：scope=%+v pending=%+v", det.Asset.Scope, det.Asset.PendingScope)
	}

	// 审计取证：review.approve 的 detail 记的是实际生效的 requested_at
	logs, err := st.ListAudit(ctx, store.AuditFilter{Action: "review.approve", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	audited := false
	for _, l := range logs {
		if !strings.Contains(l.Target, "#scope") {
			continue
		}
		raw, _ := l.Detail["requested_at"].(string)
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil || !at.Equal(*tokenB) {
			t.Fatalf("审计 detail.requested_at 应是实际生效的 %v，得到 %q（%v）", tokenB, raw, err)
		}
		audited = true
	}
	if !audited {
		t.Fatalf("范围审批审计里应记录 requested_at：%+v", logs)
	}
}

// 范围申请审核中，负责人又提交了新版本：新版本不能因为「现在还只有自己能用」就免审直接上线，
// 否则审核人看的是旧内容、批的是范围，一通过就把没人审过的新内容推给了全员。
func TestHubNewVersionDuringScopeRequestNeedsReview(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alice-password-1", "department": "销售部"}, nil)
	expect(t, "建用户", code, 201, body)
	aliceID := decodeAs[struct{ ID int64 }](t, body).ID
	alice := newClient(t, ts.URL)
	code, body = alice.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alice-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)

	spec := func(persona string) map[string]any {
		return map[string]any{"title": "销售助理", "description": "帮销售查客户", "persona": persona}
	}
	code, body = alice.do("POST", "/api/hub", map[string]any{"kind": "app", "slug": "sales-helper", "scope": map[string]any{"users": []int64{aliceID}}, "spec": spec("你是销售助理。")}, nil)
	expect(t, "自己用的应用直接发布", code, 201, body)
	appID := decodeAs[struct{ Asset store.Asset }](t, body).Asset.ID
	path := fmt.Sprintf("/api/hub/%d", appID)

	// 申请分享给全员：进审核
	code, body = alice.do("PATCH", path, map[string]any{"scope": map[string]any{"all": true}, "updated_at": scopeToken(t, alice, path)}, nil)
	expect(t, "申请全员", code, 200, body)
	token := decodeAs[store.Asset](t, body).ScopeRequestedAt

	// 审核期间提交新版本：要跟着进审核，不能直接上线
	code, body = alice.do("POST", path+"/versions", map[string]any{"spec": spec("你是销售助理。【未经审核的新内容】")}, nil)
	expect(t, "审核期间提交新版本", code, 201, body)
	if status := decodeAs[struct{ Status string }](t, body).Status; status != "pending" {
		t.Fatalf("范围申请审核中，新版本应进审核而不是 %s", status)
	}

	// 审核人批准范围：全员看到的仍是审过的旧内容
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve", "scope_requested_at": token}, nil)
	expect(t, "批准范围", code, 204, body)
	det := decodeAs[struct {
		Asset struct {
			Scope store.Scope
			Spec  struct{ Persona string }
		}
	}](t, mustGet(t, admin, path))
	if !det.Asset.Scope.All || strings.Contains(det.Asset.Spec.Persona, "未经审核") {
		t.Fatalf("批准范围后线上应仍是审过的内容：scope=%+v persona=%q", det.Asset.Scope, det.Asset.Spec.Persona)
	}
}

// 版本还在审核时负责人撤回了范围申请（范围改回只有自己）：待审版本不会因为
// 「现在只有自己能用」被悄悄发布，也不会卡死——它留在审核队列里，线上仍是审过的旧版本，
// 管理员照常审核通过后才上线。
func TestHubWithdrawScopeRequestKeepsPendingVersion(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alice-password-1", "department": "销售部"}, nil)
	expect(t, "建用户", code, 201, body)
	aliceID := decodeAs[struct{ ID int64 }](t, body).ID
	alice := newClient(t, ts.URL)
	code, body = alice.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alice-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)

	spec := func(persona string) map[string]any {
		return map[string]any{"title": "销售助理", "description": "帮销售查客户", "persona": persona}
	}
	code, body = alice.do("POST", "/api/hub", map[string]any{"kind": "app", "slug": "withdraw-helper", "scope": map[string]any{"users": []int64{aliceID}}, "spec": spec("你是销售助理。")}, nil)
	expect(t, "自己用的应用直接发布", code, 201, body)
	appID := decodeAs[struct{ Asset store.Asset }](t, body).Asset.ID
	path := fmt.Sprintf("/api/hub/%d", appID)

	code, body = alice.do("PATCH", path, map[string]any{"scope": map[string]any{"all": true}, "updated_at": scopeToken(t, alice, path)}, nil)
	expect(t, "申请全员", code, 200, body)
	code, body = alice.do("POST", path+"/versions", map[string]any{"spec": spec("你是销售助理。【待审的新内容】")}, nil)
	expect(t, "审核期间提交新版本", code, 201, body)
	pv := decodeAs[struct {
		Version int
		Status  string
	}](t, body)
	if pv.Status != "pending" {
		t.Fatalf("范围申请审核中，新版本应进审核而不是 %s", pv.Status)
	}

	// 撤回：范围改回只有自己（不需要审核），在审的申请作废
	code, body = alice.do("PATCH", path, map[string]any{"scope": map[string]any{"users": []int64{aliceID}}, "updated_at": scopeToken(t, alice, path)}, nil)
	expect(t, "撤回范围申请", code, 200, body)
	if a := decodeAs[store.Asset](t, body); a.PendingScope != nil {
		t.Fatalf("撤回后不应再有在审的范围申请：%+v", a.PendingScope)
	}

	type detail struct {
		Asset struct {
			Scope store.Scope
			Spec  struct{ Persona string }
		}
	}
	// 待审版本没有被悄悄发布：线上仍是旧内容，版本仍在审核队列里
	det := decodeAs[detail](t, mustGet(t, admin, path))
	if det.Asset.Scope.All || strings.Contains(det.Asset.Spec.Persona, "待审") {
		t.Fatalf("撤回后线上应仍是审过的旧内容、范围只有自己：scope=%+v persona=%q", det.Asset.Scope, det.Asset.Spec.Persona)
	}
	if !strings.Contains(string(mustGet(t, admin, "/api/admin/hub/review")), "withdraw-helper") {
		t.Fatal("撤回范围申请后，待审版本应仍在审核队列里")
	}

	// 也没有卡死：管理员照常审核通过后上线
	code, body = admin.do("POST", "/api/admin/hub/review", map[string]any{"asset_id": appID, "version": pv.Version, "action": "approve"}, nil)
	expect(t, "审核通过待审版本", code, 204, body)
	det = decodeAs[detail](t, mustGet(t, admin, path))
	if !strings.Contains(det.Asset.Spec.Persona, "待审") || det.Asset.Scope.All {
		t.Fatalf("审核通过后线上应是新内容、范围仍只有自己：scope=%+v persona=%q", det.Asset.Scope, det.Asset.Spec.Persona)
	}
}

// 升级前遗留的范围申请：pending_scope 在审但 scope_requested_at 为 NULL。
// 迁移 0064 用 updated_at 回填后，审核队列重新带上这份申请的令牌，管理员可以正常审批一次；
// 回填前和回填后的伪造令牌都仍是 409（快照校验没有放宽）。
func TestHubScopeReviewLegacyBackfill(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "alice-password-1", "department": "销售部"}, nil)
	expect(t, "建用户", code, 201, body)
	aliceID := decodeAs[struct{ ID int64 }](t, body).ID
	alice := newClient(t, ts.URL)
	code, body = alice.do("POST", "/api/auth/login", map[string]string{"username": "alice", "password": "alice-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)

	// 自己用的应用直接发布，再申请分享给销售部（走正常路径，拿到申请时间和旧令牌）
	code, body = alice.do("POST", "/api/hub", map[string]any{"kind": "app", "slug": "legacy-scope-app", "scope": map[string]any{"users": []int64{aliceID}},
		"spec": map[string]any{"title": "遗留范围申请应用", "description": "复现升级前遗留的范围申请", "persona": "你是销售助理。"}}, nil)
	expect(t, "自己用的应用", code, 201, body)
	appID := decodeAs[struct{ Asset store.Asset }](t, body).Asset.ID
	path := fmt.Sprintf("/api/hub/%d", appID)
	code, body = alice.do("PATCH", path, map[string]any{"scope": map[string]any{"departments": []string{"销售部"}}, "updated_at": scopeToken(t, alice, path)}, nil)
	expect(t, "申请分享", code, 200, body)
	oldToken := decodeAs[store.Asset](t, body).ScopeRequestedAt
	if oldToken == nil {
		t.Fatal("正常申请应返回 scope_requested_at")
	}

	// 模拟升级前的数据：申请在审，但快照标识为 NULL（旧版本的写入没有这一列值）
	if _, err := st.DB().ExecContext(ctx, "UPDATE assets SET scope_requested_at = NULL WHERE id = ?", appID); err != nil {
		t.Fatalf("造遗留申请：%v", err)
	}

	// 回填前：队列里没有令牌，带旧时间戳审批是 409（申请消费不了）
	code, body = admin.do("GET", "/api/admin/hub/review", nil, nil)
	expect(t, "读审核队列", code, 200, body)
	type reviewQueue struct {
		Scopes []struct {
			Asset     store.Asset `json:"asset"`
			CreatedAt *time.Time  `json:"created_at"`
		} `json:"scopes"`
	}
	queue := decodeAs[reviewQueue](t, body)
	if len(queue.Scopes) != 1 || queue.Scopes[0].CreatedAt != nil || queue.Scopes[0].Asset.ScopeRequestedAt != nil {
		t.Fatalf("回填前遗留申请不应有快照标识：%+v", queue.Scopes)
	}
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve", "scope_requested_at": oldToken}, nil)
	expect(t, "回填前用旧时间戳审批", code, 409, body)

	// 跑迁移 0064 的回填语句（api 包读不到 store 的 embed 迁移文件；store 侧
	// TestMigrationLegacyScopeBackfillApplies 直接执行迁移文件本身）。
	if _, err := st.DB().ExecContext(ctx,
		"UPDATE assets SET scope_requested_at = updated_at WHERE pending_scope IS NOT NULL AND pending_scope <> '' AND scope_requested_at IS NULL"); err != nil {
		t.Fatalf("回填遗留申请：%v", err)
	}

	// 回填后：队列里的 created_at 就是回填出来的令牌（= updated_at），可以直接用来审批
	code, body = admin.do("GET", "/api/admin/hub/review", nil, nil)
	expect(t, "回填后读审核队列", code, 200, body)
	queue = decodeAs[reviewQueue](t, body)
	if len(queue.Scopes) != 1 || queue.Scopes[0].CreatedAt == nil {
		t.Fatalf("回填后审核队列应带上申请令牌：%+v", queue.Scopes)
	}
	token := queue.Scopes[0].CreatedAt
	if !token.Equal(queue.Scopes[0].Asset.UpdatedAt) {
		t.Fatalf("回填的令牌应是申请写入时间 updated_at：token=%v updated_at=%v", token, queue.Scopes[0].Asset.UpdatedAt)
	}

	// 伪造令牌仍 409：安全语义不变
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve", "scope_requested_at": token.Add(time.Millisecond)}, nil)
	expect(t, "回填后伪造令牌", code, 409, body)
	det := decodeAs[struct{ Asset store.Asset }](t, mustGet(t, admin, path))
	if det.Asset.PendingScope == nil {
		t.Fatal("伪造令牌不能消费申请")
	}

	// 用回填的令牌审批成功：申请被消费，新范围生效
	code, body = admin.do("POST", "/api/admin/hub/scope-review", map[string]any{"asset_id": appID, "action": "approve", "scope_requested_at": *token}, nil)
	expect(t, "用回填的令牌审批", code, 204, body)
	det = decodeAs[struct{ Asset store.Asset }](t, mustGet(t, admin, path))
	if det.Asset.PendingScope != nil || det.Asset.ScopeRequestedAt != nil {
		t.Fatalf("审批后应清空申请：pending=%+v at=%+v", det.Asset.PendingScope, det.Asset.ScopeRequestedAt)
	}
	if len(det.Asset.Scope.Departments) != 1 || det.Asset.Scope.Departments[0] != "销售部" || len(det.Asset.Scope.Users) != 1 || det.Asset.Scope.Users[0] != aliceID {
		t.Fatalf("审批后应生效申请的范围并保留已有用户：%+v", det.Asset.Scope)
	}
}
