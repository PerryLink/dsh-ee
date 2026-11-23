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

	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 团队共享机器人：默认不共享；共享给部门后部门成员共用一条团队线程、互相看到消息和产物，团队外 403；
// 每一轮以发消息的成员本人身份运行（不放大所有者的权限）；取消共享、改部门、调岗都立即生效；审计记下共享变更和触发人。
func TestRuntimeTeamChatSharing(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ctx := context.Background()
	st := c.s.store
	const chat = "/api/runtime/chat"
	const rd, mkt = "研发部", "市场部"
	setDept := func(id int64, dept string) {
		t.Helper()
		if err := st.UpdateUser(ctx, id, store.UserPatch{Department: &dept}); err != nil {
			t.Fatal(err)
		}
	}
	member := func(name, dept string) (int64, func(string, string, string) *httptest.ResponseRecorder) {
		id, client := employee(name)
		setDept(id, dept)
		return id, client
	}
	ownerID, owner := member("team-owner", rd)
	aliceID, alice := member("alice", rd)
	bobID, bob := member("bob", rd)
	outsiderID, outsider := member("outsider", mkt)
	// 同部门、但没有模型使用权限的账号：能看到团队线程也不能借机器人跑任务。
	plain := &store.User{Username: "no-llm", DisplayName: "no-llm", Department: rd}
	if err := st.CreateUser(ctx, plain); err != nil {
		t.Fatal(err)
	}
	noLLM := runtimeTestClient(t, c.s, plain.ID)

	// 运行身份：worker 发给沙箱的短期凭证属于谁。
	var tokenUser int64
	c.m.run = func(cmd runtime.Command) error {
		c.prompt = cmd.Args[len(cmd.Args)-1]
		tokenUser = 0
		if tok, err := st.AccessTokenByHash(ctx, secret.Hash(cmd.Env["DSH_EE_RUNTIME_TOKEN"])); err == nil {
			tokenUser = tok.UserID
		}
		return nil
	}
	on := true
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Team analyst", false, &on)), http.StatusOK)
	if bot.SharedDepartment != "" {
		t.Fatalf("bot must not be shared by default: %+v", bot)
	}
	botPath := chat + "/bots/" + strconv.FormatInt(bot.ID, 10)
	teamCreate := fmt.Sprintf(`{"bot_id":%d,"shared":true}`, bot.ID)
	message := func(content, key string) string {
		body, _ := json.Marshal(map[string]string{"content": content, "idempotency_key": key})
		return string(body)
	}
	teamBots := func(name string, client func(string, string, string) *httptest.ResponseRecorder) []store.RuntimeChatBot {
		t.Helper()
		var out []store.RuntimeChatBot
		for _, b := range decodeRuntimeChat[[]store.RuntimeChatBot](t, name+" bots", client("GET", chat+"/bots", ""), http.StatusOK) {
			if b.Shared {
				out = append(out, b)
			}
		}
		return out
	}
	audits := func(action string) []*store.AuditLog {
		t.Helper()
		logs, err := st.ListAudit(ctx, store.AuditFilter{Action: action, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return logs
	}

	// 默认不共享：同部门的人看不到、建不了团队线程。
	if bots := teamBots("alice", alice); len(bots) != 0 {
		t.Fatalf("unshared bot is visible to the team: %+v", bots)
	}
	expectRuntimeChatCode(t, "team thread before sharing", alice("POST", chat+"/conversations", teamCreate), http.StatusForbidden, codeRuntimeChatTeamForbidden)

	// 只有所有者本人能共享，且只能共享给自己所在的部门；没填部门的所有者不能共享。
	expectRuntimeChatCode(t, "non-owner shares", alice("PUT", botPath+"/share", `{"shared":true}`), http.StatusNotFound, codeRuntimeChatBotUnavailable)
	loneID, lone := employee("no-dept-owner")
	loneBot := decodeRuntimeChat[store.RuntimeBot](t, "create lone bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, loneID, "Lone", false, &on)), http.StatusOK)
	expectRuntimeChatCode(t, "owner without department", lone("PUT", chat+"/bots/"+strconv.FormatInt(loneBot.ID, 10)+"/share", `{"shared":true}`), http.StatusConflict, codeRuntimeChatNoDepartment)
	shared := decodeRuntimeChat[map[string]string](t, "owner shares", owner("PUT", botPath+"/share", `{"shared":true}`), http.StatusOK)
	if shared["shared_department"] != rd {
		t.Fatalf("owner shared with %q, want own department", shared["shared_department"])
	}
	if logs := audits("runtime.bot.share"); len(logs) != 1 || logs[0].ActorID == nil || *logs[0].ActorID != ownerID ||
		logs[0].Detail["department"] != rd || logs[0].Detail["previous"] != "" || logs[0].Detail["by"] != "owner" {
		t.Fatalf("share audit: %+v", logs)
	}

	// 团队内可见：成员列表里有团队入口；所有者自己的入口标着共享给了哪个部门，他也在部门里所以另有团队入口。
	if bots := teamBots("alice", alice); len(bots) != 1 || bots[0].ID != bot.ID || bots[0].Department != rd || bots[0].OwnerName != "team-owner" {
		t.Fatalf("alice team bots: %+v", bots)
	}
	ownerBots := decodeRuntimeChat[[]store.RuntimeChatBot](t, "owner bots", owner("GET", chat+"/bots", ""), http.StatusOK)
	if len(ownerBots) != 2 || ownerBots[0].Shared || ownerBots[0].SharedDepartment != rd || !ownerBots[1].Shared {
		t.Fatalf("owner bots: %+v", ownerBots)
	}
	if bots := teamBots("outsider", outsider); len(bots) != 0 {
		t.Fatalf("outsider sees the team bot: %+v", bots)
	}
	if bots := decodeRuntimeChat[[]store.RuntimeChatBot](t, "no-llm bots", noLLM("GET", chat+"/bots", ""), http.StatusOK); len(bots) != 0 {
		t.Fatalf("ineligible member can pick the team bot: %+v", bots)
	}

	// 团队共用一条线程。
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "alice team thread", alice("POST", chat+"/conversations", teamCreate), http.StatusOK)
	if !thread.Shared || thread.Department != rd || thread.BotID != bot.ID {
		t.Fatalf("team thread: %+v", thread)
	}
	for name, client := range map[string]func(string, string, string) *httptest.ResponseRecorder{"bob": bob, "owner": owner} {
		if again := decodeRuntimeChat[store.RuntimeChatConversation](t, name+" team thread", client("POST", chat+"/conversations", teamCreate), http.StatusOK); again.ID != thread.ID {
			t.Fatalf("%s got thread %d, want the shared %d", name, again.ID, thread.ID)
		}
	}
	// 所有者自己的线程和团队线程是两条。
	private := decodeRuntimeChat[store.RuntimeChatConversation](t, "owner private thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	if private.ID == thread.ID || private.Shared {
		t.Fatalf("private thread %+v collides with team thread %d", private, thread.ID)
	}
	threadPath := chat + "/conversations/" + strconv.FormatInt(thread.ID, 10)

	// 团队外 403：读、发、建线程、看资料都不行；管理员也不能代看。
	for name, client := range map[string]func(string, string, string) *httptest.ResponseRecorder{"outsider": outsider, "admin": admin} {
		expectRuntimeChatCode(t, name+" reads team thread", client("GET", threadPath, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
		expectRuntimeChatCode(t, name+" posts to team thread", client("POST", threadPath+"/messages", message("hi", "x")), http.StatusForbidden, codeRuntimeChatTeamForbidden)
		expectRuntimeChatCode(t, name+" team profile", client("GET", botPath+"/profile?shared=1", ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	}
	expectRuntimeChatCode(t, "outsider creates team thread", outsider("POST", chat+"/conversations", teamCreate), http.StatusForbidden, codeRuntimeChatTeamForbidden)

	// 权限不被放大：同部门但没有模型权限的人能读线程，却不能借所有者的机器人发起任务。
	decodeRuntimeChat[runtimeChatDetail](t, "no-llm reads", noLLM("GET", threadPath, ""), http.StatusOK)
	expectRuntimeChatCode(t, "no-llm posts", noLLM("POST", threadPath+"/messages", message("run for me", "k")), http.StatusConflict, codeRuntimeChatBotDisabled)

	// alice 发消息：这一轮以 alice 本人的身份运行，短期凭证也是 alice 的，不是所有者的。
	first := decodeRuntimeChat[store.RuntimeChatTurn](t, "alice sends", alice("POST", threadPath+"/messages", message("本周的发布风险有哪些？", "a1")), http.StatusAccepted)
	if first.SenderID != aliceID || first.SenderName != "alice" {
		t.Fatalf("team turn sender: %+v", first)
	}
	// 一次一轮对整条团队线程生效。
	expectRuntimeChatCode(t, "bob while alice's turn runs", bob("POST", threadPath+"/messages", message("插一句", "b0")), http.StatusConflict, codeRuntimeChatBusy)
	task, prompt := c.runNext()
	if task.ID != first.ID || task.OwnerID != aliceID || task.RunAsID != aliceID || tokenUser != aliceID {
		t.Fatalf("team turn ran as owner %d / run_as %d / token user %d, want alice %d", task.OwnerID, task.RunAsID, tokenUser, aliceID)
	}
	if !strings.HasPrefix(prompt, runtimeEphemeralNote+"Bot instructions:\nPersona of Team analyst\n") || !strings.Contains(prompt, "shared by a team of colleagues") ||
		!strings.Contains(prompt, `The current message is from "alice"`) || !strings.HasSuffix(prompt, "\n本周的发布风险有哪些？") {
		t.Fatalf("team prompt: %q", prompt)
	}
	if logs := audits("runtime.chat.team_turn"); len(logs) != 1 || logs[0].ActorID == nil || *logs[0].ActorID != aliceID ||
		logs[0].Target != strconv.FormatInt(first.ID, 10) || logs[0].Detail["run_as"] != float64(aliceID) || logs[0].Detail["department"] != rd {
		t.Fatalf("team turn audit: %+v", logs)
	}
	// 幂等重放不重复记审计。
	decodeRuntimeChat[store.RuntimeChatTurn](t, "alice replays", alice("POST", threadPath+"/messages", message("本周的发布风险有哪些？", "a1")), http.StatusAccepted)
	if logs := audits("runtime.chat.team_turn"); len(logs) != 1 {
		t.Fatalf("replay was audited again: %d entries", len(logs))
	}

	// bob 看到同一条线程里 alice 的消息和回复，接着问；他的这一轮以 bob 自己的身份运行，提示词里带着 alice 那一轮的发送人。
	detail := decodeRuntimeChat[runtimeChatDetail](t, "bob reads", bob("GET", threadPath, ""), http.StatusOK)
	if len(detail.Messages) != 1 || detail.Messages[0].SenderName != "alice" || detail.Messages[0].Summary != "pilot summary" || !detail.Conversation.Shared {
		t.Fatalf("bob's view of the team thread: %+v", detail)
	}
	second := decodeRuntimeChat[store.RuntimeChatTurn](t, "bob sends", bob("POST", threadPath+"/messages", message("接着展开第一条", "b1")), http.StatusAccepted)
	task, prompt = c.runNext()
	if task.ID != second.ID || task.RunAsID != bobID || tokenUser != bobID {
		t.Fatalf("bob's turn ran as %d with token user %d", task.RunAsID, tokenUser)
	}
	if !strings.Contains(prompt, `"sender":"alice","employee":"本周的发布风险有哪些？"`) || !strings.Contains(prompt, `The current message is from "bob"`) {
		t.Fatalf("bob's prompt lost the sender of earlier turns: %q", prompt)
	}

	// 所有者的定时任务用所有者身份跑，产物不进团队文件库。
	decodeRuntimeChat[store.RuntimeTask](t, "admin runs bot", admin("POST", runtimeBotsTestPath+"/"+strconv.FormatInt(bot.ID, 10)+"/run", ""), http.StatusAccepted)
	scheduled, _ := c.runNext()
	if scheduled.ChatConversationID != nil || scheduled.RunAsID != ownerID {
		t.Fatalf("scheduled run: %+v", scheduled)
	}

	// 文件库：团队成员（包括没触发那一轮的人）都能看到并下载两轮的产物，团队外 403。
	profile := decodeRuntimeChat[store.RuntimeChatBotProfile](t, "bob team profile", bob("GET", botPath+"/profile?shared=1", ""), http.StatusOK)
	if !profile.Shared || profile.Department != rd || profile.Schedule != "" || len(profile.Artifacts) != 2 {
		t.Fatalf("team profile: %+v", profile)
	}
	for _, a := range profile.Artifacts {
		if a.TaskID == scheduled.ID {
			t.Fatalf("owner's scheduled artifact leaked into the team library: %+v", a)
		}
	}
	ownerProfile := decodeRuntimeChat[store.RuntimeChatBotProfile](t, "owner private profile", owner("GET", botPath+"/profile", ""), http.StatusOK)
	if ownerProfile.Shared || len(ownerProfile.Artifacts) != 1 || ownerProfile.Artifacts[0].TaskID != scheduled.ID {
		t.Fatalf("owner private profile: %+v", ownerProfile)
	}
	aliceTask := "/api/runtime/tasks/" + strconv.FormatInt(first.ID, 10)
	files := decodeRuntimeChat[[]store.RuntimeArtifact](t, "bob lists alice's files", bob("GET", aliceTask+"/artifacts", ""), http.StatusOK)
	if len(files) != 1 {
		t.Fatalf("bob sees %d files of alice's turn", len(files))
	}
	download := aliceTask + "/artifacts/" + strconv.FormatInt(files[0].ID, 10)
	if rec := bob("GET", download, ""); rec.Code != http.StatusOK || rec.Body.String() != "pilot summary" {
		t.Fatalf("bob downloads alice's file: HTTP %d", rec.Code)
	}
	expectRuntimeChatCode(t, "outsider downloads", outsider("GET", download, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	expectRuntimeChatCode(t, "outsider lists", outsider("GET", aliceTask+"/artifacts", ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	// 事件和取消仍然只认触发人本人。
	if rec := bob("GET", aliceTask+"/events", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob reads alice's task events: HTTP %d", rec.Code)
	}
	// 团队成员拿不到所有者定时任务的产物。
	if rec := bob("GET", "/api/runtime/tasks/"+strconv.FormatInt(scheduled.ID, 10)+"/artifacts", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("bob lists the owner's scheduled artifacts: HTTP %d", rec.Code)
	}

	// 调岗立即生效。
	setDept(bobID, mkt)
	expectRuntimeChatCode(t, "bob after moving", bob("GET", threadPath, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	expectRuntimeChatCode(t, "bob downloads after moving", bob("GET", download, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	setDept(bobID, rd)

	// 取消共享立即生效：成员看不到线程、入口和产物；排队中的团队轮次一并取消。
	pending := decodeRuntimeChat[store.RuntimeChatTurn](t, "alice queues", alice("POST", threadPath+"/messages", message("还有吗？", "a2")), http.StatusAccepted)
	if got := decodeRuntimeChat[map[string]string](t, "owner unshares", owner("PUT", botPath+"/share", `{"shared":false}`), http.StatusOK); got["shared_department"] != "" {
		t.Fatalf("unshare: %v", got)
	}
	for name, client := range map[string]func(string, string, string) *httptest.ResponseRecorder{"alice": alice, "bob": bob, "owner": owner} {
		expectRuntimeChatCode(t, name+" reads after unshare", client("GET", threadPath, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
		expectRuntimeChatCode(t, name+" posts after unshare", client("POST", threadPath+"/messages", message("还在吗", "u-"+name)), http.StatusForbidden, codeRuntimeChatTeamForbidden)
		if bots := teamBots(name, client); len(bots) != 0 {
			t.Fatalf("%s still sees the team bot: %+v", name, bots)
		}
	}
	expectRuntimeChatCode(t, "bob downloads after unshare", bob("GET", download, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	expectRuntimeChatCode(t, "alice downloads her turn after unshare", alice("GET", download, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	if canceled, err := st.RuntimeTask(ctx, pending.ID, aliceID); err != nil || canceled.Status != store.RuntimeCanceled {
		t.Fatalf("pending team turn after unshare: %+v, %v", canceled, err)
	}
	// 所有者自己的线程不受影响。
	decodeRuntimeChat[runtimeChatDetail](t, "owner private after unshare", owner("GET", chat+"/conversations/"+strconv.FormatInt(private.ID, 10), ""), http.StatusOK)
	if logs := audits("runtime.bot.share"); len(logs) != 2 || logs[0].Detail["department"] != "" || logs[0].Detail["previous"] != rd {
		t.Fatalf("unshare audit: %+v", logs)
	}

	// 管理员可以共享给任意部门；换了部门是另一条团队线程，原部门的记录不给新部门看。
	sharePath := runtimeBotsTestPath + "/" + strconv.FormatInt(bot.ID, 10) + "/share"
	expectRuntimeChatCode(t, "admin invalid department", admin("PUT", sharePath, `{"department":"a\u0007b"}`), http.StatusBadRequest, codeRuntimeChatInvalidDepartment)
	if rec := alice("PUT", sharePath, `{"department":"`+rd+`"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("employee used the admin share route: HTTP %d", rec.Code)
	}
	if got := decodeRuntimeChat[store.RuntimeBot](t, "admin shares", admin("PUT", sharePath, `{"department":" `+mkt+` "}`), http.StatusOK); got.SharedDepartment != mkt {
		t.Fatalf("admin share: %+v", got)
	}
	if logs := audits("runtime.bot.share"); len(logs) != 3 || logs[0].Detail["by"] != "admin" || logs[0].Detail["department"] != mkt {
		t.Fatalf("admin share audit: %+v", logs)
	}
	marketing := decodeRuntimeChat[store.RuntimeChatConversation](t, "outsider team thread", outsider("POST", chat+"/conversations", teamCreate), http.StatusOK)
	if marketing.ID == thread.ID || marketing.Department != mkt {
		t.Fatalf("marketing got thread %+v, previous team thread %d", marketing, thread.ID)
	}
	if d := decodeRuntimeChat[runtimeChatDetail](t, "marketing history", outsider("GET", chat+"/conversations/"+strconv.FormatInt(marketing.ID, 10), ""), http.StatusOK); len(d.Messages) != 0 {
		t.Fatalf("previous team's history leaked to the new department: %+v", d.Messages)
	}
	expectRuntimeChatCode(t, "outsider reads the old team thread", outsider("GET", threadPath, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	_ = outsiderID

	// 重新共享回原部门：原来的团队线程和历史回来。
	decodeRuntimeChat[store.RuntimeBot](t, "admin re-shares", admin("PUT", sharePath, `{"department":"`+rd+`"}`), http.StatusOK)
	if d := decodeRuntimeChat[runtimeChatDetail](t, "alice after re-share", alice("GET", threadPath, ""), http.StatusOK); len(d.Messages) != 3 {
		t.Fatalf("team history after re-share: %d turns", len(d.Messages))
	}

	// 管理员关掉网页对话后，所有者仍能收回共享；但关着的时候不能新共享。
	off := false
	decodeRuntimeChat[store.RuntimeBot](t, "admin disables web chat", admin("PUT", runtimeBotsTestPath+"/"+strconv.FormatInt(bot.ID, 10),
		runtimeChatBotBody(t, ownerID, "Team analyst", false, &off)), http.StatusOK)
	if got := decodeRuntimeChat[map[string]string](t, "owner unshares a disabled bot", owner("PUT", botPath+"/share", `{"shared":false}`), http.StatusOK); got["shared_department"] != "" {
		t.Fatalf("unshare after web chat was disabled: %v", got)
	}
	expectRuntimeChatCode(t, "owner shares a disabled bot", owner("PUT", botPath+"/share", `{"shared":true}`), http.StatusNotFound, codeRuntimeChatBotUnavailable)
}
