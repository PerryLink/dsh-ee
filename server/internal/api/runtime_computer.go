package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/runtime"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 每个机器人一台常驻云电脑。provider 实现了 runtime.PersistentProvider 且没有关闭常驻
// （DSH_EE_RUNTIME_PERSISTENT=off）时，机器人的定时 / 手动任务和网页对话都在同一台电脑里跑：
// 一轮结束后电脑保持开机，空闲超过 IdleTimeout 由 runCloudComputerReaper 休眠（OpenSandbox pause），
// 下一轮唤醒（resume），工作目录 runtimeWorkDir 和 DSH 主目录里的文件都在。
//
// 凭证：电脑创建时不带任何环境变量；每轮的短期令牌只通过 Exec 的环境变量传入，这一轮结束（释放电脑之前）
// 就吊销，所以休眠中的电脑里不会有有效凭证。
//
// 不支持休眠 / 唤醒的 provider 和不属于任何机器人的临时任务退回“每轮新建、跑完销毁”。

const (
	runtimeWorkDir = "/tmp/dsh-ee-work"
	runtimeHomeDir = "/tmp/dsh-ee-home"
	// 手动操作和回收器持有租约的时长。
	runtimeComputerOpLease  = 3 * time.Minute
	runtimeComputerReapTick = 30 * time.Second
)

// runtimeComputerNote 在常驻电脑上加在提示词最前面，告诉 DSH 哪些文件会留到下一轮。
const runtimeComputerNote = "You have your own persistent cloud computer. Your working directory " + runtimeWorkDir +
	" keeps its files between turns and scheduled runs, so you can save notes and work files there and read them later. " +
	"Files under " + runtimeOutputDir + " are delivered to the employee and cleared at the start of every turn.\n\n"

// runtimeComputerWait：同一台电脑被另一个任务占用时，新任务最多等这么久。测试里会调短。
var runtimeComputerWait = 90 * time.Second

var errCloudDisk = errors.New("the bot's cloud computer is over its disk limit")

// cloudComputers 返回可以常驻的 provider；不支持或已关闭时第二个返回值为假。
func (s *Server) cloudComputers() (runtime.PersistentProvider, bool) {
	p, ok := s.opts.Runtime.Provider.(runtime.PersistentProvider)
	return p, ok && s.opts.Runtime.Computer.Persistent
}

// cloudTaskBot 找出任务所属的机器人：定时 / 手动任务直接带 BotID，网页对话轮次的机器人记在对话上。
// 只有机器人以任务的运行身份运行时才能使用常驻电脑。不属于机器人的任务返回 0。
func (s *Server) cloudTaskBot(ctx context.Context, task *store.RuntimeTask) (int64, error) {
	var botID int64
	switch {
	case task.BotID != nil:
		botID = *task.BotID
	case task.ChatConversationID != nil:
		c, err := s.store.RuntimeChatConversation(ctx, *task.ChatConversationID, task.OwnerID)
		if err != nil {
			return 0, errors.New("checking the cloud task conversation failed")
		}
		botID = c.BotID
	default:
		return 0, nil
	}
	b, err := s.store.RuntimeBot(ctx, botID)
	if err != nil {
		return 0, errors.New("checking the cloud task bot failed")
	}
	if b.RunAsID != task.RunAsID || b.OwnerID != task.OwnerID {
		return 0, errCloudEmployee
	}
	return botID, nil
}

// cloudComputer 是一轮任务拿到的电脑。persistent 为假时是一台临时电脑，close 会销毁它。
type cloudComputer struct {
	sandbox    runtime.Sandbox
	persistent bool
	close      func(release bool)
}

// openCloudComputer 为任务准备电脑：机器人有常驻电脑就唤醒它（没有就新建一台），否则新建临时电脑。
func (s *Server) openCloudComputer(ctx context.Context, task *store.RuntimeTask, spec runtime.SandboxSpec) (*cloudComputer, error) {
	pp, ok := s.cloudComputers()
	if ok {
		botID, err := s.cloudTaskBot(ctx, task)
		if err != nil {
			return nil, err
		}
		if botID != 0 {
			return s.openPersistentComputer(ctx, pp, task, botID, spec)
		}
	}
	sandbox, err := s.opts.Runtime.Provider.Create(ctx, spec)
	if err != nil {
		return nil, errors.New("creating cloud sandbox failed")
	}
	return &cloudComputer{sandbox: sandbox, close: func(_ bool) {
		c, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if err := sandbox.Destroy(c); err != nil {
			s.log.Warn("销毁云任务沙箱失败", "task_id", task.ID, "err", err)
		}
	}}, nil
}

