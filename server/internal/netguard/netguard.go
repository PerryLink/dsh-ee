// Package netguard 集中「服务端主动访问目标」里所有入口都不该放行的受限地址：
// 0.0.0.0/8（RFC 1122 "this network"）与已知云主机元数据地址。
//
// 上游 npm 源（registry）、企业目录 tarball 下载（directory）和连接试连（api）对
// 内网地址的放行语义不同——registry 有 DSH_EE_UPSTREAM_ALLOW_PRIVATE、目录有
// DSH_EE_DIRECTORY_TARBALL_HOSTS 白名单、连接试连本来就允许内网 MCP——但这些地址
// 在任何一处都不该访问。清单写在一处，避免三份拷贝漂移：审计发现 registry 与
// directory 漏了 0.0.0.0/8 和阿里云元数据 100.100.100.200，而连接试连已经拦了。
package netguard

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// metadataIPs 是不在链路本地段里的已知云主机元数据地址；169.254.0.0/16 这类
// 段内地址由调用方现有的链路本地判定拦。
var metadataIPs = []net.IP{
	net.ParseIP("169.254.169.254"), // AWS / GCP / Azure / Oracle 等
	net.ParseIP("169.254.170.2"),   // AWS ECS 任务凭据
	net.ParseIP("100.100.100.200"), // 阿里云 ECS
	net.ParseIP("fd00:ec2::254"),   // AWS 的 IPv6 元数据地址
}

// MetadataIPs 返回已知云主机元数据地址的副本，供调用方在自己的清单里复用。
func MetadataIPs() []net.IP {
	out := make([]net.IP, len(metadataIPs))
	copy(out, metadataIPs)
	return out
}

// IsMetadataIP 判断 ip 是不是已知的云主机元数据地址；IPv4-mapped 写法
// （::ffff:100.100.100.200）按同一地址处理。
func IsMetadataIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	v4 := ip.To4()
	for _, m := range metadataIPs {
		if v4 != nil {
			if mv4 := m.To4(); mv4 != nil && v4.Equal(mv4) {
				return true
			}
			continue
		}
		if ip.Equal(m) {
			return true
		}
	}
	return false
}

// IsZeroNetwork 判断 ip 是否落在 0.0.0.0/8（RFC 1122 "this network"）。
// net.IP.IsUnspecified 只覆盖 0.0.0.0 本身；0/8 的其它地址在部分系统上会被
// 路由到本机或直接不可达，不能当成公网目标。
func IsZeroNetwork(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 0
}

// ---- 出站请求的重定向策略 ----
//
// 管理面（npm 源检查、OIDC 发现/换令牌/JWKS、上游连通性检查等）的出站请求由管理员配置的
// 目标地址触发，但重定向目标是对方服务器当场给的。Go 默认最多跟 10 跳，而且只在跨域时
// 去掉 Authorization；自定义头（X-API-Key、员工身份头）会原样带过去，所以一次跨主机
// 重定向就能把凭据送到别处，或把「只访问这个地址」的预期绕过去。
//
// 策略写在一处，各出站 client 共用，避免像 registry / directory / api 那样三份拷贝各写一遍
// 再漂移：只跟随同 scheme/host/port 的重定向，最多 5 跳。

// SameOrigin 比较 scheme + host + 端口（默认端口按协议补齐）；scheme 与 host 大小写不敏感。
func SameOrigin(a, b string) bool {
	ua, err := url.Parse(a)
	if err != nil {
		return false
	}
	ub, err := url.Parse(b)
	if err != nil {
		return false
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		switch u.Scheme {
		case "https":
			return "443"
		default:
			return "80"
		}
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) && strings.EqualFold(ua.Hostname(), ub.Hostname()) && port(ua) == port(ub)
}

// SameOriginRedirect 可直接赋给 http.Client.CheckRedirect：只跟随同来源重定向，跨
// scheme/host/port 一律返回可读错误。错误信息保留「重定向」「另一个地址」两个词，既有的
// 管理面测试按这两个词断言被拦原因。
func SameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 {
		return errors.New("重定向次数太多")
	}
	if len(via) == 0 {
		return nil
	}
	if !SameOrigin(via[0].URL.String(), req.URL.String()) {
		return fmt.Errorf("目标服务把请求重定向到了另一个地址（%s），为免把凭据带过去，已停止（只允许同来源重定向）", req.URL.Host)
	}
	return nil
}
