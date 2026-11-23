package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 一个员工一台云电脑 + 机器人之间派任务（runtime_computer.go、runtime_delegation.go）。

func (c *computerTest) employeeUser(name string) int64 {
	c.t.Helper()
	ctx := context.Background()
	role, err := c.s.store.GetRoleByCode(ctx, rbac.RoleMember)
	if err != nil {
		c.t.Fatal(err)
	}
	u := &store.User{Username: name, DisplayName: name}
	if err := c.s.store.CreateUser(ctx, u); err != nil {
		c.t.Fatal(err)
	}
	if err := c.s.store.SetUserRoles(ctx, u.ID, []int64{role.ID}); err != nil {
		c.t.Fatal(err)
	}
	return u.ID
}

func (c *computerTest) chatBot(owner int64, name string) *store.RuntimeBot {
	c.t.Helper()
	on := true
	b := &store.RuntimeBot{OwnerID: owner, RunAsID: owner, Name: name, Prompt: name + " instructions", Schedule: "0 9 * * *", ReadOnly: true, DeliveryTarget: runtimeFeishuOwner}
	if err := c.s.store.SaveRuntimeBot(context.Background(), b, &on); err != nil {
		c.t.Fatal(err)
	}
	return b
}

// runBot 让任意机器人以 runAs 的身份跑一轮手动任务（团队对话的轮次以发消息的人运行，机器人属于别人）。
func (c *computerTest) runBot(bot *store.RuntimeBot, runAs int64, prompt string) *store.RuntimeTask {
	c.t.Helper()
	ctx := context.Background()
	task, err := c.s.store.EnqueueRuntimeTask(ctx, store.RuntimeTask{OwnerID: runAs, RunAsID: runAs, BotID: &bot.ID, Prompt: prompt, TimeoutSeconds: 300})
	if err != nil {
		c.t.Fatal(err)
	}
	c.drain()
	got, err := c.s.store.RuntimeTask(ctx, task.ID, runAs)
	if err != nil {
		c.t.Fatal(err)
	}
	return got
}

// drain 把队列里的任务（包括派出去的、结果追加的）一个一个跑完。
func (c *computerTest) drain() {
	c.t.Helper()
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		task, err := c.s.store.ClaimRuntimeTask(ctx, runtimeAttempts, 45*time.Second)
		if notFound(err) {
			return
		}
		if err != nil {
			c.t.Fatal(err)
		}
		c.s.runCloudClaim(ctx, task)
	}
	c.t.Fatal("queue did not drain")
}

// say 员工本人在机器人的线程里发一条消息，返回线程 ID。
func (c *computerTest) say(client func(string, string, string) *httptest.ResponseRecorder, bot *store.RuntimeBot, text string) int64 {
	c.t.Helper()
	rec := client("POST", "/api/runtime/chat/conversations", `{"bot_id":`+strconv.FormatInt(bot.ID, 10)+`}`)
	var conv struct {
		ID int64 `json:"id"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &conv) != nil {
		c.t.Fatalf("open conversation: HTTP %d %s", rec.Code, rec.Body.String())
	}
	body, _ := json.Marshal(map[string]string{"content": text, "idempotency_key": strconv.FormatInt(time.Now().UnixNano(), 36)})
	if rec := client("POST", "/api/runtime/chat/conversations/"+strconv.FormatInt(conv.ID, 10)+"/messages", string(body)); rec.Code != http.StatusAccepted {
		c.t.Fatalf("send: HTTP %d %s", rec.Code, rec.Body.String())
	}
	return conv.ID
}

type mcpResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError bool           `json:"isError"`
	Meta    map[string]any `json:"_meta"`
}

func (r mcpResult) text() string {
	if len(r.Content) == 0 {
		return ""
	}
	return r.Content[0].Text
}

// mcp 用一轮的短期令牌调 /mcp（经过完整的路由和中间件）。
func (c *computerTest) mcp(token, method string, params any) json.RawMessage {
	c.t.Helper()
	body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c.s.Handler().ServeHTTP(rec, r)
	var out struct {
		Result json.RawMessage `json:"result"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		c.t.Errorf("mcp %s: HTTP %d %s", method, rec.Code, rec.Body.String())
	}
	return out.Result
}

