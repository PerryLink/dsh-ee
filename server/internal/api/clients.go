package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// API 客户端注册与 client_credentials（docs/token-lifecycle.md）：
//
//   - 管理员在网页上（keys.manage）创建 client_id / client_secret，secret 只显示一次；
//   - 第三方 Harness / CI 带这对凭据调用 POST /api/auth/client-token 换 access token，
//     不需要把管理员令牌交给对方；scope 取「客户端申请的范围 ∩ owner 账号当前权限」；
//   - owner 停用、客户端禁用、secret 不对，一律回 401 invalid_client，不区分原因；
//   - 签发出来的令牌 client_kind=third_party（写操作始终要员工在网页上人工确认），
//     审计 token.issue、via=client_credentials。

const (
	apiClientIDPrefix     = "dshc_"
	apiClientSecretPrefix = "dshs_"
	// 换出来的 access token 有效期；到期后客户端用同一对凭据再换一枚即可，无需人工介入。
	clientTokenAccessTTL = time.Hour
)

// newAPIClientID 生成 32 个十六进制字符的对外 client_id。
func newAPIClientID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 在受支持的平台上不会失败
	}
	return apiClientIDPrefix + hex.EncodeToString(b)
}

type apiClientView struct {
	ID              int64      `json:"id"`
	ClientID        string     `json:"client_id"`
	Name            string     `json:"name"`
	OwnerUserID     int64      `json:"owner_user_id"`
	OwnerUsername   string     `json:"owner_username,omitempty"`
	OwnerDepartment string     `json:"owner_department,omitempty"`
	Scopes          []string   `json:"scopes"`
	ClientKind      string     `json:"client_kind"`
	CreatedBy       int64      `json:"created_by"`
	DisabledAt      *time.Time `json:"disabled_at"`
	LastUsedAt      *time.Time `json:"last_used_at"`
	CreatedAt       time.Time  `json:"created_at"`
}

func apiClientViewOf(c *store.APIClient) apiClientView {
	return apiClientView{
		ID: c.ID, ClientID: c.ClientID, Name: c.Name, OwnerUserID: c.OwnerUserID,
		OwnerUsername: c.OwnerUsername, OwnerDepartment: c.OwnerDepartment,
		Scopes: nonNil(c.Scopes), ClientKind: store.NormalizeClientKind(c.ClientKind),
		CreatedBy: c.CreatedBy, DisabledAt: c.DisabledAt, LastUsedAt: c.LastUsedAt, CreatedAt: c.CreatedAt,
	}
}

type apiClientInput struct {
	Name        string   `json:"name"`
	OwnerUserID int64    `json:"owner_user_id"`
	Scopes      []string `json:"scopes"`
}

