package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// runtimeUploadClient 以某位员工的身份用 multipart 上传一个文件。
func runtimeUploadClient(t *testing.T, s *Server, id int64) func(path, name string, data []byte) *httptest.ResponseRecorder {
	t.Helper()
	token := secret.Token()
	if err := s.store.CreateSession(context.Background(), secret.Hash(token), id, time.Hour); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	return func(path, name string, data []byte) *httptest.ResponseRecorder {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
		mw.Close()
		r := httptest.NewRequest("POST", path, &body)
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		r.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
}

func TestRuntimeChatUploadsReachSandbox(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ws := &workspaceRuntime{writes: map[string]string{}}
	c.s.opts.Runtime.Provider = ws
	ownerID, owner := employee("upload-owner")
	otherID, other := employee("upload-other")
	upload := runtimeUploadClient(t, c.s, ownerID)
	otherUpload := runtimeUploadClient(t, c.s, otherID)
	on := true
	const chat = "/api/runtime/chat"
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	threadPath := chat + "/conversations/" + strconv.FormatInt(thread.ID, 10)
	send := func(content, key string, uploads ...int64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"content": content, "idempotency_key": key, "uploads": uploads})
		return owner("POST", threadPath+"/messages", string(body))
	}

	doc := "# 季度计划\n\n- 第三季度营收目标 1200 万\n"
	up := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload", upload(threadPath+"/uploads", "../季度计划.md", []byte(doc)), http.StatusCreated)
	if up.Name != "季度计划.md" || up.Format != "md" || up.Size != int64(len(doc)) || up.TextBytes == 0 {
		t.Fatalf("upload metadata: %+v", up)
	}
	expectRuntimeChatCode(t, "other employee uploads into the thread", otherUpload(threadPath+"/uploads", "x.md", []byte("x")), http.StatusNotFound, codeRuntimeChatNotFound)
	expectRuntimeChatCode(t, "legacy word file", upload(threadPath+"/uploads", "旧文档.doc", []byte("binary")), http.StatusBadRequest, codeRuntimeChatUploadUnsupported)
	expectRuntimeChatCode(t, "too large", upload(threadPath+"/uploads", "big.txt", bytes.Repeat([]byte("a"), runtimeChatUploadMax+1)), http.StatusRequestEntityTooLarge, codeRuntimeChatUploadTooLarge)
	expectRuntimeChatCode(t, "too many", send("", "many", 1, 2, 3, 4, 5, 6), http.StatusBadRequest, codeRuntimeChatTooManyUploads)
	expectRuntimeChatCode(t, "empty without uploads", send("", "empty"), http.StatusBadRequest, codeRuntimeChatInvalidMessage)
	expectRuntimeChatCode(t, "other employee deletes", other("DELETE", fmt.Sprintf("%s/uploads/%d", chat, up.ID), ""), http.StatusNotFound, codeRuntimeChatUploadUnavailable)

	// 拖错的文件可以在发出前删掉，文件也一起删。
	wrong := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload wrong", upload(threadPath+"/uploads", "拖错了.txt", []byte("oops")), http.StatusCreated)
	var stored store.RuntimeChatUpload
	if err := c.s.store.DB().QueryRowContext(context.Background(), `SELECT storage_key FROM runtime_chat_uploads WHERE id=?`, wrong.ID).Scan(&stored.StorageKey); err != nil {
		t.Fatal(err)
	}
	if rec := owner("DELETE", fmt.Sprintf("%s/uploads/%d", chat, wrong.ID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete upload: HTTP %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(c.s.runtimeChatUploadPath(stored.StorageKey)); !os.IsNotExist(err) {
		t.Fatalf("deleted upload left its file behind: %v", err)
	}
	expectRuntimeChatCode(t, "send a deleted upload", send("看看", "deleted", wrong.ID), http.StatusConflict, codeRuntimeChatUploadUnavailable)

	// 只发附件不写字：这一轮带着附件，执行时原件和正文写进工作目录，提示词告诉机器人在哪。
	turn := decodeRuntimeChat[store.RuntimeChatTurn](t, "send attachment", send("", "doc", up.ID), http.StatusAccepted)
	if turn.Content != "" || len(turn.Attachments) != 1 || turn.Attachments[0].ID != up.ID {
		t.Fatalf("turn attachments: %+v", turn)
	}
	c.runNext()
	rel := fmt.Sprintf(".uploads/%d/季度计划.md", turn.ID)
	dir := ws.dirs[len(ws.dirs)-1]
	if ws.files[dir+"/"+rel] != doc || !strings.Contains(ws.files[dir+"/"+rel+".md"], "第三季度营收目标") {
		t.Fatalf("attachment not written into the sandbox: %v", ws.files)
	}
	note := strings.Index(ws.prompt, rel)
	current := strings.LastIndex(ws.prompt, runtimeChatEmployeeHeader)
	if note < 0 || current < 0 || note > current || !strings.Contains(ws.prompt[current:], "without a message") {
		t.Fatalf("prompt does not point at the attachment before the current message: %q", ws.prompt)
	}
	profile := decodeRuntimeChat[store.RuntimeChatBotProfile](t, "profile", owner("GET", chat+"/bots/"+strconv.FormatInt(bot.ID, 10)+"/profile", ""), http.StatusOK)
	for _, a := range profile.Artifacts {
		if strings.Contains(a.Name, "季度计划") {
			t.Fatalf("an attachment came back as a deliverable: %+v", a)
		}
	}
	history := decodeRuntimeChat[struct {
		Messages []store.RuntimeChatTurn `json:"messages"`
	}](t, "history", owner("GET", threadPath, ""), http.StatusOK)
	if len(history.Messages) != 1 || len(history.Messages[0].Attachments) != 1 || history.Messages[0].Attachments[0].Name != "季度计划.md" {
		t.Fatalf("history attachments: %+v", history.Messages)
	}
	// 发出去的附件不能再挂到别的消息上，也不能删。
	expectRuntimeChatCode(t, "reuse a sent upload", send("再看一遍", "again", up.ID), http.StatusConflict, codeRuntimeChatUploadUnavailable)
	expectRuntimeChatCode(t, "delete a sent upload", owner("DELETE", fmt.Sprintf("%s/uploads/%d", chat, up.ID), ""), http.StatusNotFound, codeRuntimeChatUploadUnavailable)

	// budget.csv 的正文副本和原件 budget.csv.md 不能落到同一个路径上。
	csv := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload csv", upload(threadPath+"/uploads", "budget.csv", []byte("item,amount\nrent,100\n")), http.StatusCreated)
	md := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload csv.md", upload(threadPath+"/uploads", "budget.csv.md", []byte("# 另一份预算\n")), http.StatusCreated)
	pair := decodeRuntimeChat[store.RuntimeChatTurn](t, "send pair", send("对比一下", "pair", csv.ID, md.ID), http.StatusAccepted)
	c.runNext()
	dir = ws.dirs[len(ws.dirs)-1]
	base := fmt.Sprintf("%s/.uploads/%d/", dir, pair.ID)
	renamed := fmt.Sprintf("%s%d-budget.csv.md", base, md.ID)
	if !strings.Contains(ws.files[base+"budget.csv.md"], "rent") || ws.files[renamed] != "# 另一份预算\n" || !strings.Contains(ws.prompt, renamed[len(dir)+1:]) {
		t.Fatalf("colliding attachment names: %v", ws.files)
	}

	// 只上传不发：本人还没发出的附件合计有上限。
	if _, err := c.s.store.DB().ExecContext(context.Background(), `INSERT INTO runtime_chat_uploads (owner_id, conversation_id, task_id, name, size, format, text_bytes, storage_key, created_at)
		VALUES (?,?,NULL,'hoard.txt',?,'txt',0,'hoard',?)`, ownerID, thread.ID, runtimeChatUploadPendingMax, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	expectRuntimeChatCode(t, "pending quota", upload(threadPath+"/uploads", "one-more.txt", []byte("x")), http.StatusTooManyRequests, codeRuntimeChatUploadQuota)
	// 扔下超过一天的附件会被清掉，不再占额度。
	if _, err := c.s.store.DB().ExecContext(context.Background(), `UPDATE runtime_chat_uploads SET created_at=? WHERE storage_key='hoard'`, time.Now().UTC().Add(-runtimeChatUploadTTL-time.Hour)); err != nil {
		t.Fatal(err)
	}
	decodeRuntimeChat[store.RuntimeChatUpload](t, "upload after expiry", upload(threadPath+"/uploads", "one-more.txt", []byte("x")), http.StatusCreated)
}

func TestRuntimeChatUploadPrune(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ownerID, owner := employee("prune-owner")
	upload := runtimeUploadClient(t, c.s, ownerID)
	on := true
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", "/api/runtime/chat/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	path := fmt.Sprintf("/api/runtime/chat/conversations/%d/uploads", thread.ID)
	old := decodeRuntimeChat[store.RuntimeChatUpload](t, "old upload", upload(path, "old.txt", []byte("old")), http.StatusCreated)
	var key string
	ctx := context.Background()
	if err := c.s.store.DB().QueryRowContext(ctx, `SELECT storage_key FROM runtime_chat_uploads WHERE id=?`, old.ID).Scan(&key); err != nil {
		t.Fatal(err)
	}
	if _, err := c.s.store.DB().ExecContext(ctx, `UPDATE runtime_chat_uploads SET created_at=? WHERE id=?`, time.Now().UTC().Add(-2*runtimeChatUploadTTL), old.ID); err != nil {
		t.Fatal(err)
	}
	decodeRuntimeChat[store.RuntimeChatUpload](t, "new upload", upload(path, "new.txt", []byte("new")), http.StatusCreated)
	if _, err := os.Stat(c.s.runtimeChatUploadPath(key)); !os.IsNotExist(err) {
		t.Fatalf("stale upload file kept: %v", err)
	}
	var n int
	if err := c.s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_chat_uploads WHERE id=?`, old.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("stale upload row kept: %d %v", n, err)
	}
}

func TestRuntimeChatUploadFileName(t *testing.T) {
	for in, want := range map[string]string{
		"季度计划.pdf":                       "季度计划.pdf",
		"../../etc/passwd":               "passwd",
		`C:\Users\a\报告.docx`:             "报告.docx",
		".env":                           "env",
		"a\x00b:c?.txt":                  "a_b_c_.txt",
		".pdf":                           "pdf",
		"...":                            "document",
		strings.Repeat("长", 200) + ".md": strings.Repeat("长", runtimeChatUploadName) + ".md",
	} {
		if got := runtimeChatUploadFileName(in); got != want {
			t.Errorf("runtimeChatUploadFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRuntimeChatUploadPurgedAfterTask：一轮结束（成功/失败/取消）之后，后台回收只删附件的原件和正文，
// DB 行保留——历史接口仍然返回文件名、大小和格式。还没结束的轮次、还没发出的附件都不受影响。
func TestRuntimeChatUploadPurgedAfterTask(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ctx := context.Background()
	ownerID, owner := employee("gc-owner")
	upload := runtimeUploadClient(t, c.s, ownerID)
	on := true
	const chat = "/api/runtime/chat"
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", chat+"/conversations", fmt.Sprintf("{\"bot_id\":%d}", bot.ID)), http.StatusOK)
	threadPath := chat + "/conversations/" + strconv.FormatInt(thread.ID, 10)
	uploadAndSend := func(name, content, key string, data []byte) (store.RuntimeChatUpload, int64) {
		t.Helper()
		up := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload "+name, upload(threadPath+"/uploads", name, data), http.StatusCreated)
		body, _ := json.Marshal(map[string]any{"content": content, "idempotency_key": key, "uploads": []int64{up.ID}})
		turn := decodeRuntimeChat[store.RuntimeChatTurn](t, "send "+name, owner("POST", threadPath+"/messages", string(body)), http.StatusAccepted)
		return up, turn.ID
	}
	storageKey := func(id int64) string {
		t.Helper()
		var key string
		if err := c.s.store.DB().QueryRowContext(ctx, `SELECT storage_key FROM runtime_chat_uploads WHERE id=?`, id).Scan(&key); err != nil {
			t.Fatalf("读附件存储键失败：%v", err)
		}
		return key
	}
	// filesExist 断言附件的原件和正文两个文件在 / 不在。
	filesExist := func(what string, id int64, want bool) {
		t.Helper()
		key := storageKey(id)
		for _, p := range []string{c.s.runtimeChatUploadPath(key), c.s.runtimeChatUploadPath(key) + ".md"} {
			_, err := os.Stat(p)
			if want && err != nil {
				t.Fatalf("%s：附件文件不存在 %s: %v", what, p, err)
			}
			if !want && !os.IsNotExist(err) {
				t.Fatalf("%s：附件文件没有删掉 %s", what, p)
			}
		}
	}
	// metadata 断言 DB 行还在，历史接口仍返回同样的文件名、大小和格式。
	metadata := func(what string, up store.RuntimeChatUpload, taskID int64) {
		t.Helper()
		var rows int
		if err := c.s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_chat_uploads WHERE id=?`, up.ID).Scan(&rows); err != nil || rows != 1 {
			t.Fatalf("%s：附件 DB 行没了（%d, %v）", what, rows, err)
		}
		history := decodeRuntimeChat[runtimeChatDetail](t, what+" history", owner("GET", threadPath, ""), http.StatusOK)
		for _, m := range history.Messages {
			if m.ID != taskID {
				continue
			}
			if len(m.Attachments) != 1 || m.Attachments[0].ID != up.ID || m.Attachments[0].Name != up.Name ||
				m.Attachments[0].Size != up.Size || m.Attachments[0].Format != up.Format {
				t.Fatalf("%s：历史里的附件元数据不对：%+v，want %+v", what, m.Attachments, up)
			}
			return
		}
		t.Fatalf("%s：历史里找不到第 %d 轮", what, taskID)
	}

	// 成功的一轮：排队时不删；刚结束还在 grace 里不删；grace 到期删文件、留元数据。
	const doc = "# 季度计划\n\n- 第三季度营收目标 1200 万\n"
	okUp, okTask := uploadAndSend("成功.md", "", "gc-ok", []byte(doc))
	filesExist("排队中的轮次", okUp.ID, true)
	if n := c.s.purgeRuntimeChatUploads(ctx, 0); n != 0 {
		t.Fatalf("还没结束的轮次被回收了 %d 个文件", n)
	}
	filesExist("排队中的轮次", okUp.ID, true)
	c.runNext()
	if n := c.s.purgeRuntimeChatUploads(ctx, runtimeChatUploadPurgeGrace); n != 0 {
		t.Fatalf("刚结束、还在 grace 里的附件被回收了 %d 个文件", n)
	}
	filesExist("grace 内", okUp.ID, true)
	if n := c.s.purgeRuntimeChatUploads(ctx, 0); n != 2 {
		t.Fatalf("回收成功的轮次应该删掉原件和正文两个文件，实际 %d", n)
	}
	filesExist("成功之后", okUp.ID, false)
	metadata("成功之后", okUp, okTask)
	// DB 行不删，所以下一轮还会选中它；再跑一次是幂等的，不报错也不重复计数。
	if n := c.s.purgeRuntimeChatUploads(ctx, 0); n != 0 {
		t.Fatalf("重复回收又删了 %d 个文件", n)
	}

	// 失败的一轮：第一次失败会重新排队，第二次才到 failed 终态。
	failUp, failTask := uploadAndSend("失败.md", "算一下", "gc-fail", []byte("# 算一下\n"))
	run := c.m.run
	c.m.run = func(runtime.Command) error { return errors.New("boom") }
	c.runNext()
	c.runNext()
	c.m.run = run
	var status string
	if err := c.s.store.DB().QueryRowContext(ctx, `SELECT status FROM runtime_tasks WHERE id=?`, failTask).Scan(&status); err != nil || status != store.RuntimeFailed {
		t.Fatalf("没造出失败的一轮：%q %v", status, err)
	}
	if n := c.s.purgeRuntimeChatUploads(ctx, 0); n != 2 {
		t.Fatalf("回收失败的轮次应该删掉两个文件，实际 %d", n)
	}
	filesExist("失败之后", failUp.ID, false)
	metadata("失败之后", failUp, failTask)

	// 取消的一轮。
	cancelUp, cancelTask := uploadAndSend("取消.md", "取消吧", "gc-cancel", []byte("# 取消\n"))
	if rec := owner("POST", "/api/runtime/tasks/"+strconv.FormatInt(cancelTask, 10)+"/cancel", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("取消排队中的轮次：HTTP %d %s", rec.Code, rec.Body.String())
	}
	if n := c.s.purgeRuntimeChatUploads(ctx, 0); n != 2 {
		t.Fatalf("回收取消的轮次应该删掉两个文件，实际 %d", n)
	}
	filesExist("取消之后", cancelUp.ID, false)
	metadata("取消之后", cancelUp, cancelTask)

	// 还没发出去的附件不归回收任务管（另有 24h 过期清理）。
	pending := decodeRuntimeChat[store.RuntimeChatUpload](t, "pending upload", upload(threadPath+"/uploads", "待发.md", []byte("# 待发\n")), http.StatusCreated)
	if n := c.s.purgeRuntimeChatUploads(ctx, 0); n != 0 {
		t.Fatalf("待发附件被回收了 %d 个文件", n)
	}
	filesExist("待发附件", pending.ID, true)
}
