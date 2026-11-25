package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hikariming/dsh-ee/server/internal/kb"
	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 网页对话的附件：员工把文档拖进对话框（或点回形针选文件），浏览器先逐个上传到
// POST /api/runtime/chat/conversations/{id}/uploads，服务端当场转成 Markdown（和知识库同一套转换，
// 不支持的格式、扫描件、超限当场报错），原件和正文存在 DSH_EE_DATA_DIR/runtime-chat-uploads；
// 发消息时带上附件 ID，执行这一轮时 worker 把原件和正文写进机器人的工作目录 .uploads/<轮次>/，并在提示词前面告诉它。
const (
	runtimeChatUploadMax = 50 << 20
	runtimeChatUploadTTL = 24 * time.Hour
	// runtimeChatUploadPendingMax 是一位员工所有还没发出的附件（原件 + 正文）合计的上限，防止只上传不发把数据盘占满。
	runtimeChatUploadPendingMax = 500 << 20
	runtimeChatUploadsDir       = "runtime-chat-uploads"
	runtimeChatUploadName       = 120
	// runtimeChatUploadsWorkDir 是附件在机器人工作目录里的位置（相对路径），按轮次分子目录。
	runtimeChatUploadsWorkDir = ".uploads"
	// runtimeChatUploadPurgeGrace 是一轮结束多久之后回收附件的原件和正文；DB 行不删，历史里还要显示文件名、大小和格式。
	runtimeChatUploadPurgeGrace = 24 * time.Hour
	// runtimeChatUploadPurgeBatch 是后台回收每页读多少条，避免一次把全部历史附件读进内存。
	runtimeChatUploadPurgeBatch = 200
)

func (s *Server) runtimeChatUploadPath(key string) string {
	return filepath.Join(s.opts.DataDir, runtimeChatUploadsDir, key)
}

// removeRuntimeChatUpload 删掉附件的原件和正文，返回真正删掉的文件数。文件已经不在了算 0：
// 上传事务失败、上一轮周期回收已经删过都可能是这种情况，不算错误。
func (s *Server) removeRuntimeChatUpload(u store.RuntimeChatUpload) int {
	removed := 0
	for _, p := range []string{s.runtimeChatUploadPath(u.StorageKey), s.runtimeChatUploadPath(u.StorageKey) + ".md"} {
		switch err := os.Remove(p); {
		case err == nil:
			removed++
		case errors.Is(err, os.ErrNotExist):
			// 已经清理过：回收任务每轮都会重扫历史行，这里幂等。
		default:
			s.log.Warn("删除网页对话附件失败", "path", p, "err", err)
		}
	}
	return removed
}

// runtimeChatUploadFileName 把浏览器给的文件名收成一个能直接当路径最后一段的名字：去掉目录、控制字符和路径分隔符，
// 不以点开头，按字符截短但保留扩展名（转换按扩展名判断格式）。
func runtimeChatUploadFileName(name string) string {
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == utf8.RuneError || unicode.IsControl(r) || strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimSpace(strings.TrimLeft(b.String(), "."))
	ext := path.Ext(name)
	if utf8.RuneCountInString(ext) > 16 {
		ext = ""
	}
	if runes := []rune(strings.TrimSuffix(name, ext)); len(runes) > runtimeChatUploadName {
		name = string(runes[:runtimeChatUploadName]) + ext
	}
	if strings.TrimSuffix(name, ext) == "" {
		name = "document" + ext
	}
	return name
}

