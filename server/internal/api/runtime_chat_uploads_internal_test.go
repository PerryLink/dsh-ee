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

// 附件名二次撞名：两份 x.md 后一份会加 "<id>-" 前缀，而更早的附件正好也叫 "<id>-x.md"。
// 去重后的名字必须继续查重：三个附件各自的原件和正文副本都要保留，不能互相覆盖。
func TestRuntimeChatUploadNameCollisionKeepsBoth(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ws := &workspaceRuntime{writes: map[string]string{}}
	c.s.opts.Runtime.Provider = ws
	ownerID, owner := employee("collision-owner")
	upload := runtimeUploadClient(t, c.s, ownerID)
	on := true
	const chat = "/api/runtime/chat"
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	threadPath := chat + "/conversations/" + strconv.FormatInt(thread.ID, 10)
	uploadOne := func(what, name string, data []byte) store.RuntimeChatUpload {
		t.Helper()
		return decodeRuntimeChat[store.RuntimeChatUpload](t, what, upload(threadPath+"/uploads", name, data), http.StatusCreated)
	}

	// 上传顺序（按附件 ID）：x.md、<id>-x.md、x.md。第三份原名被占后加成 "<最后一份的 id>-x.md"，
	// 也就是第二份的名字：只加一次前缀不查重就会把第二份覆盖掉。
	middle := uploadOne("upload middle", "x.md", []byte("中间那份"))
	clash := fmt.Sprintf("%d-x.md", middle.ID+2)
	earlier := uploadOne("upload earlier", clash, []byte("更早那份"))
	last := uploadOne("upload last", "x.md", []byte("最后那份"))
	if earlier.ID != middle.ID+1 || last.ID != earlier.ID+1 {
		t.Fatalf("unexpected upload ids: %d %d %d", middle.ID, earlier.ID, last.ID)
	}
	body, _ := json.Marshal(map[string]any{"content": "对比一下", "idempotency_key": "collision", "uploads": []int64{middle.ID, earlier.ID, last.ID}})
	turn := decodeRuntimeChat[store.RuntimeChatTurn](t, "send", owner("POST", threadPath+"/messages", string(body)), http.StatusAccepted)
	c.runNext()
	dir := ws.dirs[len(ws.dirs)-1]
	base := fmt.Sprintf("%s/.uploads/%d/", dir, turn.ID)
	renamed := "2-" + clash
	for name, want := range map[string]string{
		base + "x.md":          "中间那份",
		base + "x.md.md":       "中间那份\n",
		base + clash:           "更早那份",
		base + clash + ".md":   "更早那份\n",
		base + renamed:         "最后那份",
		base + renamed + ".md": "最后那份\n",
	} {
		if ws.files[name] != want {
			t.Fatalf("colliding attachment names: %q = %q, want %q (files: %v)", name, ws.files[name], want, ws.files)
		}
	}
	for _, name := range []string{base + "x.md", base + clash, base + renamed} {
		if !strings.Contains(ws.prompt, name[len(dir)+1:]) {
			t.Fatalf("prompt does not mention %q: %q", name[len(dir)+1:], ws.prompt)
		}
	}
}

