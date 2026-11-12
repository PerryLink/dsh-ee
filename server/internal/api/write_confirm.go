package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/mcp"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 写操作的服务端确认（docs/plugins.md §7.5）。
//
// DSH 里的写操作由接入插件弹审批卡，员工点了才执行。但 /mcp 是标准协议，Claude Code、Codex 这类
// 客户端可以被设成自动同意，所以服务端要有自己的一道：调用方不是 DSH 时，没标只读的工具不直接执行，
// 先返回一个确认链接；员工在网页上看过参数、点了同意之后，同一个人用同样的参数再调一次才真正执行，
// 而且一次同意只能执行一次。
//
// 是不是 DSH 靠请求头 X-DshEE-Client 判断，别的客户端可以伪造它——这道确认防的是「客户端被设成自动同意」，
// 不是防恶意客户端；要一律确认，把方式改成 always。

const (
	writeConfirmSetting = "write_confirm"
	// off：不做服务端确认；non_dsh：调用方不是 DSH 时确认（默认）；always：一律确认
	confirmOff    = "off"
	confirmNonDSH = "non_dsh"
	confirmAlways = "always"

	confirmPendingTTL  = 15 * time.Minute
	confirmApprovedTTL = 10 * time.Minute
	confirmMaxArgs     = 256 << 10

	auditWriteConfirm = "write.confirm"
)

type writeConfirmSettings struct {
	Mode string `json:"mode"`
}

func (s *Server) writeConfirmMode(ctx context.Context) string {
	var set writeConfirmSettings
	if ok, err := s.store.GetSettingJSON(ctx, writeConfirmSetting, &set); err != nil || !ok {
		return confirmNonDSH
	}
	switch set.Mode {
	case confirmOff, confirmAlways:
		return set.Mode
	}
	return confirmNonDSH
}

// GET /api/admin/write-confirm
func (s *Server) handleGetWriteConfirm(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, writeConfirmSettings{Mode: s.writeConfirmMode(r.Context())})
}

// PUT /api/admin/write-confirm {mode}
func (s *Server) handlePutWriteConfirm(w http.ResponseWriter, r *http.Request) {
	var req writeConfirmSettings
	if !readJSON(w, r, &req) {
		return
	}
	if req.Mode != confirmOff && req.Mode != confirmNonDSH && req.Mode != confirmAlways {
		writeErr(w, http.StatusBadRequest, "方式只能是 off、non_dsh 或 always")
		return
	}
	if err := s.store.PutSettingJSON(r.Context(), writeConfirmSetting, req, currentUser(r).ID); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, "settings.update", writeConfirmSetting, map[string]any{"mode": req.Mode})
	writeJSON(w, http.StatusOK, req)
}

// canonicalArgs 把参数规整成确定的 JSON（键排序），同样的参数算出同样的摘要。
func canonicalArgs(raw json.RawMessage) (string, string) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if len(bytes.TrimSpace(raw)) == 0 || dec.Decode(&v) != nil || v == nil {
		v = map[string]any{}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	text := string(bytes.TrimRight(buf.Bytes(), "\n"))
	sum := sha256.Sum256([]byte(text))
	return text, hex.EncodeToString(sum[:])
}

