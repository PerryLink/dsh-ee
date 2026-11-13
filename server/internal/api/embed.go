package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/embed"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 微应用嵌入：v1 跨站（设计见 docs/cross-site-embedding.md；宿主接入文档见 docs/embedding.md）。
//
// 宿主与 dsh-ee 不同站时，SameSite=Lax 的网页会话 cookie 不会在 iframe 里发送。这里单独开一条
// cookie-free 认证面：
//
//  1. 宿主后端用【每用户】的 ee_at_ 令牌（agent scope）调 POST /api/embed/sessions，
//     拿一个 60 秒、一次性、绑定 用户+应用+宿主 origin 的 code；
//  2. 宿主页面把 code 通过 postMessage 交给 iframe（不进 URL、不进 localStorage）；
//  3. iframe 在自己同源的 fetch 里调 POST /api/auth/embed/exchange，换 ee_emb_ 内存 Bearer，
//     响应不 Set-Cookie；
//  4. 之后 iframe 的 /api/* 请求带 Authorization: Bearer ee_emb_…，服务端按 limited 会话处理
//     （员工权限、无 keys.self、无管理权限），并且只能访问 embedRouteAllowed 里的白名单接口；
//  5. 过期/被吊销时 iframe 不跳登录页，而是通知宿主重新 mint。
//
// 有效配置 = 部署环境变量（只读基线）∪ 管理端 settings.embed（DB 覆盖/追加）：
// crossSite 以 DB 显式值为准，allowedOrigins 取并集，sessionMinutes 非 0 时覆盖 env。
// 默认关闭；只有配置了来源且开关打开时才可用（fail closed）。

const (
	embedTokenPrefix = "ee_emb_"
	// code 60 秒有效；Options.EmbedCodeTTL 只给自动化测试缩短用。
	embedCodeTTL           = 60 * time.Second
	embedMintPerMinute     = 30
	embedExchangePerMinute = 60
	defaultEmbedSessionTTL = 2 * time.Hour
)

// embedPrincipal 是一次 ee_emb_ 请求的会话信息（放进请求上下文）。
type embedPrincipal struct {
	appSlug    string
	hostOrigin string
	tokenHash  string
	expiresAt  time.Time
}

// embedConfig 是每次请求实际生效的嵌入配置（env ∪ DB）。
type embedConfig struct {
	crossSite  bool
	origins    []string
	apps       []string
	sessionTTL time.Duration
}

// enabled 报告跨站 mint/exchange 是否可用：开关打开且至少有一个来源（fail closed）。
func (c embedConfig) enabled() bool { return c.crossSite && len(c.origins) > 0 }

func (c embedConfig) allowsApp(slug string) bool {
	if len(c.apps) == 0 {
		return true
	}
	for _, app := range c.apps {
		if app == slug {
			return true
		}
	}
	return false
}

// embedEnvSessionTTL 是部署环境变量里的会话有效期（0 时用默认 2h）。
func (s *Server) embedEnvSessionTTL() time.Duration {
	if s.opts.EmbedSessionTTL > 0 {
		return s.opts.EmbedSessionTTL
	}
	return defaultEmbedSessionTTL
}

func (s *Server) embedCodeTTL() time.Duration {
	if s.opts.EmbedCodeTTL > 0 {
		return s.opts.EmbedCodeTTL
	}
	return embedCodeTTL
}

