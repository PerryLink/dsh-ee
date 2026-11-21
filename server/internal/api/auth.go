package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

const (
	sessionCookie  = "dsh_ee_session"
	bcryptCost     = 12
	minPasswordLen = 10
	// bcrypt 只看前 72 字节，更长的密码直接拒绝，免得用户以为后半截有用。
	maxPasswordLen = 72
)

// 用户不存在时也做一次比对，避免靠响应时间判断用户名是否存在。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dsh-ee-dummy-password"), bcryptCost)

// comparePassword 是 bcrypt 比对的间接层：测试里可替换以统计比对次数、断言走的是
// 真实 hash 还是 dummyHash。
var comparePassword = bcrypt.CompareHashAndPassword

// bcryptHashLen 是 bcrypt 哈希的固定长度：$2a$12$（7 字节）+ 22 字节盐 + 31 字节摘要。
const bcryptHashLen = 60

// isBcryptHash 只做长度与前缀（$2a$ / $2b$ / $2y$）的粗筛，不解析，避免歧义。
// 放宽一点没有风险：筛过去的值仍会走 bcrypt；筛掉的值走 dummyHash，耗时一致。
func isBcryptHash(hash []byte) bool {
	if len(hash) != bcryptHashLen {
		return false
	}
	switch string(hash[:4]) {
	case "$2a$", "$2b$", "$2y$":
		return true
	}
	return false
}

// verifyLocalPassword 验证本地密码。PasswordHash 为空或不是 bcrypt 格式时不能直接交给
// bcrypt——bcrypt 会立刻返回错误，响应时间就能区分「账号存在但没有本地密码」（OIDC /
// 设备码等渠道建的号）与「账号不存在」（后者已用 dummyHash 等时化）。这里同样跑一次
// dummyHash 的完整比对再拒绝。没有本地密码的账号永远登录失败（空密码也不行）。
func verifyLocalPassword(u *store.User, password string) bool {
	if u == nil || !isBcryptHash([]byte(u.PasswordHash)) {
		equalizePasswordCompare(password)
		return false
	}
	return comparePassword([]byte(u.PasswordHash), []byte(password)) == nil
}

// equalizePasswordCompare 在「账号存在但注定拒绝」的分支里跑一次完整的 bcrypt（比对
// dummyHash 与用户提交的密码）。少了这一步，这些分支会比「账号不存在」（同样比对
// dummyHash）快一个 bcrypt 的时间，攻击者就能靠响应时间枚举账号。调用方随后仍返回
// errBadCredentials，外部语义不变。
func equalizePasswordCompare(password string) {
	_ = comparePassword(dummyHash, []byte(password))
}

func HashPassword(pw string) (string, error) {
	if utf8.RuneCountInString(pw) < minPasswordLen {
		return "", errWeakPassword
	}
	if len(pw) > maxPasswordLen {
		return "", errLongPassword
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(b), err
}

type validationError string

func (e validationError) Error() string { return string(e) }

const (
	errWeakPassword validationError = "密码至少 10 个字符"
	errLongPassword validationError = "密码不能超过 72 字节"
)

// Bootstrap 在每次启动时调用：同步代码里的权限点目录，菜单为空时写入默认菜单，
// 用户表为空且给了密码时创建第一个超级管理员。返回是否创建了管理员。
func Bootstrap(ctx context.Context, st *store.Store, username, password string) (created bool, err error) {
	if err := st.SyncPermissions(ctx, rbac.Catalog); err != nil {
		return false, fmt.Errorf("同步权限点: %w", err)
	}
	if err := st.ImportLegacyProviderModels(ctx); err != nil {
		return false, fmt.Errorf("导入旧的模型配置: %w", err)
	}
	if n, err := st.CountMenus(ctx); err != nil {
		return false, err
	} else if n == 0 {
		if err := st.ResetMenus(ctx, rbac.DefaultMenus); err != nil {
			return false, fmt.Errorf("写入默认菜单: %w", err)
		}
	}
	st.SetSuperDisabled(loadAdminPolicy(ctx, st).SuperDisabled)
	n, err := st.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if password == "" {
		return false, nil
	}
	if _, err := createSuperAdmin(ctx, st, username, password); err != nil {
		return false, err
	}
	return true, nil
}

// createSuperAdmin 建一个本地超级管理员。启动时的 DSH_EE_BOOTSTRAP_ADMIN_PASSWORD 与网页上的首次初始化共用。
func createSuperAdmin(ctx context.Context, st *store.Store, username, password string) (*store.User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	super, err := st.GetRoleByCode(ctx, rbac.RoleSuper)
	if err != nil {
		return nil, fmt.Errorf("找不到超级管理员角色: %w", err)
	}
	u := &store.User{Username: username, DisplayName: "管理员", PasswordHash: hash}
	if err := st.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	if err := st.SetUserRoles(ctx, u.ID, []int64{super.ID}); err != nil {
		return nil, err
	}
	return u, nil
}

// ---- 登录限流：同一用户名 15 分钟内连续失败 5 次即锁定 15 分钟 ----
//
// 锁是按用户名全局生效的，所以它只用于惩罚「继续输错密码」的请求：检查点在密码校验
// 之后（见 login），密码正确的合法用户不会被锁挡住。锁定期间不再累加失败、也不延长
// 锁定到期时间，否则未认证者每发一次错误密码就能把 until 续到 now+lockFor，把用户名
// 永久锁死。

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*failState
	// now 可替换，测试里用假时钟验证锁定到期与续期语义。
	now func() time.Time
}

