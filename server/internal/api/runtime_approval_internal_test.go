package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 三种审批模式（外加只读）对一次写操作的判定：纯逻辑，不连数据库。
func TestDecideRuntimeWrite(t *testing.T) {
	rules := []string{"wiki__create_*", "*__comment"}
	for _, tc := range []struct {
		mode, tool string
		want       runtimeWriteAction
		via, rule  string
	}{
		{store.RuntimeApprovalReadOnly, "wiki__create_page", runtimeWriteBlock, "", ""},
		{"", "wiki__create_page", runtimeWriteBlock, "", ""},
		{"bogus", "wiki__create_page", runtimeWriteBlock, "", ""},
		{store.RuntimeApprovalManual, "wiki__create_page", runtimeWriteAsk, "", ""},
		{store.RuntimeApprovalAuto, "wiki__create_page", runtimeWriteAllow, "rule", "wiki__create_*"},
		{store.RuntimeApprovalAuto, "crm__comment", runtimeWriteAllow, "rule", "*__comment"},
		{store.RuntimeApprovalAuto, "wiki__delete_page", runtimeWriteAsk, "", ""},
		{store.RuntimeApprovalAuto, "crm__comment_delete", runtimeWriteAsk, "", ""},
		{store.RuntimeApprovalFull, "wiki__delete_page", runtimeWriteAllow, "full", ""},
	} {
		got := decideRuntimeWrite(tc.mode, rules, tc.tool)
		if got.Action != tc.want || got.Via != tc.via || got.Rule != tc.rule {
			t.Errorf("%s / %s: got %+v, want action %d via %q rule %q", tc.mode, tc.tool, got, tc.want, tc.via, tc.rule)
		}
	}
	// 自动审批没有规则时等于逐个审批
	if got := decideRuntimeWrite(store.RuntimeApprovalAuto, nil, "wiki__create_page"); got.Action != runtimeWriteAsk {
		t.Fatalf("auto without rules: %+v", got)
	}
}

func TestNormalizeRuntimeApprovalRules(t *testing.T) {
	got, ok := normalizeRuntimeApprovalRules([]string{" wiki__create_page ", "", "wiki__create_page", "*__comment"})
	if !ok || len(got) != 2 || got[0] != "wiki__create_page" || got[1] != "*__comment" {
		t.Fatalf("normalize: %v %v", got, ok)
	}
	// 单独的通配等于完全放行，必须走带风险确认的完全放行模式
	for _, bad := range []string{"*", "**", "*__*", "a/b", "wiki create", "wiki__[", strings.Repeat("a", runtimeApprovalMaxRuleLen+1)} {
		if _, ok := normalizeRuntimeApprovalRules([]string{bad}); ok {
			t.Errorf("rule %q accepted", bad)
		}
	}
	many := make([]string, runtimeApprovalMaxRules+1)
	for i := range many {
		many[i] = fmt.Sprintf("wiki__tool_%d", i)
	}
	if _, ok := normalizeRuntimeApprovalRules(many); ok {
		t.Fatal("too many rules accepted")
	}
	if got, ok := normalizeRuntimeApprovalRules(nil); !ok || got == nil || len(got) != 0 {
		t.Fatalf("empty rules: %v %v", got, ok)
	}
}

func TestRuntimeWriteGuidance(t *testing.T) {
	if runtimeWriteGuidance(store.RuntimeApprovalReadOnly) != "This turn is read-only. " || runtimeWriteGuidance("") != "This turn is read-only. " {
		t.Fatal("read-only guidance changed")
	}
	for _, mode := range []string{store.RuntimeApprovalManual, store.RuntimeApprovalAuto} {
		if g := runtimeWriteGuidance(mode); !strings.Contains(g, "approves") || !strings.Contains(g, "do not retry") {
			t.Errorf("%s guidance: %q", mode, g)
		}
	}
	if g := runtimeWriteGuidance(store.RuntimeApprovalFull); !strings.Contains(g, "without per-action approval") {
		t.Errorf("full guidance: %q", g)
	}
}

