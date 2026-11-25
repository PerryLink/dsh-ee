package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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

// countReadBody 记录 handler 从请求体里实际读了多少字节。
type countReadBody struct {
	r io.Reader
	n int64
}

func (b *countReadBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += int64(n)
	return n, err
}

func (b *countReadBody) Close() error { return nil }

// 转换名额满了要立刻 429，不能先把请求体（最大 50 MB）解析进内存再拒绝：几十个并发上传
// 同时读 body 就能把内存打爆，转换闸门等于没设。修前这里 body 会被整份读完（读字节数 > 0），
// 修后必须在读 body 之前就挡住，且任何提前返回都不能把名额漏掉。
func TestRuntimeChatUploadChecksBusyBeforeReadingBody(t *testing.T) {
	c, employee, admin := newRuntimeChatTest(t)
	ownerID, owner := employee("upload-gate-owner")
	on := true
	bot := decodeRuntimeChat[store.RuntimeBot](t, "create bot", admin("POST", runtimeBotsTestPath, runtimeChatBotBody(t, ownerID, "Reader", false, &on)), http.StatusOK)
	thread := decodeRuntimeChat[store.RuntimeChatConversation](t, "open thread", owner("POST", "/api/runtime/chat/conversations", fmt.Sprintf(`{"bot_id":%d}`, bot.ID)), http.StatusOK)
	path := fmt.Sprintf("/api/runtime/chat/conversations/%d/uploads", thread.ID)
	// 上传接口的 multipart 请求要自己造（测试客户端只会发 JSON）。
	token := secret.Token()
	if err := c.s.store.CreateSession(context.Background(), secret.Hash(token), ownerID, time.Hour); err != nil {
		t.Fatal(err)
	}
	h := c.s.Handler()
	upload := func(data []byte) *http.Request {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		fw, err := mw.CreateFormFile("file", "big.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", path, &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		return r
	}
	withSession := func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
	}

	// 先手动塞满转换名额，模拟另外两个大文件正在转换。
	var held []func()
	t.Cleanup(func() {
		for _, release := range held {
			release()
		}
	})
	for i := 0; i < maxConcurrentKBConverts; i++ {
		release, ok := beginKBConvert()
		if !ok {
			t.Fatalf("第 %d 个转换名额应该能占到（上限 %d）", i+1, maxConcurrentKBConverts)
		}
		held = append(held, release)
	}

	// 请求体用计数 Reader：名额已满时 handler 一个字节都不该读。
	seed := bytes.Repeat([]byte("a"), 4<<20)
	probe := upload(seed)
	counting := &countReadBody{r: probe.Body}
	probe.Body = counting
	withSession(probe)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, probe)
	expectRuntimeChatCode(t, "gate full before body read", rec, http.StatusTooManyRequests, codeRuntimeChatUploadBusy)
	if counting.n != 0 {
		t.Fatalf("转换名额已满时不应该读请求体，实际读了 %d 字节", counting.n)
	}

	// 名额还回去之后闸门必须干净：正常上传成功，转换结束名额全部释放。
	for _, release := range held {
		release()
	}
	held = nil
	ok := upload([]byte("hi"))
	withSession(ok)
	okRec := httptest.NewRecorder()
	h.ServeHTTP(okRec, ok)
	decodeRuntimeChat[store.RuntimeChatUpload](t, "upload after release", okRec, http.StatusCreated)
	if got := len(kbConvertSlots); got != 0 {
		t.Fatalf("上传结束后转换名额没有全部释放：%d/%d 还被占着", got, maxConcurrentKBConverts)
	}
}