func (c *computerTest) tool(token, name string, args any) mcpResult {
	c.t.Helper()
	var out mcpResult
	if err := json.Unmarshal(c.mcp(token, "tools/call", map[string]any{"name": name, "arguments": args}), &out); err != nil {
		c.t.Errorf("tool %s: %v", name, err)
	}
	return out
}

func (c *computerTest) audits(action string) []*store.AuditLog {
	c.t.Helper()
	list, err := c.s.store.ListAudit(context.Background(), store.AuditFilter{Action: action, Limit: 100})
	if err != nil {
		c.t.Fatal(err)
	}
	return list
}

func botDir(id int64) string { return runtimeBotWorkDir(id) }

// 同一员工的两个机器人共用一台电脑：A 写在共享目录里的文件 B 能读到；各自的工作目录和 DSH 主目录分开。
func TestOwnerComputerSharedByBots(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 20})
	second := c.chatBot(c.employee, "Organizer")

	if got := c.runBot(c.bot, c.employee, "SHARE:Q3 目标 42"); got.Status != store.RuntimeSucceeded || got.Summary != "shared" {
		t.Fatalf("bot A: %+v", got)
	}
	if got := c.runBot(second, c.employee, "READ_SHARED"); got.Status != store.RuntimeSucceeded || got.Summary != "shared note: Q3 目标 42" {
		t.Fatalf("bot B should read bot A's shared file on the same computer: %+v", got)
	}
	// 各自目录里的文件互不可见。
	if got := c.runBot(c.bot, c.employee, "WRITE:A 的私人笔记"); got.Summary != "saved" {
		t.Fatalf("bot A write: %+v", got)
	}
	if got := c.runBot(second, c.employee, "READ"); got.Summary != "no note" {
		t.Fatalf("bot B must not see bot A's own folder as its working directory: %+v", got)
	}
	if f.creates != 1 || len(f.boxes) != 1 {
		t.Fatalf("one employee must have one computer: creates=%d boxes=%d", f.creates, len(f.boxes))
	}
	want := map[string]bool{
		botDir(c.bot.ID) + "|" + runtimeBotHomeDir(c.bot.ID):   true,
		botDir(second.ID) + "|" + runtimeBotHomeDir(second.ID): true,
	}
	for _, d := range f.dirs {
		if !want[d] {
			t.Fatalf("unexpected working directory / DSH home %q", d)
		}
	}
	for _, token := range f.tokens {
		if c.tokenValid(token) {
			t.Fatal("credential still valid after its run")
		}
	}
	if v := c.profile(); v.OwnerKind != store.RuntimeComputerOwnerUser || v.SharedBots != 2 || v.State != store.RuntimeComputerRunning {
		t.Fatalf("profile should show 我的电脑 shared by both bots: %+v", v)
	}
	// 「我的电脑」的手动操作作用在这台共用的电脑上。
	if rec := c.owner("POST", "/api/runtime/chat/bots/"+strconv.FormatInt(second.ID, 10)+"/computer/hibernate", ""); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"state":"hibernated"`) || f.hibernates != 1 {
		t.Fatalf("hibernate my computer: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if got := c.runBot(c.bot, c.employee, "READ_SHARED"); got.Summary != "shared note: Q3 目标 42" || f.wakes != 1 || f.creates != 1 {
		t.Fatalf("wake after hibernate: %+v wakes=%d", got, f.wakes)
	}
}

// 团队对话的轮次用团队电脑：不落在机器人所有者的个人电脑上，成员之间能看到团队电脑里的文件；
// 每轮以发消息的成员运行，电脑排队独占，上一位成员的令牌在下一位成员的轮次开始前就已吊销。
func TestTeamBotUsesTeamComputer(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 20})
	team := c.chatBot(c.employee, "Team helper")
	alice, bob := c.employeeUser("alice"), c.employeeUser("bob")
	c.s.teamComputer = func(_ context.Context, task *store.RuntimeTask) (string, int64, error) {
		if task.BotID != nil && *task.BotID == team.ID && task.RunAsID != c.employee {
			return "研发部", team.ID, nil
		}
		return "", 0, nil
	}
	var seen []string
	f.check = func(token string) {
		if !c.tokenValid(token) {
			t.Error("the running member's credential must be valid")
		}
		for _, old := range seen {
			if c.tokenValid(old) {
				t.Error("an earlier member's credential is still valid on the shared team computer")
			}
		}
		seen = append(seen, token)
	}

	if got := c.run("SHARE:owner private"); got.Status != store.RuntimeSucceeded {
		t.Fatalf("owner personal run: %+v", got)
	}
	if got := c.runBot(team, alice, "SHARE:团队周报草稿"); got.Status != store.RuntimeSucceeded {
		t.Fatalf("alice team run: %+v", got)
	}
	if got := c.runBot(team, bob, "READ_SHARED"); got.Summary != "shared note: 团队周报草稿" {
		t.Fatalf("bob should see the team computer's files: %+v", got)
	}
	if got := c.run("READ_SHARED"); got.Summary != "shared note: owner private" {
		t.Fatalf("the owner's personal computer must be separate from the team computer: %+v", got)
	}
	if f.creates != 2 {
		t.Fatalf("want one personal and one team computer, creates=%d", f.creates)
	}
	ctx := context.Background()
	teamComputer, err := c.s.store.RuntimeComputerOf(ctx, store.TeamComputerOwner("研发部"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.store.RuntimeComputerOf(ctx, store.UserComputerOwner(alice)); !notFound(err) {
		t.Fatalf("a team turn must not create a computer for the member: %v", err)
	}
	// 每轮的令牌签给发消息的成员本人。
	for _, token := range f.tokens[1:3] {
		p, err := c.s.store.AccessTokenByHash(ctx, secret.Hash(token))
		if err != nil || (p.UserID != alice && p.UserID != bob) {
			t.Fatalf("team turn credential must be issued to the sender: %+v %v", p, err)
		}
	}
	rec := c.admin("GET", "/api/admin/runtime/computers", "")
	var list struct {
		Computers []runtimeAdminComputer `json:"computers"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &list) != nil || len(list.Computers) != 2 {
		t.Fatalf("admin list: HTTP %d %s", rec.Code, rec.Body.String())
	}
	foundTeam := false
	for _, it := range list.Computers {
		if it.ID == teamComputer.ID {
			foundTeam = it.OwnerKind == store.RuntimeComputerOwnerTeam && it.Department == "研发部" && it.OwnerID == 0
		}
	}
	if !foundTeam {
		t.Fatalf("admin list should show the team computer: %s", rec.Body.String())
	}
	// 团队对话的轮次不能派任务（团队共享合进来前不开放）。
	f.onRun = func(token, _, prompt string) (string, bool) {
		if !strings.Contains(prompt, "TEAM_DELEGATE") {
			return "", false
		}
		return c.tool(token, "bots__send", map[string]any{"bot_id": c.bot.ID, "content": "x"}).text(), true
	}
	if got := c.runBot(team, alice, "TEAM_DELEGATE"); !strings.Contains(got.Summary, "Team conversations cannot delegate") {
		t.Fatalf("team turn delegation: %+v", got)
	}
}

