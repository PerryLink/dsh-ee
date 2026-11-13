package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 端侧扩展（docs/plugins.md §8）：跑在员工电脑上的服务扩展，调用不经过 dsh-ee。
// 端侧 SDK（extensions/sdk-local）用员工自己的接入令牌调这两个接口（POST /api/local/confirm、/api/local/audit）：
//
//   - 写操作前先问 dsh-ee 要确认：和服务端的写操作确认（write_confirm.go）是同一张表、同一个确认页，
//     同意后同样的参数再调一次才执行，一次同意只能用一次。
//   - 每次调用后回报摘要（扩展、版本、工具、耗时、成败），记进审计。不带返回内容。
//
// 这是弱保证（§13 待定 2）：本机进程可以不问、不报。dsh-ee 能做的是让正常写出来的扩展默认就问、就报。

const (
	auditLocalCall = "local.call"
	// 确认表里端侧扩展没有资产：asset_id 记 0，工具记成 local:<扩展 id>/<工具名>
	localToolPrefix = "local:"
)

var (
	localExtID   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,40}/[a-z0-9][a-z0-9._-]{0,60}$`)
	localToolRe  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
	localCodeRe  = regexp.MustCompile(`^[a-z_]{0,32}$`)
	localVersion = regexp.MustCompile(`^[0-9A-Za-z.+-]{0,64}$`)
)

type localCallReq struct {
	Extension string          `json:"extension"`
	Version   string          `json:"version"`
	Tool      string          `json:"tool"`
	Args      json.RawMessage `json:"args"`
	// 调用方是哪个 harness：dsh 自己有审批卡，write_confirm 为 non_dsh 时不用再确认。
	// 但只认令牌属性（client_kind=dsh），third_party 令牌即使自报 dsh 也要人工确认（和 /mcp 一致）。
	Harness string `json:"harness"`
	// 回报用
	OK         *bool  `json:"ok"`
	DurationMs int64  `json:"duration_ms"`
	Code       string `json:"code"`
}

func (q *localCallReq) check() string {
	switch {
	case !localExtID.MatchString(q.Extension):
		return "extension 要是「作者/名字」，例如 acme/legacy-oa"
	case !localToolRe.MatchString(q.Tool):
		return "tool 不对"
	case !localVersion.MatchString(q.Version):
		return "version 不对"
	case !localCodeRe.MatchString(q.Code):
		return "code 不对"
	}
	return ""
}

func (q *localCallReq) key() string { return localToolPrefix + q.Extension + "/" + q.Tool }

// POST /api/local/confirm {extension, version, tool, args, harness}
// → {approved: true}，或 {approved: false, rejected?, id, url, message}：把 message 原样交给模型，员工在网页上确认后再调一次。
func (s *Server) handleLocalConfirm(w http.ResponseWriter, r *http.Request) {
	var q localCallReq
	if !readJSONLimit(w, r, &q, confirmMaxArgs+4096) {
		return
	}
	if msg := q.check(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()
	u := currentUser(r)
	switch mode := s.writeConfirmMode(ctx); {
	case mode == confirmOff, mode == confirmNonDSH && q.Harness == "dsh" && currentClientKind(r) == store.ClientKindDSH:
		writeJSON(w, http.StatusOK, map[string]any{"approved": true})
		return
	}
	text, hash := canonicalArgs(q.Args)
	if len(text) > confirmMaxArgs {
		writeErr(w, http.StatusBadRequest, "参数太大（超过 256 KB），没法交给员工确认；请拆成几次")
		return
	}
	tool := q.key()
	if ok, err := s.store.FindWriteConfirmation(ctx, u.ID, 0, tool, hash, store.ConfirmApproved); err == nil {
		used, err := s.store.UseWriteConfirmation(ctx, ok.ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		if used {
			s.audit(r, auditWriteConfirm, tool, map[string]any{"id": ok.ID, "use": true, "args_sha256": hash})
			writeJSON(w, http.StatusOK, map[string]any{"approved": true})
			return
		}
	} else if !notFound(err) {
		s.internalErr(w, r, err)
		return
	}
	link := func(id int64) string { return s.callerBase(r) + "/confirm/" + strconv.FormatInt(id, 10) }
	ask := func(id int64) {
		writeJSON(w, http.StatusOK, map[string]any{"approved": false, "id": id, "url": link(id),
			"message": "这个操作会修改「" + q.Extension + "」里的数据，要先由你本人在网页上确认，现在还没有执行。\n" +
				"请打开 " + link(id) + " 核对参数并点「同意」，然后让我用完全相同的参数再调用一次。\n" +
				"同意后 10 分钟内有效，只能执行一次；参数变了要重新确认。"})
	}
	if _, err := s.store.FindWriteConfirmation(ctx, u.ID, 0, tool, hash, store.ConfirmRejected); err == nil {
		writeJSON(w, http.StatusOK, map[string]any{"approved": false, "rejected": true,
			"message": "你刚才在网页上拒绝了这个操作，所以没有执行。如果还是要做，请先告诉我，我会重新发起确认；不要自动重试。"})
		return
	}
	if pending, err := s.store.FindWriteConfirmation(ctx, u.ID, 0, tool, hash, store.ConfirmPending); err == nil {
		ask(pending.ID)
		return
	}
	client := "端侧扩展 " + q.Extension
	if q.Version != "" {
		client += "@" + q.Version
	}
	c := &store.WriteConfirmation{UserID: u.ID, Tool: tool, ArgsHash: hash, Args: text, TokenID: currentTokenID(r), Client: cutUTF8(client, 120)}
	if err := s.store.CreateWriteConfirmation(ctx, c, secret.Hash(secret.Token()), confirmPendingTTL); err != nil {
		s.internalErr(w, r, err)
		return
	}
	ask(c.ID)
}

// POST /api/local/audit {extension, version, tool, ok, duration_ms, code}：回报一次本机调用的摘要。
func (s *Server) handleLocalAudit(w http.ResponseWriter, r *http.Request) {
	var q localCallReq
	if !readJSONLimit(w, r, &q, 8192) {
		return
	}
	if msg := q.check(); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if q.OK == nil {
		writeErr(w, http.StatusBadRequest, "要带 ok（这次调用成没成）")
		return
	}
	detail := map[string]any{"version": q.Version, "tool": q.Tool, "ok": *q.OK, "duration_ms": min(max(q.DurationMs, 0), int64(24*time.Hour/time.Millisecond)), "reported": "local"}
	if q.Code != "" {
		detail["code"] = q.Code
	}
	if q.Harness != "" {
		detail["harness"] = cutUTF8(strings.TrimSpace(q.Harness), 32)
	}
	s.audit(r, auditLocalCall, localToolPrefix+q.Extension, detail)
	w.WriteHeader(http.StatusNoContent)
}

// requireToken 只认员工自己的接入令牌（任何范围都行），不要求开启 Agent 功能：端侧扩展是员工本机上的程序，
// 和 Agent 工具不是一回事。网页会话不行：确认和回报都只该由本机进程发起。
func (s *Server) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			writeErr(w, http.StatusUnauthorized, "要带员工的接入令牌（Authorization: Bearer …）")
			return
		}
		p, err := s.resolveBearer(r.Context(), strings.TrimSpace(token), clientIP(r), r.Header.Get("X-DshEE-Client"), r.Header.Get("X-DshEE-Host"))
		if err != nil {
			if be, ok := err.(bearerError); ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效或已过期", "code": string(be)})
			} else {
				s.internalErr(w, r, err)
			}
			return
		}
		next(w, r.WithContext(withTokenUser(r.Context(), p.User, p.Access, p.TokenID, p.ClientKind)))
	}
}

// localConfirmView 补全确认页上端侧扩展的系统名和工具名。
func localConfirmView(out *confirmView) {
	rest, ok := strings.CutPrefix(out.Tool, localToolPrefix)
	if !ok || out.AssetID != 0 {
		return
	}
	if i := strings.LastIndex(rest, "/"); i > 0 {
		out.System = "端侧扩展 " + rest[:i]
		out.Description = "在你自己的电脑上执行（" + rest[i+1:] + "）"
	}
}
