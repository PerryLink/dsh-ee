package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
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
)

// 用户不存在时也做一次比对，避免靠响应时间判断用户名是否存在。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dsh-ee-dummy-password"), bcryptCost)

func HashPassword(pw string) (string, error) {
	if utf8.RuneCountInString(pw) < minPasswordLen {
		return "", errWeakPassword
	}
	// bcrypt 只看前 72 字节，更长的密码直接拒绝，免得用户以为后半截有用。
	if len(pw) > 72 {
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
	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	super, err := st.GetRoleByCode(ctx, rbac.RoleSuper)
	if err != nil {
		return false, fmt.Errorf("找不到超级管理员角色: %w", err)
	}
	u := &store.User{Username: username, DisplayName: "管理员", PasswordHash: hash}
	if err := st.CreateUser(ctx, u); err != nil {
		return false, err
	}
	if err := st.SetUserRoles(ctx, u.ID, []int64{super.ID}); err != nil {
		return false, err
	}
	return true, nil
}

// ---- 登录限流：同一用户名 15 分钟内连续失败 5 次即锁定 15 分钟 ----

type loginLimiter struct {
	mu    sync.Mutex
	fails map[string]*failState
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

func newLoginLimiter() *loginLimiter { return &loginLimiter{fails: map[string]*failState{}} }

func (l *loginLimiter) locked(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[user]
	return f != nil && time.Now().Before(f.until)
}

func (l *loginLimiter) fail(user string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := time.Now()
	f := l.fails[user]
	if f == nil || t.Sub(f.first) > failWindow {
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

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginReq
	if !readJSON(w, r, &req) {
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if s.limiter.locked(username) {
		writeErr(w, http.StatusTooManyRequests, "失败次数过多，请 15 分钟后再试")
		return
	}
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
	u, err := s.authenticate(r.Context(), username, password)
	switch {
	case errors.Is(err, errBadCredentials):
		s.limiter.fail(username)
		s.auditAs(r, nil, auditLoginFailed, username, map[string]any{"via": via})
		writeErr(w, http.StatusUnauthorized, err.Error())
		return nil, false
	case errors.Is(err, errNotProvisioned):
		s.auditAs(r, nil, auditLoginFailed, username, map[string]any{"via": via, "reason": "not_provisioned"})
		writeErr(w, http.StatusForbidden, err.Error())
		return nil, false
	case errors.Is(err, errDirectory):
		writeErr(w, http.StatusBadGateway, err.Error())
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
	// Embed 非空表示这次请求用的是跨站嵌入的 ee_emb_ 会话（只能看被嵌入的那个应用）
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
		writeErr(w, http.StatusBadRequest, "这个账号的密码由企业目录或统一登录管理，请在公司的账号系统里修改")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.OldPassword)) != nil {
		writeErr(w, http.StatusBadRequest, "原密码不正确")
		return
	}
	hash, err := HashPassword(req.NewPassword)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
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
			writeErr(w, http.StatusUnauthorized, "请先登录")
			return
		}
		u, limited, err := s.store.SessionInfo(r.Context(), secret.Hash(c.Value))
		if notFound(err) {
			writeErr(w, http.StatusUnauthorized, "登录已过期，请重新登录")
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

// requirePerm 要求拥有任一权限点（超级管理员总是通过）。须套在 requireUser 里面。
func (s *Server) requirePerm(codes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !currentAccess(r).Has(codes...) {
				writeErr(w, http.StatusForbidden, "没有权限执行这个操作")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