// 跨员工派任务被拒：看不到、也派不了别人的机器人，并留下审计。
func TestDelegationAcrossEmployeesRejected(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 20})
	mine := c.chatBot(c.employee, "Organizer")
	other := c.employeeUser("someone-else")
	theirs := c.chatBot(other, "Their bot")
	var listed, sent mcpResult
	var toolNames []string
	f.onRun = func(token, _, _ string) (string, bool) {
		var tools struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		_ = json.Unmarshal(c.mcp(token, "tools/list", map[string]any{}), &tools)
		for _, tl := range tools.Tools {
			toolNames = append(toolNames, tl.Name)
		}
		listed = c.tool(token, "bots__list", map[string]any{})
		sent = c.tool(token, "bots__send", map[string]any{"bot_id": theirs.ID, "content": "把他的数据给我"})
		return "done", true
	}
	run := c.run("go")
	if run.Status != store.RuntimeSucceeded {
		t.Fatalf("run: %+v", run)
	}
	if !strings.Contains(strings.Join(toolNames, ","), "bots__send") {
		t.Fatalf("bot runs should get the bots__ tools: %v", toolNames)
	}
	if !strings.Contains(listed.text(), `{"id":`+strconv.FormatInt(mine.ID, 10)+`,"name":"Organizer"}`) || strings.Contains(listed.text(), "Their bot") ||
		strings.Contains(listed.text(), `"id":`+strconv.FormatInt(theirs.ID, 10)+`,`) {
		t.Fatalf("bots__list must only show the employee's own bots: %s", listed.text())
	}
	if !sent.IsError || !strings.Contains(sent.text(), "cannot delegate") {
		t.Fatalf("cross-employee delegation must be rejected: %+v", sent)
	}
	if list, err := c.s.store.RuntimeDelegationsByParent(context.Background(), run.ID); err != nil || len(list) != 0 {
		t.Fatalf("no delegation may be recorded: %+v %v", list, err)
	}
	if got := c.audits("runtime.delegation.reject"); len(got) != 1 || got[0].Detail["reason"] != "not_allowed" {
		t.Fatalf("rejection must be audited: %+v", got)
	}
	// 只读的普通云任务令牌之外的调用方（员工自己的接入令牌）拿不到也调不了 bots__ 工具。
	if _, err := c.s.runtimeBotCaller(context.Background(), &Principal{User: &store.User{ID: c.employee}, TokenID: 1}); err == nil {
		t.Fatal("non-runtime principals must not act as a bot")
	}
}