// POST /api/admin/clients：创建客户端，返回只出现一次明文 secret。
func (s *Server) handleCreateAPIClient(w http.ResponseWriter, r *http.Request) {
	var req apiClientInput
	if !readJSON(w, r, &req) {
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len([]rune(req.Name)) > 128 {
		writeErr(w, http.StatusBadRequest, "客户端名称必填，且不能超过 128 个字符")
		return
	}
	if req.OwnerUserID <= 0 {
		writeErr(w, http.StatusBadRequest, "owner_user_id 必填：令牌要签给哪个员工或机器人账号")
		return
	}
	owner, err := s.store.GetUser(r.Context(), req.OwnerUserID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusBadRequest, "owner 账号不存在")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if owner.Disabled {
		writeErr(w, http.StatusBadRequest, "owner 账号已停用，不能注册客户端")
		return
	}
	// 客户端不直接持有权限：先按 owner 当前权限校验申请范围，签发时再收敛一次。
	ownerAccess, err := s.store.UserAccess(r.Context(), owner.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if len(req.Scopes) == 0 {
		req.Scopes = []string{rbac.ScopeModels, rbac.ScopeRegistryRead, rbac.ScopeHub, rbac.ScopeConnections}
	}
	scopes, ok := validTokenScopes(ownerAccess, req.Scopes)
	if !ok {
		writeErr(w, http.StatusBadRequest, "scope 超出 owner 账号权限，或包含暂不支持的 admin")
		return
	}
	plainSecret := apiClientSecretPrefix + secret.Token()
	c := &store.APIClient{
		ClientID: newAPIClientID(), SecretHash: secret.Hash(plainSecret), Name: req.Name,
		OwnerUserID: owner.ID, Scopes: scopes, ClientKind: store.ClientKindThirdParty, CreatedBy: currentUser(r).ID,
	}
	if err := s.store.CreateAPIClient(r.Context(), c); err != nil {
		s.internalErr(w, r, err)
		return
	}
	c.OwnerUsername, c.OwnerDepartment = owner.Username, owner.Department
	s.audit(r, auditAPIClientCreate, c.ClientID, map[string]any{
		"id": c.ID, "owner_user_id": owner.ID, "scopes": scopes, "client_kind": c.ClientKind,
	})
	// 明文 secret 只在这里出现一次；列表 / 详情永远不返回它或它的哈希。
	writeJSON(w, http.StatusCreated, map[string]any{"client": apiClientViewOf(c), "client_secret": plainSecret})
}

// GET /api/admin/clients：全部客户端（含已禁用）。
func (s *Server) handleListAPIClients(w http.ResponseWriter, r *http.Request) {
	clients, err := s.store.ListAPIClients(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	out := make([]apiClientView, 0, len(clients))
	for _, c := range clients {
		out = append(out, apiClientViewOf(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

// DELETE /api/admin/clients/{client_id}：注销客户端（幂等）。
// 只影响之后能不能换 token；已经签出的 access token 按自身有效期自然过期。
func (s *Server) handleDisableAPIClient(w http.ResponseWriter, r *http.Request) {
	clientID := strings.TrimSpace(r.PathValue("client_id"))
	if clientID == "" {
		writeErr(w, http.StatusBadRequest, "client_id 不能为空")
		return
	}
	c, err := s.store.APIClientByClientID(r.Context(), clientID)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "客户端不存在")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	changed, err := s.store.DisableAPIClient(r.Context(), clientID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if changed {
		s.audit(r, auditAPIClientRevoke, c.ClientID, map[string]any{"id": c.ID, "owner_user_id": c.OwnerUserID})
	}
	// 重新读一次，带出 disabled_at（幂等重复调用也返回当前状态）
	if fresh, err := s.store.APIClientByClientID(r.Context(), clientID); err == nil {
		c = fresh
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": apiClientViewOf(c)})
}

type clientTokenRequest struct {
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
}

type clientTokenResponse struct {
	AccessToken string   `json:"access_token"`
	TokenType   string   `json:"token_type"`
	ExpiresIn   int64    `json:"expires_in"`
	Scopes      []string `json:"scopes"`
	ClientID    string   `json:"client_id"`
	ClientName  string   `json:"client_name"`
	ClientKind  string   `json:"client_kind"`
	TokenID     string   `json:"token_id"`
}

// writeClientTokenErr 统一客户端凭据失败：401 invalid_client，不说明是 client_id 不存在、
// secret 不对、客户端禁用还是 owner 停用，避免被用来探测注册信息。
func writeClientTokenErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": msg})
}

// auditClientToken 记录 client_credentials 签发。请求没有网页会话，actor 记 owner 账号，
// via=client_credentials、client=客户端名称；detail 不含明文与哈希。
func (s *Server) auditClientToken(r *http.Request, owner *store.User, c *store.APIClient, t *store.AccessToken) {
	a := &store.AuditLog{
		Action: auditTokenIssue, Target: t.Prefix, Via: "client_credentials", Client: c.Name, IP: s.clientIP(r),
		Detail: map[string]any{
			"id": t.ID, "user_id": owner.ID, "scopes": t.Scopes, "client_kind": t.ClientKind,
			"client_name": c.Name, "client_id": c.ClientID, "expires_at": t.ExpiresAt,
		},
	}
	if owner != nil {
		id := owner.ID
		a.ActorID, a.ActorName = &id, owner.Username
	}
	// 客户端可能已经断开；审计不随请求上下文取消
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := s.store.InsertAudit(ctx, a); err != nil {
		s.log.Warn("写审计日志失败", "action", a.Action, "err", err)
	}
}

// POST /api/auth/client-token：client_credentials。
//
//	{"client_id":"dshc_…","client_secret":"dshs_…"}
//	→ {"access_token":"ee_at_…","token_type":"Bearer","expires_in",
//	   "scopes", "client_id", "client_name", "client_kind":"third_party", "token_id"}
func (s *Server) handleClientToken(w http.ResponseWriter, r *http.Request) {
	if s.clientTokenRate != nil && !s.clientTokenRate.allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		writeClientTokenErr(w, http.StatusTooManyRequests, "rate_limited", "请求过于频繁，请稍后再试")
		return
	}
	var req clientTokenRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.ClientID = strings.TrimSpace(req.ClientID)
	c, err := s.store.APIClientByClientID(r.Context(), req.ClientID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeClientTokenErr(w, http.StatusUnauthorized, "invalid_client", "客户端凭据无效")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	// 空 secret 也会走到这里，hash 后比较必然失败
	got := secret.Hash(req.ClientSecret)
	if subtle.ConstantTimeCompare([]byte(got), []byte(c.SecretHash)) != 1 || c.DisabledAt != nil {
		writeClientTokenErr(w, http.StatusUnauthorized, "invalid_client", "客户端凭据无效")
		return
	}
	owner, err := s.store.GetUser(r.Context(), c.OwnerUserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeClientTokenErr(w, http.StatusUnauthorized, "invalid_client", "客户端凭据无效")
			return
		}
		s.internalErr(w, r, err)
		return
	}
	// owner 停用 / 被删：立即停止签发（已签出的令牌由 access token 校验兜底）
	if owner.Disabled {
		writeClientTokenErr(w, http.StatusUnauthorized, "invalid_client", "客户端凭据无效")
		return
	}
	ownerAccess, err := s.store.UserAccess(r.Context(), owner.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	// scope = 客户端申请范围 ∩ owner 当前权限：管理员收回权限后，下一个 token 立即变窄
	scopes := allowedScopes(ownerAccess, c.Scopes)
	if len(scopes) == 0 {
		writeClientTokenErr(w, http.StatusForbidden, "insufficient_scope", "客户端申请的 scope 已超出 owner 账号当前权限")
		return
	}
	plain := accessTokenPrefix + secret.Token()
	expires := time.Now().UTC().Add(clientTokenAccessTTL).Truncate(time.Millisecond)
	t := &store.AccessToken{
		UserID: owner.ID, Name: c.Name, Prefix: plain[:12], Source: "issued", ClientKind: store.ClientKindThirdParty,
		Scopes: scopes, ClientName: c.Name, CreatedBy: c.CreatedBy, ExpiresAt: &expires,
	}
	if err := s.store.CreateAccessToken(r.Context(), t, secret.Hash(plain)); err != nil {
		s.internalErr(w, r, err)
		return
	}
	_ = s.store.TouchAPIClient(r.Context(), c.ID)
	s.auditClientToken(r, owner, c, t)
	writeJSON(w, http.StatusOK, clientTokenResponse{
		AccessToken: plain, TokenType: "Bearer", ExpiresIn: int64(clientTokenAccessTTL.Seconds()),
		Scopes: nonNil(scopes), ClientID: c.ClientID, ClientName: c.Name,
		ClientKind: store.NormalizeClientKind(c.ClientKind), TokenID: "at:" + strconv.FormatInt(t.ID, 10),
	})
}
