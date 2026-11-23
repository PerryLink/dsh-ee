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

// 每个所有者一台常驻云电脑（docs/dev/bot-delegation.md）。provider 实现了 runtime.PersistentProvider 且没有关闭常驻
// （DSH_EE_RUNTIME_PERSISTENT=off）时：
//   - 一位员工的所有个人机器人（定时 / 手动任务和网页对话）共用他的一台电脑（owner=user）；
//   - 团队对话的轮次用团队（部门）的电脑（owner=team），不放在任何个人的电脑上（见 cloudTaskComputer）。
//
// 一轮结束后电脑保持开机，空闲超过 IdleTimeout 由 runCloudComputerReaper 休眠（OpenSandbox pause），
// 下一轮唤醒（resume）。工作目录 runtimeWorkDir 下每个机器人有自己的子目录（bots/<id>，也是它的当前目录），
// 另有一个共享目录 shared/，同一台电脑上的机器人都能读写；DSH 主目录按机器人分开。
//
// 并发：同一台电脑同一时间只跑一轮（沿用 DB 租约排队），不让多个 run 同时 exec。原因：
//   - 凭证：团队电脑上相邻两轮可能是两个人（每轮以发消息的人运行），同时 exec 时一个人的令牌会和另一个人的进程
//     同处一台机器；排队后电脑里任何时候最多只有一个人的令牌，并且在释放电脑前吊销。
//   - 共享目录没有并发写冲突；休眠 / 重置 / 回收的判断和 321 完全一样。
//   代价是同一位员工的机器人一个接一个地跑，所以派任务是异步的：派出的一轮在发起的那一轮结束、释放电脑之后才入队
//   （见 runtime_delegation.go），不会出现发起方占着电脑等目标、目标又等电脑的死锁。
//
// 凭证：电脑创建时不带任何环境变量；每轮的短期令牌只通过 Exec 的环境变量传入，这一轮结束（释放电脑之前）
// 就吊销，所以休眠中的电脑里不会有有效凭证，下一轮（不论是谁）也拿不到上一轮的令牌。
//
// 不支持休眠 / 唤醒的 provider 和不属于任何机器人的临时任务退回“每轮新建、跑完销毁”。

const (
	runtimeWorkDir = "/tmp/dsh-ee-work"
	runtimeHomeDir = "/tmp/dsh-ee-home"
	// 手动操作和回收器持有租约的时长。
	runtimeComputerOpLease  = 3 * time.Minute
	runtimeComputerReapTick = 30 * time.Second
)

// 回收旧电脑时每次 provider 调用最多等这么久：provider 控制面挂起时不能拖住整个回收器
// （休眠 / retention 回收也跟着停摆，云成本泄漏）。测试会调短。
var runtimeComputerOrphanCallTimeout = 30 * time.Second

// 回收失败后的内存退避：30s → 1m → 2m……上限 runtimeComputerOrphanRetryMax，
// 避免每 30s 无退避地重试一次永远失败的删除。测试会把基数调小。
var runtimeComputerOrphanRetryBase = 30 * time.Second

const runtimeComputerOrphanRetryMax = 30 * time.Minute

// computerOrphanRetry 是一条旧电脑回收记录的重试状态：下次重试时间与连续失败次数。
type computerOrphanRetry struct {
	next     time.Time
	attempts int
}

const (
	// runtimeSharedDir 是同一台电脑上所有机器人共用的目录。
	runtimeSharedDir = runtimeWorkDir + "/shared"
	runtimeBotsDir   = runtimeWorkDir + "/bots"
	runtimeBotHomes  = runtimeHomeDir + "/bots"
)

func runtimeBotWorkDir(botID int64) string {
	return runtimeBotsDir + "/" + strconv.FormatInt(botID, 10)
}

