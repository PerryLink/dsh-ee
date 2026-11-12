package api

import "net/http"

// registryReady 在 npm 源没有初始化时直接回 503。
// newRegistry 在数据目录不可写/磁盘满时会返回 nil，但服务仍会启动；
// 这些管理接口不能直接解引用 s.registry，否则 net/http 只能把 panic 变成断开的连接。
func (s *Server) registryReady(w http.ResponseWriter) bool {
	if s.registry != nil {
		return true
	}
	writeErr(w, http.StatusServiceUnavailable, "npm 源暂不可用：服务端数据目录未初始化或不可写")
	return false
}
