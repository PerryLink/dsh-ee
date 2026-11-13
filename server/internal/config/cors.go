package config

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// parseCORSOrigins 解析 DSH_EE_CORS_ORIGINS：逗号分隔的完整 origin。
//
// 只接受 https://host[:port]（HTTP 只放行 localhost，方便本地联调），拒绝 *、null、
// 带路径/查询串/片段/用户信息/通配符的写法。Access-Control-Allow-Origin 必须和浏览器
// 发送的 Origin 完全一致，写错不会被浏览器接受；静默忽略又会让管理员误以为浏览器内
// Harness 已放行，所以启动时直接报错（fail closed）。
func parseCORSOrigins(raw string) ([]string, error) {
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		origin := strings.TrimSpace(part)
		if origin == "" {
			return nil, errors.New("来源列表里不能有空项（逗号分隔）")
		}
		if lower := strings.ToLower(origin); lower == "*" || lower == "null" {
			return nil, fmt.Errorf("不接受 %q：请写浏览器页面所在站点的完整 origin，例如 https://harness.example.com", origin)
		}
		u, err := url.Parse(origin)
		if err != nil {
			return nil, fmt.Errorf("%q 不是合法的 URL", origin)
		}
		scheme := strings.ToLower(u.Scheme)
		if scheme != "https" && !(scheme == "http" && strings.EqualFold(u.Hostname(), "localhost")) {
			return nil, fmt.Errorf("%q 必须是 https://host[:port]；http 只允许 localhost", origin)
		}
		// origin 由 scheme + host + port 组成，不允许带通配符、路径、查询串、片段或用户信息。
		// 尾部的一个 / 是常见写法，等价于不带路径，放行。
		if u.Host == "" || strings.Contains(u.Host, "*") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("%q 必须是完整的 origin（scheme://host[:port]），不能带通配符、路径、查询串或用户信息", origin)
		}
		normalized := scheme + "://" + strings.ToLower(u.Host)
		if seen[normalized] {
			continue
		}
		seen[normalized] = true
		origins = append(origins, normalized)
	}
	return origins, nil
}