// runtimeBotOutputDir 是机器人这一轮的产物目录（在它自己的工作目录下）。
func runtimeBotOutputDir(botID int64) string {
	return runtimeBotWorkDir(botID) + "/output"
}
func runtimeBotHomeDir(botID int64) string {
	return runtimeBotHomes + "/" + strconv.FormatInt(botID, 10)
}

// runtimeComputerNote 在常驻电脑上加在提示词最前面，告诉 DSH 哪些文件会留到下一轮、哪些目录和别的机器人共用。
func runtimeComputerNote(kind string, botID int64) string {
	whose := "the employee's persistent cloud computer, shared with the employee's other bots"
	if kind == store.RuntimeComputerOwnerTeam {
		whose = "your team's persistent cloud computer, shared with the team's other bots; every team member can see what is stored here"
	}
	return "You are running on " + whose + ". Your own working directory is " + runtimeBotWorkDir(botID) +
		"; it keeps its files between turns and scheduled runs. " + runtimeSharedDir + " is a shared folder every bot on this computer can read and write: " +
		"put files there that another bot should work on, and look there for files other bots left for you. Other bots' folders are under " + runtimeBotsDir + ". " +
		"Save notes and work files in your working directory (not in output) to read them later. " +
		"The deliverables directory is " + runtimeBotOutputDir(botID) + " (relative path: output/); files there are published to the employee's file library and cleared at the start of every turn.\n\n"
}

// runtimeComputerWait：同一台电脑被另一个任务占用时，新任务最多等这么久。测试里会调短。
var runtimeComputerWait = 90 * time.Second

var errCloudDisk = errors.New("the cloud computer is over its disk limit")

// cloudComputers 返回可以常驻的 provider；不支持或已关闭时第二个返回值为假。
func (s *Server) cloudComputers() (runtime.PersistentProvider, bool) {
	p, ok := s.opts.Runtime.Provider.(runtime.PersistentProvider)
	return p, ok && s.opts.Runtime.Computer.Persistent
}

func (s *Server) runtimeTeamComputer(ctx context.Context, task *store.RuntimeTask) (string, int64, error) {
	if task.ChatConversationID == nil {
		return "", 0, nil
	}
	c, err := s.store.RuntimeChatConversation(ctx, *task.ChatConversationID, task.RunAsID)
	if err != nil {
		return "", 0, err
	}
	if !c.Shared {
		return "", 0, nil
	}
	return c.Department, c.BotID, nil
}

// cloudTaskComputer 决定一轮任务用哪台电脑：返回电脑的所有者和任务所属的机器人。不属于机器人的任务返回 botID=0（用临时电脑）。
//
//   - 团队对话的轮次（s.teamComputer 认出来的）用那个团队（部门）的电脑。这一轮以发消息的人运行，凭证也只签给他；
//     电脑排队独占，所以团队电脑上不会同时有两个人的令牌。
//   - 其余轮次（定时 / 手动任务、员工本人的网页对话）用机器人所有者本人的电脑，而且只有机器人以任务的运行身份
//     运行时才行：个人电脑上永远只有这位员工自己的令牌。
func (s *Server) cloudTaskComputer(ctx context.Context, task *store.RuntimeTask) (store.RuntimeComputerOwner, int64, error) {
	if s.teamComputer != nil {
		department, botID, err := s.teamComputer(ctx, task)
		if err != nil {
			return store.RuntimeComputerOwner{}, 0, errors.New("checking the cloud task team failed")
		}
		if department != "" {
			return store.TeamComputerOwner(department), botID, nil
		}
	}
	botID, err := s.cloudTaskBot(ctx, task)
	if err != nil || botID == 0 {
		return store.RuntimeComputerOwner{}, 0, err
	}
	return store.UserComputerOwner(task.RunAsID), botID, nil
}

