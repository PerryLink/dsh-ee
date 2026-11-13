package api

import (
	"net/http"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

// requireConfig 接受网页会话，或带 config 范围的令牌。令牌的权限是账号权限和 config 范围的交集，
// 所以配置令牌能导出、试算、提交变更请求，但批准变更的接口只认网页会话（docs/agent-dev.md §7「批准永远是人」）。
func (s *Server) requireConfig(next http.Handler) http.Handler {
	user := s.requireUser(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			user.ServeHTTP(w, r)
			return
		}
		p, err := s.resolveBearer(r.Context(), strings.TrimSpace(token), s.clientIP(r), r.Header.Get("X-DshEE-Client"), r.Header.Get("X-DshEE-Host"))
		if err != nil {
			if be, ok := err.(bearerError); ok {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效或已过期", "code": string(be)})
			} else {
				s.internalErr(w, r, err)
			}
			return
		}
		if !p.Scopes.Has(rbac.ScopeConfig) {
			writeErr(w, http.StatusForbidden, "这个令牌没有 config 范围：在「我的令牌」里建一个带 config 范围的令牌")
			return
		}
		next.ServeHTTP(w, r.WithContext(withTokenUser(r.Context(), p.User, p.Access, p.TokenID, p.ClientKind)))
	})
}
