// Package embed 是跨站嵌入用到的来源规则：配置（DSH_EE_EMBED_ORIGINS）与
// mint 接口（POST /api/embed/sessions 的 host_origin）共用，避免两处校验口径不一致。
package embed

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// NormalizeOrigin 把宿主来源规范成 scheme://host[:port]。
//
// 只接受 https；http 仅放行回环地址（localhost / 127.0.0.1 / [::1]），方便本地联调。
// 拒绝 *、null、通配符、路径、查询串、片段和用户信息：这些值放进 CSP 不会按预期工作，
// 静默降级又可能让管理员以为嵌入已放行，所以直接报错（fail closed）。
func NormalizeOrigin(raw string) (string, error) {
	origin := strings.TrimSpace(raw)
	if origin == "" {
		return "", errors.New("来源不能为空")
	}
	switch strings.ToLower(origin) {
	case "*", "null":
		return "", fmt.Errorf("不接受 %q：请写宿主页面的完整 origin，例如 https://harness.example.com", origin)
	}
	u, err := url.Parse(origin)
	if err != nil {
		return "", fmt.Errorf("%q 不是合法的 URL", origin)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && !(scheme == "http" && isLoopback(u.Hostname())) {
		return "", fmt.Errorf("%q 必须是 https://host[:port]；http 只允许回环地址（localhost / 127.0.0.1 / [::1]）", origin)
	}
	// origin 由 scheme + host + port 组成，不允许带通配符、路径、查询串、片段或用户信息；
	// 尾部的一个 / 是常见写法，等价于不带路径，放行。
	if u.Host == "" || strings.Contains(u.Host, "*") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return "", fmt.Errorf("%q 必须是完整的 origin（scheme://host[:port]），不能带通配符、路径、查询串或用户信息", origin)
	}
	return scheme + "://" + strings.ToLower(u.Host), nil
}

// ParseOrigins 解析逗号分隔的来源列表：去空白、大小写归一化、去重。
// 任一项非法即返回错误（调用方应让服务启动失败，不静默忽略）。
func ParseOrigins(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) == "" {
			return nil, errors.New("来源列表里不能有空项（逗号分隔）")
		}
		origin, err := NormalizeOrigin(part)
		if err != nil {
			return nil, err
		}
		if seen[origin] {
			continue
		}
		seen[origin] = true
		origins = append(origins, origin)
	}
	return origins, nil
}

// Allowed 报告 origin 是否在允许列表里（都应是 NormalizeOrigin 规范化后的值）。
func Allowed(origins []string, origin string) bool {
	for _, o := range origins {
		if o == origin {
			return true
		}
	}
	return false
}

func isLoopback(host string) bool {
	switch strings.ToLower(host) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