// embedConfig 解析「环境变量基线 + 管理端设置」。CSP 中间件、mint、exchange、会话校验共用，
// 保证同一次请求/同一时刻三处看到同一份有效配置。
func (s *Server) embedConfig(ctx context.Context) (embedConfig, error) {
	cfg := embedConfig{
		crossSite:  s.opts.EmbedCrossSite,
		origins:    append([]string{}, s.opts.EmbedOrigins...),
		sessionTTL: s.embedEnvSessionTTL(),
	}
	// 单元测试会直接构造 &Server{}（没有 store）；只看环境变量基线。
	if s.store == nil {
		return cfg, nil
	}
	set, err := s.appSettings(ctx)
	if err != nil {
		return cfg, err
	}
	if set.Embed.CrossSite != nil {
		cfg.crossSite = *set.Embed.CrossSite
	}
	for _, raw := range set.Embed.AllowedOrigins {
		// 启动/保存时已经校验过；这里再规范化一次，非法值直接跳过（不把坏值写进 CSP）。
		origin, err := embed.NormalizeOrigin(raw)
		if err != nil || embed.Allowed(cfg.origins, origin) {
			continue
		}
		cfg.origins = append(cfg.origins, origin)
	}
	cfg.apps = set.Embed.Apps
	if set.Embed.SessionMinutes > 0 {
		cfg.sessionTTL = time.Duration(set.Embed.SessionMinutes) * time.Minute
	}
	return cfg, nil
}

// embedConfigView 是管理端页面读的有效值 JSON。
func embedConfigView(cfg embedConfig) map[string]any {
	origins := cfg.origins
	if origins == nil {
		origins = []string{}
	}
	apps := cfg.apps
	if apps == nil {
		apps = []string{}
	}
	return map[string]any{
		"crossSite":      cfg.crossSite,
		"allowedOrigins": origins,
		"apps":           apps,
		"sessionSeconds": int(cfg.sessionTTL.Seconds()),
	}
}

// newEmbedRequestID 生成一次嵌入请求的 id：错误体与 X-Request-Id 一致，便于宿主/日志对账。
func newEmbedRequestID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeEmbedErr 写统一的错误体：{error, code, stage, request_id}。
// stage 标明失败发生在哪一步（mint / exchange / logout / session），宿主与日志都按它排查。
func writeEmbedErr(w http.ResponseWriter, requestID, stage string, status int, code, msg string) {
	writeJSON(w, status, map[string]string{
		"error":      msg,
		"code":       code,
		"stage":      stage,
		"request_id": requestID,
	})
}

// embedCodePending 是一个等待 exchange 的一次性登录码（只存哈希，取用即删）。
type embedCodePending struct {
	userID     int64
	appSlug    string
	appTitle   string
	hostOrigin string
	next       string
	exp        time.Time
}

type embedCodes struct {
	mu sync.Mutex
	m  map[string]embedCodePending
}

func (l *embedCodes) put(hash string, p embedCodePending) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]embedCodePending{}
	}
	now := time.Now()
	for k, v := range l.m {
		if now.After(v.exp) {
			delete(l.m, k)
		}
	}
	l.m[hash] = p
}

// take 取走 code；found=false 表示无效，expired=true 表示找到了但已过期（错误码要区分）。
func (l *embedCodes) take(hash string) (p embedCodePending, found, expired bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	p, found = l.m[hash]
	if found {
		delete(l.m, hash)
	}
	return p, found, found && time.Now().After(p.exp)
}

// auditEmbedDenied 记录一次被拒绝的嵌入请求（不记 code / 令牌明文），带上已知的 app/host。
func (s *Server) auditEmbedDenied(r *http.Request, u *store.User, reason string, detail map[string]any) {
	d := map[string]any{"reason": reason}
	for k, v := range detail {
		d[k] = v
	}
	s.auditAs(r, u, auditEmbedDeny, "", d)
}