// confirmWrite 在 /mcp 上执行一个写操作之前调用。返回 nil 表示可以执行（不需要确认，或员工已经同意、这次把同意用掉）；
// 否则返回要回给调用方的结果：请员工去确认。
func (s *Server) confirmWrite(r *http.Request, v viewer, a *store.Asset, t *store.ConnectionTool, args json.RawMessage) json.RawMessage {
	ctx := r.Context()
	mode := s.writeConfirmMode(ctx)
	if t.ReadOnly || mode == confirmOff || (mode == confirmNonDSH && dshConnect(r)) {
		return nil
	}
	text, hash := canonicalArgs(args)
	if len(text) > confirmMaxArgs {
		return mcp.TextResult("这次写操作的参数太大（超过 256 KB），没法交给员工确认；请拆成几次。", true)
	}
	if ok, err := s.store.FindWriteConfirmation(ctx, v.user.ID, a.ID, t.Name, hash, store.ConfirmApproved); err == nil {
		used, err := s.store.UseWriteConfirmation(ctx, ok.ID)
		if err != nil {
			s.log.Warn("用掉写操作确认失败", "err", err)
			return mcp.TextResult("确认记录暂时读不了，请稍后再试。", true)
		}
		if used {
			return nil
		}
	} else if !notFound(err) {
		s.log.Warn("查写操作确认失败", "err", err)
		return mcp.TextResult("确认记录暂时读不了，请稍后再试。", true)
	}
	base := s.callerBase(r)
	link := func(id int64) string { return base + "/confirm/" + strconv.FormatInt(id, 10) }
	// ask 的文本保持原样（旧客户端按文本展示），同时把确认信息放进结果级 _meta：
	// 第三方 Harness 不用解析中文文本就能拿到确认页，旧客户端忽略 _meta 不受影响。
	ask := func(id int64, expiresAt time.Time) json.RawMessage {
		text := "这个操作会修改「" + a.Title + "」里的数据，要先由你本人在网页上确认，现在还没有执行。\n" +
			"请打开 " + link(id) + " 核对参数并点「同意」，然后让我用完全相同的参数再调用一次。\n" +
			"同意后 10 分钟内有效，只能执行一次；参数变了要重新确认。不要改用别的工具绕过。"
		return mcp.TextResultWithMeta(text, true, map[string]any{
			"dsh-ee/confirmation": map[string]any{
				"id":         id,
				"url":        link(id),
				"expires_at": expiresAt.UTC().Format(time.RFC3339),
			},
		})
	}
	if rejected, err := s.store.FindWriteConfirmation(ctx, v.user.ID, a.ID, t.Name, hash, store.ConfirmRejected); err == nil && rejected != nil {
		return mcp.TextResult("你刚才在网页上拒绝了这个操作，所以没有执行。如果还是要做，请先告诉我，我会重新发起确认；不要自动重试。", true)
	}
	if pending, err := s.store.FindWriteConfirmation(ctx, v.user.ID, a.ID, t.Name, hash, store.ConfirmPending); err == nil {
		return ask(pending.ID, pending.ExpiresAt)
	}
	c := &store.WriteConfirmation{UserID: v.user.ID, AssetID: a.ID, Tool: t.Name, ArgsHash: hash, Args: text, Client: cutUTF8(r.Header.Get("User-Agent"), 120)}
	if err := s.store.CreateWriteConfirmation(ctx, c, secret.Hash(secret.Token()), confirmPendingTTL); err != nil {
		s.log.Warn("创建写操作确认失败", "err", err)
		return mcp.TextResult("没能发起确认，请稍后再试。", true)
	}
	return ask(c.ID, c.ExpiresAt)
}

type confirmView struct {
	*store.WriteConfirmation
	System      string `json:"system"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Expired     bool   `json:"expired"`
}

func (s *Server) confirmView(ctx context.Context, c *store.WriteConfirmation) *confirmView {
	out := &confirmView{WriteConfirmation: c, Expired: !time.Now().Before(c.ExpiresAt)}
	if a, err := s.store.GetAsset(ctx, c.AssetID); err == nil {
		out.System, out.Slug = a.Title, a.Slug
		if tools, err := s.store.ConnectionTools(ctx, a.ID); err == nil {
			for _, t := range tools {
				if t.Name == c.Tool {
					out.Description = t.Description
				}
			}
		}
	}
	localConfirmView(out)
	return out
}

func (s *Server) loadConfirmation(w http.ResponseWriter, r *http.Request) (*store.WriteConfirmation, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	c, err := s.store.GetWriteConfirmation(r.Context(), id)
	// 只有发起调用的本人能看、能决定
	if notFound(err) || (err == nil && c.UserID != currentUser(r).ID) {
		writeErr(w, http.StatusNotFound, "没有这条待确认的操作，或它不是你发起的")
		return nil, false
	}
	if err != nil {
		s.internalErr(w, r, err)
		return nil, false
	}
	return c, true
}

// GET /api/me/confirmations：我还在等确认的写操作。
func (s *Server) handleMyConfirmations(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.PendingWriteConfirmations(r.Context(), currentUser(r).ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	out := make([]*confirmView, len(list))
	for i, c := range list {
		out[i] = s.confirmView(r.Context(), c)
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

// GET /api/me/confirmations/{id}
func (s *Server) handleGetConfirmation(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfirmation(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.confirmView(r.Context(), c))
}

// POST /api/me/confirmations/{id} {approve}
func (s *Server) handleDecideConfirmation(w http.ResponseWriter, r *http.Request) {
	c, ok := s.loadConfirmation(w, r)
	if !ok {
		return
	}
	var req struct {
		Approve bool `json:"approve"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	done, err := s.store.DecideWriteConfirmation(r.Context(), c.ID, c.UserID, req.Approve, confirmApprovedTTL)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !done {
		writeErr(w, http.StatusConflict, "这条操作已经处理过或已经过期；需要的话让 Agent 重新发起")
		return
	}
	view := s.confirmView(r.Context(), c)
	s.audit(r, auditWriteConfirm, "connection:"+view.Slug, map[string]any{"id": c.ID, "tool": c.Tool, "approve": req.Approve, "args_sha256": c.ArgsHash})
	fresh, err := s.store.GetWriteConfirmation(r.Context(), c.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.confirmView(r.Context(), fresh))
}