// 完整流程：A 写共享文件并把「整理这个文件」派给 B；B 在同一台电脑上读到文件并完成，想派回 A 被截断；
// 结果作为追加的一轮回到 A 的线程，卡片数据里有目标、状态、结果和 B 的线程。
func TestDelegationRoundTripAndLoopTruncated(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 20})
	b := c.chatBot(c.employee, "Organizer")
	var back mcpResult
	var followupPrompt string
	f.onRun = func(token, dir, prompt string) (string, bool) {
		switch {
		case dir == botDir(b.ID) && strings.Contains(prompt, "delegated to you by your fellow bot «Notes»"):
			back = c.tool(token, "bots__send", map[string]any{"bot_id": c.bot.ID, "content": "你来做"})
			return "整理好了：Q3 目标 42", true
		// 追加轮的提示词里也有前面的历史（「请整理…」），所以先认它。
		case dir == botDir(c.bot.ID) && strings.Contains(prompt, "The work you delegated"):
			followupPrompt = prompt
			return "Organizer 已经整理好：Q3 目标 42", true
		case dir == botDir(c.bot.ID) && strings.Contains(prompt, "请整理"):
			res := c.tool(token, "bots__send", map[string]any{"bot_id": b.ID, "kind": "task", "content": "整理 " + runtimeSharedDir + "/note.txt"})
			if res.IsError {
				t.Errorf("delegate: %s", res.text())
			}
			return "已交给 Organizer", true
		}
		return "", false
	}
	if got := c.run("SHARE:Q3 目标 42"); got.Summary != "shared" {
		t.Fatalf("write: %+v", got)
	}
	thread := c.say(c.owner, c.bot, "请整理我刚写的文件")
	c.drain()

	if !back.IsError || !strings.Contains(back.text(), "loop") {
		t.Fatalf("delegating back to the delegator must be truncated: %+v", back)
	}
	if got := c.audits("runtime.delegation.truncate"); len(got) != 1 || got[0].Detail["reason"] != "cycle" {
		t.Fatalf("truncation must be audited: %+v", got)
	}
	if !strings.Contains(followupPrompt, "整理好了：Q3 目标 42") {
		t.Fatalf("the follow-up turn must carry B's result: %q", followupPrompt)
	}
	rec := c.owner("GET", "/api/runtime/chat/conversations/"+strconv.FormatInt(thread, 10), "")
	var detail struct {
		Messages    []store.RuntimeChatTurn   `json:"messages"`
		Delegations []store.RuntimeDelegation `json:"delegations"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &detail) != nil {
		t.Fatalf("conversation: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if len(detail.Messages) != 2 || detail.Messages[1].Summary != "Organizer 已经整理好：Q3 目标 42" {
		t.Fatalf("A's thread should hold the request and the follow-up: %+v", detail.Messages)
	}
	if len(detail.Delegations) != 1 {
		t.Fatalf("delegation cards: %+v", detail.Delegations)
	}
	d := detail.Delegations[0]
	if d.TargetBotID != b.ID || d.TargetBotName != "Organizer" || d.Status != store.RuntimeSucceeded || d.Result != "整理好了：Q3 目标 42" ||
		d.ParentTaskID != detail.Messages[0].ID || d.FollowupTaskID == nil || *d.FollowupTaskID != detail.Messages[1].ID || d.ChildConversationID == nil {
		t.Fatalf("delegation card: %+v", d)
	}
	// B 的线程里有这一轮，并标着来自 A。
	rec = c.owner("GET", "/api/runtime/chat/conversations/"+strconv.FormatInt(*d.ChildConversationID, 10), "")
	var child struct {
		Messages    []store.RuntimeChatTurn   `json:"messages"`
		Delegations []store.RuntimeDelegation `json:"delegations"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &child) != nil || len(child.Messages) != 1 || len(child.Delegations) != 1 ||
		child.Delegations[0].ParentBotName != "Notes" || child.Messages[0].ID != *child.Delegations[0].ChildTaskID {
		t.Fatalf("B's thread: HTTP %d %s", rec.Code, rec.Body.String())
	}
	for _, action := range []string{"runtime.delegation.create", "runtime.delegation.dispatch", "runtime.delegation.finish", "runtime.delegation.followup"} {
		if got := c.audits(action); len(got) != 1 {
			t.Fatalf("audit %s: %+v", action, got)
		}
	}
	if f.creates != 1 {
		t.Fatalf("all turns should run on the one shared computer: creates=%d", f.creates)
	}
}

