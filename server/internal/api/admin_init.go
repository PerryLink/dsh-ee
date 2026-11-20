package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"net/http"
	"strings"
	"sync"
)

// 首次初始化：库里还没有任何用户、也没有设置 DSH_EE_BOOTSTRAP_ADMIN_PASSWORD 时，
// 服务端启动时生成一个初始化码打印到日志。只有能看到服务端日志的人拿得到它，
// 凭它在登录页创建第一个超级管理员；建好后网页初始化永久关闭（之后用户表不再为空）。

const (
	initCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	initCodeLen      = 12
	// 初始化码错这么多次就作废，要重启服务拿新码。码有 60 bit，这只是兜底。
	initCodeMaxFails = 10
	auditInitAdmin   = "auth.init_admin"
)

// NewInitCode 生成一个初始化码，形如 ABCD-EFGH-JKLM。
func NewInitCode() string {
	b := make([]byte, initCodeLen)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	var sb strings.Builder
	for i, c := range b {
		if i > 0 && i%4 == 0 {
			sb.WriteByte('-')
		}
		sb.WriteByte(initCodeAlphabet[int(c)%len(initCodeAlphabet)])
	}
	return sb.String()
}

func normalizeInitCode(code string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
}

type initAdminState struct {
	mu    sync.Mutex
	fails int
	done  bool
}

// initAdminOpen 报告登录页是否该显示「创建管理员」：配了初始化码、没作废、库里还没有用户。
func (s *Server) initAdminOpen(ctx context.Context) bool {
	if s.opts.InitCode == "" {
		return false
	}
	s.initAdmin.mu.Lock()
	closed := s.initAdmin.done || s.initAdmin.fails >= initCodeMaxFails
	s.initAdmin.mu.Unlock()
	if closed {
		return false
	}
	n, err := s.store.CountUsers(ctx)
	return err == nil && n == 0
}

type initAdminReq struct {
	Code     string `json:"code"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleInitAdmin(w http.ResponseWriter, r *http.Request) {
	var req initAdminReq
	if !readJSON(w, r, &req) {
		return
	}
	// 整个过程持锁：两个请求同时到达时只有一个能建出管理员。
	st := &s.initAdmin
	st.mu.Lock()
	defer st.mu.Unlock()
	if s.opts.InitCode == "" || st.done {
		writeErr(w, http.StatusNotFound, "已经初始化过了，请直接登录")
		return
	}
	if st.fails >= initCodeMaxFails {
		writeErr(w, http.StatusTooManyRequests, "初始化码错误次数过多，已作废；请重启服务，从日志里拿新的初始化码")
		return
	}
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	if n > 0 {
		st.done = true
		writeErr(w, http.StatusNotFound, "已经初始化过了，请直接登录")
		return
	}
	want := normalizeInitCode(s.opts.InitCode)
	if subtle.ConstantTimeCompare([]byte(normalizeInitCode(req.Code)), []byte(want)) != 1 {
		st.fails++
		s.auditAs(r, nil, auditLoginFailed, "", map[string]any{"via": "init_admin"})
		writeErr(w, http.StatusForbidden, "初始化码不对，请到服务端日志里查看")
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if !usernameRe.MatchString(username) {
		writeErr(w, http.StatusBadRequest, "用户名只能用小写字母、数字和 . _ -，2 到 64 个字符")
		return
	}
	// 不跟随请求取消：建用户与授角色不在一个事务里，中途断开会留下没有角色的第一个用户，网页初始化也随之关闭。
	u, err := createSuperAdmin(context.WithoutCancel(r.Context()), s.store, username, req.Password)
	if err != nil {
		if ve, ok := err.(validationError); ok {
			writeErr(w, http.StatusBadRequest, string(ve))
			return
		}
		s.internalErr(w, r, err)
		return
	}
	st.done = true
	s.log.Info("已在网页上创建初始管理员", "username", u.Username)
	s.auditAs(r, u, auditInitAdmin, u.Username, nil)
	s.startWebSession(w, r, u)
}
