package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/access"
	"github.com/hikariming/dsh-ee/server/internal/extension"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 扩展字段和按字段的访问规则（docs/access-control.md §4～§5）。
//
// 判断和拦截都在这里：每个出口（检索、阅读、目录、文档页、最近更新）在算分、截片段之前就把这个人
// 看不到的文档去掉，看不到就当不存在。维护者和「管理知识库」的人看得到自己库里的全部文档（否则没法维护）。

const (
	auditFieldCreate = "field.create"
	auditFieldUpdate = "field.update"
	auditFieldDelete = "field.delete"
	auditFieldValue  = "field.value"
	auditRuleCreate  = "access_rule.create"
	auditRuleUpdate  = "access_rule.update"
	auditRuleDelete  = "access_rule.delete"
)

// accessConfig 读当前的字段和规则。
func (s *Server) accessConfig(ctx context.Context) (*access.Config, error) {
	fields, err := s.store.ListFields(ctx)
	if err != nil {
		return nil, err
	}
	rules, err := s.store.ListAccessRules(ctx)
	if err != nil {
		return nil, err
	}
	return &access.Config{Fields: fields, Rules: rules}, nil
}

// valuesOf 把一个对象存着的字段值换成「键 → 值」。
func valuesOf(c *access.Config, object string, list []store.FieldValue) access.Values {
	byID := map[int64]string{}
	for _, f := range c.Fields {
		if f.Object == object {
			byID[f.ID] = f.Key
		}
	}
	out := access.Values{}
	for _, fv := range list {
		if k, ok := byID[fv.FieldID]; ok {
			out[k] = fv.Value
		}
	}
	return out
}

// userValues 是这个人在规则里用的属性：用户字段，加上部门、用户名、角色三个内置属性。
func (s *Server) userValues(ctx context.Context, c *access.Config, u *store.User, a *rbac.Access) (access.Values, error) {
	list, err := s.store.ObjectFieldValues(ctx, access.ObjectUser, u.ID)
	if err != nil {
		return nil, err
	}
	vals := valuesOf(c, access.ObjectUser, list)
	if u.Department != "" {
		vals[access.UserDepartment] = u.Department
	}
	vals[access.UserUsername] = u.Username
	if a != nil {
		vals[access.UserRoles] = append([]string{}, a.RoleCodes...)
	}
	return vals, nil
}

// docGate 是一个人在一个知识库里的文档过滤：返回 nil 表示不过滤（没有生效的规则，或他是维护者）。
type docGate func(docID int64) bool

func (s *Server) kbGate(ctx context.Context, c *access.Config, v viewer, b *store.KBase) (docGate, error) {
	if !c.Active(b.Slug) || v.kbMaintains(b) {
		return nil, nil
	}
	user, err := s.userValues(ctx, c, v.user, v.access)
	if err != nil {
		return nil, err
	}
	docVals, err := s.docValues(ctx, c, b.ID)
	if err != nil {
		return nil, err
	}
	allowed := map[int64]bool{}
	cache := map[int64]bool{}
	return func(id int64) bool {
		if ok, seen := cache[id]; seen {
			return ok
		}
		ok, _ := c.Evaluate(b.Slug, user, docVals[id])
		cache[id] = ok
		if ok {
			allowed[id] = true
		}
		return ok
	}, nil
}

// docValues 取一个知识库里所有文档在规则用到的字段上的值。
func (s *Server) docValues(ctx context.Context, c *access.Config, baseID int64) (map[int64]access.Values, error) {
	var ids []int64
	key := map[int64]string{}
	for _, f := range c.Fields {
		if f.Object == access.ObjectDocument {
			ids = append(ids, f.ID)
			key[f.ID] = f.Key
		}
	}
	raw, err := s.store.KBaseDocFieldValues(ctx, baseID, ids)
	if err != nil {
		return nil, err
	}
	out := map[int64]access.Values{}
	for doc, vals := range raw {
		out[doc] = access.Values{}
		for fid, v := range vals {
			out[doc][key[fid]] = v
		}
	}
	return out, nil
}

