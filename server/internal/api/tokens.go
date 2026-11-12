package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

const accessTokenPrefix = "ee_at_"

type tokenView struct {
	ID                string     `json:"id"`
	Kind              string     `json:"kind"`
	UserID            int64      `json:"user_id,omitempty"`
	Username          string     `json:"username,omitempty"`
	Department        string     `json:"department,omitempty"`
	AccountType       string     `json:"account_type,omitempty"`
	Name              string     `json:"name"`
	Prefix            string     `json:"prefix"`
	Source            string     `json:"source"`
	Scopes            []string   `json:"scopes"`
	ClientName        string     `json:"client_name"`
	ClientOS          string     `json:"client_os"`
	MonthlyTokenLimit *int64     `json:"monthly_token_limit"`
	MonthUsed         int64      `json:"month_used"`
	AllowedModels     []string   `json:"allowed_models"`
	ExpiresAt         *time.Time `json:"expires_at"`
	LastUsedAt        *time.Time `json:"last_used_at"`
	LastIP            string     `json:"last_ip"`
	RevokedAt         *time.Time `json:"revoked_at"`
	CreatedAt         time.Time  `json:"created_at"`
	// 管理模式的到期时间；没开启或已到期时为空
	AdminUntil *time.Time `json:"admin_until"`
}

func accessView(t *store.AccessToken, used int64) tokenView {
	var adminUntil *time.Time
	if t.AdminUntil != nil && t.AdminUntil.After(time.Now()) {
		adminUntil = t.AdminUntil
	}
	return tokenView{AdminUntil: adminUntil,
		ID: "at:" + strconv.FormatInt(t.ID, 10), Kind: "access", Name: t.Name, Prefix: t.Prefix,
		UserID: t.UserID, Username: t.Username, Department: t.Department, AccountType: t.AccountType,
		Source: t.Source, Scopes: nonNil(t.Scopes), ClientName: t.ClientName, ClientOS: t.ClientOS,
		MonthlyTokenLimit: t.MonthlyTokenLimit, MonthUsed: used, AllowedModels: nonNil(t.AllowedModels),
		ExpiresAt: t.ExpiresAt, LastUsedAt: t.LastUsedAt, LastIP: t.LastIP, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt,
	}
}