type failState struct {
	count int
	first time.Time
	until time.Time
}

const (
	maxFails   = 5
	failWindow = 15 * time.Minute
	lockFor    = 15 * time.Minute
)

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{fails: map[string]*failState{}, now: time.Now}
}

// lockRemaining 返回该用户名锁定的剩余时间；不在锁定窗口内返回 0。
func (l *loginLimiter) lockRemaining(user string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[user]
	if f == nil {
		return 0
	}
	d := f.until.Sub(l.now())
	if d <= 0 {
		return 0
	}
	return d
}

func (l *loginLimiter) locked(user string) bool {
	return l.lockRemaining(user) > 0
}

func (l *loginLimiter) fail(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.now()
	f := l.fails[user]
	// 锁定窗口内不再计数：既不延长锁定，也不改变到期时间。
	if f != nil && !f.until.IsZero() && t.Before(f.until) {
		return
	}
	// 锁定已到期或失败窗口已过：从零开始计数。否则锁定到期后的一次错误就会
	// 接着旧的失败次数立刻把用户名重新锁上。
	if f == nil || t.Sub(f.first) > failWindow || (!f.until.IsZero() && !t.Before(f.until)) {
		f = &failState{first: t}
		l.fails[user] = f
	}
	f.count++
	if f.count >= maxFails {
		f.until = t.Add(lockFor)
	}
	// 顺手清理过期条目，防止表无限增长
	if len(l.fails) > 10000 {
		for k, v := range l.fails {
			if t.Sub(v.first) > failWindow && t.After(v.until) {
				delete(l.fails, k)
			}
		}
	}
}

func (l *loginLimiter) reset(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, user)
}

// ---- 处理函数 ----

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// usernameLocked 检查同一用户名的失败锁定。它只在密码校验失败之后调用（见 login）：
// 密码正确时根本走不到这里，所以锁定不会把合法用户挡在门外，未认证者也就无法用
// 错误密码对已知用户名做登录 DoS。命中时写 429（带 Retry-After）并计入 /metrics，
// 调用方直接返回；锁定期内的失败不计数、不续期。
func (s *Server) usernameLocked(w http.ResponseWriter, username string) bool {
	remaining := s.limiter.lockRemaining(username)
	if remaining <= 0 {
		return false
	}
	s.loginUsernameLimited.Add(1)
	if w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(remaining)))
	}
	// 文案里的 15 分钟与 lockFor 一致；params 给前端多语言文案插值用。
	writeErrCode(w, http.StatusTooManyRequests, codeAuthTooManyAttempts,
		map[string]any{"minutes": int(lockFor / time.Minute)}, "失败次数过多，请 15 分钟后再试")
	return true
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !readJSON(w, r, &req) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	u, ok := s.login(w, r, username, req.Password, "web")
	if !ok {
		return
	}
	s.startWebSession(w, r, u)
}

