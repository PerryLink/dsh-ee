package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/mcp"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 云端机器人的写操作审批（docs/dev/bot-approval.md）。
//
// 每个机器人有一个审批模式（store.RuntimeApproval*），由管理员设置，管理员可以允许所有者本人在对话页切换：
//   - read_only：只读（默认，与旧行为一致）。写工具不下发，调用一律拦下。
//   - manual：逐个审批。复用写操作确认（write_confirm.go 的 write_confirmations）：建一条带 runtime_task_id 的待确认记录，
//     对话页把它显示成那一轮里的审批卡；/mcp 这次调用就停在这里等员工点批准 / 拒绝，批准了才执行，然后机器人接着干。
//   - auto：命中管理员配置的规则（工具全名的通配模式）直接执行，其余照 manual 出卡。
//   - full：完全放行，不再审批。开启时必须显式确认风险（confirm_full_access），页面上持续显示「完全放行」。
//
// 不论哪种模式放行，每次执行写操作都写一条 runtime.write 审计：模式、谁批的（approved_by=user + approver_id）
// 或按哪条规则自动批的（approved_by=rule + rule）、完全放行（approved_by=full）。被拒、超时、只读拦下的也各记一条。
//
// 凭证不变：沙箱里只有这一轮的短期令牌（随任务超时过期、任务结束吊销，只有 models / connections 范围）。
// 写操作由服务端按员工本人的连接权限代为执行，连接凭据和管理员凭据从不进沙箱。审批模式每次调用现读，管理员改成只读立即生效。

const (
	auditRuntimeWrite        = "runtime.write"
	auditRuntimeWriteBlocked = "runtime.write.blocked"
	auditRuntimeBotApproval  = "runtime.bot.approval"

	runtimeApprovalMaxRules   = 50
	runtimeApprovalMaxRuleLen = 160

	runtimeReadOnlyRejection = "Tool unavailable to read-only cloud tasks"
)

// runtimeApprovalPoll 是等员工审批时读确认状态的间隔；测试里调小。
var runtimeApprovalPoll = time.Second

// runtimeWriteAction 是审批模式对一次写操作的判定。
type runtimeWriteAction int

const (
	runtimeWriteBlock runtimeWriteAction = iota // 拦下，不执行
	runtimeWriteAllow                           // 直接执行（规则命中或完全放行）
	runtimeWriteAsk                             // 出审批卡，等员工决定
)

type runtimeWriteDecision struct {
	Action runtimeWriteAction
	// Via 记进审计的放行方式：rule（自动审批规则）、full（完全放行）；Rule 是命中的那条规则。
	Via  string
	Rule string
}

// decideRuntimeWrite 是纯函数：给定机器人的审批模式和规则、写工具的全名（<连接>__<工具>），决定放行、出卡还是拦下。
func decideRuntimeWrite(mode string, rules []string, tool string) runtimeWriteDecision {
	switch mode {
	case store.RuntimeApprovalFull:
		return runtimeWriteDecision{Action: runtimeWriteAllow, Via: "full"}
	case store.RuntimeApprovalAuto:
		if rule, ok := matchRuntimeApprovalRule(rules, tool); ok {
			return runtimeWriteDecision{Action: runtimeWriteAllow, Via: "rule", Rule: rule}
		}
		return runtimeWriteDecision{Action: runtimeWriteAsk}
	case store.RuntimeApprovalManual:
		return runtimeWriteDecision{Action: runtimeWriteAsk}
	}
	return runtimeWriteDecision{Action: runtimeWriteBlock}
}

// matchRuntimeApprovalRule 返回第一条匹配工具全名的规则。规则是 path.Match 通配模式（* 不跨 /，工具名里也没有 /），
// 比如 "wiki__create_page"（一个工具）、"wiki__*"（一个连接的全部写操作）、"*__create_*"（各系统的新建类操作）。
func matchRuntimeApprovalRule(rules []string, tool string) (string, bool) {
	for _, rule := range rules {
		if ok, err := path.Match(rule, tool); err == nil && ok {
			return rule, true
		}
	}
	return "", false
}