func (s *Server) tokenViews(ctx context.Context, userID int64) ([]tokenView, error) {
	tokens, err := s.store.ListUserTokens(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := []tokenView{}
	for _, t := range tokens {
		used, err := s.store.TokenMonthUsage(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, accessView(t, used))
	}
	npm, err := s.store.ListNpmTokens(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, t := range npm {
		scopes := []string{rbac.ScopeModels, rbac.ScopeRegistryRead, rbac.ScopeHub, rbac.ScopeConnections}
		if t.Access == "publish" {
			scopes = append(scopes, rbac.ScopeRegistryPublish)
		}
		out = append(out, tokenView{ID: "npm:" + strconv.FormatInt(t.ID, 10), Kind: "npm", Name: t.Name, Prefix: t.Prefix,
			Source: "legacy", Scopes: scopes, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt, CreatedAt: t.CreatedAt})
	}
	keys, err := s.store.ListAPIKeys(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		out = append(out, tokenView{ID: "key:" + strconv.FormatInt(k.ID, 10), Kind: "key", Name: k.Name, Prefix: k.Prefix,
			Source: "legacy", Scopes: []string{rbac.ScopeModels, rbac.ScopeHub, rbac.ScopeConnections}, MonthlyTokenLimit: k.MonthlyTokenLimit,
			MonthUsed: k.MonthTokens, AllowedModels: nonNil(k.AllowedModels), ExpiresAt: k.ExpiresAt, LastUsedAt: k.LastUsedAt,
			RevokedAt: k.RevokedAt, CreatedAt: k.CreatedAt})
	}
	return out, nil
}

func (s *Server) handleMyTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.tokenViews(r.Context(), currentUser(r).ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": nonNil(tokens)})
}

type tokenInput struct {
	Name              string   `json:"name"`
	Scopes            []string `json:"scopes"`
	MonthlyTokenLimit *int64   `json:"monthly_token_limit"`
	AllowedModels     []string `json:"allowed_models"`
	ExpiresInDays     int      `json:"expires_in_days"`
	UserID            int64    `json:"user_id"`
}

func validTokenScopes(a *rbac.Access, scopes []string) ([]string, bool) {
	seen := map[string]bool{}
	out := []string{}
	for _, scope := range scopes {
		if scope == rbac.ScopeAdmin || seen[scope] {
			return nil, false
		}
		seen[scope] = true
		switch scope {
		case rbac.ScopeModels:
			if !a.Has(rbac.LLMUse) {
				return nil, false
			}
		case rbac.ScopeRegistryRead:
			if !a.Has(rbac.RegistryUse) {
				return nil, false
			}
		case rbac.ScopeRegistryPublish:
			if !a.Has(rbac.RegistryPublish) {
				return nil, false
			}
		case rbac.ScopeHub, rbac.ScopeConnections:
			if !a.Has(rbac.PortalView) {
				return nil, false
			}
		case rbac.ScopeAgent:
			if !a.Has(rbac.PortalView) {
				return nil, false
			}
		case rbac.ScopeConfig:
			if !a.Has(rbac.FieldsManage, rbac.RolesManage) {
				return nil, false
			}
		default:
			return nil, false
		}
		out = append(out, scope)
	}
	return out, true
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request, owner *store.User, source string, req tokenInput) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 128 {
		writeErr(w, http.StatusBadRequest, "令牌名称必填，且不能超过 128 个字符")
		return
	}
	if req.MonthlyTokenLimit != nil && *req.MonthlyTokenLimit < 0 {
		writeErr(w, http.StatusBadRequest, "monthly_token_limit 不能为负数")
		return
	}
	if req.ExpiresInDays < 0 || req.ExpiresInDays > 3650 {
		writeErr(w, http.StatusBadRequest, "expires_in_days 应在 0–3650 之间，0 表示长期有效")
		return
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{rbac.ScopeModels, rbac.ScopeRegistryRead, rbac.ScopeHub, rbac.ScopeConnections}
	}
	scopeOwner := currentAccess(r)
	if source == "issued" {
		var err error
		scopeOwner, err = s.store.UserAccess(r.Context(), owner.ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	scopes, ok := validTokenScopes(scopeOwner, req.Scopes)
	if !ok {
		writeErr(w, http.StatusBadRequest, "scope 超出账号权限，或包含暂不支持的 admin")
		return
	}
	plain := accessTokenPrefix + secret.Token()
	t := &store.AccessToken{
		UserID: owner.ID, Name: req.Name, Prefix: plain[:12], Source: source, Scopes: scopes,
		MonthlyTokenLimit: req.MonthlyTokenLimit, AllowedModels: req.AllowedModels, CreatedBy: currentUser(r).ID,
	}
	if req.ExpiresInDays > 0 {
		expires := time.Now().UTC().AddDate(0, 0, req.ExpiresInDays).Truncate(time.Millisecond)
		t.ExpiresAt = &expires
	}
	if err := s.store.CreateAccessToken(r.Context(), t, secret.Hash(plain)); err != nil {
		s.internalErr(w, r, err)
		return
	}
	action := "token.create"
	if source == "issued" {
		action = "token.issue"
	}
	s.audit(r, action, plain[:12], map[string]any{"id": t.ID, "user_id": owner.ID, "scopes": scopes})
	writeJSON(w, http.StatusCreated, map[string]any{"token": plain, "info": accessView(t, 0)})
}

func (s *Server) handleCreateMyToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireFullSession(w, r) {
		return
	}
	settings, err := s.appSettings(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !settings.Connect.AllowManualTokens {
		writeErr(w, http.StatusForbidden, "管理员已关闭员工手动生成令牌")
		return
	}
	var req tokenInput
	if !readJSON(w, r, &req) {
		return
	}
	s.createToken(w, r, currentUser(r), "manual", req)
}

func (s *Server) handleRevokeMyToken(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	kind, raw, ok := strings.Cut(id, ":")
	if !ok {
		writeErr(w, http.StatusBadRequest, "令牌 id 格式不正确")
		return
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "令牌 id 格式不正确")
		return
	}
	ctx := r.Context()
	switch kind {
	case "at":
		t, err := s.store.ListUserTokens(ctx, currentUser(r).ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		found := false
		for _, token := range t {
			if token.ID == n {
				found = true
			}
		}
		if !found {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		}
		if err := s.store.RevokeToken(ctx, n); err != nil {
			s.internalErr(w, r, err)
			return
		}
	case "npm":
		// 只能吊销自己的：不是自己的一律按不存在处理，不暴露别人的令牌 id
		own, err := s.store.ListNpmTokens(ctx, currentUser(r).ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		if !slices.ContainsFunc(own, func(token *store.NpmToken) bool { return token.ID == n }) {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		}
		if err := s.store.RevokeNpmToken(ctx, n); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "令牌不存在")
			} else {
				s.internalErr(w, r, err)
			}
			return
		}
	case "key":
		if k, err := s.store.GetAPIKey(ctx, n); notFound(err) || (err == nil && k.UserID != currentUser(r).ID) {
			writeErr(w, http.StatusNotFound, "令牌不存在")
			return
		} else if err != nil {
			s.internalErr(w, r, err)
			return
		}
		if err := s.store.RevokeAPIKey(ctx, n); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeErr(w, http.StatusNotFound, "令牌不存在")
			} else {
				s.internalErr(w, r, err)
			}
			return
		}
	default:
		writeErr(w, http.StatusBadRequest, "令牌类型不正确")
		return
	}
	s.audit(r, "token.revoke", id, nil)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminTokenScope(r *http.Request) (string, bool) {
	if currentAccess(r).Has(rbac.KeysManage) {
		return "", true
	}
	if currentAccess(r).Has(rbac.TokensDeptManage) && currentUser(r).Department != "" {
		return currentUser(r).Department, true
	}
	return "", false
}