// startWebSession 建网页会话、写 cookie，并返回和 /api/auth/me 一样的内容。密码登录与演示站的一键体验共用。
func (s *Server) startWebSession(w http.ResponseWriter, r *http.Request, u *store.User) {
	token := secret.Token()
	if err := s.store.CreateSession(r.Context(), secret.Hash(token), u.ID, s.opts.SessionTTL); err != nil {
		s.internalErr(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(s.opts.SessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.opts.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	// 登录响应与 /api/auth/me 保持一致（带角色与权限），前端登录后可以直接按权限渲染。
	a, err := s.store.UserAccess(r.Context(), u.ID)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	payload, err := s.mePayload(r.Context(), u, a)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

// login 验证密码并处理限流、审计与错误响应；失败时已写好响应，返回 false。via 是 web / npm。
func (s *Server) login(w http.ResponseWriter, r *http.Request, username, password, via string) (*store.User, bool) {
	ip := s.clientIP(r)
	// 同一出口 IP 失败次数过多时直接拒绝，省掉后面昂贵的密码校验。
	if retry, blocked := s.loginIPLimiter.blocked(ip); blocked {
		s.loginIPLimited.Add(1)
		writeLoginRateLimited(w, retry, codeAuthIPRateLimited,
			map[string]any{"retry_after": retryAfterSeconds(retry)}, "登录失败次数过多，请稍后再试")
		return nil, false
	}
	u, err := s.authenticate(r.Context(), username, password)
	switch {
	case errors.Is(err, errBadCredentials):
		// 用户名锁定检查放在密码校验之后：上面的 authenticate 已经跑完恒时 bcrypt（或
		// LDAP），密码正确的合法用户会直接走成功分支，不会因锁定被挡成 429。
		// 只有密码确实错误、且该用户名正处于锁定窗口时才 429；此时不调用 limiter.fail，
		// 否则 429 会不断把锁定到期时间往后推，攻击者可以永久锁死用户名。
		if s.usernameLocked(w, username) {
			return nil, false
		}
		s.loginFailures.Add(1)
		s.loginIPLimiter.fail(ip)
		s.limiter.fail(username)
		s.auditAs(r, nil, auditLoginFailed, username, map[string]any{"via": via})
		writeErrCode(w, http.StatusUnauthorized, codeAuthBadCredentials, nil, err.Error())
		return nil, false
	case errors.Is(err, errNotProvisioned):
		s.auditAs(r, nil, auditLoginFailed, username, map[string]any{"via": via, "reason": "not_provisioned"})
		writeErrCode(w, http.StatusForbidden, codeAuthNotProvisioned, nil, err.Error())
		return nil, false
	case errors.Is(err, errDirectory):
		writeErrCode(w, http.StatusBadGateway, codeAuthDirectoryUnavailable, nil, err.Error())
		return nil, false
	case err != nil:
		s.internalErr(w, r, err)
		return nil, false
	}
	s.limiter.reset(username)
	if err := s.store.TouchLogin(r.Context(), u.ID); err != nil {
		s.log.Warn("记录登录时间失败", "err", err)
	}
	s.auditAs(r, u, auditLogin, u.Username, map[string]any{"via": via, "source": u.AuthSource})
	return u, true
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.store.DeleteSession(r.Context(), secret.Hash(c.Value)); err != nil {
			s.internalErr(w, r, err)
			return
		}
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.opts.CookieSecure, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

// meResponse 是当前用户、角色与有效权限；前端据此决定显示什么，后端每个接口仍会自行校验。
type meResponse struct {
	*store.User
	IsSuper     bool     `json:"is_super"`
	Permissions []string `json:"permissions"`
	// Limited：从 DSH 直接打开的网页会话，只带员工权限；Reauth：账号本身有管理权限，输入密码登录后可用
	Limited bool `json:"limited,omitempty"`
	Reauth  bool `json:"reauth,omitempty"`
	// Embed 非空表示这次请求用的是跨站嵌入的 ee_emb_ 会话（只能访问 mint 时绑定的那个应用：
	// 路径里的 {slug} 必须与之一致，见 requireEmbedUser）
	Embed *meEmbed `json:"embed,omitempty"`
}

type meEmbed struct {
	App  string `json:"app"`
	Host string `json:"host"`
}

// mePayload 组装「当前用户 + 角色 + 有效权限」，登录与 /api/auth/me 共用。
func (s *Server) mePayload(ctx context.Context, u *store.User, a *rbac.Access) (*meResponse, error) {
	refs, err := s.store.UserRoleRefs(ctx)
	if err != nil {
		return nil, err
	}
	u.Roles = nonNil(refs[u.ID])
	perms := []string{}
	if a.Super {
		all, err := s.store.ListPermissions(ctx)
		if err != nil {
			return nil, err
		}
		for _, p := range all {
			perms = append(perms, p.Code)
		}
	} else {
		for c := range a.Perms {
			perms = append(perms, c)
		}
		slices.Sort(perms)
	}
	return &meResponse{User: u, IsSuper: a.Super, Permissions: perms}, nil
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	payload, err := s.mePayload(r.Context(), currentUser(r), currentAccess(r))
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if sessionLimited(r) {
		payload.Limited = true
		full, err := s.store.UserAccess(r.Context(), currentUser(r).ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		payload.Reauth = rbac.HasAdminPerms(full)
	}
	if info := sessionEmbed(r); info != nil {
		payload.Embed = &meEmbed{App: info.appSlug, Host: info.hostOrigin}
	}
	writeJSON(w, http.StatusOK, payload)
}

type changePasswordReq struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordReq
	if !readJSON(w, r, &req) {
		return
	}
	u := currentUser(r)
	if u.AuthSource == store.AuthLDAP || u.AuthSource == store.AuthOIDC {
		writeErrCode(w, http.StatusBadRequest, codeAuthPasswordManagedExternally, nil,
			"这个账号的密码由企业目录或统一登录管理，请在公司的账号系统里修改")
		return
	}
	// 与登录共用等时化校验：没有本地密码的账号（理论上的脏数据）也拒绝，不因空 hash 提前返回。
	if !verifyLocalPassword(u, req.OldPassword) {
		writeErrCode(w, http.StatusBadRequest, codeAuthBadOldPassword, nil, "原密码不正确")
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		// 只有「太短 / 太长」两种可预期的失败带 code；其它（理论上不会发生）保持原行为。
		switch {
		case errors.Is(err, errWeakPassword):
			writeErrCode(w, http.StatusBadRequest, codeAuthWeakPassword,
				map[string]any{"min": minPasswordLen}, err.Error())
		case errors.Is(err, errLongPassword):
			writeErrCode(w, http.StatusBadRequest, codeAuthPasswordTooLong,
				map[string]any{"max": maxPasswordLen}, err.Error())
		default:
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	if err := s.store.UpdateUser(r.Context(), u.ID, store.UserPatch{PasswordHash: &hash}); err != nil {
		s.internalErr(w, r, err)
		return
	}
	// 改密码后让其它设备上的会话全部失效，当前请求需要重新登录。
	if err := s.store.DeleteUserSessions(r.Context(), u.ID); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, auditPasswordChange, u.Username, nil)
	w.WriteHeader(http.StatusNoContent)
}

// ---- 中间件 ----

func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			// 没有网页会话 cookie：跨站 iframe 里用 ee_emb_ 内存 Bearer（cookie 永远不会在三方上下文发送）。
			if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && strings.HasPrefix(token, embedTokenPrefix) {
				s.requireEmbedUser(w, r, next, token)
				return
			}
			writeErrCode(w, http.StatusUnauthorized, codeAuthLoginRequired, nil, "请先登录")
			return
		}
		u, limited, err := s.store.SessionInfo(r.Context(), secret.Hash(c.Value))
		if notFound(err) {
			writeErrCode(w, http.StatusUnauthorized, codeAuthSessionExpired, nil, "登录已过期，请重新登录")
			return
		}
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		// 每个请求现查权限，角色或权限改动立即生效
		a, err := s.store.UserAccess(r.Context(), u.ID)
		if err != nil {
			s.internalErr(w, r, err)
			return
		}
		if limited {
			next.ServeHTTP(w, r.WithContext(withLimitedUser(r.Context(), u, a)))
			return
		}
		next.ServeHTTP(w, r.WithContext(withUser(r.Context(), u, a)))
	})
}

// tokenRequest 处理带 Authorization: Bearer 的请求：解析令牌，返回带账号上下文的请求和主体。
// 没有 Authorization 时 hasToken 为 false；令牌无效时已经写好响应，ok 为 false。
func (s *Server) tokenRequest(w http.ResponseWriter, r *http.Request) (rp *http.Request, p *Principal, hasToken, ok bool) {
	token, has := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !has {
		return r, nil, false, false
	}
	p, err := s.resolveBearer(r.Context(), strings.TrimSpace(token), s.clientIP(r), r.Header.Get("X-DshEE-Client"), r.Header.Get("X-DshEE-Host"))
	if err != nil {
		if be, ok := err.(bearerError); ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效或已过期", "code": string(be)})
		} else {
			s.internalErr(w, r, err)
		}
		return nil, nil, true, false
	}
	return r.WithContext(withTokenUser(r.Context(), p.User, p.Access, p.TokenID, p.ClientKind)), p, true, true
}

// requireUserOrToken 接受网页会话，或带任意范围的访问令牌。令牌的权限是账号权限与令牌范围
// （rbac.ScopeFilter）的交集，之后与网页会话一样按路由权限点校验。
// 没有 Authorization 时行为与 requireUser 完全一致，浏览器跨站表单发不出 Bearer 头。
// 只给既由网页又由命令行调用的接口用。
func (s *Server) requireUserOrToken(next http.Handler) http.Handler {
	user := s.requireUser(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp, _, hasToken, ok := s.tokenRequest(w, r)
		if !hasToken {
			user.ServeHTTP(w, r)
			return
		}
		if !ok {
			return
		}
		next.ServeHTTP(w, rp)
	})
}

// requirePerm 要求拥有任一权限点（超级管理员总是通过）。须套在 requireUser 里面。
func (s *Server) requirePerm(codes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !currentAccess(r).Has(codes...) {
				writeErrCode(w, http.StatusForbidden, codeAuthForbidden, nil, "没有权限执行这个操作")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