// 云任务的写操作：只读拦下、完全放行、规则自动批准、逐个审批（批准 / 拒绝 / 等不到），每次都留审计；
// 以及管理员与员工设置审批模式时的风险确认和「是否允许员工修改」。
func TestRuntimeApprovalModes(t *testing.T) {
	old := runtimeApprovalPoll
	runtimeApprovalPoll = 20 * time.Millisecond
	t.Cleanup(func() { runtimeApprovalPoll = old })

	s, employee, admin := newRuntimeBotTest(t)
	ctx := context.Background()
	owner := runtimeTestClient(t, s, employee.ID)

	botBody := func(mode string, rules []string, editable, confirm bool) string {
		body, _ := json.Marshal(map[string]any{
			"owner_id": employee.ID, "run_as_id": employee.ID, "name": "Writer", "prompt": "Help", "schedule": "0 9 * * *",
			"enabled": false, "web_chat_enabled": true, "approval_mode": mode, "approval_rules": rules,
			"approval_employee_editable": editable, "confirm_full_access": confirm,
		})
		return string(body)
	}
	expectCode := func(what string, rec *httptest.ResponseRecorder, status int, code string) {
		t.Helper()
		var e struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &e)
		if rec.Code != status || e.Code != code {
			t.Fatalf("%s: HTTP %d %s, want %d %s", what, rec.Code, rec.Body.String(), status, code)
		}
	}

	// 管理员：非法模式、非法规则、未确认风险就开完全放行，都存不进去。
	expectCode("invalid mode", admin("POST", runtimeBotsTestPath, botBody("yolo", nil, false, false)), http.StatusBadRequest, codeRuntimeApprovalInvalidMode)
	expectCode("wildcard rule", admin("POST", runtimeBotsTestPath, botBody(store.RuntimeApprovalAuto, []string{"*"}, false, false)), http.StatusBadRequest, codeRuntimeApprovalInvalidRules)
	expectCode("full unconfirmed", admin("POST", runtimeBotsTestPath, botBody(store.RuntimeApprovalFull, nil, false, false)), http.StatusBadRequest, codeRuntimeApprovalRiskUnconfirmed)
	rec := admin("POST", runtimeBotsTestPath, botBody(store.RuntimeApprovalManual, []string{"wiki__create_*"}, false, false))
	var bot store.RuntimeBot
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &bot) != nil || bot.ApprovalMode != store.RuntimeApprovalManual || bot.ReadOnly ||
		len(bot.ApprovalRules) != 1 || !bot.WebChatEnabled {
		t.Fatalf("create manual bot: HTTP %d %s", rec.Code, rec.Body.String())
	}
	botPath := runtimeBotsTestPath + "/" + strconv.FormatInt(bot.ID, 10)
	setMode := func(mode string) {
		t.Helper()
		if rec := admin("PUT", botPath, botBody(mode, []string{"wiki__create_*"}, false, true)); rec.Code != http.StatusOK {
			t.Fatalf("set mode %s: HTTP %d %s", mode, rec.Code, rec.Body.String())
		}
	}

	// 一轮正在运行的云任务和它的短期凭证（与 worker 走同一条路径）
	if _, err := s.enqueueCloudBot(ctx, &bot, ""); err != nil {
		t.Fatal(err)
	}
	task, err := s.store.ClaimRuntimeTask(ctx, runtimeAttempts, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	token, revoke, err := s.cloudCredential(ctx, task)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(revoke)
	routeCtx := context.WithValue(ctx, runtimeTokenRouteKey{}, true)
	pr, err := s.resolveBearer(routeCtx, token, "")
	if err != nil || !pr.Runtime {
		t.Fatalf("runtime principal: %v", err)
	}
	v := viewer{user: pr.User, access: pr.Access, tokenID: pr.TokenID, clientKind: pr.ClientKind}
	asset := &store.Asset{ID: 424242, Slug: "wiki", Title: "Wiki"}
	create := &store.ConnectionTool{Name: "create_page"}
	remove := &store.ConnectionTool{Name: "delete_page"}
	args := json.RawMessage(`{"title":"周报"}`)
	call := func(c context.Context, tool *store.ConnectionTool) string {
		r := httptest.NewRequest("POST", "/mcp", nil).WithContext(context.WithValue(c, runtimeTokenRouteKey{}, true))
		r.Header.Set("Authorization", "Bearer "+token)
		return string(s.runtimeWrite(r, v, pr, asset, tool, args))
	}
	calls := func() int {
		var n int
		if err := s.store.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM connection_calls WHERE asset_id = ?", asset.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	seen := 0
	// lastAudit 取最新一条写操作审计（runtime.write / runtime.write.blocked），并检查共同字段。
	lastAudit := func(action string) map[string]any {
		t.Helper()
		list, err := s.store.ListAudit(ctx, store.AuditFilter{Action: action, Limit: 1})
		if err != nil || len(list) == 0 {
			t.Fatalf("no %s audit: %v", action, err)
		}
		a := list[0]
		if a.ID <= int64(seen) {
			t.Fatalf("no new %s audit (latest %d)", action, a.ID)
		}
		seen = int(a.ID)
		d := a.Detail
		if a.ActorID == nil || *a.ActorID != employee.ID || a.Target != "connection:wiki" || d["task_id"] != float64(task.ID) ||
			d["bot_id"] != float64(bot.ID) || d["args_sha256"] == "" || a.Via != "token" {
			t.Fatalf("audit fields: %+v", a)
		}
		return d
	}
	pending := func() *store.WriteConfirmation {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			list, err := s.store.PendingWriteConfirmations(ctx, employee.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range list {
				if c.RuntimeTaskID == task.ID {
					return c
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("approval card never appeared")
		return nil
	}

	// 只读：写工具不下发，调用拦下且不执行，留一条 blocked 审计
	setMode(store.RuntimeApprovalReadOnly)
	if s.runtimeWritesListed(ctx, pr) {
		t.Fatal("read-only bot lists write tools")
	}
	if out := call(ctx, create); !strings.Contains(out, runtimeReadOnlyRejection) || calls() != 0 {
		t.Fatalf("read-only write: %s (calls %d)", out, calls())
	}
	if d := lastAudit(auditRuntimeWriteBlocked); d["mode"] != store.RuntimeApprovalReadOnly || d["reason"] != "read_only" {
		t.Fatalf("read-only audit: %v", d)
	}

	// 完全放行：直接执行，审计记模式和 approved_by=full
	setMode(store.RuntimeApprovalFull)
	if !s.runtimeWritesListed(ctx, pr) {
		t.Fatal("full-access bot does not list write tools")
	}
	call(ctx, remove)
	if calls() != 1 {
		t.Fatalf("full access did not execute (calls %d)", calls())
	}
	if d := lastAudit(auditRuntimeWrite); d["mode"] != store.RuntimeApprovalFull || d["approved_by"] != "full" || d["tool"] != "wiki__delete_page" || d["outcome"] == nil {
		t.Fatalf("full audit: %v", d)
	}

	// 自动审批：命中规则的直接执行并记下规则；没命中的出卡，员工拒绝后不执行
	setMode(store.RuntimeApprovalAuto)
	call(ctx, create)
	if calls() != 2 {
		t.Fatalf("auto rule did not execute (calls %d)", calls())
	}
	if d := lastAudit(auditRuntimeWrite); d["mode"] != store.RuntimeApprovalAuto || d["approved_by"] != "rule" || d["rule"] != "wiki__create_*" {
		t.Fatalf("auto audit: %v", d)
	}
	done := make(chan string, 1)
	go func() { done <- call(ctx, remove) }()
	card := pending()
	if card.Tool != "delete_page" || card.Args != `{"title":"周报"}` {
		t.Fatalf("approval card: %+v", card)
	}
	// 卡片在员工的待确认列表里带着任务 ID，对话页据此显示
	if rec := owner("GET", "/api/me/confirmations", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"runtime_task_id":%d`, task.ID)) {
		t.Fatalf("my confirmations: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner("POST", fmt.Sprintf("/api/me/confirmations/%d", card.ID), `{"approve":false}`); rec.Code != http.StatusOK {
		t.Fatalf("reject: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if out := <-done; !strings.Contains(out, "rejected") || calls() != 2 {
		t.Fatalf("rejected write: %s (calls %d)", out, calls())
	}
	if d := lastAudit(auditRuntimeWriteBlocked); d["mode"] != store.RuntimeApprovalAuto || d["reason"] != "rejected" || d["confirmation_id"] != float64(card.ID) {
		t.Fatalf("rejection audit: %v", d)
	}

	// 逐个审批：规则不再生效，出卡；员工批准后执行，审计记批准人
	setMode(store.RuntimeApprovalManual)
	go func() { done <- call(ctx, create) }()
	card = pending()
	if rec := owner("POST", fmt.Sprintf("/api/me/confirmations/%d", card.ID), `{"approve":true}`); rec.Code != http.StatusOK {
		t.Fatalf("approve: HTTP %d %s", rec.Code, rec.Body.String())
	}
	<-done
	if calls() != 3 {
		t.Fatalf("approved write did not execute (calls %d)", calls())
	}
	if d := lastAudit(auditRuntimeWrite); d["mode"] != store.RuntimeApprovalManual || d["approved_by"] != "user" ||
		d["approver_id"] != float64(employee.ID) || d["confirmation_id"] != float64(card.ID) {
		t.Fatalf("manual audit: %v", d)
	}
	if list, err := s.store.ListAudit(ctx, store.AuditFilter{Action: auditWriteConfirm, Limit: 5}); err != nil || len(list) != 2 {
		t.Fatalf("write.confirm decisions: %d %v", len(list), err)
	}

	// 逐个审批但等不到（这一轮被取消 / 请求断开）：不执行，卡片立即过期，之后再批也没用
	waitCtx, cancel := context.WithCancel(ctx)
	go func() { done <- call(waitCtx, create) }()
	card = pending()
	cancel()
	if out := <-done; !strings.Contains(out, "did not approve") || calls() != 3 {
		t.Fatalf("unanswered write: %s (calls %d)", out, calls())
	}
	if d := lastAudit(auditRuntimeWriteBlocked); d["reason"] != "not_approved_in_time" {
		t.Fatalf("timeout audit: %v", d)
	}
	if rec := owner("POST", fmt.Sprintf("/api/me/confirmations/%d", card.ID), `{"approve":true}`); rec.Code != http.StatusConflict {
		t.Fatalf("late approval: HTTP %d %s", rec.Code, rec.Body.String())
	}

	// 任务结束后凭证对应的任务不在运行：即使是完全放行也一律拦下
	setMode(store.RuntimeApprovalFull)
	if err := s.store.FinishRuntimeTask(ctx, task, store.RuntimeSucceeded, "", "", nil); err != nil {
		t.Fatal(err)
	}
	if out := call(ctx, create); !strings.Contains(out, runtimeReadOnlyRejection) || calls() != 3 {
		t.Fatalf("write after task finished: %s (calls %d)", out, calls())
	}

	// 员工在对话页切换：管理员没允许时 403；允许后切完全放行要确认风险；切换留审计
	approval := "/api/runtime/chat/bots/" + strconv.FormatInt(bot.ID, 10) + "/approval"
	expectCode("locked", owner("PUT", approval, `{"approval_mode":"manual"}`), http.StatusForbidden, codeRuntimeApprovalLocked)
	if rec := admin("PUT", botPath, botBody(store.RuntimeApprovalManual, nil, true, false)); rec.Code != http.StatusOK {
		t.Fatalf("allow employee: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := owner("GET", "/api/runtime/chat/bots", ""); !strings.Contains(rec.Body.String(), `"approval_mode":"manual","approval_editable":true`) {
		t.Fatalf("chat bot list: %s", rec.Body.String())
	}
	expectCode("bad mode", owner("PUT", approval, `{"approval_mode":"yolo"}`), http.StatusBadRequest, codeRuntimeApprovalInvalidMode)
	expectCode("employee full unconfirmed", owner("PUT", approval, `{"approval_mode":"full"}`), http.StatusBadRequest, codeRuntimeApprovalRiskUnconfirmed)
	if rec := owner("PUT", approval, `{"approval_mode":"full","confirm_full_access":true}`); rec.Code != http.StatusOK {
		t.Fatalf("employee full: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got, _ := s.store.RuntimeBot(ctx, bot.ID); got.ApprovalMode != store.RuntimeApprovalFull || got.ReadOnly {
		t.Fatalf("employee change not stored: %+v", got)
	}
	// 同一模式再设一次是幂等的
	if rec := owner("PUT", approval, `{"approval_mode":"full"}`); rec.Code != http.StatusOK {
		t.Fatalf("repeat full: HTTP %d %s", rec.Code, rec.Body.String())
	}
	list, err := s.store.ListAudit(ctx, store.AuditFilter{Action: auditRuntimeBotApproval, Limit: 1})
	if err != nil || len(list) == 0 || list[0].Detail["by"] != "owner" || list[0].Detail["risk_confirmed"] != true {
		t.Fatalf("employee approval audit: %+v %v", list, err)
	}
	// 不是本人的机器人（管理员也不行）：一律 404
	expectCode("admin as owner", admin("PUT", approval, `{"approval_mode":"manual"}`), http.StatusNotFound, codeRuntimeChatBotUnavailable)
}