// cloudTaskBot 找出任务所属的个人机器人：定时 / 手动任务直接带 BotID，网页对话轮次的机器人记在对话上。
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
		if c.Shared {
			return 0, nil
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
// workDir / homeDir 是这一轮的当前目录和 DSH 主目录；常驻电脑上按机器人分开。
type cloudComputer struct {
	sandbox    runtime.Sandbox
	persistent bool
	ownerKind  string
	botID      int64
	workDir    string
	outputDir  string
	homeDir    string
	close      func(release bool)
}

// openCloudComputer 为任务准备电脑：任务属于机器人时唤醒所有者的常驻电脑（没有就新建一台），否则新建临时电脑。
func (s *Server) openCloudComputer(ctx context.Context, task *store.RuntimeTask, spec runtime.SandboxSpec) (*cloudComputer, error) {
	pp, ok := s.cloudComputers()
	if ok {
		owner, botID, err := s.cloudTaskComputer(ctx, task)
		if err != nil {
			return nil, err
		}
		if botID != 0 {
			return s.openPersistentComputer(ctx, pp, task, owner, botID, spec)
		}
	}
	sandbox, err := s.opts.Runtime.Provider.Create(ctx, spec)
	if err != nil {
		return nil, errors.New("creating cloud sandbox failed")
	}
	return &cloudComputer{sandbox: sandbox, workDir: runtimeOutputDir, outputDir: runtimeOutputDir, homeDir: runtimeHomeDir, close: func(_ bool) {
		c, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer stop()
		if err := sandbox.Destroy(c); err != nil {
			s.log.Warn("销毁云任务沙箱失败", "task_id", task.ID, "err", err)
		}
	}}, nil
}

func (s *Server) acquireComputerWithin(ctx context.Context, owner store.RuntimeComputerOwner, botID int64, holder string, until time.Time, wait time.Duration) (*store.RuntimeComputer, error) {
	giveUp := time.Now().Add(wait)
	for {
		c, err := s.store.AcquireRuntimeComputer(ctx, owner, holder, botID, until)
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

func (s *Server) openPersistentComputer(ctx context.Context, pp runtime.PersistentProvider, task *store.RuntimeTask, owner store.RuntimeComputerOwner, botID int64, spec runtime.SandboxSpec) (*cloudComputer, error) {
	if len(spec.AllowedHosts) != 1 || spec.AllowedHosts[0] == "" {
		return nil, errors.New("persistent cloud computer requires one gateway hostname")
	}
	holder := fmt.Sprintf("task:%d:%d", task.ID, task.Attempts)
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Duration(task.TimeoutSeconds) * time.Second)
	}
	leaseUntil := deadline.Add(30 * time.Second)
	c, err := s.acquireComputerWithin(ctx, owner, botID, holder, leaseUntil, runtimeComputerWait)
	if err != nil {
		if errors.Is(err, store.ErrRuntimeComputerBusy) || ctx.Err() != nil {
			return nil, err
		}
		return nil, errors.New("reserving the cloud computer failed")
	}
	computerID := c.ID
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	release := func() {
		bc, stop := bg()
		defer stop()
		if err := s.store.ReleaseRuntimeComputer(bc, computerID, holder); err != nil {
			s.log.Warn("释放云电脑失败", "computer_id", computerID, "err", err)
		}
	}
	policy := s.opts.Runtime.Computer
	limit := policy.DiskLimitBytes
	if c.SandboxID != "" && limit > 0 && c.DiskBytes > limit {
		err := pp.CheckPersistent(ctx, c.SandboxID)
		switch {
		case errors.Is(err, runtime.ErrSandboxGone):
			c.SandboxID = ""
		case err != nil:
			release()
			s.log.Warn("检查云电脑失败", "computer_id", computerID, "err", err)
			return nil, errors.New("checking whether the cloud sandbox still exists failed")
		default:
			release()
			return nil, fmt.Errorf("%w (%d MB used of %d MB); reset the computer from the bot panel or ask an administrator to reclaim it",
				errCloudDisk, c.DiskBytes>>20, limit>>20)
		}
	}
	save := func(touched, done bool) error {
		bc, stop := bg()
		defer stop()
		return s.store.UpdateRuntimeComputer(bc, c, holder, touched, done)
	}
	if err := recoverCloudComputer(ctx, pp, c); err != nil {
		release()
		s.log.Warn("查找未登记的云电脑失败", "computer_id", computerID, "err", err)
		return nil, errors.New("locating the cloud computer failed")
	}
	gatewayHost := spec.AllowedHosts[0]
	var box runtime.PersistentSandbox
	if c.SandboxID != "" {
		if c.State != store.RuntimeComputerRunning {
			c.State = store.RuntimeComputerWaking
			_ = save(false, false)
		}
		box, err = pp.Wake(ctx, c.SandboxID)
		if errors.Is(err, runtime.ErrSandboxGone) {
			// 电脑已经不在了（被外部删除或过期）：当作被重置，新建一台。
			s.log.Warn("云电脑已不存在，重新新建", "computer_id", computerID, "owner", owner.String(), "task_id", task.ID)
			c.SandboxID, box, err = "", nil, nil
		}
		if err != nil {
			_ = save(false, true)
			return nil, errors.New("waking the cloud computer failed")
		}
		if box != nil && c.GatewayHost != gatewayHost {
			c.State = store.RuntimeComputerRunning
			if p, ok := pp.(runtime.PersistentNetworkPolicy); !ok {
				_ = save(false, true)
				return nil, errors.New("cloud sandbox cannot update its gateway allowlist; restore the previous gateway hostname")
			} else if err := p.ReplaceAllowedHosts(ctx, c.SandboxID, spec.AllowedHosts); err != nil {
				_ = save(false, true)
				s.log.Warn("更新云电脑网关策略失败", "computer_id", computerID, "err", err)
				return nil, errors.New("updating the cloud sandbox gateway allowlist failed")
			}
			c.GatewayHost = gatewayHost
		}
	}
	if box == nil {
		c.SandboxID, c.State, c.DiskBytes = "", store.RuntimeComputerWaking, 0
		c.GatewayHost = gatewayHost
		if _, ok := pp.(runtime.PersistentRecovery); ok {
			c.CreationKey = runtime.NewPersistentCreationKey()
		}
		if err := save(true, false); err != nil {
			release()
			return nil, errors.New("recording the cloud sandbox creation failed")
		}
		createSpec := runtime.SandboxSpec{Image: spec.Image, AllowedHosts: spec.AllowedHosts, CreationKey: c.CreationKey}
		if policy.NativeDiskQuota {
			createSpec.DiskQuotaBytes = limit
		}
		if box, err = pp.CreatePersistent(ctx, createSpec); err != nil {
			createCtxErr := ctx.Err()
			switch {
			case c.CreationKey == "", errors.Is(err, runtime.ErrSandboxCreateRejected):
				c.CreationKey = ""
				_ = save(false, true)
			default:
				p := pp.(runtime.PersistentRecovery)
				lookup, stop := bg()
				id, lookupErr := p.FindPersistent(lookup, c.CreationKey)
				stop()
				if lookupErr == nil && id != "" {
					c.SandboxID, c.CreationKey = id, ""
					if err := save(false, true); err == nil {
						break
					}
				}
				// A lost response may arrive before the provider has listed the sandbox.
				// Leave the correlation key intact until the lease expires.
				task.RetryAfter = &leaseUntil
			}
			s.log.Warn("创建常驻沙箱失败", "computer_id", computerID, "native_disk_quota", policy.NativeDiskQuota, "err", err)
			if createCtxErr != nil {
				return nil, createCtxErr
			}
			return nil, errCloudCreateRetryable
		}
		c.SandboxID, c.CreationKey = box.ID(), ""
	}
	c.State = store.RuntimeComputerRunning
	if err := save(true, false); err != nil {
		// 租约丢了（例如被管理员回收）：不再碰这台电脑。
		task.RetryAfter = &leaseUntil
		return nil, errors.New("the cloud computer was taken over")
	}
	outputDir := runtimeBotOutputDir(botID)
	return &cloudComputer{sandbox: box, persistent: true, ownerKind: owner.Kind, botID: botID,
		workDir: runtimeBotWorkDir(botID), outputDir: outputDir, homeDir: runtimeBotHomeDir(botID), close: func(release bool) {
			// 产物已经收走：清掉，免得留在电脑上被同一台电脑的其他机器人或下一轮看到。
			cleanup, cancel := bg()
			out, err := box.Exec(cleanup, runtime.Command{Args: []string{"rm", "-rf", outputDir}, Timeout: 10 * time.Second})
			cancel()
			if err != nil || out.ExitCode != 0 {
				s.log.Warn("清理云电脑产物失败", "computer_id", computerID, "bot_id", botID, "err", err, "exit_code", out.ExitCode)
			}
			bc, stop := bg()
			defer stop()
			if disk, err := computerDiskUsage(bc, box); err == nil {
				c.DiskBytes = disk
			} else {
				s.log.Warn("统计云电脑磁盘用量失败", "computer_id", computerID, "err", err)
			}
			c.State = store.RuntimeComputerRunning
			if err := s.store.UpdateRuntimeComputer(bc, c, holder, true, release); err != nil {
				s.log.Warn("保存云电脑状态失败", "computer_id", computerID, "err", err)
			}
		}}, nil
}

// The key was committed before CreatePersistent. Reconcile under the computer's
// lease so a lost create response or process exit does not strand a sandbox.
func recoverCloudComputer(ctx context.Context, pp runtime.PersistentProvider, c *store.RuntimeComputer) error {
	if c.SandboxID != "" || c.CreationKey == "" {
		return nil
	}
	p, ok := pp.(runtime.PersistentRecovery)
	if !ok {
		return errors.New("runtime cannot look up the pending sandbox")
	}
	id, err := p.FindPersistent(ctx, c.CreationKey)
	if err != nil && !errors.Is(err, runtime.ErrSandboxGone) {
		return err
	}
	c.SandboxID, c.CreationKey = id, ""
	return nil
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
	s.reapComputerOrphans(ctx)
	list, err := s.store.IdleRuntimeComputers(ctx, t.Add(-idle), retainBefore)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("读取空闲云电脑失败", "err", err)
		}
		return
	}
	for _, c := range list {
		action := computerHibernate
		// 列表和取得租约之间电脑可能刚被用过：拿到租约后按最新状态再判断一次。
		guard := func(c *store.RuntimeComputer) bool {
			return c.State != store.RuntimeComputerHibernated && c.LastUsedAt.Before(t.Add(-idle))
		}
		if c.State == store.RuntimeComputerHibernated {
			action = computerReset
			guard = func(c *store.RuntimeComputer) bool {
				return c.State == store.RuntimeComputerHibernated && c.HibernatedAt != nil && c.HibernatedAt.Before(retainBefore)
			}
		}
		_, err := s.cloudComputerAction(ctx, c.ID, action, "reaper", guard)
		if err != nil && errors.Is(err, errComputerSkipped) {
			continue
		}
		if err != nil &&
			!errors.Is(err, store.ErrRuntimeComputerBusy) && ctx.Err() == nil {
			s.log.Warn("处理空闲云电脑失败", "computer_id", c.ID, "action", action, "err", err)
			continue
		}
		if err == nil && action == computerReset {
			s.auditSystem(ctx, "runtime.computer.reclaim", computerTarget(&c), map[string]any{"reason": "retention"})
		}
	}
}