// 幂等重放不新建任务，这次请求带的新附件再也没机会挂上去：行和文件都要当场清掉，
// 不再占本人待发额度；第一次请求已经挂好的附件、以及另一个线程的待发附件都不受影响。
func TestRuntimeChatReplayDiscardsPendingUploads(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ws := &workspaceRuntime{writes: map[string]string{}}
	c.s.opts.Runtime.Provider = ws
	ctx := context.Background()
	ownerID, owner := employee("replay-owner")
	upload := runtimeUploadClient(t, c.s, ownerID)
	on := true
	const chat = "/api/runtime/chat"
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	threadPath := chat + "/conversations/" + strconv.FormatInt(thread.ID, 10)
	send := func(content, key string, uploads ...int64) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"content": content, "idempotency_key": key, "uploads": uploads})
		return owner("POST", threadPath+"/messages", string(body))
	}
	storageKey := func(id int64) string {
		t.Helper()
		var key string
		if err := c.s.store.DB().QueryRowContext(ctx, `SELECT storage_key FROM runtime_chat_uploads WHERE id=?`, id).Scan(&key); err != nil {
			t.Fatalf("storage key of upload %d: %v", id, err)
		}
		return key
	}

	first := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload first", upload(threadPath+"/uploads", "first.md", []byte("# 第一份")), http.StatusCreated)
	firstKey := storageKey(first.ID)
	turn := decodeRuntimeChat[store.RuntimeChatTurn](t, "send", send("总结一下", "replay", first.ID), http.StatusAccepted)
	if len(turn.Attachments) != 1 || turn.Attachments[0].ID != first.ID {
		t.Fatalf("first turn attachments: %+v", turn)
	}

	// 重试时重新上传的附件：重放同一个幂等键不会把它挂到任何一轮上。
	second := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload second", upload(threadPath+"/uploads", "second.md", []byte("# 第二份")), http.StatusCreated)
	secondKey := storageKey(second.ID)
	replay := decodeRuntimeChat[store.RuntimeChatTurn](t, "replay with a new upload", send("总结一下", "replay", second.ID), http.StatusAccepted)
	if replay.ID != turn.ID || replay.Content != turn.Content || replay.Status != turn.Status || !replay.CreatedAt.Equal(turn.CreatedAt) {
		t.Fatalf("replay changed the first turn: %+v, want %+v", replay, turn)
	}
	if len(replay.Attachments) != 1 || replay.Attachments[0].ID != first.ID {
		t.Fatalf("replay attachments changed: %+v", replay.Attachments)
	}
	var n int
	if err := c.s.store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM runtime_chat_uploads WHERE id=?`, second.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("upload from the replayed request kept its row: %d %v", n, err)
	}
	if pending, err := c.s.store.RuntimeChatPendingUploadBytes(ctx, ownerID); err != nil || pending != 0 {
		t.Fatalf("replayed request left pending bytes: %d %v", pending, err)
	}
	for _, p := range []string{secondKey, secondKey + ".md"} {
		if _, err := os.Stat(c.s.runtimeChatUploadPath(p)); !os.IsNotExist(err) {
			t.Fatalf("upload from the replayed request kept %s: %v", p, err)
		}
	}
	// 第一次请求挂上去的附件原样保留。
	if _, err := os.Stat(c.s.runtimeChatUploadPath(firstKey)); err != nil {
		t.Fatalf("first request's attachment file: %v", err)
	}
	if err := c.s.store.DB().QueryRowContext(ctx, `SELECT COALESCE(task_id,0) FROM runtime_chat_uploads WHERE id=?`, first.ID).Scan(&n); err != nil || int64(n) != turn.ID {
		t.Fatalf("first request's attachment lost its turn: %d %v", n, err)
	}

	// 引用第一次就挂好的附件原样重放：仍然是幂等成功，附件不动。
	again := decodeRuntimeChat[store.RuntimeChatTurn](t, "replay the same attachments", send("总结一下", "replay", first.ID), http.StatusAccepted)
	if again.ID != turn.ID || len(again.Attachments) != 1 || again.Attachments[0].ID != first.ID {
		t.Fatalf("replaying the attached upload changed the turn: %+v", again)
	}
	if pending, err := c.s.store.RuntimeChatPendingUploadBytes(ctx, ownerID); err != nil || pending != 0 {
		t.Fatalf("pending bytes after the plain replay: %d %v", pending, err)
	}

	// 另一个线程里还没发出的附件不归这次重放管，别顺手删掉。
	otherBot := decodeRuntimeChat[store.RuntimeBot](t, "create other bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Writer", false, &on)), http.StatusOK)
	otherThread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open other thread", owner("POST", chat+"/conversations", fmt.Sprintf(`{"bot_id":%d}`, otherBot.ID)), http.StatusOK)
	otherPath := chat + "/conversations/" + strconv.FormatInt(otherThread.ID, 10)
	otherUpload := decodeRuntimeChat[store.RuntimeChatUpload](t, "upload in another thread", upload(otherPath+"/uploads", "elsewhere.md", []byte("# 别的线程")), http.StatusCreated)
	decodeRuntimeChat[store.RuntimeChatTurn](t, "replay with another thread's upload", send("总结一下", "replay", otherUpload.ID), http.StatusAccepted)
	if pending, err := c.s.store.RuntimeChatPendingUploadBytes(ctx, ownerID); err != nil || pending != otherUpload.Size+otherUpload.TextBytes {
		t.Fatalf("another thread's pending upload: %d %v", pending, err)
	}

	// 执行这一轮：工作目录里只有第一次那份附件，提示词不提重试上传的文件。
	c.runNext()
	dir := ws.dirs[len(ws.dirs)-1]
	base := fmt.Sprintf("%s/.uploads/%d/", dir, turn.ID)
	if got := ws.files[base+"first.md"]; got != "# 第一份" {
		t.Fatalf("attachment missing from the sandbox: %v", ws.files)
	}
	attached := 0
	for name := range ws.files {
		if strings.HasPrefix(name, base) {
			attached++
		}
	}
	if attached != 2 || strings.Contains(ws.prompt, "second.md") {
		t.Fatalf("sandbox picked up a discarded upload: %v %q", ws.files, ws.prompt)
	}
	history := decodeRuntimeChat[runtimeChatDetail](t, "history", owner("GET", threadPath, ""), http.StatusOK)
	if len(history.Messages) != 1 || len(history.Messages[0].Attachments) != 1 || history.Messages[0].Attachments[0].ID != first.ID {
		t.Fatalf("history after the replay: %+v", history.Messages)
	}
}
