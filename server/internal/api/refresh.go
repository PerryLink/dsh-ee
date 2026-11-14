package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 刷新令牌（docs/token-lifecycle.md §3）：OAuth 风格的 access + refresh 双令牌。
//
//   - 默认关闭（DSH_EE_REFRESH_TOKENS）：关闭时 POST /api/auth/refresh 返回 404，
//     设备登录 / 手工签发的响应里也不会多出 refresh_token；
//   - 开启后一次轮换：旧 refresh 标记 rotated，返回新的 access + refresh；
//   - 旧 refresh 再次出现 = 复用，吊销整个令牌族并记审计 token.refresh_reuse；
//   - 失败一律返回 invalid_grant，不区分「不存在 / 过期 / 吊销」，不泄漏令牌是否存在；
//   - 响应只含明文令牌本身，永远不返回哈希。

const refreshTokenPrefix = "ee_rt_"

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
	// 可选：客户端声明自己属于哪类客户端（dsh / third_party）；填了必须与令牌族一致
	ClientKind string `json:"client_kind"`
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	// 新 refresh token 的剩余有效秒数（受令牌族绝对上限约束）
	RefreshExpiresIn int64    `json:"refresh_expires_in"`
	Scopes           []string `json:"scopes"`
	ClientKind       string   `json:"client_kind"`
}

func writeRefreshErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// secondsUntil 把到期时间换算成剩余秒数；至少返回 1，避免客户端按 0 处理成「立刻过期」。
func secondsUntil(t time.Time) int64 {
	sec := int64(time.Until(t).Seconds())
	if sec < 1 {
		return 1
	}
	return sec
}

// newRefreshFamilyID 生成 32 位十六进制族 id。
func newRefreshFamilyID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand 在受支持的平台上不会失败
	}
	return hex.EncodeToString(b)
}

// newRefreshSeed 生成第一枚 refresh token 的库内记录与明文。明文只出现在签发响应里。
func newRefreshSeed(userID int64, clientKind, ip string, familyTTL, tokenTTL time.Duration) (*store.RefreshToken, string) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	familyExpires := now.Add(familyTTL)
	expires := now.Add(tokenTTL)
	if expires.After(familyExpires) {
		expires = familyExpires
	}
	plain := refreshTokenPrefix + secret.Token()
	rt := &store.RefreshToken{
		FamilyID: newRefreshFamilyID(), UserID: userID, ClientKind: clientKind,
		SecretHash: secret.Hash(plain), IssuedAt: now, ExpiresAt: expires,
		FamilyExpiresAt: familyExpires, CreatedIP: ip,
	}
	return rt, plain
}

// auditRefresh 记录刷新相关事件。刷新请求没有 Bearer 头（凭据在请求体里），
// 所以直接用 store.InsertAudit 并把 via 记成 token、actor 记成令牌 owner。
func (s *Server) auditRefresh(r *http.Request, u *store.User, action, target string, detail map[string]any, clientName string) {
	a := &store.AuditLog{Action: action, Target: target, Detail: detail, IP: s.clientIP(r), Via: "token", Client: clientName}
	if u != nil {
		id := u.ID
		a.ActorID, a.ActorName = &id, u.Username
	}
	// 请求可能已经结束（客户端断开），审计记录不随请求上下文取消
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
	defer cancel()
	if err := s.store.InsertAudit(ctx, a); err != nil {
		s.log.Warn("写审计日志失败", "action", action, "err", err)
	}
}