// kbGates 一次算好几个库的过滤；全都不过滤时返回 nil。
func (s *Server) kbGates(ctx context.Context, v viewer, bases []*store.KBase) (map[int64]docGate, error) {
	c, err := s.accessConfig(ctx)
	if err != nil {
		return nil, err
	}
	if len(c.Rules) == 0 {
		return nil, nil
	}
	gates := map[int64]docGate{}
	for _, b := range bases {
		g, err := s.kbGate(ctx, c, v, b)
		if err != nil {
			return nil, err
		}
		if g != nil {
			gates[b.ID] = g
		}
	}
	if len(gates) == 0 {
		return nil, nil
	}
	return gates, nil
}

// allowFunc 把几个库的过滤合成检索用的一个函数。
func allowFunc(gates map[int64]docGate) func(base, doc int64) bool {
	if gates == nil {
		return nil
	}
	return func(base, doc int64) bool {
		g := gates[base]
		return g == nil || g(doc)
	}
}

// canSeeDoc 判断这个人能不能看这篇文档（库级权限之外，再按字段规则）。
func (s *Server) canSeeDoc(ctx context.Context, v viewer, b *store.KBase, docID int64) (bool, error) {
	gates, err := s.kbGates(ctx, v, []*store.KBase{b})
	if err != nil || gates == nil {
		return err == nil, err
	}
	return gates[b.ID](docID), nil
}

// filterDocs 去掉看不到的文档。
func filterDocs[T any](ctx context.Context, s *Server, v viewer, b *store.KBase, docs []T, id func(T) int64) ([]T, error) {
	gates, err := s.kbGates(ctx, v, []*store.KBase{b})
	if err != nil || gates == nil {
		return docs, err
	}
	g := gates[b.ID]
	out := docs[:0:0]
	for _, d := range docs {
		if g(id(d)) {
			out = append(out, d)
		}
	}
	return out, nil
}

// ---- 管理接口：字段 ----

type fieldItem struct {
	access.Field
	// 有多少个对象填了值；被几条规则用到
	Values int      `json:"values"`
	Rules  []string `json:"rules"`
}

// GET /api/admin/fields：全部字段和规则。
func (s *Server) handleListFields(w http.ResponseWriter, r *http.Request) {
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	items := make([]fieldItem, len(c.Fields))
	for i, f := range c.Fields {
		n, _ := s.store.CountFieldValues(r.Context(), f.ID)
		items[i] = fieldItem{Field: f, Values: n, Rules: rulesUsing(c, f)}
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": items, "rules": c.Rules, "builtins": access.UserBuiltins})
}

func rulesUsing(c *access.Config, f access.Field) []string {
	out := []string{}
	for _, r := range c.Rules {
		if (f.Object == access.ObjectDocument && r.DocField == f.Key) || (f.Object == access.ObjectUser && r.UserField == f.Key) {
			out = append(out, r.Name)
		}
	}
	return out
}

// POST /api/admin/fields
func (s *Server) handleCreateField(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	var f access.Field
	if !readJSON(w, r, &f) {
		return
	}
	f.ID, f.Source = 0, ""
	if msg := access.ValidateField(&f); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if strings.Contains(f.Key, ".") || strings.Contains(f.Key, "/") {
		writeErr(w, http.StatusBadRequest, "手工建的字段键里不能有 . 和 /（这是扩展带进来的字段用的）")
		return
	}
	if err := s.store.CreateField(r.Context(), &f, currentUser(r).ID); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, http.StatusConflict, "已经有同名的字段了")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditFieldCreate, f.Object+"."+f.Key, map[string]any{"type": f.Type, "access": f.Access})
	writeJSON(w, http.StatusCreated, f)
}

