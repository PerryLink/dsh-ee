package api

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

const (
	runtimeTokenPrefix = accessTokenPrefix + "rt_"
	runtimeTimeout     = 5 * time.Minute
	runtimeFeishuOwner = "feishu_owner"
)

type runtimeTokenRouteKey struct{}

func runtimeTokenRoute(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/models", "POST /v1/chat/completions", "POST /mcp", "GET /mcp":
			r = r.WithContext(context.WithValue(r.Context(), runtimeTokenRouteKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) runtimeAvailable(w http.ResponseWriter) bool {
	if s.opts.Runtime.Provider == nil || s.opts.DemoSite != nil {
		writeErr(w, http.StatusNotFound, "云任务未启用")
		return false
	}
	return true
}

func runtimeOwner(r *http.Request) int64 {
	if currentAccess(r).Has(rbac.SettingsManage) {
		return 0
	}
	return currentUser(r).ID
}

func runtimeID(r *http.Request, name string) int64 {
	id, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id
}

func (s *Server) runtimeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case notFound(err):
		writeErr(w, http.StatusNotFound, "任务、机器人或产物不存在")
	case errors.Is(err, store.ErrConflict):
		writeErr(w, http.StatusConflict, "任务状态或幂等键冲突")
	case errors.Is(err, store.ErrRuntimeQueueFull):
		writeErr(w, http.StatusTooManyRequests, "排队或运行中的云任务已达上限")
	default:
		s.internalErr(w, r, err)
	}
}

func (s *Server) runtimeEmployee(ctx context.Context, id int64) (*store.User, error) {
	u, err := s.store.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	access, err := s.store.UserAccess(ctx, id)
	if err != nil {
		return nil, err
	}
	if u.Disabled || u.AccountType != store.AccountHuman || !access.Has(rbac.LLMUse) || !access.Has(rbac.PortalView) || rbac.HasAdminPerms(access) {
		return nil, store.ErrNotFound
	}
	return u, nil
}

func (s *Server) handleRuntimeSubmit(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	var in struct {
		Prompt         string `json:"prompt"`
		IdempotencyKey string `json:"idempotency_key"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.TimeoutSeconds == 0 {
		in.TimeoutSeconds = int(runtimeTimeout.Seconds())
	}
	if len(in.Prompt) == 0 || len(in.Prompt) > 8000 || strings.ContainsRune(in.Prompt, 0) || len(in.IdempotencyKey) > 100 || in.TimeoutSeconds < 15 || in.TimeoutSeconds > int(runtimeTimeout.Seconds()) {
		writeErr(w, http.StatusBadRequest, "任务参数无效")
		return
	}
	u, err := s.runtimeEmployee(r.Context(), currentUser(r).ID)
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	key := ""
	if in.IdempotencyKey != "" {
		key = "user:" + in.IdempotencyKey
	}
	task, err := s.store.EnqueueRuntimeTask(r.Context(), store.RuntimeTask{OwnerID: u.ID, RunAsID: u.ID, Prompt: in.Prompt, IdempotencyKey: key, TimeoutSeconds: in.TimeoutSeconds})
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, task)
}

func (s *Server) handleRuntimeTasks(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	tasks, err := s.store.RuntimeTasks(r.Context(), runtimeOwner(r))
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleRuntimeTask(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	task, err := s.store.RuntimeTask(r.Context(), runtimeID(r, "id"), runtimeOwner(r))
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleRuntimeEvents(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	id, owner := runtimeID(r, "id"), runtimeOwner(r)
	if _, err := s.store.RuntimeTask(r.Context(), id, owner); err != nil {
		s.runtimeError(w, r, err)
		return
	}
	events, err := s.store.RuntimeTaskEvents(r.Context(), id, owner)
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleRuntimeCancel(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	id, owner := runtimeID(r, "id"), runtimeOwner(r)
	if _, err := s.store.RuntimeTask(r.Context(), id, owner); err != nil {
		s.runtimeError(w, r, err)
		return
	}
	if err := s.store.CancelRuntimeTask(r.Context(), id, owner); err != nil {
		s.runtimeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRuntimeArtifacts(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	id, owner := runtimeID(r, "id"), runtimeOwner(r)
	task, err := s.store.RuntimeTask(r.Context(), id, owner)
	if notFound(err) && owner != 0 {
		// 团队线程里别的成员触发的一轮：此刻在这个团队里就能看和下载它的产物，团队外 403。
		task, err = s.store.RuntimeTeamChatTask(r.Context(), id, owner)
		if errors.Is(err, store.ErrRuntimeChatTeamForbidden) {
			writeErrCode(w, http.StatusForbidden, codeRuntimeChatTeamForbidden, nil, "这个机器人没有共享给你所在的部门")
			return
		}
	}
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	files, err := s.store.RuntimeArtifacts(r.Context(), id, task.OwnerID)
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	if r.PathValue("artifact") == "" {
		writeJSON(w, http.StatusOK, files)
		return
	}
	for _, a := range files {
		if a.ID != runtimeID(r, "artifact") {
			continue
		}
		f, err := os.Open(filepath.Join(s.opts.DataDir, "runtime-artifacts", a.StorageKey))
		if err != nil {
			s.runtimeError(w, r, err)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Name}))
		w.Header().Set("Cache-Control", "private, no-store")
		http.ServeContent(w, r, a.Name, a.CreatedAt, f)
		return
	}
	s.runtimeError(w, r, store.ErrNotFound)
}

func (s *Server) handleRuntimeBots(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	bots, err := s.store.RuntimeBots(r.Context(), false)
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, bots)
}

func (s *Server) handleRuntimeSaveBot(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	var id int64
	if r.Method == http.MethodPut {
		var ok bool
		if id, ok = pathID(w, r); !ok {
			return
		}
	}
	// web_chat_enabled 单独用指针接：老的管理端请求不带这个字段，此时新建按关闭写入，
	// PUT 由存储层在 UPDATE 里原子地保留现值（见 SaveRuntimeBot），这里不先读后写。
	var in struct {
		store.RuntimeBot
		WebChatEnabled *bool `json:"web_chat_enabled"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	bot := in.RuntimeBot
	if bot.DeliveryTarget == "" {
		bot.DeliveryTarget = runtimeFeishuOwner
	}
	bot.ID = id
	bot.Name, bot.Prompt, bot.Schedule = strings.TrimSpace(bot.Name), strings.TrimSpace(bot.Prompt), strings.TrimSpace(bot.Schedule)
	_, _, validSchedule := runtimeDailySchedule(bot.Schedule)
	if bot.Name == "" || utf8.RuneCountInString(bot.Name) > 128 || bot.Prompt == "" || len(bot.Prompt) > 8000 || strings.ContainsRune(bot.Prompt, 0) ||
		!validSchedule || !bot.ReadOnly || bot.OwnerID != bot.RunAsID || bot.RunAsID <= 0 || bot.DeliveryTarget != runtimeFeishuOwner {
		writeErr(w, http.StatusBadRequest, "机器人必须以所有者身份运行，只读，且使用 UTC 每日 cron（分钟 小时 * * *）")
		return
	}
	// 显式打开网页对话与打开定时一样，要求运行身份此刻是合格员工；省略字段的保留不算。
	if id == 0 || bot.Enabled || in.WebChatEnabled != nil && *in.WebChatEnabled {
		if _, err := s.runtimeEmployee(r.Context(), bot.RunAsID); err != nil {
			s.runtimeError(w, r, err)
			return
		}
	}
	if err := s.store.SaveRuntimeBot(r.Context(), &bot, in.WebChatEnabled); err != nil {
		s.runtimeError(w, r, err)
		return
	}
	s.audit(r, "runtime.bot.save", strconv.FormatInt(bot.ID, 10), nil)
	stored, err := s.store.RuntimeBot(r.Context(), bot.ID)
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, stored)
}

