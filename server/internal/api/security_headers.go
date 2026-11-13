package api

import (
	"mime"
	"net/http"
	"strings"
)

// securityHeaders 给 HTML 文档加安全头。
//
// CSP 的 frame-ancestors 决定哪些页面可以用 iframe 嵌入 dsh-ee 的网页。只有微应用网页版
// （/a/<标识>）放行有效配置里的宿主 origin；其它页面（尤其是 /admin 管理页）一律 'self'，
// 免得把管理后台也框进配置过的宿主里。其余三个头是通用的低风险加固。
// 只加在 HTML 出口上：静态资源、JSON、3xx 重定向、SSE 流都不受影响。
//
// 不设 X-Frame-Options：ALLOW-FROM 已被废弃，现代浏览器看 frame-ancestors；两边各写一份
// （反代一份、Go 一份）反而容易出现「改了其中一边以为放行了」的歧义，CSP 是唯一事实源。
//
// 来源每请求从「env ∪ settings.embed」解析（appSettings 有 5 秒缓存），管理端保存后立即生效。
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		csp := "frame-ancestors 'self'"
		if isEmbedPage(r.URL.Path) {
			if cfg, err := s.embedConfig(r.Context()); err == nil {
				for _, origin := range cfg.origins {
					csp += " " + origin
				}
			}
		}
		next.ServeHTTP(&securityHeadersWriter{ResponseWriter: w, csp: csp}, r)
	})
}

// isEmbedPage 只有微应用网页版的路由可以配置宿主 origin；/admin、/login、/go 等页面
// 保持只允许同站，避免 DSH_EE_EMBED_ORIGINS 顺带把管理页面也放出去。
func isEmbedPage(path string) bool {
	return strings.HasPrefix(path, "/a/")
}

// securityHeadersWriter 在第一次写响应前检查 Content-Type，只给 HTML 加头。
// 响应头必须先于 WriteHeader 落到 header map 里，所以 WriteHeader 和 Write 两个入口都要判断
// （handler 不调 WriteHeader 时由第一次 Write 隐式写 200）。
type securityHeadersWriter struct {
	http.ResponseWriter
	csp   string
	wrote bool
}

func (w *securityHeadersWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	// 1xx 只是预告，真正的响应头后面还会写。
	if status < 200 {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	w.wrote = true
	// 3xx 不是可渲染的文档：http.Redirect 会给 GET 加一个 text/html 的跳转体，
	// 但这层加的是文档安全头，不加在重定向上，免得覆盖 handoff 这类接口自己设的 no-referrer。
	if status < 300 || status >= 400 {
		w.setSecurityHeaders()
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *securityHeadersWriter) Write(p []byte) (int, error) {
	if !w.wrote {
		// handler 没写 WriteHeader，第一次 Write 隐式写 200。
		w.wrote = true
		w.setSecurityHeaders()
	}
	return w.ResponseWriter.Write(p)
}

// setSecurityHeaders 只给 HTML 出口加头；非 HTML（JSON、静态资源、SSE）保持原样。
func (w *securityHeadersWriter) setSecurityHeaders() {
	if !isHTMLContentType(w.Header().Get("Content-Type")) {
		return
	}
	h := w.Header()
	h.Set("Content-Security-Policy", w.csp)
	h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}

// Flush 让 SSE（网关流式回答、/mcp 事件流）穿过这层中间件。
func (w *securityHeadersWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap 供 http.ResponseController 取回底层 ResponseWriter。
func (w *securityHeadersWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func isHTMLContentType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	return err == nil && mt == "text/html"
}