// reapComputerOrphans 删除 0058 迁移合并下来的多余旧电脑（每位员工只保留最近使用的一台）。
//
// 每次 provider 调用都有独立超时：控制面 TCP 连上却不回 header（见 runtime 包的 transport）时，
// 没有超时的调用会让整个回收器（包括空闲休眠和 retention 回收）停摆。失败按内存退避重试，
// 正常情况下 30s 的回收周期只会重试到期的记录。
func (s *Server) reapComputerOrphans(ctx context.Context) {
	pp, ok := s.opts.Runtime.Provider.(runtime.PersistentProvider)
	if !ok {
		return
	}
	const batchSize = 20
	orphans, err := s.store.RuntimeComputerOrphans(ctx, s.computerOrphanCursor, batchSize)
	if err != nil {
		if ctx.Err() == nil {
			s.log.Warn("读取待回收的旧云电脑失败", "err", err)
		}
		return
	}
	if len(orphans) < batchSize {
		s.computerOrphanCursor = 0
	} else {
		s.computerOrphanCursor = orphans[len(orphans)-1].ID
	}
	for _, o := range orphans {
		if !s.computerOrphanDue(o.ID, time.Now()) {
			continue
		}
		id := o.SandboxID
		if id == "" {
			// 旧记录新建到一半中断：按 creation_key 找沙箱；找不到就是从没建成，直接去掉记录。
			p, ok := pp.(runtime.PersistentRecovery)
			if ok {
				callCtx, cancel := context.WithTimeout(ctx, runtimeComputerOrphanCallTimeout)
				found, err := p.FindPersistent(callCtx, o.CreationKey)
				cancel()
				if err != nil && !errors.Is(err, runtime.ErrSandboxGone) {
					s.log.Warn("查找待回收的旧云电脑失败", "err", err)
					s.computerOrphanFailed(o.ID, time.Now())
					continue
				}
				id = found
			}
		}
		if id != "" {
			callCtx, cancel := context.WithTimeout(ctx, runtimeComputerOrphanCallTimeout)
			err := pp.Remove(callCtx, id)
			cancel()
			if err != nil && !errors.Is(err, runtime.ErrSandboxGone) {
				s.log.Warn("回收旧云电脑失败", "err", err)
				s.computerOrphanFailed(o.ID, time.Now())
				continue
			}
		}
		if err := s.store.DeleteRuntimeComputerOrphan(ctx, o.ID); err != nil {
			s.log.Warn("删除旧云电脑记录失败", "err", err)
			continue
		}
		s.computerOrphanReclaimed(o.ID)
		if id != "" {
			s.auditSystem(ctx, "runtime.computer.reclaim", "sandbox", map[string]any{"reason": "merged_per_owner"})
		}
	}
}