// normalizeRuntimeApprovalRules 去掉空白和重复，校验每条规则。返回 false 表示规则不合法。
func normalizeRuntimeApprovalRules(rules []string) ([]string, bool) {
	out := []string{}
	seen := map[string]bool{}
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" || seen[rule] {
			continue
		}
		if len(rule) > runtimeApprovalMaxRuleLen || strings.ContainsAny(rule, "/\\ \t\r\n\x00") {
			return nil, false
		}
		// 单独一个 * 等于完全放行，必须走完全放行模式（有风险确认），不能藏在规则里。
		if strings.Trim(rule, "*?_") == "" {
			return nil, false
		}
		if _, err := path.Match(rule, ""); err != nil {
			return nil, false
		}
		seen[rule] = true
		out = append(out, rule)
	}
	return out, len(out) <= runtimeApprovalMaxRules
}

// runtimeWriteGuidance 是拼进任务提示词的写操作说明，跟机器人入队时的审批模式一致（执行时仍以现读的模式为准）。
func runtimeWriteGuidance(mode string) string {
	const noRetry = "If a write is rejected or not approved in time, do not retry it or work around it with other tools; tell the employee instead. "
	switch mode {
	case store.RuntimeApprovalManual:
		return "Enterprise write tools are available, but each write waits until the employee approves it in the chat. " + noRetry
	case store.RuntimeApprovalAuto:
		return "Enterprise write tools are available. Writes covered by the administrator's auto-approval rules run immediately; " +
			"other writes wait until the employee approves them in the chat. " + noRetry
	case store.RuntimeApprovalFull:
		return "Enterprise write tools are available and run without per-action approval; every write is still audited. " +
			"Only make the changes the employee asked for. "
	}
	return "This turn is read-only. "
}

// runtimeWriteContext 找回这个云任务短期令牌对应的任务和机器人；任务不在运行、运行身份对不上时返回 nil。
func (s *Server) runtimeWriteContext(ctx context.Context, pr *Principal) *store.RuntimeWriteContext {
	if pr == nil || !pr.Runtime || pr.TokenID == 0 {
		return nil
	}
	wc, err := s.store.RuntimeWriteContextByToken(ctx, pr.TokenID)
	if err != nil {
		if !notFound(err) {
			s.log.Warn("读取云任务审批设置失败", "err", err)
		}
		return nil
	}
	if wc.Status != store.RuntimeRunning || pr.User == nil || wc.RunAsID != pr.User.ID {
		return nil
	}
	return wc
}

// runtimeWritesListed：tools/list 要不要给这个云任务下发写工具（审批模式不是只读）。
func (s *Server) runtimeWritesListed(ctx context.Context, pr *Principal) bool {
	wc := s.runtimeWriteContext(ctx, pr)
	return wc != nil && wc.ApprovalMode != store.RuntimeApprovalReadOnly
}