// POST /api/embed/sessions {app, host_origin, next?}
// 宿主后端用每用户 ee_at_ 令牌换一次性 code；浏览器不接触 ee_at_。
func (s *Server) handleEmbedSessionCreate(w http.ResponseWriter, r *http.Request) {
	rid := newEmbedRequestID()
	w.Header().Set("X-Request-Id", rid)
	cfg, err := s.embedConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !cfg.enabled() {
		s.auditEmbedDenied(r, nil, "embed_disabled", nil)
		writeEmbedErr(w, rid, "mint", http.StatusNotFound, "embed_disabled", "没有开启跨站嵌入")
		return
	}
	if !s.embedMintRate.allow(s.clientIP(r)) {
		writeEmbedErr(w, rid, "mint", http.StatusTooManyRequests, "rate_limited", "请求太频繁，请稍后再试")
		return
	}
	p, ok := s.dshPrincipal(w, r)
	if !ok {
		return
	}
	if p.User.AccountType != store.AccountHuman {
		s.auditEmbedDenied(r, p.User, "bot_account", nil)
		writeEmbedErr(w, rid, "mint", http.StatusForbidden, "forbidden", "机器人账号不能嵌入网页")
		return
	}
	// 每用户令牌是硬前提：共享令牌会让所有嵌入都变成令牌主人的身份，权限与审计都会失真。
	if !p.Scopes.Has(rbac.ScopeAgent) {
		s.auditEmbedDenied(r, p.User, "agent_scope_required", nil)
		writeEmbedErr(w, rid, "mint", http.StatusForbidden, "forbidden", "这个令牌不能用于嵌入：需要 agent 范围")
		return
	}
	var req struct {
		App        string `json:"app"`
		HostOrigin string `json:"host_origin"`
		Next       string `json:"next"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	appSlug := strings.TrimSpace(req.App)
	if appSlug == "" {
		writeEmbedErr(w, rid, "mint", http.StatusBadRequest, "app_required", "缺少应用标识")
		return
	}
	hostOrigin, normErr := embed.NormalizeOrigin(req.HostOrigin)
	if normErr != nil || !embed.Allowed(cfg.origins, hostOrigin) {
		s.auditEmbedDenied(r, p.User, "origin_not_allowed", map[string]any{"app": appSlug, "host_origin": strings.TrimSpace(req.HostOrigin)})
		writeEmbedErr(w, rid, "mint", http.StatusForbidden, "origin_not_allowed", "宿主来源不在允许列表里")
		return
	}
	v := viewer{user: p.User, access: p.Access, agent: s.agentOn(r.Context(), p), canAdmin: p.CanAdmin, adminLive: p.AdminLive, account: p.Account}
	a, sp, _, err := s.webAppTarget(r.Context(), v, appSlug)
	var ae *appDataError
	switch {
	case errors.As(err, &ae) && ae.status == http.StatusNotFound:
		s.auditEmbedDenied(r, p.User, "app_not_found", map[string]any{"app": appSlug, "host_origin": hostOrigin})
		writeEmbedErr(w, rid, "mint", http.StatusNotFound, "app_not_found", ae.msg)
		return
	case errors.As(err, &ae):
		s.auditEmbedDenied(r, p.User, "app_not_web", map[string]any{"app": appSlug, "host_origin": hostOrigin})
		writeEmbedErr(w, rid, "mint", ae.status, "app_not_web", ae.msg)
		return
	case err != nil:
		s.internalErr(w, r, err)
		return
	}
	if !sp.Web {
		s.auditEmbedDenied(r, p.User, "app_not_web", map[string]any{"app": appSlug, "host_origin": hostOrigin})
		writeEmbedErr(w, rid, "mint", http.StatusForbidden, "app_not_web", "这个应用没有开放网页版")
		return
	}
	// 管理端的应用白名单：空 = 所有开放网页版的应用。
	if !cfg.allowsApp(a.Slug) {
		s.auditEmbedDenied(r, p.User, "app_not_allowed", map[string]any{"app": a.Slug, "host_origin": hostOrigin})
		writeEmbedErr(w, rid, "mint", http.StatusForbidden, "app_not_allowed", "这个应用不在嵌入白名单里")
		return
	}
	next := ""
	if req.Next != "" {
		next = webNext(req.Next)
		if next == "" || !embedNextMatches(next, a.Slug) {
			writeEmbedErr(w, rid, "mint", http.StatusBadRequest, "next_invalid", "next 只能是这个应用的站内路径（/a/"+a.Slug+"）")
			return
		}
	}
	if next == "" {
		next = "/a/" + a.Slug
	}
	codeTTL := s.embedCodeTTL()
	code := secret.Token()
	s.embedCodes.put(secret.Hash(code), embedCodePending{
		userID: p.User.ID, appSlug: a.Slug, appTitle: sp.Title, hostOrigin: hostOrigin, next: next, exp: time.Now().Add(codeTTL),
	})
	s.auditAs(r, p.User, auditEmbedCreate, a.Slug, map[string]any{"app": a.Slug, "host_origin": hostOrigin})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":        code,
		"expires_in":  int(codeTTL.Seconds()),
		"app":         map[string]string{"slug": a.Slug, "title": sp.Title},
		"user":        map[string]string{"username": p.User.Username, "display_name": p.User.DisplayName},
		"session_ttl": int(cfg.sessionTTL.Seconds()),
		"request_id":  rid,
	})
}

// embedNextMatches 只允许本应用的 /a/<标识>（可带 query/hash），避免 code 被用来跳管理页。
func embedNextMatches(next, slug string) bool {
	prefix := "/a/" + slug
	if !strings.HasPrefix(next, prefix) {
		return false
	}
	rest := next[len(prefix):]
	if rest != "" && !strings.HasPrefix(rest, "/") && !strings.HasPrefix(rest, "?") && !strings.HasPrefix(rest, "#") {
		return false
	}
	// 不允许 .. / . 段：浏览器会把 /a/x/../admin 归一成 /admin。
	path := next
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == ".." || seg == "." {
			return false
		}
	}
	return true
}

// POST /api/auth/embed/exchange {code}：iframe 同源调用，code 换 ee_emb_ 内存 Bearer。
// 响应不 Set-Cookie：跨站场景不依赖任何三方 cookie。v2 起同时返回 mint 时绑定的 next（deeplink）。
func (s *Server) handleEmbedExchange(w http.ResponseWriter, r *http.Request) {
	rid := newEmbedRequestID()
	w.Header().Set("X-Request-Id", rid)
	cfg, err := s.embedConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !cfg.enabled() {
		writeEmbedErr(w, rid, "exchange", http.StatusNotFound, "embed_disabled", "没有开启跨站嵌入")
		return
	}
	if !s.embedExchangeRate.allow(s.clientIP(r)) {
		writeEmbedErr(w, rid, "exchange", http.StatusTooManyRequests, "rate_limited", "请求太频繁，请稍后再试")
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if req.Code == "" {
		writeEmbedErr(w, rid, "exchange", http.StatusBadRequest, "embed_code_invalid", "登录码无效")
		return
	}
	pending, found, expired := s.embedCodes.take(secret.Hash(req.Code))
	if expired {
		writeEmbedErr(w, rid, "exchange", http.StatusBadRequest, "embed_code_expired", "登录码已过期，请在宿主页面重新打开")
		return
	}
	if !found {
		s.auditEmbedDenied(r, nil, "code_invalid", nil)
		writeEmbedErr(w, rid, "exchange", http.StatusBadRequest, "embed_code_invalid", "登录码无效")
		return
	}
	u, err := s.store.GetUser(r.Context(), pending.userID)
	if notFound(err) || (err == nil && (u.Disabled || u.AccountType != store.AccountHuman)) {
		s.auditEmbedDenied(r, nil, "user_unavailable", map[string]any{"app": pending.appSlug, "host_origin": pending.hostOrigin})
		writeEmbedErr(w, rid, "exchange", http.StatusBadRequest, "embed_code_invalid", "登录码无效")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	token := embedTokenPrefix + secret.Token()
	if err := s.store.CreateEmbedSession(r.Context(), secret.Hash(token), u.ID, pending.appSlug, pending.hostOrigin, cfg.sessionTTL); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.auditAs(r, u, auditLogin, u.Username, map[string]any{"via": "embed", "app": pending.appSlug, "host_origin": pending.hostOrigin, "limited": true})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"token_type": "Bearer",
		"expires_in": int(cfg.sessionTTL.Seconds()),
		"app":        map[string]string{"slug": pending.appSlug, "title": pending.appTitle},
		"user":       map[string]string{"username": u.Username, "display_name": u.DisplayName},
		"limited":    true,
		"next":       pending.next,
		"request_id": rid,
	})
}

// POST /api/auth/embed/logout：撤销本次嵌入会话（不影响用户的网页会话）。
func (s *Server) handleEmbedLogout(w http.ResponseWriter, r *http.Request) {
	rid := newEmbedRequestID()
	w.Header().Set("X-Request-Id", rid)
	info := sessionEmbed(r)
	if info == nil {
		writeEmbedErr(w, rid, "logout", http.StatusBadRequest, "not_embed_session", "当前不是嵌入会话")
		return
	}
	deleted, err := s.store.DeleteEmbedSession(r.Context(), info.tokenHash)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if deleted {
		s.audit(r, auditEmbedRevoke, info.appSlug, map[string]any{"host_origin": info.hostOrigin})
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireEmbedUser 是 requireUser 的无 cookie 分支：只认 ee_emb_ 内存 Bearer。
func (s *Server) requireEmbedUser(w http.ResponseWriter, r *http.Request, next http.Handler, token string) {
	rid := newEmbedRequestID()
	w.Header().Set("X-Request-Id", rid)
	cfg, err := s.embedConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if !cfg.enabled() {
		writeErr(w, http.StatusUnauthorized, "请先登录")
		return
	}
	hash := secret.Hash(token)
	u, appSlug, hostOrigin, expiresAt, err := s.store.EmbedSessionInfo(r.Context(), hash)
	if notFound(err) {
		writeErr(w, http.StatusUnauthorized, "嵌入会话已过期，请在宿主页面重新打开")
		return
	}
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	// 路由白名单是额外约束，不替代 RBAC：白名单外的接口一律 403，避免宿主拿 token 去发插件/改配置。
	if !embedRouteAllowed(r.Method, r.URL.Path) {
		writeEmbedErr(w, rid, "session", http.StatusForbidden, "embed_scope", "嵌入会话不能访问这个接口")
		return
	}
	access, err := s.store.UserAccess(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	ctx := withEmbedUser(r.Context(), u, access, &embedPrincipal{appSlug: appSlug, hostOrigin: hostOrigin, tokenHash: hash, expiresAt: expiresAt})
	next.ServeHTTP(w, r.WithContext(ctx))
}

// embedRouteAllowed 是嵌入会话能访问的全部接口。前端启动与微应用页面实际请求的只有这些；
// 新增页面接口时要显式评估并加进来（有测试固定这张清单）。
func embedRouteAllowed(method, path string) bool {
	switch {
	case method == http.MethodGet && (path == "/api/auth/me" || path == "/api/auth/methods" || path == "/api/settings" || path == "/api/me/menus"):
		return true
	case method == http.MethodPost && path == "/api/auth/embed/logout":
		return true
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// /api/me/apps/{slug}/web：GET；/api/me/apps/{slug}/source：POST
	if len(parts) == 5 && parts[0] == "api" && parts[1] == "me" && parts[2] == "apps" {
		switch {
		case method == http.MethodGet && parts[4] == "web":
			return true
		case method == http.MethodPost && parts[4] == "source":
			return true
		}
		return false
	}
	// /api/apps/{slug}/data…：应用自带数据表
	if len(parts) >= 4 && parts[0] == "api" && parts[1] == "apps" && parts[3] == "data" {
		switch {
		case len(parts) == 4 && method == http.MethodGet: // 表清单
			return true
		case len(parts) == 5 && method == http.MethodGet: // 行列表
			return true
		case len(parts) == 5 && method == http.MethodPost: // 新增行
			return true
		case len(parts) == 6 && (method == http.MethodPatch || method == http.MethodDelete): // 改/删行
			return true
		case len(parts) == 6 && method == http.MethodGet && parts[5] == "export": // 导出 CSV
			return true
		}
		return false
	}
	return false
}