// 防循环的数量与层数上限（存储层，在所有者锁下判断）。
func TestDelegationLimits(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour})
	ctx := context.Background()
	bots := []*store.RuntimeBot{c.bot}
	for i := 0; i < 5; i++ {
		bots = append(bots, c.chatBot(c.employee, "B"+strconv.Itoa(i)))
	}
	delegate := func(parentTask int64, from, to *store.RuntimeBot) (*store.RuntimeDelegation, error) {
		d := &store.RuntimeDelegation{OwnerID: c.employee, ParentTaskID: parentTask, ParentBotID: from.ID, TargetBotID: to.ID, Kind: store.RuntimeDelegationTask, Content: "x"}
		return d, c.s.store.CreateRuntimeDelegation(ctx, d)
	}
	// 一条链：根任务 1000（bots[0]）→ 1001（bots[1]）→ 1002（bots[2]）→ 1003（bots[3]），再往下就超过 3 层。
	parent := int64(1000)
	for i := 0; i < 3; i++ {
		d, err := delegate(parent, bots[i], bots[i+1])
		if err != nil {
			t.Fatalf("level %d: %v", i+1, err)
		}
		if d.Depth != i+1 || d.RootTaskID != 1000 {
			t.Fatalf("lineage at level %d: %+v", i+1, d)
		}
		parent++
		if err := c.s.store.MarkRuntimeDelegationDispatched(ctx, d.ID, parent, 1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := delegate(parent, bots[3], bots[4]); err != store.ErrRuntimeDelegationDepth {
		t.Fatalf("4th level must be truncated: %v", err)
	}
	if _, err := delegate(parent, bots[3], bots[1]); err != store.ErrRuntimeDelegationCycle {
		t.Fatalf("delegating back up the chain must be a cycle: %v", err)
	}
	if _, err := delegate(parent, bots[3], bots[3]); err != store.ErrRuntimeDelegationCycle {
		t.Fatalf("self delegation must be a cycle: %v", err)
	}
	// 一轮最多 3 个；同一轮不能给同一个机器人两个待派任务。
	if _, err := delegate(2000, bots[0], bots[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := delegate(2000, bots[0], bots[1]); err != store.ErrRuntimeDelegationDuplicate {
		t.Fatalf("duplicate: %v", err)
	}
	for _, to := range bots[2:4] {
		if _, err := delegate(2000, bots[0], to); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := delegate(2000, bots[0], bots[4]); err != store.ErrRuntimeDelegationFanout {
		t.Fatalf("4th delegation from one run: %v", err)
	}
	// 整棵树最多 8 个：根 3000 派 3 个，它们各自（作为子任务）再派，凑满 8 个后第 9 个被拒。
	var children []int64
	for i, to := range bots[1:4] {
		d, err := delegate(3000, bots[0], to)
		if err != nil {
			t.Fatal(err)
		}
		child := int64(3100 + i)
		if err := c.s.store.MarkRuntimeDelegationDispatched(ctx, d.ID, child, 1); err != nil {
			t.Fatal(err)
		}
		children = append(children, child)
	}
	made := 3
	for i, child := range children {
		from := bots[i+1]
		for _, to := range []*store.RuntimeBot{bots[4], bots[5]} {
			if made == store.RuntimeDelegationMaxPerRoot {
				break
			}
			if _, err := delegate(child, from, to); err != nil {
				t.Fatalf("delegation %d: %v", made+1, err)
			}
			made++
		}
	}
	if made != store.RuntimeDelegationMaxPerRoot {
		t.Fatalf("made %d delegations", made)
	}
	if _, err := delegate(children[2], bots[3], bots[5]); err != store.ErrRuntimeDelegationTotal {
		t.Fatalf("tree total must be capped at %d: %v", store.RuntimeDelegationMaxPerRoot, err)
	}
}

// 机器人新建机器人：先由员工本人在网页上同意，同意后同样的参数再调一次才建，一次同意只能用一次；
// 每位员工最多 5 个机器人建的机器人；新机器人派出去的任务同样受每员工 5 个排队 / 运行的上限约束。
func TestBotCreateNeedsApprovalAndLimit(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour})
	ctx := context.Background()
	args := map[string]any{"name": "临时助手", "prompt": "帮我整理共享目录里的文件"}
	var res mcpResult
	f.onRun = func(token, _, prompt string) (string, bool) {
		if strings.Contains(prompt, "CREATE") {
			res = c.tool(token, "bots__create", args)
			return "asked", true
		}
		return "", false
	}
	c.run("CREATE")
	if !res.IsError || !strings.Contains(res.text(), "网页上确认") {
		t.Fatalf("first create must ask for approval: %+v", res)
	}
	confirmation, _ := res.Meta["dsh-ee/confirmation"].(map[string]any)
	id, _ := confirmation["id"].(float64)
	if id == 0 {
		t.Fatalf("confirmation meta: %+v", res.Meta)
	}
	bots, _ := c.s.store.RuntimeColleagueBots(ctx, c.employee)
	if len(bots) != 1 {
		t.Fatal("no bot may be created before approval")
	}
	// 确认页显示的是机器人新建，不是某个企业连接。
	path := "/api/me/confirmations/" + strconv.FormatInt(int64(id), 10)
	if rec := c.owner("GET", path, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"system":"云端机器人"`) {
		t.Fatalf("confirmation view: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.owner("POST", path, `{"approve":true}`); rec.Code != http.StatusOK {
		t.Fatalf("approve: HTTP %d %s", rec.Code, rec.Body.String())
	}
	c.run("CREATE")
	if res.IsError || !strings.Contains(res.text(), `"name":"临时助手"`) {
		t.Fatalf("create after approval: %+v", res)
	}
	bots, _ = c.s.store.RuntimeColleagueBots(ctx, c.employee)
	if len(bots) != 2 || bots[1].Name != "临时助手" || bots[1].OwnerID != c.employee || bots[1].RunAsID != c.employee || !bots[1].ReadOnly || bots[1].Enabled {
		t.Fatalf("created bot: %+v", bots)
	}
	// 一次同意只能用一次：再建一个同样的要重新确认。
	c.run("CREATE")
	if !res.IsError || !strings.Contains(res.text(), "网页上确认") {
		t.Fatalf("approval must be single use: %+v", res)
	}
	if got := c.audits("runtime.bot.create_by_bot"); len(got) != 1 {
		t.Fatalf("audit: %+v", got)
	}
	// 上限：已经有 5 个机器人建的机器人时直接拒绝，不再请员工确认。
	for i := 0; i < store.RuntimeBotCreatedLimit-1; i++ {
		b := &store.RuntimeBot{OwnerID: c.employee, Name: "helper " + strconv.Itoa(i), Prompt: "p", Schedule: "0 9 * * *", DeliveryTarget: runtimeFeishuOwner}
		if err := c.s.store.CreateRuntimeBotByBot(ctx, b, c.bot.ID, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.s.store.CreateRuntimeBotByBot(ctx, &store.RuntimeBot{OwnerID: c.employee, Name: "x", Prompt: "p", Schedule: "0 9 * * *"}, c.bot.ID, 1); err != store.ErrRuntimeBotCreateLimit {
		t.Fatalf("store limit: %v", err)
	}
	args["name"] = "第六个"
	c.run("CREATE")
	if !res.IsError || !strings.Contains(res.text(), "at most 5") {
		t.Fatalf("create over the limit: %+v", res)
	}
	if got := c.audits("runtime.bot.create_reject"); len(got) != 1 || got[0].Detail["reason"] != "limit" {
		t.Fatalf("limit rejection audit: %+v", got)
	}

	// 派给新机器人的任务也受每员工排队上限约束：队列满时派不出去，记成 failed。
	helper := bots[1]
	for i := 0; i < store.RuntimeTaskQueueLimit; i++ {
		if _, err := c.s.store.EnqueueRuntimeTask(ctx, store.RuntimeTask{OwnerID: c.employee, RunAsID: c.employee, Prompt: "busy " + strconv.Itoa(i), TimeoutSeconds: 60}); err != nil {
			t.Fatal(err)
		}
	}
	d := &store.RuntimeDelegation{OwnerID: c.employee, ParentTaskID: 9999, ParentBotID: c.bot.ID, TargetBotID: helper.ID, Kind: store.RuntimeDelegationTask, Content: "整理"}
	if err := c.s.store.CreateRuntimeDelegation(ctx, d); err != nil {
		t.Fatal(err)
	}
	c.s.dispatchDelegation(ctx, &store.RuntimeTask{ID: 9999}, d)
	got, err := c.s.store.RuntimeDelegation(ctx, d.ID, c.employee)
	if err != nil || got.Status != store.RuntimeDelegationFailed || !strings.Contains(got.Error, "queued or running") {
		t.Fatalf("dispatch over the queue limit: %+v %v", got, err)
	}
}

// 两层委派：A 派给 B，B 再派给 C。A 拿到的是 B 在 C 的结果回来之后做完的最终回复，而不是 B 中途的「已转交」。
func TestDelegationChainReturnsFinalResult(t *testing.T) {
	f := &fakeComputers{}
	c := newComputerTest(t, f, runtime.ComputerPolicy{Persistent: true, IdleTimeout: time.Hour, DiskLimitBytes: 1 << 20})
	b, cc := c.chatBot(c.employee, "Organizer"), c.chatBot(c.employee, "Checker")
	var aFinal string
	f.onRun = func(token, dir, prompt string) (string, bool) {
		followup := strings.Contains(prompt, "The work you delegated")
		switch {
		case dir == botDir(c.bot.ID) && followup:
			aFinal = prompt
			return "全部完成", true
		case dir == botDir(c.bot.ID):
			c.tool(token, "bots__send", map[string]any{"bot_id": b.ID, "content": "整理并核对"})
			return "交给 Organizer 了", true
		case dir == botDir(b.ID) && followup:
			return "整理并核对完毕：C 说没问题", true
		case dir == botDir(b.ID):
			c.tool(token, "bots__send", map[string]any{"bot_id": cc.ID, "content": "核对一下"})
			return "我先请 Checker 核对", true
		case dir == botDir(cc.ID):
			if res := c.tool(token, "bots__send", map[string]any{"bot_id": c.bot.ID, "content": "回给 A"}); !res.IsError {
				t.Error("C delegating back to A (the root) must be a cycle")
			}
			return "没问题", true
		}
		return "", false
	}
	c.say(c.owner, c.bot, "开始")
	c.drain()
	if !strings.Contains(aFinal, "整理并核对完毕：C 说没问题") || strings.Contains(aFinal, `"result":"我先请 Checker 核对"`) {
		t.Fatalf("A must get B's final result after C came back: %q", aFinal)
	}
	if got := c.audits("runtime.delegation.followup"); len(got) != 2 {
		t.Fatalf("one follow-up for B and one for A: %+v", got)
	}
}