// POST /api/auth/refresh
//
//	{"refresh_token":"ee_rt_…","client_kind":"third_party"}
//	→ {"access_token","token_type":"Bearer","expires_in","refresh_token","refresh_expires_in","scopes","client_kind"}
//
// 关闭 DSH_EE_REFRESH_TOKENS 时 404；refresh token 无效/过期/已轮换/已吊销时统一 401 invalid_grant。
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if !s.opts.RefreshTokens {
		writeJSON(w, http.StatusNotFound, apiError{Error: "刷新令牌未启用"})
		return
	}
	var req refreshRequest
	if !readJSON(w, r, &req) {
		return
	}
	req.RefreshToken = strings.TrimSpace(req.RefreshToken)
	if req.RefreshToken == "" {
		writeRefreshErr(w, http.StatusBadRequest, "invalid_request", "refresh_token 必填")
		return
	}
	// 前缀不对的显然不是本服务签的令牌，直接按无效处理，不查库
	if !strings.HasPrefix(req.RefreshToken, refreshTokenPrefix) {
		writeRefreshErr(w, http.StatusUnauthorized, "invalid_grant", "刷新令牌无效，请重新登录")
		return
	}
	newAccessPlain := accessTokenPrefix + secret.Token()
	newRefreshPlain := refreshTokenPrefix + secret.Token()
	grant := store.RefreshGrant{
		OldHash: secret.Hash(req.RefreshToken), DeclaredClientKind: strings.TrimSpace(req.ClientKind), IP: s.clientIP(r),
		NewAccessHash: secret.Hash(newAccessPlain), NewAccessPrefix: newAccessPlain[:12],
		NewRefreshHash: secret.Hash(newRefreshPlain),
		AccessTTL:      s.opts.RefreshAccessTTL, RefreshTTL: s.opts.RefreshTokenTTL, Grace: s.opts.RefreshGrace,
	}
	if grant.Grace > 0 {
		// 宽限期内的重试要原样返回上次的响应：把这次的响应加密后存在旧行上。
		// 换出来的令牌明文只在内存和这条密文里出现，不写日志、不进审计。
		grant.SealSuccessor = func(rot store.RefreshRotation) ([]byte, error) {
			payload, err := json.Marshal(refreshResponse{
				AccessToken: newAccessPlain, TokenType: "Bearer", ExpiresIn: secondsUntil(rot.AccessExpires),
				RefreshToken: newRefreshPlain, RefreshExpiresIn: secondsUntil(rot.RefreshExpires),
				Scopes: nonNil(rot.Scopes), ClientKind: rot.ClientKind,
			})
			if err != nil {
				return nil, err
			}
			return s.box.Seal(payload)
		}
	}
	result, err := s.store.RotateRefreshToken(r.Context(), grant)
	switch {
	case errors.Is(err, store.ErrRefreshClientKind):
		writeRefreshErr(w, http.StatusBadRequest, "invalid_request", "client_kind 与刷新令牌不一致")
		return
	case errors.Is(err, store.ErrRefreshExpired), errors.Is(err, store.ErrRefreshInvalid), errors.Is(err, store.ErrNotFound):
		writeRefreshErr(w, http.StatusUnauthorized, "invalid_grant", "刷新令牌无效或已过期，请重新登录")
		return
	case err != nil:
		s.internalErr(w, r, err)
		return
	}
	if result.Reused {
		u, _ := s.store.GetUser(r.Context(), result.UserID)
		// 审计 detail 里只放元数据与计数：不写 refresh 明文/哈希
		s.auditRefresh(r, u, auditTokenRefreshReuse, "family:"+result.FamilyID, map[string]any{
			"family_id": result.FamilyID, "reused_refresh_token_id": result.OldRefreshID,
			"client_kind": result.ClientKind, "revoked_access_tokens": result.RevokedAccess,
			"revoked_refresh_tokens": result.RevokedRefresh,
		}, "")
		writeRefreshErr(w, http.StatusUnauthorized, "invalid_grant", "刷新令牌已被使用过，整个令牌族已吊销，请重新登录")
		return
	}
	// 宽限期重试：原样返回上次响应，不写新记录、不再记一条轮换审计。
	if result.Replayed {
		body, err := s.box.Open(result.ReplaySealed)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	// 宽限期开启时，首次成功的响应也用加密时保存的那份，保证与重试逐字节一致。
	sealed := result.SuccessorSealed
	resp := refreshResponse{
		AccessToken: newAccessPlain, TokenType: "Bearer", ExpiresIn: secondsUntil(result.AccessExpires),
		RefreshToken: newRefreshPlain, RefreshExpiresIn: secondsUntil(result.RefreshExpires),
		Scopes: nonNil(result.Scopes), ClientKind: result.ClientKind,
	}
	body := []byte(nil)
	if len(sealed) > 0 {
		if body, err = s.box.Open(sealed); err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	u, _ := s.store.GetUser(r.Context(), result.UserID)
	s.auditRefresh(r, u, auditTokenRefresh, "at:"+itoa64(result.AccessID), map[string]any{
		"family_id": result.FamilyID, "refresh_token_id": result.OldRefreshID,
		"access_token_id": result.AccessID, "client_kind": result.ClientKind, "expires_in": resp.ExpiresIn,
	}, result.ClientName)
	if body != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