// runtimeWrite 处理云任务发起的一次连接写操作：按审批模式放行、出卡等待或拦下，执行了就留审计。返回给 DSH 的工具结果。
func (s *Server) runtimeWrite(r *http.Request, v viewer, pr *Principal, a *store.Asset, t *store.ConnectionTool, args json.RawMessage) json.RawMessage {
	ctx := r.Context()
	tool := mcpToolName(a.Slug, t.Name)
	wc := s.runtimeWriteContext(ctx, pr)
	mode := store.RuntimeApprovalReadOnly
	var rules []string
	if wc != nil {
		mode, rules = wc.ApprovalMode, wc.ApprovalRules
	}
	text, hash := canonicalArgs(args)
	detail := map[string]any{"mode": mode, "tool": tool, "connection": a.Slug, "args_sha256": hash}
	if wc != nil {
		detail["task_id"], detail["bot_id"] = wc.TaskID, wc.BotID
	}
	audit := func(action string, extra map[string]any) {
		for k, val := range extra {
			detail[k] = val
		}
		// 请求可能已经断开（任务被取消、等不到审批）：审计照样要按令牌归属写进去
		s.auditAs(r.WithContext(context.WithoutCancel(ctx)), v.user, action, "connection:"+a.Slug, detail)
	}
	execute := func(extra map[string]any) json.RawMessage {
		result := s.callConnectionTool(ctx, a, t, v, s.callerBase(r), args)
		extra["outcome"] = runtimeWriteOutcome(result)
		audit(auditRuntimeWrite, extra)
		return result
	}
	d := decideRuntimeWrite(mode, rules, tool)
	switch d.Action {
	case runtimeWriteAllow:
		extra := map[string]any{"approved_by": d.Via}
		if d.Rule != "" {
			extra["rule"] = d.Rule
		}
		return execute(extra)
	case runtimeWriteAsk:
		// 往下走
	default:
		audit(auditRuntimeWriteBlocked, map[string]any{"reason": "read_only"})
		return mcp.TextResult(runtimeReadOnlyRejection, true)
	}
	if len(text) > confirmMaxArgs {
		audit(auditRuntimeWriteBlocked, map[string]any{"reason": "args_too_large"})
		return mcp.TextResult("The arguments of this write are larger than 256 KB, too large to show the employee for approval. Split it into smaller writes.", true)
	}
	// 等待上限：确认的有效期和这一轮短期凭证的剩余时间取小，凭证过期后批准了也执行不了。
	wait := confirmPendingTTL
	if pr.ExpiresAt != nil {
		if left := time.Until(*pr.ExpiresAt); left < wait {
			wait = left
		}
	}
	if wait <= 0 {
		audit(auditRuntimeWriteBlocked, map[string]any{"reason": "expired"})
		return mcp.TextResult("This cloud task has no time left to wait for approval; the write was not executed.", true)
	}
	c := &store.WriteConfirmation{UserID: v.user.ID, AssetID: a.ID, Tool: t.Name, ArgsHash: hash, Args: text, TokenID: v.tokenID,
		RuntimeTaskID: wc.TaskID, Client: "cloud runtime"}
	if err := s.store.CreateWriteConfirmation(ctx, c, secret.Hash(secret.Token()), wait); err != nil {
		s.log.Warn("创建云任务写操作审批失败", "err", err)
		return mcp.TextResult("Could not ask the employee for approval; the write was not executed. Try again later.", true)
	}
	detail["confirmation_id"] = c.ID
	status := s.awaitRuntimeApproval(ctx, pr, c)
	switch status {
	case store.ConfirmApproved:
		used, err := s.store.UseWriteConfirmation(ctx, c.ID)
		if err != nil || !used {
			audit(auditRuntimeWriteBlocked, map[string]any{"reason": "approval_unusable"})
			return mcp.TextResult("The approval could not be used (it expired or was already used); the write was not executed.", true)
		}
		return execute(map[string]any{"approved_by": "user", "approver_id": v.user.ID})
	case store.ConfirmRejected:
		audit(auditRuntimeWriteBlocked, map[string]any{"reason": "rejected", "rejected_by": v.user.ID})
		return mcp.TextResult("The employee rejected this write in the chat, so it was not executed. Do not retry it or use other tools to work around it; tell the employee what you would have changed.", true)
	default:
		audit(auditRuntimeWriteBlocked, map[string]any{"reason": "not_approved_in_time"})
		return mcp.TextResult("The employee did not approve this write in time, so it was not executed. Do not retry it automatically; tell the employee it is waiting for approval.", true)
	}
}

// awaitRuntimeApproval 等员工批准或拒绝这条确认，返回最终状态；等不到（超时、任务被取消、请求断开）时返回 pending，
// 并让这条确认立即过期，对话页上的卡片随之消失，之后再点也不会执行。
func (s *Server) awaitRuntimeApproval(ctx context.Context, pr *Principal, c *store.WriteConfirmation) string {
	tick := time.NewTicker(runtimeApprovalPoll)
	defer tick.Stop()
	deadline := time.NewTimer(time.Until(c.ExpiresAt))
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			s.expireRuntimeApproval(ctx, c.ID)
			return store.ConfirmPending
		case <-deadline.C:
			s.expireRuntimeApproval(ctx, c.ID)
			return store.ConfirmPending
		case <-tick.C:
		}
		got, err := s.store.GetWriteConfirmation(ctx, c.ID)
		if err != nil {
			if notFound(err) {
				return store.ConfirmPending
			}
			continue
		}
		if got.Status != store.ConfirmPending {
			return got.Status
		}
		// 任务被取消或已结束：不再等
		if s.runtimeWriteContext(ctx, pr) == nil {
			s.expireRuntimeApproval(ctx, c.ID)
			return store.ConfirmPending
		}
	}
}