func (s *Server) handleRuntimeRunBot(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	bot, err := s.store.RuntimeBot(r.Context(), runtimeID(r, "id"))
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	task, err := s.enqueueCloudBot(r.Context(), bot, "")
	if err != nil {
		s.runtimeError(w, r, err)
		return
	}
	s.audit(r, "runtime.bot.run", strconv.FormatInt(bot.ID, 10), nil)
	writeJSON(w, http.StatusAccepted, task)
}

func runtimeDailySchedule(schedule string) (hour, minute int, ok bool) {
	fields := strings.Fields(schedule)
	if len(fields) != 5 || strings.Join(fields[2:], " ") != "* * *" {
		return 0, 0, false
	}
	minute, minErr := strconv.Atoi(fields[0])
	hour, hourErr := strconv.Atoi(fields[1])
	return hour, minute, minErr == nil && hourErr == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24
}

func (s *Server) enqueueCloudBot(ctx context.Context, b *store.RuntimeBot, slot string) (*store.RuntimeTask, error) {
	if !b.ReadOnly || b.OwnerID != b.RunAsID {
		return nil, store.ErrConflict
	}
	if _, err := s.runtimeEmployee(ctx, b.RunAsID); err != nil {
		return nil, err
	}
	key := ""
	if slot != "" {
		key = fmt.Sprintf("bot:%d:%s", b.ID, slot)
	}
	task := store.RuntimeTask{OwnerID: b.OwnerID, RunAsID: b.RunAsID, BotID: &b.ID, Prompt: b.Prompt,
		IdempotencyKey: key, TimeoutSeconds: int(runtimeTimeout.Seconds())}
	if slot != "" {
		return s.store.EnqueueScheduledRuntimeTask(ctx, task)
	}
	return s.store.EnqueueRuntimeTask(ctx, task)
}

func (s *Server) runCloudScheduler(ctx context.Context) {
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		now := time.Now().UTC()
		bots, err := s.store.RuntimeBots(ctx, true)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("读取云任务机器人失败", "err", err)
		}
		for _, b := range bots {
			hour, minute, valid := runtimeDailySchedule(b.Schedule)
			if valid && hour == now.Hour() && minute == now.Minute() {
				if _, err := s.enqueueCloudBot(ctx, &b, now.Format("2006-01-02T15:04")); err != nil &&
					!errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrRuntimeBotBusy) && !errors.Is(err, store.ErrRuntimeQueueFull) {
					s.log.Warn("机器人定时任务未入队", "bot_id", b.ID, "err", err)
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