// PATCH /api/admin/fields/{id} {label, options, access}：类型和键定了不能改；被规则用到的字段不能关掉访问控制、不能删选项。
func (s *Server) handlePatchField(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	old, err := s.store.GetField(r.Context(), id)
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "没有这个字段")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	var req struct {
		Label   *string   `json:"label"`
		Options *[]string `json:"options"`
		Access  *bool     `json:"access"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	f := *old
	if req.Label != nil {
		f.Label = *req.Label
	}
	if req.Options != nil {
		f.Options = *req.Options
	}
	if req.Access != nil {
		f.Access = *req.Access
	}
	if msg := access.ValidateField(&f); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if used := rulesUsing(c, *old); len(used) > 0 {
		if !f.Access {
			writeErr(w, http.StatusConflict, "规则「"+strings.Join(used, "、")+"」用着这个字段，不能关掉访问控制；先停用或删掉这些规则")
			return
		}
		for _, o := range old.Options {
			if !slices.Contains(f.Options, o) {
				writeErr(w, http.StatusConflict, "规则用着这个字段，不能删掉选项「"+o+"」：删掉之后填了这个值的文档会突然变成谁都看不到或谁都看得到")
				return
			}
		}
		if old.Type == access.TypeLevel && !slices.Equal(old.Options, f.Options[:min(len(f.Options), len(old.Options))]) {
			writeErr(w, http.StatusConflict, "规则用着这个等级字段，调整已有等级的顺序要先用 dsh-ee config plan 看影响范围，再通过配置变更提交")
			return
		}
	}
	if err := s.store.UpdateField(r.Context(), &f); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditFieldUpdate, f.Object+"."+f.Key, map[string]any{"options": f.Options, "access": f.Access})
	writeJSON(w, http.StatusOK, f)
}

// DELETE /api/admin/fields/{id}：被规则用到的字段不能删。
func (s *Server) handleDeleteField(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	f, err := s.store.GetField(r.Context(), id)
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "没有这个字段")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if used := rulesUsing(c, *f); len(used) > 0 {
		writeErr(w, http.StatusConflict, "规则「"+strings.Join(used, "、")+"」用着这个字段，先删掉这些规则")
		return
	}
	if err := s.store.DeleteField(r.Context(), id); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditFieldDelete, f.Object+"."+f.Key, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- 管理接口：规则 ----

// POST /api/admin/access-rules
func (s *Server) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	var req access.Rule
	if !readJSON(w, r, &req) {
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	req.ID = 0
	if msg := c.ValidateRule(&req); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.CreateAccessRule(r.Context(), &req, currentUser(r).ID); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, http.StatusConflict, "已经有同名的规则了")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditRuleCreate, req.Name, map[string]any{"rule": req})
	s.hub.notify()
	writeJSON(w, http.StatusCreated, req)
}

// PUT /api/admin/access-rules/{id}
func (s *Server) handleUpdateRule(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetAccessRule(r.Context(), id); notFound(err) {
		writeErr(w, http.StatusNotFound, "没有这条规则")
		return
	} else if err != nil {
		s.internalErr(w, r, err)
		return
	}
	var req access.Rule
	if !readJSON(w, r, &req) {
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	req.ID = id
	if msg := c.ValidateRule(&req); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.store.UpdateAccessRule(r.Context(), &req); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			writeErr(w, http.StatusConflict, "已经有同名的规则了")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditRuleUpdate, req.Name, map[string]any{"rule": req})
	s.hub.notify()
	writeJSON(w, http.StatusOK, req)
}

// DELETE /api/admin/access-rules/{id}
func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rule, err := s.store.GetAccessRule(r.Context(), id)
	if notFound(err) {
		writeErr(w, http.StatusNotFound, "没有这条规则")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if err := s.store.DeleteAccessRule(r.Context(), id); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditRuleDelete, rule.Name, nil)
	s.hub.notify()
	w.WriteHeader(http.StatusNoContent)
}

// ---- 字段值 ----

// objectFields 是一个对象的字段和值，给页面显示和编辑。
type objectFieldView struct {
	access.Field
	Value any `json:"value"`
	// 当前这个人能不能改
	Editable bool `json:"editable"`
}

func (s *Server) objectFieldViews(ctx context.Context, c *access.Config, object string, id int64, canEdit, canManage bool) ([]objectFieldView, error) {
	list, err := s.store.ObjectFieldValues(ctx, object, id)
	if err != nil {
		return nil, err
	}
	vals := valuesOf(c, object, list)
	out := []objectFieldView{}
	for _, f := range c.Fields {
		if f.Object != object {
			continue
		}
		out = append(out, objectFieldView{Field: f, Value: vals[f.Key], Editable: canManage || (canEdit && !f.Access)})
	}
	return out, nil
}

// canEditObject 判断这个人能不能改这个对象的字段：用于访问控制的字段只有 fields.manage 能改。
func (s *Server) fieldObject(w http.ResponseWriter, r *http.Request) (object string, id int64, canEdit bool, ok bool) {
	object = r.PathValue("object")
	id, err := strconv.ParseInt(r.PathValue("oid"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "对象 id 不对")
		return "", 0, false, false
	}
	v := viewerOf(r)
	ctx := r.Context()
	switch object {
	case access.ObjectDocument:
		d, err := s.store.GetKDoc(ctx, id)
		if err != nil {
			writeErr(w, http.StatusNotFound, "文档不存在，或你没有查看权限")
			return "", 0, false, false
		}
		b, err := s.store.GetKBase(ctx, d.BaseID)
		if err != nil || !v.kbReads(b) {
			writeErr(w, http.StatusNotFound, "文档不存在，或你没有查看权限")
			return "", 0, false, false
		}
		if seen, err := s.canSeeDoc(ctx, v, b, id); err != nil || !seen {
			writeErr(w, http.StatusNotFound, "文档不存在，或你没有查看权限")
			return "", 0, false, false
		}
		return object, id, v.kbMaintains(b), true
	case access.ObjectKBase:
		b, err := s.store.GetKBase(ctx, id)
		if err != nil || !v.kbReads(b) {
			writeErr(w, http.StatusNotFound, "知识库不存在，或你没有查看权限")
			return "", 0, false, false
		}
		return object, id, v.kbMaintains(b), true
	case access.ObjectUser:
		if !v.access.Has(rbac.UsersView, rbac.FieldsManage) && id != v.user.ID {
			writeErr(w, http.StatusNotFound, "用户不存在")
			return "", 0, false, false
		}
		if _, err := s.store.GetUser(ctx, id); err != nil {
			writeErr(w, http.StatusNotFound, "用户不存在")
			return "", 0, false, false
		}
		// 自己不能改自己的属性（docs/access-control.md §3.2「不能给自己授权」对字段同样适用）
		return object, id, v.access.Has(rbac.UsersManage) && id != v.user.ID, true
	}
	writeErr(w, http.StatusBadRequest, "对象只能是 document / kbase / user")
	return "", 0, false, false
}

// GET /api/fields/{object}/{oid}：一个对象的字段和值。
func (s *Server) handleObjectFields(w http.ResponseWriter, r *http.Request) {
	object, id, canEdit, ok := s.fieldObject(w, r)
	if !ok {
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	canManage := viewerOf(r).access.Has(rbac.FieldsManage) && !(object == access.ObjectUser && id == currentUser(r).ID && !s.selfGrantAllowed(r.Context()))
	views, err := s.objectFieldViews(r.Context(), c, object, id, canEdit, canManage)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": views})
}

// PUT /api/fields/{object}/{oid} {values: {键: 值}}：改字段值。值为 null 表示清空。
func (s *Server) handleSetObjectFields(w http.ResponseWriter, r *http.Request) {
	object, id, canEdit, ok := s.fieldObject(w, r)
	if !ok {
		return
	}
	var req struct {
		Values map[string]any `json:"values"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	v := viewerOf(r)
	canManage := v.access.Has(rbac.FieldsManage)
	if object == access.ObjectUser && id == v.user.ID && !s.selfGrantAllowed(r.Context()) {
		writeErr(w, http.StatusForbidden, "不能改自己的属性：请另一位管理员来改")
		return
	}
	type change struct {
		f *access.Field
		v any
	}
	var changes []change
	for key, raw := range req.Values {
		f := c.Field(object, key)
		if f == nil {
			writeErr(w, http.StatusBadRequest, "没有字段 "+key)
			return
		}
		if f.Access && !canManage {
			writeErr(w, http.StatusForbidden, "「"+f.Label+"」用于访问控制，只有有「管理字段和访问规则」权限的人能改")
			return
		}
		if !f.Access && !canEdit && !canManage {
			writeErr(w, http.StatusForbidden, "你不能改这个对象的字段")
			return
		}
		val, msg := access.CleanValue(f, raw)
		if msg != "" {
			writeErr(w, http.StatusBadRequest, msg)
			return
		}
		changes = append(changes, change{f, val})
	}
	for _, ch := range changes {
		if _, err := s.store.SetFieldValue(r.Context(), object, id, ch.f.ID, ch.v, true, v.user.ID); err != nil {
			s.internalErr(w, r, err)
			return
		}
		s.audit(r, auditFieldValue, fmt.Sprintf("%s:%d", object, id), map[string]any{"field": ch.f.Key, "value": ch.v, "access": ch.f.Access})
	}
	s.hub.notify()
	views, err := s.objectFieldViews(r.Context(), c, object, id, canEdit, canManage)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"fields": views})
}

