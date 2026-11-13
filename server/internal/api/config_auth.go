package api

import (
	"net/http"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

// requireConfig 接受网页会话，或带 config 范围的令牌。令牌的权限是账号权限和 config 范围的交集，
// 所以配置令牌能导出、试算、提交变更请求，但批准变更的接口只认网页会话（docs/agent-dev.md §7「批准永远是人」）。
func (s *Server) requireConfig(next http.Handler) http.Handler {
	user := s.requireUser(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp, p, hasToken, ok := s.tokenRequest(w, r)
		if !hasToken {
			user.ServeHTTP(w, r)
			return
		}
		if !ok {
			return
		}
		if !p.Scopes.Has(rbac.ScopeConfig) {
			writeErr(w, http.StatusForbidden, "这个令牌没有 config 范围：在「我的令牌」里建一个带 config 范围的令牌")
			return
		}
		next.ServeHTTP(w, rp)
	})
}
