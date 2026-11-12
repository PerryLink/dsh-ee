package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/mcp"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// 公开协议面（/v1/* 与 /mcp）的每令牌限流与请求 ID。
//
// 限流和 device.go 里的 ipRate 用同一套固定窗口：每个 key 一个窗口，窗口到期重新计数。
// key 是 Authorization 里令牌的 sha256（进程里不留明文），没有令牌时退回客户端 IP；
// 同一个令牌的模型调用与 MCP 调用共用一份额度，不同令牌互不影响。

// tokenRate 是按令牌的固定窗口限流（window 内最多 limit 次）。
type tokenRate struct {
	mu        sync.Mutex
	limit     int
	window    time.Duration
	items     map[string]*rateWindow
	lastPurge time.Time
}

// 一个限流窗口内最多记多少个 key。key 来自令牌（攻击者可以随便造无效令牌），
// 所以要有上限；超限时新 key 共用一个溢出桶，内存不会跟着请求量无限涨。
const (
	maxRateKeys    = 10000
	rateOverflowID = "\x00overflow"
)

func newTokenRate(limit int, window time.Duration) *tokenRate {
	if window <= 0 {
		window = time.Minute
	}
	return &tokenRate{limit: limit, window: window, items: map[string]*rateWindow{}}
}

// rateInfo 是一次限流判定的结果，用来写 X-RateLimit-* 与 Retry-After。
type rateInfo struct {
	Limit     int
	Remaining int
	Reset     time.Time
}

// take 记一次请求并返回当前窗口状态；窗口内的第 limit+1 次起返回 false。
func (l *tokenRate) take(key string) (rateInfo, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if len(l.items) >= maxRateKeys && now.Sub(l.lastPurge) >= time.Minute {
		// 顺手清掉过期窗口；限频，避免大量新 key 时每个请求都扫一遍 map
		for k, v := range l.items {
			if now.Sub(v.start) >= l.window {
				delete(l.items, k)
			}
		}
		l.lastPurge = now
	}
	if len(l.items) >= maxRateKeys {
		// 已经不认识的 key 一律进溢出桶，已有的 key 继续用自己的窗口
		if _, ok := l.items[key]; !ok {
			key = rateOverflowID
		}
	}
	w := l.items[key]
	if w == nil || now.Sub(w.start) >= l.window {
		w = &rateWindow{start: now}
		l.items[key] = w
	}
	w.count++
	info := rateInfo{Limit: l.limit, Remaining: l.limit - w.count, Reset: w.start.Add(l.window)}
	if info.Remaining < 0 {
		info.Remaining = 0
	}
	return info, w.count <= l.limit
}

// rateLimit 给 /v1/* 与 /mcp 加上每令牌限流。成功与 429 响应都带 X-RateLimit-*，
// 429 另带 Retry-After；错误体维持各端点原来的形状（见 writeRateLimited）。
// 其它路径完全不受影响。
func (s *Server) rateLimit(next http.Handler) http.Handler {
	if s.tokenRate == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rateLimitedPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// CORS 预检由浏览器自动发出、每个跨域请求前都可能来一次，不是业务调用，
		// 不占令牌额度，也不返回限流头（否则浏览器里的第三方 Harness 会被预检拖累）。
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		info, ok := s.tokenRate.take(rateLimitKey(r))
		setRateLimitHeaders(w.Header(), info)
		if !ok {
			// Retry-After 按秒向上取整，至少 1 秒；客户端照它退避。
			retry := int((time.Until(info.Reset) + time.Second - 1) / time.Second)
			if retry < 1 {
				retry = 1
			}
			w.Header().Set("Retry-After", strconv.Itoa(retry))
			writeRateLimited(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitedPath 只覆盖公开协议面：OpenAI 兼容网关与 MCP。
func rateLimitedPath(path string) bool {
	return isV1Path(path) || isMCPPath(path)
}

func isV1Path(path string) bool { return path == "/v1" || strings.HasPrefix(path, "/v1/") }

func isMCPPath(path string) bool { return path == "/mcp" || strings.HasPrefix(path, "/mcp/") }

// rateLimitKey 用令牌决定额度；没有 Authorization 时退回客户端 IP。
// 只放哈希，进程里的 map 不留令牌明文。
func rateLimitKey(r *http.Request) string {
	if token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if token = strings.TrimSpace(token); token != "" {
			return "token:" + secret.Hash(token)
		}
	}
	return "ip:" + clientIP(r)
}

func setRateLimitHeaders(h http.Header, info rateInfo) {
	h.Set("X-RateLimit-Limit", strconv.Itoa(info.Limit))
	h.Set("X-RateLimit-Remaining", strconv.Itoa(info.Remaining))
	h.Set("X-RateLimit-Reset", strconv.FormatInt(info.Reset.Unix(), 10))
}

// writeRateLimited 超限时保留原来的错误体形状：/v1 用 OpenAI 错误壳，
// /mcp 用 JSON-RPC 错误对象（code -32000，-32000 起是服务端自定义错误码）。
func writeRateLimited(w http.ResponseWriter, r *http.Request) {
	const msg = "请求过于频繁，请稍后重试"
	switch {
	case isMCPPath(r.URL.Path):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"jsonrpc": "2.0",
			"id":      nil,
			"error":   &mcp.Error{Code: -32000, Message: msg},
		})
	case isV1Path(r.URL.Path):
		writeOpenAIErr(w, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", msg)
	default:
		writeErr(w, http.StatusTooManyRequests, msg)
	}
}

// ---- X-Request-Id ----

type requestIDCtxKey struct{}

// requestID 生成或透传 X-Request-Id：请求头里带合法值就原样沿用（不覆盖），
// 否则生成一个随机 id。响应头回写同一个值，日志里也带上，便于和客户端/代理对账。
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := validRequestID(r.Header.Get("X-Request-Id"))
		if id == "" {
			id = newRequestID()
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDCtxKey{}, id)))
	})
}

// requestIDFrom 取中间件放进上下文的请求 id（日志用）。
func requestIDFrom(r *http.Request) string {
	id, _ := r.Context().Value(requestIDCtxKey{}).(string)
	return id
}

// validRequestID 只接受能安全回写的请求 id：可见 ASCII、长度有上限。
// 不合法时返回空串，交给中间件重新生成，避免响应头被注入或日志被撑爆。
func validRequestID(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 128 {
		return ""
	}
	for i := 0; i < len(v); i++ {
		if v[i] < 0x21 || v[i] > 0x7e {
			return ""
		}
	}
	return v
}

func newRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand 基本不会失败；退回时间戳，至少保证每个请求有 id
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
