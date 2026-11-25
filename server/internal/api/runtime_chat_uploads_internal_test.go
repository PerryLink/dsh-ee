package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

// 预检只看原件大小（转换后的正文要到转换完才知道），真正的配额检查在写库事务里原子完成：
// 已有待发附件占到「上限 - 1 字节」时，传 1 字节的文件预检正好放行，转换出的正文让原子检查超限。
// 这时仍返回前端认识的 runtime_chat.upload_quota，而且已经写下的原件和 .md 副本都要删掉。
func TestRuntimeChatUploadQuotaCheckedAtomically(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ownerID, owner := employee("quota-owner")
	on := true
	const chat = "/api/runtime/chat"
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	path := fmt.Sprintf("%s/conversations/%d/uploads", chat, thread.ID)
	ctx := context.Background()
	// 预检口径是「已有待发合计 + 原件大小」：让它正好等于上限，预检放行；正文的 1 字节才让写库时超限。
	if _, err := c.s.store.DB().ExecContext(ctx, `INSERT INTO runtime_chat_uploads (owner_id, conversation_id, task_id, name, size, format, text_bytes, storage_key, created_at)
		VALUES (?,?,NULL,'hoard.txt',?,'txt',0,'hoard',?)`, ownerID, thread.ID, runtimeChatUploadPendingMax-1, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	upload := runtimeUploadClient(t, c.s, ownerID)
	expectRuntimeChatCode(t, "quota checked after convert", upload(path, "one-byte.txt", []byte("x")), http.StatusTooManyRequests, codeRuntimeChatUploadQuota)
	// 库里没有多出记录
	var n int
	if err := c.s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_chat_uploads WHERE owner_id=?`, ownerID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("被配额拒绝的上传不应写库：count=%d err=%v", n, err)
	}
	// 盘上也没有孤儿文件（原件和 .md 副本都删掉了）
	entries, err := os.ReadDir(filepath.Join(c.s.opts.DataDir, runtimeChatUploadsDir))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("被配额拒绝的上传留下了文件：%v", entries)
	}
}