func (s *Server) handleAdminTokens(w http.ResponseWriter, r *http.Request) {
	department, ok := s.adminTokenScope(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "没有令牌管理权限")
		return
	}
	q := r.URL.Query()
	f := store.TokenFilter{}
	if v := q.Get("user_id"); v != "" {
		id, _ := strconv.ParseInt(v, 10, 64)
		f.UserID = &id
	}
	if v := q.Get("department"); v != "" {
		f.Department = &v
	}
	if v := q.Get("source"); v != "" {
		f.Source = &v
	}
	if v := q.Get("client_name"); v != "" {
		f.ClientName = &v
	}
	if v := q.Get("status"); v == "revoked" {
		revoked := true
		f.Revoked = &revoked
	} else if v == "active" {
		revoked := false
		f.Revoked = &revoked
	}
	tokens, err := s.store.ListTokens(r.Context(), f)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	out := []tokenView{}
	for _, t := range tokens {
		if department != "" && (t.Department != department || t.AccountType != store.AccountBot) {
			continue
		}
		used, err := s.store.TokenMonthUsage(r.Context(), t.ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		out = append(out, accessView(t, used))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": nonNil(out)})
}

func (s *Server) handleIssueToken(w http.ResponseWriter, r *http.Request) {
	department, ok := s.adminTokenScope(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "没有令牌管理权限")
		return
	}
	var req tokenInput
	if !readJSON(w, r, &req) {
		return
	}
	owner, err := s.store.GetUser(r.Context(), req.UserID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "账号不存在")
		return
	}
	if department != "" && (owner.Department != department || owner.AccountType != store.AccountBot) {
		writeErr(w, http.StatusForbidden, "只能管理本部门机器人账号的令牌")
		return
	}
	s.createToken(w, r, owner, "issued", req)
}

func (s *Server) handleRevokeTokens(w http.ResponseWriter, r *http.Request) {
	// department 非空表示调用者只有「本部门令牌管理」：只能动本部门机器人账号的令牌
	department, ok := s.adminTokenScope(r)
	if !ok {
		writeErr(w, http.StatusForbidden, "没有令牌管理权限")
		return
	}
	var req struct {
		IDs        []string `json:"ids"`
		UserID     *int64   `json:"user_id"`
		ClientName *string  `json:"client_name"`
		Department *string  `json:"department"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	const deptOnly = "只能吊销本部门机器人账号的令牌"
	if len(req.IDs) > 0 {
		ids := make([]int64, 0, len(req.IDs))
		for _, raw := range req.IDs {
			kind, v, ok := strings.Cut(raw, ":")
			id, err := strconv.ParseInt(v, 10, 64)
			if !ok || kind != "at" || err != nil || id <= 0 {
				writeErr(w, http.StatusBadRequest, "批量吊销目前只支持 at:<id> 形式")
				return
			}
			t, err := s.store.AccessTokenByID(r.Context(), id)
			if notFound(err) {
				writeErr(w, http.StatusBadRequest, "令牌不存在："+raw)
				return
			}
			if err != nil {
				s.internalErr(w, r, err)
				return
			}
			// 先全部校验再动手，避免越权请求吊销了一部分
			if department != "" && (t.Department != department || t.AccountType != store.AccountBot) {
				writeErr(w, http.StatusForbidden, deptOnly)
				return
			}
			ids = append(ids, id)
		}
		count := 0
		for _, id := range ids {
			err := s.store.RevokeToken(r.Context(), id)
			if notFound(err) {
				continue // 已经吊销过
			}
			if err != nil {
				s.internalErr(w, r, err)
				return
			}
			count++
		}
		s.audit(r, "token.revoke_bulk", "ids", map[string]any{"count": count, "ids": req.IDs})
		writeJSON(w, http.StatusOK, map[string]any{"revoked": count})
		return
	}
	f := store.TokenRevokeFilter{UserID: req.UserID, ClientName: req.ClientName, Department: req.Department}
	if f.ClientName != nil && *f.ClientName == "" && f.UserID == nil && f.Department == nil {
		f.ClientName = nil // 只给空设备名等于没有条件
	}
	if f.Empty() {
		writeErr(w, http.StatusBadRequest, "请指定要吊销的令牌、账号、设备或部门")
		return
	}
	if department != "" {
		if req.Department != nil && *req.Department != department {
			writeErr(w, http.StatusForbidden, deptOnly)
			return
		}
		if req.UserID != nil {
			u, err := s.store.GetUser(r.Context(), *req.UserID)
			if err != nil || u.Department != department || u.AccountType != store.AccountBot {
				writeErr(w, http.StatusForbidden, deptOnly)
				return
			}
		}
		// 无论请求怎么写，都限定在本部门的机器人账号
		f.Department = &department
		f.BotOnly = true
	}
	n, err := s.store.RevokeTokensBy(r.Context(), f)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, "token.revoke_bulk", "filter", map[string]any{"count": n, "user_id": req.UserID, "client_name": req.ClientName, "department": f.Department})
	writeJSON(w, http.StatusOK, map[string]any{"revoked": n})
}
