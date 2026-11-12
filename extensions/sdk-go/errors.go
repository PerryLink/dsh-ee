package sdk

import (
	"fmt"
	"net/http"
)

// 错误码（docs/plugins.md §6.6）。dsh-ee 按它决定怎么告诉员工和模型。
const (
	// 这位员工还没授权，或授权过期了：dsh-ee 会让员工去「我的连接」授权
	CodeAuthorize = "authorize"
	// 管理员没配好：dsh-ee 会让员工联系管理员
	CodeSetup = "setup"
	// 源系统说这位员工没权限：原样告诉员工，不重试
	CodeForbidden = "forbidden"
	// 源系统暂时不可用
	CodeUnavailable = "unavailable"
	// 要找的东西不存在（文档 id 不对等）
	CodeNotFound = "not_found"
	// 参数不对
	CodeInvalid = "invalid"
	// 请求没有通过 dsh-ee 身份令牌的校验（SDK 自己报）
	CodeUnauthenticated = "unauthenticated"
	// 扩展没有实现这个约定接口（SDK 自己报）
	CodeNotImplemented = "not_implemented"
	CodeInternal       = "internal"
)

// Error 是带错误码的错误。处理函数里用 sdk.Errorf 返回它。
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) status() int {
	switch e.Code {
	case CodeAuthorize:
		return http.StatusForbidden
	case CodeForbidden:
		return http.StatusForbidden
	case CodeNotFound, CodeNotImplemented:
		return http.StatusNotFound
	case CodeInvalid:
		return http.StatusBadRequest
	case CodeUnavailable:
		return http.StatusServiceUnavailable
	case CodeUnauthenticated:
		return http.StatusUnauthorized
	}
	return http.StatusInternalServerError
}

// Errorf 造一个带错误码的错误，例如 sdk.Errorf(sdk.CodeAuthorize, "请先在 OA 里授权")。
func Errorf(code, format string, args ...any) error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
