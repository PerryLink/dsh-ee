package config

import (
	"fmt"
	"net/netip"
	"strings"
)

// DefaultTrustedProxies 是默认可信的反向代理地址：只有本机回环地址。
// 同机 nginx、经 loopback 转发的 sidecar 都在这一档；反向代理在别的机器、
// 容器网络或云负载均衡后面时，必须用 DSH_EE_TRUSTED_PROXIES 显式加入，
// 否则服务端不会采信它传来的 X-Forwarded-*。
func DefaultTrustedProxies() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	}
}

// ParseTrustedProxies 解析逗号分隔的 IP / CIDR 列表，返回默认回环地址加上显式配置的网段。
// 空字符串表示只用默认值。裸 IP 按单个地址（/32 或 /128）处理。
func ParseTrustedProxies(s string) ([]netip.Prefix, error) {
	out := DefaultTrustedProxies()
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		p, err := parseTrustedPrefix(item)
		if err != nil {
			return nil, fmt.Errorf("DSH_EE_TRUSTED_PROXIES %q: %w", item, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func parseTrustedPrefix(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