// computerOrphanDue 报告某条旧电脑记录是否该重试；退避期内直接跳过，不再占用一轮回收。
func (s *Server) computerOrphanDue(id int64, now time.Time) bool {
	r, ok := s.computerOrphanRetries[id]
	return !ok || !now.Before(r.next)
}

// computerOrphanFailed 记一次回收失败，并把下次重试推后：30s、1m、2m……最多 30m。
func (s *Server) computerOrphanFailed(id int64, now time.Time) {
	r := s.computerOrphanRetries[id]
	r.attempts++
	delay := runtimeComputerOrphanRetryBase
	for i := 1; i < r.attempts && delay < runtimeComputerOrphanRetryMax; i++ {
		delay *= 2
	}
	if delay > runtimeComputerOrphanRetryMax {
		delay = runtimeComputerOrphanRetryMax
	}
	r.next = now.Add(delay)
	if s.computerOrphanRetries == nil {
		s.computerOrphanRetries = map[int64]computerOrphanRetry{}
	}
	s.computerOrphanRetries[id] = r
}

// computerOrphanReclaimed 在记录删除（或确认从没建成）后清掉退避状态。
func (s *Server) computerOrphanReclaimed(id int64) {
	delete(s.computerOrphanRetries, id)
}

// computerTarget 是审计里电脑的标识：computer:<id>:user:<员工 ID> 或 computer:<id>:team:<部门>。
func computerTarget(c *store.RuntimeComputer) string {
	return "computer:" + strconv.FormatInt(c.ID, 10) + ":" + c.Owner().String()
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
func (s *Server) cloudComputerAction(ctx context.Context, computerID int64, action, who string, guard func(*store.RuntimeComputer) bool) (*store.RuntimeComputer, error) {
	pp, ok := s.opts.Runtime.Provider.(runtime.PersistentProvider)
	if !ok {
		return nil, errComputerUnsupported
	}
	holder := who + ":" + secret.Token()[:12]
	ctx, cancel := context.WithTimeout(ctx, runtimeComputerOpLease-30*time.Second)
	defer cancel()
	c, err := s.store.AcquireRuntimeComputerByID(ctx, computerID, holder, 0, time.Now().Add(runtimeComputerOpLease))
	if err != nil {
		return nil, err
	}
	bg := func() (context.Context, context.CancelFunc) {
		return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	}
	finish := func(touched bool) error {
		bc, stop := bg()
		defer stop()
		err := s.store.UpdateRuntimeComputer(bc, c, holder, touched, true)
		c.Busy = false
		return err
	}
	if guard != nil && !guard(c) {
		bc, stop := bg()
		defer stop()
		_ = s.store.ReleaseRuntimeComputer(bc, computerID, holder)
		return nil, errComputerSkipped
	}
	if err := recoverCloudComputer(ctx, pp, c); err != nil {
		_ = finish(true)
		return nil, errComputerProvider{err}
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
		_ = s.store.UpdateRuntimeComputer(bc, c, holder, false, false)
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
	Persistent bool `json:"persistent"`
	// OwnerKind 是 user（我的电脑：员工的所有个人机器人共用）或 team（团队电脑）。
	OwnerKind string `json:"owner_kind"`
	// Department 是团队电脑所属的部门。
	Department string `json:"department,omitempty"`
	// SharedBots 是共用这台员工电脑的个人机器人数（团队电脑为 0）。
	SharedBots         int        `json:"shared_bots,omitempty"`
	State              string     `json:"state"`
	Busy               bool       `json:"busy"`
	DiskBytes          int64      `json:"disk_bytes"`
	DiskLimitBytes     int64      `json:"disk_limit_bytes"`
	IdleTimeoutSeconds int64      `json:"idle_timeout_seconds"`
	LastUsedAt         *time.Time `json:"last_used_at,omitempty"`
	HibernatedAt       *time.Time `json:"hibernated_at,omitempty"`
}

func (s *Server) computerView(owner store.RuntimeComputerOwner, c *store.RuntimeComputer) runtimeComputerView {
	_, persistent := s.cloudComputers()
	policy := s.opts.Runtime.Computer
	v := runtimeComputerView{Persistent: persistent, OwnerKind: owner.Kind, Department: owner.Department, State: store.RuntimeComputerNone,
		DiskLimitBytes: policy.DiskLimitBytes, IdleTimeoutSeconds: int64(policy.IdleTimeout / time.Second)}
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

// ownerComputerView 是一个所有者的电脑状态；员工电脑带上共用它的机器人数。
func (s *Server) ownerComputerView(ctx context.Context, owner store.RuntimeComputerOwner) (runtimeComputerView, error) {
	c, err := s.store.RuntimeComputerOf(ctx, owner)
	if notFound(err) {
		c, err = nil, nil
	}
	if err != nil {
		return runtimeComputerView{}, err
	}
	v := s.computerView(owner, c)
	if owner.Kind == store.RuntimeComputerOwnerUser {
		if v.SharedBots, err = s.store.RuntimeComputerBotCount(ctx, owner.UserID); err != nil {
			return runtimeComputerView{}, err
		}
	}
	return v, nil
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
	case notFound(err):
		writeErrCode(w, http.StatusNotFound, codeRuntimeComputerNotFound, nil, "这台云电脑不存在（可能已经被回收）")
	default:
		s.internalErr(w, r, err)
	}
}

// handleRuntimeChatComputer 是员工本人对「我的电脑」的手动操作：hibernate（立即休眠）或 reset（删除重建）。
// 路径里的机器人只用来确认员工有权操作：个人机器人共用员工自己的电脑，操作的是这台电脑，影响他的所有机器人。
func (s *Server) handleRuntimeChatComputer(w http.ResponseWriter, r *http.Request) {
	if !allowRuntimeComputerMutation(w, r) || !s.runtimeAvailable(w) {
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
	owner := store.UserComputerOwner(currentUser(r).ID)
	existing, err := s.store.RuntimeComputerOf(r.Context(), owner)
	if notFound(err) {
		// 还没有电脑：什么也不用做。
		view, err := s.ownerComputerView(r.Context(), owner)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, view)
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if _, err := s.cloudComputerAction(r.Context(), existing.ID, action, "owner", nil); err != nil {
		s.runtimeComputerError(w, r, err)
		return
	}
	s.audit(r, "runtime.computer."+action, computerTarget(existing), map[string]any{"bot_id": botID})
	view, err := s.ownerComputerView(r.Context(), owner)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type runtimeAdminComputer struct {
	ID        int64  `json:"id"`
	OwnerID   int64  `json:"owner_id,omitempty"`
	OwnerName string `json:"owner_name,omitempty"`
	BotID     int64  `json:"bot_id,omitempty"`
	BotName   string `json:"bot_name,omitempty"`
	runtimeComputerView
}

// handleRuntimeComputers 列出所有云电脑（员工电脑和团队电脑，管理员看占用和回收用），附带当前的资源上限。
func (s *Server) handleRuntimeComputers(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	list, next, err := s.store.RuntimeComputers(r.Context(), before)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	out := make([]runtimeAdminComputer, 0, len(list))
	for i := range list {
		c := &list[i]
		out = append(out, runtimeAdminComputer{ID: c.ID, OwnerID: c.OwnerID, OwnerName: c.OwnerName, BotID: c.BotID, BotName: c.BotName,
			runtimeComputerView: s.computerView(c.Owner(), c)})
	}
	_, persistent := s.cloudComputers()
	policy := s.opts.Runtime.Computer
	response := map[string]any{
		"persistent": persistent, "idle_timeout_seconds": int64(policy.IdleTimeout / time.Second),
		"disk_limit_bytes": policy.DiskLimitBytes, "retention_seconds": int64(policy.Retention / time.Second),
		"computers": out,
	}
	if next > 0 {
		response["next_before"] = strconv.FormatInt(next, 10)
	}
	writeJSON(w, http.StatusOK, response)
}

// handleRuntimeComputerAdmin 是管理员对任一云电脑（按电脑 ID）的操作：POST .../hibernate 休眠，DELETE 回收（删除整台电脑）。
func (s *Server) handleRuntimeComputerAdmin(w http.ResponseWriter, r *http.Request) {
	if !allowRuntimeComputerMutation(w, r) || !s.runtimeAvailable(w) {
		return
	}
	action := computerReset
	if r.Method == http.MethodPost {
		action = computerHibernate
	}
	existing, err := s.store.RuntimeComputer(r.Context(), runtimeID(r, "id"))
	if err != nil {
		s.runtimeComputerError(w, r, err)
		return
	}
	c, err := s.cloudComputerAction(r.Context(), existing.ID, action, "admin", nil)
	if err != nil {
		s.runtimeComputerError(w, r, err)
		return
	}
	event := "runtime.computer.hibernate"
	if action == computerReset {
		event = "runtime.computer.reclaim"
	}
	s.audit(r, event, computerTarget(existing), nil)
	writeJSON(w, http.StatusOK, s.computerView(c.Owner(), c))
}

var runtimeComputerOrigin http.CrossOriginProtection

func allowRuntimeComputerMutation(w http.ResponseWriter, r *http.Request) bool {
	if err := runtimeComputerOrigin.Check(r); err != nil {
		writeErrCode(w, http.StatusForbidden, codeAuthForbidden, nil, "云电脑操作不允许跨来源请求")
		return false
	}
	return true
}