// ---- explain / simulate（docs/agent-dev.md §5.2）----

type explainReq struct {
	// 用户名或 id
	User string `json:"user"`
	// 文档 id
	Doc int64 `json:"doc"`
	// 要试算的配置（还没应用的）；不给用当前的
	Config *access.Config `json:"config"`
}

type explainStep struct {
	Stage  string `json:"stage"`
	Result string `json:"result"`
	Reason string `json:"reason"`
	*access.Step
}

// findUser 按用户名或 id 找人。
func (s *Server) findUser(ctx context.Context, who string) (*store.User, error) {
	if id, err := strconv.ParseInt(who, 10, 64); err == nil {
		return s.store.GetUser(ctx, id)
	}
	return s.store.GetUserByUsername(ctx, who)
}

// explainAccess 解释一个人能不能看一篇文档：库级权限、维护者、每条字段规则。c 为 nil 用当前配置。
func (s *Server) explainAccess(ctx context.Context, c *access.Config, u *store.User, d *store.KDoc) (bool, []explainStep, error) {
	if c == nil {
		var err error
		if c, err = s.accessConfig(ctx); err != nil {
			return false, nil, err
		}
	}
	b, err := s.store.GetKBase(ctx, d.BaseID)
	if err != nil {
		return false, nil, err
	}
	a, err := s.store.UserAccess(ctx, u.ID)
	if err != nil {
		return false, nil, err
	}
	v := viewer{user: u, access: a}
	var steps []explainStep
	if u.Disabled {
		return false, append(steps, explainStep{Stage: "账号", Result: "deny", Reason: "账号已停用"}), nil
	}
	if !v.kbReads(b) {
		return false, append(steps, explainStep{Stage: "知识库权限", Result: "deny", Reason: "不在知识库「" + b.Title + "」的可用范围内，也不是维护者"}), nil
	}
	steps = append(steps, explainStep{Stage: "知识库权限", Result: "pass", Reason: "在知识库「" + b.Title + "」的可用范围内"})
	if v.kbMaintains(b) {
		return true, append(steps, explainStep{Stage: "维护者", Result: "pass", Reason: "是这个库的维护者（或能管理知识库），字段规则不限制维护者"}), nil
	}
	if !c.Active(b.Slug) {
		return true, append(steps, explainStep{Stage: "字段规则", Result: "pass", Reason: "没有对这个库生效的规则"}), nil
	}
	user, err := s.userValues(ctx, c, u, a)
	if err != nil {
		return false, nil, err
	}
	list, err := s.store.ObjectFieldValues(ctx, access.ObjectDocument, d.ID)
	if err != nil {
		return false, nil, err
	}
	ok, rs := c.Evaluate(b.Slug, user, valuesOf(c, access.ObjectDocument, list))
	for i := range rs {
		st := rs[i]
		steps = append(steps, explainStep{Stage: "字段规则", Result: st.Result, Reason: st.Reason, Step: &st})
	}
	return ok, steps, nil
}

