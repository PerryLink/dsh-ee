package api

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

const (
	tokenRevoked = "token_revoked"
	tokenExpired = "token_expired"
	tokenInvalid = "token_invalid"
)

type bearerError string

func (e bearerError) Error() string { return string(e) }

type ScopeSet map[string]bool

func (s ScopeSet) Has(scope string) bool { return s[scope] }

type Principal struct {
	User      *store.User
	Access    *rbac.Access
	Scopes    ScopeSet
	TokenID   int64
	Legacy    string
	LegacyID  int64
	Limit     *int64
	Models    []string
	AdminLive bool
	// 账号本身有管理权限（与令牌当前有没有开管理模式无关）
	CanAdmin bool
	// 账号本身的权限（不按令牌范围收窄）；只用来决定给哪些说明性的东西（系统技能），不用来放行操作
	Account *rbac.Access
	// 管理模式到期时间；AdminLive 为 false 时无意义
	AdminUntil *time.Time
	Client     string
	// 令牌签发给哪类客户端：dsh / third_party（store.ClientKind*）。
	// 网页会话不是令牌，为空。写确认只认这个属性，不认可伪造的 X-DshEE-Client 头。
	ClientKind string
	Prefix     string
	ExpiresAt  *time.Time
}

func (p *Principal) quota() *store.UserQuota {
	return &store.UserQuota{
		MonthlyTokenLimit: p.User.MonthlyTokenLimit,
		AllowedModels:     p.User.AllowedModels,
		QuotaExpiresAt:    p.User.QuotaExpiresAt,
	}
}

func tokenError(code string) error { return bearerError(code) }

// effectiveAccessTokenScopes 按「管理模式是否有效」收窄 access 令牌的 scope：
// admin 范围只在 admin_until 有效且账号本身有管理权限时生效。resolveBearer 与 introspection 共用。
func effectiveAccessTokenScopes(account *rbac.Access, scopes []string, adminUntil *time.Time, now time.Time) (ScopeSet, []string, bool) {
	live := slices.Contains(scopes, rbac.ScopeAdmin) && adminUntil != nil && adminUntil.After(now) && rbac.HasAdminPerms(account)
	effective := make([]string, 0, len(scopes))
	set := ScopeSet{}
	for _, scope := range scopes {
		if scope == rbac.ScopeAdmin && !live {
			continue
		}
		effective = append(effective, scope)
		set[scope] = true
	}
	return set, effective, live
}

// legacyNpmScopes 是旧版 npm 令牌（ee_npm_）的固定 scope。
// restricted 时收敛回它的原始用途（npm registry）；默认保留旧的「顺便能调模型/工作台/连接」行为，
// 以免现有客户端突然失效。
func legacyNpmScopes(access string, restricted bool) []string {
	var scopes []string
	if restricted {
		scopes = []string{rbac.ScopeRegistryRead}
	} else {
		scopes = []string{rbac.ScopeModels, rbac.ScopeRegistryRead, rbac.ScopeHub, rbac.ScopeConnections}
	}
	if access == "publish" {
		scopes = append(scopes, rbac.ScopeRegistryPublish)
	}
	return scopes
}

// legacyKeyScopes 是旧版 API key（sk-ee-）的固定 scope；restricted 时收敛回模型网关。
func legacyKeyScopes(restricted bool) []string {
	if restricted {
		return []string{rbac.ScopeModels}
	}
	return []string{rbac.ScopeModels, rbac.ScopeHub, rbac.ScopeConnections}
}

func (s *Server) resolveBearer(ctx context.Context, token, ip string, clientHost ...string) (*Principal, error) {
	token = strings.TrimSpace(token)
	hash := secret.Hash(token)
	switch {
	case strings.HasPrefix(token, "ee_at_"):
		t, err := s.store.ActiveAccessToken(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			inactive, lookupErr := s.store.AccessTokenByHash(ctx, hash)
			if lookupErr == nil {
				switch {
				case inactive.RevokedAt != nil:
					return nil, tokenError(tokenRevoked)
				case inactive.ExpiresAt != nil && !inactive.ExpiresAt.After(time.Now()):
					return nil, tokenError(tokenExpired)
				}
			}
			return nil, tokenError(tokenInvalid)
		}
		if err != nil {
			return nil, err
		}
		u, err := s.store.GetUser(ctx, t.UserID)
		if err != nil {
			return nil, err
		}
		account, err := s.store.UserAccess(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		// admin 范围只在管理模式有效期内生效：到期（或提前结束）后令牌立刻退回员工权限，
		// 不依赖任何定时任务；账号本身没有管理权限时也不放行。
		now := time.Now()
		scopes, effective, live := effectiveAccessTokenScopes(account, t.Scopes, t.AdminUntil, now)
		needsTouch := t.LastUsedAt == nil || t.LastUsedAt.Before(now.Add(-time.Minute))
		if t.SlidingDays != nil && (t.ExpiresAt == nil || t.ExpiresAt.Before(now.AddDate(0, 0, *t.SlidingDays-1))) {
			needsTouch = true
		}
		if needsTouch {
			_ = s.store.TouchAccessToken(ctx, t.ID, ip, clientHost...)
		}
		return &Principal{
			User: u, Access: rbac.ScopeFilter(account, effective), Scopes: scopes, TokenID: t.ID,
			Limit: t.MonthlyTokenLimit, Models: t.AllowedModels, Client: t.ClientName, ClientKind: store.NormalizeClientKind(t.ClientKind),
			Prefix: t.Prefix, ExpiresAt: t.ExpiresAt, AdminLive: live, AdminUntil: t.AdminUntil, CanAdmin: rbac.HasAdminPerms(account), Account: account,
		}, nil
	case strings.HasPrefix(token, npmTokenPrefix):
		t, u, err := s.store.ActiveNpmToken(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			return nil, tokenError(tokenInvalid)
		}
		if err != nil {
			return nil, err
		}
		_ = s.store.TouchNpmToken(ctx, t.ID)
		scopes := legacyNpmScopes(t.Access, s.opts.RestrictLegacyTokenScopes)
		account, err := s.store.UserAccess(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		set := ScopeSet{}
		for _, scope := range scopes {
			set[scope] = true
		}
		// 旧版 npm 令牌没有客户端类型；按旧行为当 DSH 处理，避免老接入方式突然都要人工确认
		return &Principal{User: u, Access: rbac.ScopeFilter(account, scopes), Scopes: set, Legacy: "npm", ClientKind: store.ClientKindDSH, Prefix: t.Prefix}, nil
	case strings.HasPrefix(token, keyPrefix):
		k, _, err := s.store.ActiveKeyByHash(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			return nil, tokenError(tokenInvalid)
		}
		if err != nil {
			return nil, err
		}
		u, err := s.store.GetUser(ctx, k.UserID)
		if err != nil {
			return nil, err
		}
		account, err := s.store.UserAccess(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		scopes := legacyKeyScopes(s.opts.RestrictLegacyTokenScopes)
		set := ScopeSet{}
		for _, scope := range scopes {
			set[scope] = true
		}
		return &Principal{
			User: u, Access: rbac.ScopeFilter(account, scopes), Scopes: set, Legacy: "key", LegacyID: k.ID, ClientKind: store.ClientKindDSH,
			Models: k.AllowedModels, Prefix: k.Prefix, ExpiresAt: k.ExpiresAt,
		}, nil
	default:
		return nil, tokenError(tokenInvalid)
	}
}