func (s *Server) acquireComputerWithin(ctx context.Context, botID int64, holder string, until time.Time, wait time.Duration) (*store.RuntimeBotComputer, error) {
	giveUp := time.Now().Add(wait)
	for {
		c, err := s.store.AcquireRuntimeBotComputer(ctx, botID, holder, until)
		if !errors.Is(err, store.ErrRuntimeComputerBusy) || !time.Now().Before(giveUp) {
			return c, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (s *Server) openPersistentComputer(ctx context.Context, pp runtime.PersistentProvider, task *store.RuntimeTask, botID int64, spec runtime.SandboxSpec) (*cloudComputer, error) {
	holder := fmt.Sprintf("task:%d:%d", task.ID, task.Attempts)
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Duration(task.TimeoutSeconds) * time.Second)
	}
	c, err := s.acquireComputerWithin(ctx, botID, holder, deadline.Add(30*time.Second), runtimeComputerWait)
	if err != nil {
		if errors.Is(err, store.ErrRuntimeComputerBusy) || ctx.Err() != nil {
			return nil, err
		}
		return nil, errors.New("reserving the bot's cloud computer failed")
	}
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	release := func() {
		bc, stop := bg()
		defer stop()
		if err := s.store.ReleaseRuntimeBotComputer(bc, botID, holder); err != nil {
			s.log.Warn("释放机器人电脑失败", "bot_id", botID, "err", err)
		}
	}
	limit := s.opts.Runtime.Computer.DiskLimitBytes
	if c.SandboxID != "" && limit > 0 && c.DiskBytes > limit {
		release()
		return nil, fmt.Errorf("%w (%d MB used of %d MB); reset the computer from the bot panel or ask an administrator to reclaim it",
			errCloudDisk, c.DiskBytes>>20, limit>>20)
	}
	save := func(touched, done bool) error {
		bc, stop := bg()
		defer stop()
		return s.store.UpdateRuntimeBotComputer(bc, c, holder, touched, done)
	}
	var box runtime.PersistentSandbox
	if c.SandboxID != "" {
		if c.State != store.RuntimeComputerRunning {
			c.State = store.RuntimeComputerWaking
			_ = save(false, false)
		}
		box, err = pp.Wake(ctx, c.SandboxID)
		if errors.Is(err, runtime.ErrSandboxGone) {
			// 电脑已经不在了（被外部删除或过期）：当作被重置，新建一台。
			s.log.Warn("机器人电脑已不存在，重新新建", "bot_id", botID, "task_id", task.ID)
			c.SandboxID, box, err = "", nil, nil
		}
		if err != nil {
			_ = save(false, true)
			return nil, errors.New("waking the bot's cloud computer failed")
		}
	}
	if box == nil {
		c.SandboxID, c.State, c.DiskBytes = "", store.RuntimeComputerWaking, 0
		_ = save(false, false)
		if box, err = pp.CreatePersistent(ctx, runtime.SandboxSpec{Image: spec.Image, AllowedHosts: spec.AllowedHosts}); err != nil {
			c.SandboxID = ""
			_ = save(false, true)
			return nil, errors.New("creating cloud sandbox failed")
		}
		c.SandboxID = box.ID()
	}
	c.State = store.RuntimeComputerRunning
	if err := save(true, false); err != nil {
		// 租约丢了（例如被管理员回收）：不再碰这台电脑。
		return nil, errors.New("the bot's cloud computer was taken over")
	}
	return &cloudComputer{sandbox: box, persistent: true, close: func(release bool) {
		bc, stop := bg()
		defer stop()
		if disk, err := computerDiskUsage(bc, box); err == nil {
			c.DiskBytes = disk
		} else {
			s.log.Warn("统计机器人电脑磁盘用量失败", "bot_id", botID, "err", err)
		}
		c.State = store.RuntimeComputerRunning
		if err := s.store.UpdateRuntimeBotComputer(bc, c, holder, true, release); err != nil {
			s.log.Warn("保存机器人电脑状态失败", "bot_id", botID, "err", err)
		}
	}}, nil
}

// computerDiskUsage 统计工作目录和 DSH 主目录的用量（du -sk，各行相加）。
func computerDiskUsage(ctx context.Context, box runtime.Sandbox) (int64, error) {
	r, err := box.Exec(ctx, runtime.Command{Args: []string{"du", "-sk", runtimeWorkDir, runtimeHomeDir}, Timeout: 20 * time.Second})
	if err != nil {
		return 0, err
	}
	if r.ExitCode != 0 {
		return 0, fmt.Errorf("du exited with %d", r.ExitCode)
	}
	var total int64
	for _, line := range strings.Split(strings.TrimSpace(r.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		kb, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || kb < 0 {
			return 0, errors.New("unexpected du output")
		}
		total += kb << 10
	}
	return total, nil
}

// runCloudComputerReaper 定时让空闲的电脑休眠，回收休眠超过 Retention 的电脑。
// 只要 provider 支持常驻就运行：即使后来关闭了常驻，以前留下的电脑也会被休眠和回收。
func (s *Server) runCloudComputerReaper(ctx context.Context) {
	if _, ok := s.opts.Runtime.Provider.(runtime.PersistentProvider); !ok {
		return
	}
	tick := time.NewTicker(runtimeComputerReapTick)
	defer tick.Stop()
	for {
		s.reapCloudComputers(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) reapCloudComputers(ctx context.Context) {
	policy := s.opts.Runtime.Computer
	idle := policy.IdleTimeout
	if idle <= 0 || !policy.Persistent {
		// 关闭常驻后，留下的电脑一旦空闲就休眠。
		idle = time.Minute
	}
	t := time.Now().UTC()
	var retainBefore time.Time
	if policy.Retention > 0 {
		retainBefore = t.Add(-policy.Retention)
	}
	list, err := s.store.IdleRuntimeBotComputers(ctx, t.Add(-idle), retainBefore)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("读取空闲机器人电脑失败", "err", err)
		}
		return
	}
	for _, c := range list {
		action := computerHibernate
		// 列表和取得租约之间电脑可能刚被用过：拿到租约后按最新状态再判断一次。
		guard := func(c *store.RuntimeBotComputer) bool {
			return c.State != store.RuntimeComputerHibernated && c.LastUsedAt.Before(t.Add(-idle))
		}
		if c.State == store.RuntimeComputerHibernated {
			action = computerReset
			guard = func(c *store.RuntimeBotComputer) bool {
				return c.State == store.RuntimeComputerHibernated && c.HibernatedAt != nil && c.HibernatedAt.Before(retainBefore)
			}
		}
		_, err := s.cloudComputerAction(ctx, c.BotID, action, "reaper", guard)
		if err != nil && errors.Is(err, errComputerSkipped) {
			continue
		}
		if err != nil &&
			!errors.Is(err, store.ErrRuntimeComputerBusy) && ctx.Err() == nil {
			s.log.Warn("处理空闲机器人电脑失败", "bot_id", c.BotID, "action", action, "err", err)
			continue
		}
		if err == nil && action == computerReset {
			s.auditSystem(ctx, "runtime.computer.reclaim", strconv.FormatInt(c.BotID, 10), map[string]any{"reason": "retention"})
		}
	}
}

const (
	computerHibernate = "hibernate"
	computerReset     = "reset"
)

// errComputerProvider 包装沙箱服务的失败，接口按 502 回答。
type errComputerProvider struct{ err error }

func (e errComputerProvider) Error() string {
	return "cloud computer provider failed: " + e.err.Error()
}

// cloudComputerAction 手动（或回收器）休眠 / 重置一台电脑。电脑被占用时返回 store.ErrRuntimeComputerBusy。
// 重置会删除整台电脑，工作目录里的文件随之删除；下一轮任务会新建一台。
// guard 不为空时，拿到租约后先用它检查最新状态，不满足就什么也不做并返回 errComputerSkipped。
func (s *Server) cloudComputerAction(ctx context.Context, botID int64, action, who string, guard func(*store.RuntimeBotComputer) bool) (*store.RuntimeBotComputer, error) {
	pp, ok := s.opts.Runtime.Provider.(runtime.PersistentProvider)
	if !ok {
		return nil, errComputerUnsupported
	}
	holder := who + ":" + secret.Token()[:12]
	ctx, cancel := context.WithTimeout(ctx, runtimeComputerOpLease-30*time.Second)
	defer cancel()
	c, err := s.store.AcquireRuntimeBotComputer(ctx, botID, holder, time.Now().Add(runtimeComputerOpLease))
	if err != nil {
		return nil, err
	}
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	finish := func(touched bool) error {
		bc, stop := bg()
		defer stop()
		err := s.store.UpdateRuntimeBotComputer(bc, c, holder, touched, true)
		c.Busy = false
		return err
	}
	if guard != nil && !guard(c) {
		bc, stop := bg()
		defer stop()
		_ = s.store.ReleaseRuntimeBotComputer(bc, botID, holder)
		return nil, errComputerSkipped
	}
	if c.SandboxID == "" {
		return c, finish(false)
	}
	switch action {
	case computerHibernate:
		if c.State == store.RuntimeComputerHibernated {
			return c, finish(false)
		}
		c.State = store.RuntimeComputerHibernating
		bc, stop := bg()
		_ = s.store.UpdateRuntimeBotComputer(bc, c, holder, false, false)
		stop()
		err := pp.Hibernate(ctx, c.SandboxID)
		switch {
		case errors.Is(err, runtime.ErrSandboxGone):
			c.SandboxID = ""
		case err != nil:
			// 休眠失败：电脑还开着，空闲计时重新开始，过一个空闲周期再试。
			c.State = store.RuntimeComputerRunning
			_ = finish(true)
			return nil, errComputerProvider{err}
		default:
			c.State = store.RuntimeComputerHibernated
		}
		return c, finish(false)
	case computerReset:
		if err := pp.Remove(ctx, c.SandboxID); err != nil && !errors.Is(err, runtime.ErrSandboxGone) {
			_ = finish(false)
			return nil, errComputerProvider{err}
		}
		c.SandboxID = ""
		return c, finish(false)
	}
	_ = finish(false)
	return nil, errors.New("unknown computer action")
}

var (
	errComputerUnsupported = errors.New("cloud computers are not persistent with this runtime")
	errComputerSkipped     = errors.New("cloud computer changed before the action")
)

// runtimeComputerView 是给员工和管理员看的电脑状态。
type runtimeComputerView struct {
	// Persistent 为假：这个运行时每轮都是新电脑，没有休眠 / 唤醒，State 恒为 none。
	Persistent         bool       `json:"persistent"`
	State              string     `json:"state"`
	Busy               bool       `json:"busy"`
	DiskBytes          int64      `json:"disk_bytes"`
	DiskLimitBytes     int64      `json:"disk_limit_bytes"`
	IdleTimeoutSeconds int64      `json:"idle_timeout_seconds"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	HibernatedAt       *time.Time `json:"hibernated_at,omitempty"`
}

func (s *Server) computerView(c *store.RuntimeBotComputer) runtimeComputerView {
	_, persistent := s.cloudComputers()
	policy := s.opts.Runtime.Computer
	v := runtimeComputerView{Persistent: persistent, State: store.RuntimeComputerNone, DiskLimitBytes: policy.DiskLimitBytes,
		IdleTimeoutSeconds: int64(policy.IdleTimeout / time.Second)}
	if c != nil {
		v.State, v.Busy, v.DiskBytes, v.HibernatedAt = c.State, c.Busy, c.DiskBytes, c.HibernatedAt
		if c.SandboxID == "" && !c.Busy {
			// 新建到一半就中断（持有者崩溃、租约过期）的记录：其实还没有电脑。
			v.State, v.DiskBytes = store.RuntimeComputerNone, 0
		}
		if c.State != store.RuntimeComputerNone {
			used := c.LastUsedAt
			v.LastUsedAt = &used
		}
	}
	return v
}

func (s *Server) botComputerView(ctx context.Context, botID int64) (runtimeComputerView, error) {
	c, err := s.store.RuntimeBotComputer(ctx, botID)
	if notFound(err) {
		return s.computerView(nil), nil
	}
	if err != nil {
		return runtimeComputerView{}, err
	}
	return s.computerView(c), nil
}

func (s *Server) runtimeComputerError(w http.ResponseWriter, r *http.Request, err error) {
	var provider errComputerProvider
	switch {
	case errors.Is(err, store.ErrRuntimeComputerBusy):
		writeErrCode(w, http.StatusConflict, codeRuntimeComputerBusy, nil, "云电脑正在工作或正在被操作，请稍后再试")
	case errors.Is(err, errComputerUnsupported):
		writeErrCode(w, http.StatusConflict, codeRuntimeComputerUnsupported, nil, "当前运行时不支持常驻云电脑，每轮任务都会使用一台新电脑")
	case errors.As(err, &provider):
		s.log.Warn("云电脑操作失败", "err", err)
		writeErrCode(w, http.StatusBadGateway, codeRuntimeComputerProviderFailed, nil, "沙箱服务操作失败，请稍后再试")
	default:
		s.internalErr(w, r, err)
	}
}

// handleRuntimeChatComputer 是员工本人对自己机器人电脑的手动操作：hibernate（立即休眠）或 reset（删除重建）。
func (s *Server) handleRuntimeChatComputer(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	action := r.PathValue("action")
	if action != computerHibernate && action != computerReset {
		writeErrCode(w, http.StatusNotFound, codeRuntimeChatBotUnavailable, nil, "机器人不存在或未开启网页对话")
		return
	}
	employee, err := s.runtimeChatEmployee(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	botID := runtimeID(r, "id")
	// 与资料页同一套可见性：不属于本人、不存在的机器人一律 404。
	if _, err := s.store.RuntimeChatBotProfile(r.Context(), currentUser(r).ID, botID, employee); err != nil {
		if notFound(err) {
			writeErrCode(w, http.StatusNotFound, codeRuntimeChatBotUnavailable, nil, "机器人不存在或未开启网页对话")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	if _, ok := s.cloudComputers(); !ok {
		s.runtimeComputerError(w, r, errComputerUnsupported)
		return
	}
	c, err := s.cloudComputerAction(r.Context(), botID, action, "owner", nil)
	if err != nil {
		s.runtimeComputerError(w, r, err)
		return
	}
	s.audit(r, "runtime.computer."+action, strconv.FormatInt(botID, 10), nil)
	writeJSON(w, http.StatusOK, s.computerView(c))
}

type runtimeAdminComputer struct {
	BotID   int64  `json:"bot_id"`
	BotName string `json:"bot_name"`
	OwnerID int64  `json:"owner_id"`
	runtimeComputerView
}

// handleRuntimeComputers 列出所有机器人电脑（管理员看占用和回收用），附带当前的资源上限。
func (s *Server) handleRuntimeComputers(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	list, err := s.store.RuntimeBotComputers(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	out := make([]runtimeAdminComputer, 0, len(list))
	for i := range list {
		c := &list[i]
		out = append(out, runtimeAdminComputer{BotID: c.BotID, BotName: c.BotName, OwnerID: c.OwnerID, runtimeComputerView: s.computerView(c)})
	}
	_, persistent := s.cloudComputers()
	policy := s.opts.Runtime.Computer
	writeJSON(w, http.StatusOK, map[string]any{
		"persistent": persistent, "idle_timeout_seconds": int64(policy.IdleTimeout / time.Second),
		"disk_limit_bytes": policy.DiskLimitBytes, "retention_seconds": int64(policy.Retention / time.Second),
		"computers": out,
	})
}

// handleRuntimeComputerAdmin 是管理员对任一机器人电脑的操作：POST .../hibernate 休眠，DELETE 回收（删除整台电脑）。
func (s *Server) handleRuntimeComputerAdmin(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	action := computerReset
	if r.Method == http.MethodPost {
		action = computerHibernate
	}
	botID := runtimeID(r, "id")
	if _, err := s.store.RuntimeBotComputer(r.Context(), botID); err != nil {
		if notFound(err) {
			writeErrCode(w, http.StatusNotFound, codeRuntimeComputerNotFound, nil, "这个机器人没有云电脑")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	c, err := s.cloudComputerAction(r.Context(), botID, action, "admin", nil)
	if err != nil {
		s.runtimeComputerError(w, r, err)
		return
	}
	event := "runtime.computer.hibernate"
	if action == computerReset {
		event = "runtime.computer.reclaim"
	}
	s.audit(r, event, strconv.FormatInt(botID, 10), nil)
	writeJSON(w, http.StatusOK, s.computerView(c))
}