// prepareConfig 检查一份试算用的配置：字段要合法，规则要对得上字段。字段的 id 按键对上现有的（值是按 id 存的）。
func (s *Server) prepareConfig(ctx context.Context, c *access.Config) (*access.Config, string) {
	if c == nil {
		return nil, ""
	}
	cur, err := s.store.ListFields(ctx)
	if err != nil {
		return nil, err.Error()
	}
	for i := range c.Fields {
		f := &c.Fields[i]
		if msg := access.ValidateField(f); msg != "" {
			return nil, "字段 " + f.Key + "：" + msg
		}
		f.ID = 0
		for _, old := range cur {
			if old.Object == f.Object && old.Key == f.Key {
				f.ID = old.ID
			}
		}
	}
	for i := range c.Rules {
		if msg := c.ValidateRule(&c.Rules[i]); msg != "" {
			return nil, "规则「" + c.Rules[i].Name + "」：" + msg
		}
	}
	return c, ""
}

// POST /api/admin/access/explain {user, doc, config?}
func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	var req explainReq
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	c, msg := s.prepareConfig(ctx, req.Config)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	u, err := s.findUser(ctx, req.User)
	if err != nil {
		writeErr(w, http.StatusNotFound, "没有这个用户")
		return
	}
	d, err := s.store.GetKDoc(ctx, req.Doc)
	if err != nil || !s.callerSeesDoc(r, d) {
		// 只能问到自己看得到的文档，不然 explain 会变成探测文档存在的口子
		writeErr(w, http.StatusNotFound, "没有这篇文档，或你自己看不到它")
		return
	}
	ok, steps, err := s.explainAccess(ctx, c, u, d)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"allowed": ok, "user": u.Username, "doc": d.ID, "title": d.Title, "steps": steps})
}

