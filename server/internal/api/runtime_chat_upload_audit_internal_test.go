package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 团队线程里的附件上传 / 删除写审计（谁在何时上传、删除了哪个文件），个人线程不写；
// 越权、超限、配额这些失败路径也不写。
//
// 回归：#345 的删除审计原先依赖带可见性过滤的 RuntimeChatConversation，员工上传后调岗或机器人取消共享，
// 删除成功但审计不写。现在按线程归属判断，这两种情况都仍然记删除审计。
func TestRuntimeChatTeamUploadAudit(t *testing.T) {
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
	ownerID, owner := employee("upload-audit-owner")
	setDept(ownerID, rd)
	aliceID, alice := employee("upload-audit-alice")
	setDept(aliceID, rd)
	otherID, other := employee("upload-audit-other")
	setDept(otherID, mkt)
	aliceUpload := runtimeUploadClient(t, c.s, aliceID)
	ownerUpload := runtimeUploadClient(t, c.s, ownerID)
	otherUpload := runtimeUploadClient(t, c.s, otherID)
	on := true
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Auditor", false, &on)), http.StatusOK)
	botPath := chat + "/bots/" + strconv.FormatInt(bot.ID, 10)
	decodeRuntimeChat[map[string]string](t, "owner shares", owner("PUT", botPath+"/share", `{"shared":true}`), http.StatusOK)
	team := decodeRuntimeChat[store.RuntimeChatConversation](t, "alice team thread", alice("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d,"shared":true}`, bot.ID)), http.StatusOK)
	teamPath := chat + "/conversations/" + strconv.FormatInt(team.ID, 10)
	// 所有者自己的线程：个人对话，附件和消息内容一样不审计。
	personal := decodeRuntimeChat[store.RuntimeChatConversation](t, "owner private thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	personalPath := chat + "/conversations/" + strconv.FormatInt(personal.ID, 10)
	audits := func(action string) []*store.AuditLog {
		t.Helper()
		logs, err := st.ListAudit(ctx, store.AuditFilter{Action: action, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return logs
	}
	// 团队附件审计：操作人是上传 / 删除的员工，对象是附件 ID，detail 只有元数据。
	// total 是当前这个动作的审计总条数：同一测试里的多次上传 / 删除都会留下记录。
	assertTeamAudit := func(action string, actorID int64, actorName string, upload store.RuntimeChatUpload, total int) {
		t.Helper()
		logs := audits(action)
		if len(logs) != total {
			t.Fatalf("%s audits: got %d, want %d: %+v", action, len(logs), total, logs)
		}
		var log *store.AuditLog
		for _, l := range logs {
			if l.Target == strconv.FormatInt(upload.ID, 10) {
				log = l
				break
			}
		}
		if log == nil || log.Action != action || log.ActorID == nil || *log.ActorID != actorID || log.ActorName != actorName {
			t.Fatalf("%s audit for upload %d: %+v", action, upload.ID, log)
		}
		want := map[string]any{
			"conversation_id": float64(team.ID),
			"bot_id":          float64(team.BotID),
			"upload_id":       float64(upload.ID),
			"name":            upload.Name,
			"size":            float64(upload.Size),
			"format":          upload.Format,
		}
		for key, value := range want {
			if log.Detail[key] != value {
				t.Fatalf("%s audit detail %s=%v, want %v (all: %+v)", action, key, log.Detail[key], value, log.Detail)
			}
		}
	}

	// 个人线程：上传、删除都不写审计。
	privateUpload := decodeRuntimeChat[store.RuntimeChatUpload](t, "personal upload", ownerUpload(personalPath+"/uploads", "私密.md", []byte("# 私密\n")), http.StatusCreated)
	if logs := audits(auditRuntimeChatUpload); len(logs) != 0 {
		t.Fatalf("personal upload was audited: %+v", logs)
	}
	if rec := owner("DELETE", fmt.Sprintf("%s/uploads/%d", chat, privateUpload.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete personal upload: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if logs := audits(auditRuntimeChatUploadDelete); len(logs) != 0 {
		t.Fatalf("personal upload delete was audited: %+v", logs)
	}

	// 团队线程：上传后记一条上传审计。
	doc := []byte("# 团队季度计划\n\n- 目标 1200 万\n")
	up := decodeRuntimeChat[store.RuntimeChatUpload](t, "team upload", aliceUpload(teamPath+"/uploads", "团队计划.md", doc), http.StatusCreated)
	assertTeamAudit(auditRuntimeChatUpload, aliceID, "upload-audit-alice", up, 1)

	// 团队线程：删除（发出前撤回）后记一条删除审计；别人删不掉也不会写。
	expectRuntimeChatCode(t, "other employee deletes a team upload", other("DELETE", fmt.Sprintf("%s/uploads/%d", chat, up.ID), ""), http.StatusNotFound, codeRuntimeChatUploadUnavailable)
	if logs := audits(auditRuntimeChatUploadDelete); len(logs) != 0 {
		t.Fatalf("failed delete was audited: %+v", logs)
	}
	if rec := alice("DELETE", fmt.Sprintf("%s/uploads/%d", chat, up.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete team upload: HTTP %d %s", rec.Code, rec.Body.String())
	}
	assertTeamAudit(auditRuntimeChatUploadDelete, aliceID, "upload-audit-alice", up, 1)

	// 回归（#345 的 follow-up）：团队附件上传后员工调岗，删除仍然要写审计。
	// 修复前删除路径用 RuntimeChatConversation 复核可见性，调岗后查不到对话，删除成功但审计不写。
	beforeTransfer := decodeRuntimeChat[store.RuntimeChatUpload](t, "team upload before transfer",
		aliceUpload(teamPath+"/uploads", "调岗前上传.md", []byte("# 调岗前\n")), http.StatusCreated)
	assertTeamAudit(auditRuntimeChatUpload, aliceID, "upload-audit-alice", beforeTransfer, 2)
	setDept(aliceID, mkt)
	expectRuntimeChatCode(t, "moved employee reads the team thread", alice("GET", teamPath, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	if rec := alice("DELETE", fmt.Sprintf("%s/uploads/%d", chat, beforeTransfer.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete team upload after transfer: HTTP %d %s", rec.Code, rec.Body.String())
	}
	assertTeamAudit(auditRuntimeChatUploadDelete, aliceID, "upload-audit-alice", beforeTransfer, 2)

	// 取消共享之后再删也一样：附件仍然属于团队线程，审计照写。
	setDept(aliceID, rd)
	beforeUnshare := decodeRuntimeChat[store.RuntimeChatUpload](t, "team upload before unshare",
		aliceUpload(teamPath+"/uploads", "取消共享前上传.md", []byte("# 取消共享前\n")), http.StatusCreated)
	assertTeamAudit(auditRuntimeChatUpload, aliceID, "upload-audit-alice", beforeUnshare, 3)
	if rec := owner("PUT", botPath+"/share", `{"shared":false}`); rec.Code != http.StatusOK {
		t.Fatalf("owner unshares: HTTP %d %s", rec.Code, rec.Body.String())
	}
	expectRuntimeChatCode(t, "unshared employee reads the team thread", alice("GET", teamPath, ""), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	if rec := alice("DELETE", fmt.Sprintf("%s/uploads/%d", chat, beforeUnshare.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete team upload after unshare: HTTP %d %s", rec.Code, rec.Body.String())
	}
	assertTeamAudit(auditRuntimeChatUploadDelete, aliceID, "upload-audit-alice", beforeUnshare, 3)
	if rec := owner("PUT", botPath+"/share", `{"shared":true}`); rec.Code != http.StatusOK {
		t.Fatalf("owner re-shares: HTTP %d %s", rec.Code, rec.Body.String())
	}

	// 读不到对话（线程记录已经不在）时只 Warn：删除仍然成功，也仍然不写审计。
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO runtime_chat_uploads (owner_id, conversation_id, task_id, name, size, format, pages, text_bytes, storage_key, created_at)
		VALUES (?,?,NULL,'orphan.md',1,'md',0,1,'audit-orphan',?)`, aliceID, int64(999999999), time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var orphanID int64
	if err := st.DB().QueryRowContext(ctx, `SELECT id FROM runtime_chat_uploads WHERE storage_key='audit-orphan'`).Scan(&orphanID); err != nil {
		t.Fatal(err)
	}
	if rec := alice("DELETE", fmt.Sprintf("%s/uploads/%d", chat, orphanID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete upload with missing conversation: HTTP %d %s", rec.Code, rec.Body.String())
	}

	// 失败路径都不写：团队外不可见（403）、超限（413）、别人的个人线程（404）、配额用尽（429）。
	expectRuntimeChatCode(t, "outsider uploads into the team thread", otherUpload(teamPath+"/uploads", "越权.md", []byte("x")), http.StatusForbidden, codeRuntimeChatTeamForbidden)
	expectRuntimeChatCode(t, "too large team upload", aliceUpload(teamPath+"/uploads", "big.txt", bytes.Repeat([]byte("a"), runtimeChatUploadMax+1)), http.StatusRequestEntityTooLarge, codeRuntimeChatUploadTooLarge)
	expectRuntimeChatCode(t, "upload into someone else's private thread", aliceUpload(personalPath+"/uploads", "x.md", []byte("x")), http.StatusNotFound, codeRuntimeChatNotFound)
	if _, err := st.DB().ExecContext(ctx, `INSERT INTO runtime_chat_uploads (owner_id, conversation_id, task_id, name, size, format, pages, text_bytes, storage_key, created_at)
		VALUES (?,?,NULL,'hoard.txt',?,'txt',0,0,'audit-hoard',?)`, aliceID, team.ID, runtimeChatUploadPendingMax, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	expectRuntimeChatCode(t, "team upload over quota", aliceUpload(teamPath+"/uploads", "one-more.txt", []byte("x")), http.StatusTooManyRequests, codeRuntimeChatUploadQuota)
	if logs := audits(auditRuntimeChatUpload); len(logs) != 3 {
		t.Fatalf("failed uploads were audited: %+v", logs)
	}
	if logs := audits(auditRuntimeChatUploadDelete); len(logs) != 3 {
		t.Fatalf("failed deletes were audited: %+v", logs)
	}
}