func (s *Server) expireRuntimeApproval(ctx context.Context, id int64) {
	c, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err := s.store.ExpireWriteConfirmation(c, id); err != nil {
		s.log.Warn("让云任务写操作审批过期失败", "id", id, "err", err)
	}
}

// runtimeWriteOutcome 从工具结果里看执行成没成功：ok 或 error。
func runtimeWriteOutcome(result json.RawMessage) string {
	var r struct {
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(result, &r) == nil && r.IsError {
		return "error"
	}
	return "ok"
}

// runtimeApprovalInput 是管理端和对话页设置审批模式时共用的字段。
type runtimeApprovalInput struct {
	Mode string `json:"approval_mode"`
	// ConfirmFullAccess：切到完全放行时必须为 true，表示操作人看过了风险提示。
	ConfirmFullAccess bool `json:"confirm_full_access"`
}

// checkRuntimeApprovalChange 校验一次审批模式变更；previous 是变更前的模式（新建时为空）。写出错误响应时返回 false。
func checkRuntimeApprovalChange(w http.ResponseWriter, in runtimeApprovalInput, previous string) bool {
	if !store.ValidRuntimeApprovalMode(in.Mode) {
		writeErrCode(w, http.StatusBadRequest, codeRuntimeApprovalInvalidMode, nil, "审批模式只能是 read_only、manual、auto 或 full")
		return false
	}
	if in.Mode == store.RuntimeApprovalFull && previous != store.RuntimeApprovalFull && !in.ConfirmFullAccess {
		writeErrCode(w, http.StatusBadRequest, codeRuntimeApprovalRiskUnconfirmed, nil, "开启完全放行前必须确认风险：机器人的写操作将不经审批直接执行")
		return false
	}
	return true
}

// PUT /api/runtime/chat/bots/{id}/approval {approval_mode, confirm_full_access}：所有者本人在对话页切换审批模式，
// 只有管理员允许员工修改时才行。规则仍只由管理员设置。
func (s *Server) handleRuntimeChatBotApproval(w http.ResponseWriter, r *http.Request) {
	if !s.runtimeAvailable(w) {
		return
	}
	var in runtimeApprovalInput
	if !readJSON(w, r, &in) {
		return
	}
	owner := currentUser(r).ID
	employee, err := s.runtimeChatEmployee(r)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	bot, err := s.store.RuntimeBot(r.Context(), runtimeID(r, "id"))
	if notFound(err) || err == nil && (!employee || bot.OwnerID != owner || bot.RunAsID != owner || !bot.WebChatEnabled) {
		writeErrCode(w, http.StatusNotFound, codeRuntimeChatBotUnavailable, nil, "机器人不存在或未开启网页对话")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !bot.ApprovalEmployeeEditable {
		writeErrCode(w, http.StatusForbidden, codeRuntimeApprovalLocked, nil, "管理员没有允许你修改这个机器人的审批模式")
		return
	}
	if !checkRuntimeApprovalChange(w, in, bot.ApprovalMode) {
		return
	}
	if err := s.store.SetRuntimeBotApprovalMode(r.Context(), bot.ID, owner, in.Mode); err != nil {
		if notFound(err) {
			// 读和写之间管理员收回了修改权限
			writeErrCode(w, http.StatusForbidden, codeRuntimeApprovalLocked, nil, "管理员没有允许你修改这个机器人的审批模式")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditRuntimeBotApproval, strconv.FormatInt(bot.ID, 10), map[string]any{
		"from": bot.ApprovalMode, "to": in.Mode, "by": "owner", "risk_confirmed": in.Mode == store.RuntimeApprovalFull,
	})
	writeJSON(w, http.StatusOK, map[string]any{"approval_mode": in.Mode, "approval_editable": true})
}