// callerSeesDoc：调接口的人自己能不能看这篇。
func (s *Server) callerSeesDoc(r *http.Request, d *store.KDoc) bool {
	v := viewerOf(r)
	b, err := s.store.GetKBase(r.Context(), d.BaseID)
	if err != nil || !v.kbReads(b) {
		return false
	}
	ok, err := s.canSeeDoc(r.Context(), v, b, d.ID)
	return err == nil && ok
}

// POST /api/admin/access/simulate {users: [...], docs: [...] | kb: 标识, config?}：一张「谁能看什么」的表。
func (s *Server) handleSimulate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Users  []string       `json:"users"`
		Docs   []int64        `json:"docs"`
		KB     string         `json:"kb"`
		Config *access.Config `json:"config"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	ctx := r.Context()
	c, msg := s.prepareConfig(ctx, req.Config)
	if msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	if len(req.Users) == 0 || len(req.Users) > 50 {
		writeErr(w, http.StatusBadRequest, "users 要给 1～50 个用户名")
		return
	}
	var docs []*store.KDoc
	if req.KB != "" {
		b, err := s.store.GetKBaseBySlug(ctx, req.KB)
		if err != nil {
			writeErr(w, http.StatusNotFound, "没有知识库 "+req.KB)
			return
		}
		if docs, err = s.store.ListKDocs(ctx, b.ID); err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	for _, id := range req.Docs {
		if d, err := s.store.GetKDoc(ctx, id); err == nil {
			docs = append(docs, d)
		}
	}
	var visible []*store.KDoc
	for _, d := range docs {
		if s.callerSeesDoc(r, d) {
			visible = append(visible, d)
		}
	}
	if len(visible) > 500 {
		visible = visible[:500]
	}
	type cell struct {
		Doc     int64  `json:"doc"`
		Title   string `json:"title"`
		Allowed bool   `json:"allowed"`
		Why     string `json:"why,omitempty"`
	}
	rows := []map[string]any{}
	for _, who := range req.Users {
		u, err := s.findUser(ctx, who)
		if err != nil {
			rows = append(rows, map[string]any{"user": who, "error": "没有这个用户"})
			continue
		}
		cells := []cell{}
		for _, d := range visible {
			ok, steps, err := s.explainAccess(ctx, c, u, d)
			if err != nil {
				s.internalErr(w, r, err)
				return
			}
			ce := cell{Doc: d.ID, Title: d.Title, Allowed: ok}
			if !ok {
				for _, st := range steps {
					if st.Result == "deny" {
						ce.Why = st.Reason
						break
					}
				}
			}
			cells = append(cells, ce)
		}
		rows = append(rows, map[string]any{"user": u.Username, "docs": cells})
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// ---- 扩展带进来的字段（docs/access-control.md §6.0）----

// extFieldKey 是扩展带进来的字段在 dsh-ee 里的键：扩展 id + 键，不会和手工建的、别的扩展建的冲突。
func extFieldKey(extID, key string) string { return extID + "." + key }

// installExtFields 装扩展时建好它带的字段。已经有的不动（升级时管理员可能改过名字和选项）。返回新建的字段键。
func (s *Server) installExtFields(ctx context.Context, m *extension.Manifest, by int64) ([]string, error) {
	if len(m.Fields) == 0 {
		return nil, nil
	}
	existing, err := s.store.ListFields(ctx)
	if err != nil {
		return nil, err
	}
	var created []string
	for _, ef := range m.Fields {
		key := extFieldKey(m.ID, ef.Key)
		if slices.ContainsFunc(existing, func(f access.Field) bool { return f.Object == ef.Object && f.Key == key }) {
			continue
		}
		f := access.Field{Object: ef.Object, Key: key, Label: ef.Label.Any(), Type: ef.Type, Options: ef.Options, Access: ef.Access, Source: m.ID}
		if msg := access.ValidateField(&f); msg != "" {
			return created, fmt.Errorf("扩展的字段 %s：%s", ef.Key, msg)
		}
		if err := s.store.CreateField(ctx, &f, by); err != nil && !errors.Is(err, store.ErrDuplicate) {
			return created, err
		}
		created = append(created, key)
	}
	return created, nil
}

// applySourceFields 把知识源给的字段值写到同步进来的文档上：只认这个扩展清单里声明过的字段，人手工改过的不覆盖。
func (s *Server) applySourceFields(ctx context.Context, sp *assetSpec, docID int64, vals map[string]any) {
	if len(vals) == 0 || sp.Extension == nil {
		return
	}
	c, err := s.accessConfig(ctx)
	if err != nil {
		return
	}
	for k, raw := range vals {
		f := c.Field(access.ObjectDocument, extFieldKey(sp.Extension.ID, k))
		if f == nil || f.Source != sp.Extension.ID {
			continue
		}
		val, msg := access.CleanValue(f, raw)
		if msg != "" {
			continue
		}
		if _, err := s.store.SetFieldValue(ctx, access.ObjectDocument, docID, f.ID, val, false, 0); err != nil {
			s.log.Warn("写知识源给的字段值失败", "doc", docID, "field", f.Key, "err", err)
		}
	}
}
