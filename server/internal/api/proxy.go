package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// 反向代理的信任边界：只有直连对端本身是可信代理时，才采信它传来的
// X-Forwarded-For / X-Forwarded-Host / X-Forwarded-Proto。默认可信的只有
// 本机回环地址（同机 nginx 等）；别的机器、容器网络或云负载均衡里的代理
// 必须用 DSH_EE_TRUSTED_PROXIES 显式加入，否则这些头一律忽略。

// defaultTrustedProxies 是没配置可信代理时的默认值，与 config.DefaultTrustedProxies 保持一致：
// 只信任本机回环地址。这里单独实现一份，保证 Options 忘记接线时不会退回更宽的信任范围。
func defaultTrustedProxies() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("127.0.0.1/32"),
		netip.MustParsePrefix("::1/128"),
	}
}

// peerAddr 从 RemoteAddr（可能是 host:port）解析出对端 IP。
func peerAddr(remoteAddr string) (netip.Addr, bool) {
	host := strings.TrimSpace(remoteAddr)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func (s *Server) isTrustedProxy(addr netip.Addr) bool {
	for _, p := range s.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// trustedPeer 判断请求的直连对端是否是可信任的反向代理。
func (s *Server) trustedPeer(r *http.Request) bool {
	addr, ok := peerAddr(r.RemoteAddr)
	return ok && s.isTrustedProxy(addr)
}

// clientIP 取客户端地址。对端不是可信代理时直接用它自己的地址，不解析 X-Forwarded-For
// （否则直连的客户端可以随便伪造审计/限流用的 IP）。对端是可信代理时，从右往左跳过
// 可信代理，取第一个不可信的跳：那是最近一个可信代理看到的客户端地址。
func (s *Server) clientIP(r *http.Request) string {
	peer, ok := peerAddr(r.RemoteAddr)
	if !ok {
		return r.RemoteAddr
	}
	if !s.isTrustedProxy(peer) {
		return peer.String()
	}
	hops := forwardedHops(r.Header.Values("X-Forwarded-For"))
	for i := len(hops) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(hops[i])
		if err != nil {
			break
		}
		addr = addr.Unmap()
		if s.isTrustedProxy(addr) {
			continue
		}
		return addr.String()
	}
	return peer.String()
}

// forwardedHops 把多行 X-Forwarded-For 展开成按顺序排列的跳（每行逗号分隔）。
func forwardedHops(lines []string) []string {
	var hops []string
	for _, line := range lines {
		for _, h := range strings.Split(line, ",") {
			if h = strings.TrimSpace(h); h != "" {
				hops = append(hops, h)
			}
		}
	}
	return hops
}

// forwardedValue 取多行转发头里最后一个非空值：离服务最近的那一跳代理写入的值。
func forwardedValue(lines []string) string {
	last := ""
	for _, line := range lines {
		for _, v := range strings.Split(line, ",") {
			if v = strings.TrimSpace(v); v != "" {
				last = v
			}
		}
	}
	return last
}