func (s *Server) handleRuntimeChatUpload(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	owner := currentUser(r).ID
	conversation, err := s.store.RuntimeChatConversation(r.Context(), runtimeID(r, "id"), owner)
	if err != nil {
		s.runtimeChatError(w, r, err)
		return
	}
	ok, err := s.runtimeChatEmployee(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !ok {
		s.runtimeChatError(w, r, store.ErrRuntimeChatBotUnavailable)
		return
	}
	tooLarge := func() {
		writeErrCode(w, http.StatusRequestEntityTooLarge, codeRuntimeChatUploadTooLarge, map[string]any{"max_mb": runtimeChatUploadMax >> 20},
			fmt.Sprintf("单个文件不能超过 %d MB", runtimeChatUploadMax>>20))
	}
	r.Body = http.MaxBytesReader(w, r.Body, runtimeChatUploadMax+1<<20)
	file, header, err := r.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			tooLarge()
			return
		}
		writeErr(w, http.StatusBadRequest, "请用 multipart/form-data 的 file 字段上传文件")
		return
	}
	defer file.Close()
	// FormFile 解析的是 requireUser 换过 context 的请求副本，net/http 只清理原请求的临时文件
	defer r.MultipartForm.RemoveAll()
	if header.Size > runtimeChatUploadMax {
		tooLarge()
		return
	}
	data, err := io.ReadAll(io.LimitReader(file, runtimeChatUploadMax+1))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "读取上传的文件失败")
		return
	}
	if len(data) > runtimeChatUploadMax {
		tooLarge()
		return
	}
	name := runtimeChatUploadFileName(header.Filename)
	// 先清过期的，免得一天前扔下的附件一直占着额度
	s.pruneRuntimeChatUploads(r.Context())
	pending, err := s.store.RuntimeChatPendingUploadBytes(r.Context(), owner)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if pending+int64(len(data)) > runtimeChatUploadPendingMax {
		writeErrCode(w, http.StatusTooManyRequests, codeRuntimeChatUploadQuota, map[string]any{"max_mb": runtimeChatUploadPendingMax >> 20},
			"还没发出的附件太多了，先发出或删掉一些再上传")
		return
	}
	release, ok := beginKBConvert()
	if !ok {
		writeErrCode(w, http.StatusTooManyRequests, codeRuntimeChatUploadBusy, nil, "同时在转换的文件太多，请稍后再试")
		return
	}
	conv, err := convertKB(r.Context(), name, data, kb.Options{})
	release()
	if err != nil {
		if errors.Is(err, kb.ErrUnsupported) || !slices.Contains(kb.Formats, strings.ToLower(path.Ext(name))) {
			writeErrCode(w, http.StatusBadRequest, codeRuntimeChatUploadUnsupported, nil, err.Error())
			return
		}
		writeErrCode(w, http.StatusBadRequest, codeRuntimeChatUploadUnreadable, nil, "读不出这个文件的内容："+err.Error())
		return
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		s.internalErr(w, r, err)
		return
	}
	u := &store.RuntimeChatUpload{
		OwnerID: owner, ConversationID: conversation.ID, Name: name, Size: int64(len(data)),
		Format: conv.Info.Format, Pages: conv.Info.Pages, TextBytes: int64(len(conv.Markdown)), StorageKey: hex.EncodeToString(raw[:]),
	}
	if err := os.MkdirAll(filepath.Join(s.opts.DataDir, runtimeChatUploadsDir), 0o700); err != nil {
		s.internalErr(w, r, err)
		return
	}
	if err := os.WriteFile(s.runtimeChatUploadPath(u.StorageKey), data, 0o600); err != nil {
		s.internalErr(w, r, err)
		return
	}
	if err := os.WriteFile(s.runtimeChatUploadPath(u.StorageKey)+".md", []byte(conv.Markdown), 0o600); err != nil {
		s.removeRuntimeChatUpload(*u)
		s.internalErr(w, r, err)
		return
	}
	if err := s.store.CreateRuntimeChatUpload(r.Context(), u); err != nil {
		s.removeRuntimeChatUpload(*u)
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

// pruneRuntimeChatUploads 顺手清掉上传了一天还没发出去的附件（关了页面、拖错文件没删）。
func (s *Server) pruneRuntimeChatUploads(ctx context.Context) {
	removed, err := s.store.PruneRuntimeChatUploads(ctx, time.Now().UTC().Add(-runtimeChatUploadTTL))
	if err != nil {
		s.log.Warn("清理网页对话附件失败", "err", err)
	}
	for _, u := range removed {
		s.removeRuntimeChatUpload(u)
	}
}

// purgeRuntimeChatUploads 回收挂在一轮已经结束（成功/失败/取消）且结束超过 grace 的附件：只删原件和正文，
// DB 行保留，历史页还显示文件名、大小和格式。分页读，避免一次把全部历史附件读进内存；文件已经不在
// （上一轮回收删过、磁盘被手动清过）不算失败，返回真正删掉的文件数。
func (s *Server) purgeRuntimeChatUploads(ctx context.Context, grace time.Duration) int {
	before := time.Now().UTC().Add(-grace)
	removed := 0
	var afterID int64
	for ctx.Err() == nil {
		list, err := s.store.RuntimeChatUploadsFinishedBefore(ctx, before, afterID, runtimeChatUploadPurgeBatch)
		if err != nil {
			s.log.Warn("列出要回收的网页对话附件失败", "err", err)
			return removed
		}
		if len(list) == 0 {
			return removed
		}
		for _, u := range list {
			removed += s.removeRuntimeChatUpload(u)
		}
		afterID = list[len(list)-1].ID
		if len(list) < runtimeChatUploadPurgeBatch {
			return removed
		}
	}
	return removed
}

// runRuntimeChatUploadGC 每小时回收一次网页对话的附件：过期的待发附件删行删文件（关了页面、拖错文件没发出去的），
// 已结束轮次的附件只删文件、DB 行留给历史页。多实例下由租约保证只有一个实例执行；启动时先跑一轮，
// 升级后立刻回收积压。
func (s *Server) runRuntimeChatUploadGC(ctx context.Context) {
	for {
		s.runLeased(ctx, leaseRuntimeChatUploadGC, func(ctx context.Context) {
			s.pruneRuntimeChatUploads(ctx)
			if n := s.purgeRuntimeChatUploads(ctx, runtimeChatUploadPurgeGrace); n > 0 {
				s.log.Info("回收了已结束轮次的网页对话附件文件", "files", n)
			}
		})
		timer := time.NewTimer(time.Hour)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Server) handleRuntimeChatUploadDelete(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	u, err := s.store.DeleteRuntimeChatUpload(r.Context(), currentUser(r).ID, runtimeID(r, "id"))
	if err != nil {
		if notFound(err) {
			writeErrCode(w, http.StatusNotFound, codeRuntimeChatUploadUnavailable, nil, "附件不存在或已经发出")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.removeRuntimeChatUpload(*u)
	w.WriteHeader(http.StatusNoContent)
}

// writeRuntimeChatUploads 把这一轮的附件写进机器人的工作目录 .uploads/<轮次>/，返回要加在提示词前面的说明（没有附件时为空）。
func (s *Server) writeRuntimeChatUploads(ctx context.Context, task *store.RuntimeTask, sandbox runtime.Sandbox, workDir string) (string, error) {
	if task.ChatConversationID == nil {
		return "", nil
	}
	byTask, err := s.store.RuntimeChatUploadsForTasks(ctx, []int64{task.ID})
	if err != nil {
		return "", err
	}
	uploads := byTask[task.ID]
	if len(uploads) == 0 {
		return "", nil
	}
	// 隐藏目录：一次性电脑上工作目录就是产物目录，隐藏路径不会被当成交付物收回文件库。
	rel := path.Join(runtimeChatUploadsWorkDir, strconv.FormatInt(task.ID, 10))
	setup, err := sandbox.Exec(ctx, runtime.Command{Args: []string{"mkdir", "-p", path.Join(workDir, rel)}, Timeout: 10 * time.Second})
	if err != nil || setup.ExitCode != 0 {
		return "", errors.New("preparing chat attachments failed")
	}
	var note strings.Builder
	note.WriteString("The employee attached these documents to the current message. They are in your working directory; next to each original is an extracted-text copy (same path plus .md). Read the .md copy to answer, and open the original only if you need something the text lost:\n")
	used := map[string]bool{}
	for _, u := range uploads {
		// 原件和正文副本（加 .md）都不能跟别的附件撞名：budget.csv 的正文和原件 budget.csv.md 是同一个路径
		name := u.Name
		if used[name] || used[name+".md"] {
			name = strconv.FormatInt(u.ID, 10) + "-" + name
		}
		used[name], used[name+".md"] = true, true
		for _, f := range []struct{ local, remote string }{
			{s.runtimeChatUploadPath(u.StorageKey), path.Join(workDir, rel, name)},
			{s.runtimeChatUploadPath(u.StorageKey) + ".md", path.Join(workDir, rel, name+".md")},
		} {
			data, err := os.ReadFile(f.local)
			if err != nil {
				return "", errors.New("reading chat attachment failed")
			}
			if err := sandbox.WriteFile(ctx, f.remote, data); err != nil {
				return "", errors.New("writing chat attachment failed")
			}
		}
		details := []string{strings.ToUpper(u.Format)}
		if u.Pages > 0 {
			details = append(details, fmt.Sprintf("%d pages", u.Pages))
		}
		details = append(details, fmt.Sprintf("%s, extracted text %s", runtimeChatSize(u.Size), runtimeChatSize(u.TextBytes)))
		fmt.Fprintf(&note, "- %s (%s)\n", path.Join(rel, name), strings.Join(details, ", "))
	}
	note.WriteString("\n")
	return note.String(), nil
}

func runtimeChatSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
