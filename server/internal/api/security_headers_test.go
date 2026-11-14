package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newSecurityHeadersServer(origins []string) *Server {
	return newSecurityHeadersServerWithOptions(Options{EmbedOrigins: origins})
}

func newSecurityHeadersServerWithOptions(opts Options) *Server {
	return &Server{
		opts: opts,
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func htmlHandler(contentType string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html></html>"))
	})
}

func assertSecurityHeaders(t *testing.T, header http.Header, wantCSP string) {
	t.Helper()
	if got := header.Get("Content-Security-Policy"); got != wantCSP {
		t.Errorf("Content-Security-Policy = %q，希望 %q", got, wantCSP)
	}
	if got := header.Get("Referrer-Policy"); got != "strict-origin-when-cross-origin" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := header.Get("Permissions-Policy"); got != "camera=(), microphone=(), geolocation=()" {
		t.Errorf("Permissions-Policy = %q", got)
	}
	// frame-ancestors 是唯一事实源：设了 X-Frame-Options 反而会让反代/浏览器行为不一致。
	if got := header.Get("X-Frame-Options"); got != "" {
		t.Errorf("不应该设 X-Frame-Options，实际 = %q", got)
	}
}

func TestSecurityHeadersHTMLDefaultsToSameSite(t *testing.T) {
	s := newSecurityHeadersServer(nil)
	rec := httptest.NewRecorder()
	s.securityHeaders(htmlHandler("text/html; charset=utf-8")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a/demo", nil))

	assertSecurityHeaders(t, rec.Result().Header, "frame-ancestors 'self'")
	if rec.Body.String() != "<!doctype html><html></html>" {
		t.Fatalf("响应体被改动：%q", rec.Body.String())
	}
}

func TestSecurityHeadersHTMLAppendsEmbedOrigins(t *testing.T) {
	s := newSecurityHeadersServer([]string{"https://harness.acme.internal", "http://localhost:5173"})
	rec := httptest.NewRecorder()
	s.securityHeaders(htmlHandler("text/html")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a/demo?embed=1", nil))

	assertSecurityHeaders(t, rec.Result().Header, "frame-ancestors 'self' https://harness.acme.internal http://localhost:5173")
}

// handler 不显式写 WriteHeader 时由第一次 Write 隐式写 200，Header 也要在那之前加上。
func TestSecurityHeadersHTMLImplicitWrite(t *testing.T) {
	s := newSecurityHeadersServer(nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html></html>"))
	})
	rec := httptest.NewRecorder()
	s.securityHeaders(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assertSecurityHeaders(t, rec.Result().Header, "frame-ancestors 'self'")
}

// 可配置的宿主 origin 只放行微应用页（/a/*）；管理页、登录页等其它 HTML 必须保持 'self'。
func TestSecurityHeadersOnlyAppPagesUseEmbedOrigins(t *testing.T) {
	s := newSecurityHeadersServer([]string{"https://harness.acme.internal", "http://localhost:5173"})
	appCSP := "frame-ancestors 'self' https://harness.acme.internal http://localhost:5173"
	for _, c := range []struct {
		path string
		want string
	}{
		{"/a/demo", appCSP},
		{"/a/demo?embed=1&host=https://harness.acme.internal", appCSP},
		{"/admin/menus", "frame-ancestors 'self'"},
		{"/", "frame-ancestors 'self'"},
		{"/login", "frame-ancestors 'self'"},
		{"/go", "frame-ancestors 'self'"},
		{"/a", "frame-ancestors 'self'"},
	} {
		t.Run(c.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			rec := httptest.NewRecorder()
			s.securityHeaders(htmlHandler("text/html; charset=utf-8")).ServeHTTP(rec, req)
			if got := rec.Result().Header.Get("Content-Security-Policy"); got != c.want {
				t.Fatalf("%s 的 CSP = %q，希望 %q", c.path, got, c.want)
			}
		})
	}
}

func TestSecurityHeadersNonHTMLUnaffected(t *testing.T) {
	s := newSecurityHeadersServer([]string{"https://harness.acme.internal"})
	for _, c := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"JSON", "application/json; charset=utf-8", `{"error":"接口不存在"}`},
		{"静态资源", "text/javascript; charset=utf-8", "console.log(1)"},
		{"纯文本错误页", "text/plain; charset=utf-8", "method not allowed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.contentType)
				_, _ = w.Write([]byte(c.body))
			})
			rec := httptest.NewRecorder()
			s.securityHeaders(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			header := rec.Result().Header
			for _, key := range []string{"Content-Security-Policy", "Referrer-Policy", "X-Content-Type-Options", "Permissions-Policy"} {
				if got := header.Get(key); got != "" {
					t.Errorf("非 HTML 响应不应该带 %s，实际 = %q", key, got)
				}
			}
		})
	}
}

// 重定向不是可渲染文档：http.Redirect 会给 GET 加 text/html 跳转体，但 middleware 不能覆盖
// handoff 这类接口自己设的更严格的 Referrer-Policy。
func TestSecurityHeadersSkipRedirects(t *testing.T) {
	s := newSecurityHeadersServer([]string{"https://harness.acme.internal"})
	h := s.securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, "/login?handoff=expired", http.StatusSeeOther)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/handoff?code=expired", nil))

	if got := rec.Code; got != http.StatusSeeOther {
		t.Fatalf("status = %d", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("重定向应保留调用方设的 Referrer-Policy，实际 = %q", got)
	}
	for _, key := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Permissions-Policy"} {
		if got := rec.Header().Get(key); got != "" {
			t.Errorf("重定向不应该带 %s，实际 = %q", key, got)
		}
	}
}

// 流式响应依赖 http.Flusher 类型断言，中间件不能把它吃掉。
func TestSecurityHeadersKeepsFlush(t *testing.T) {
	s := newSecurityHeadersServer(nil)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("中间件包装后 ResponseWriter 不再实现 http.Flusher")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: 1\n\n"))
		f.Flush()
	})
	rec := httptest.NewRecorder()
	s.securityHeaders(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/events", nil))
	if !rec.Flushed {
		t.Fatal("Flush 没有透传到底层 ResponseWriter")
	}
}

// Handler() 必须挂上这层中间件：网页入口返回的 HTML 才有 CSP。
func TestHandlerAddsSecurityHeadersToHTML(t *testing.T) {
	s := newSecurityHeadersServer([]string{"https://harness.acme.internal"})
	s.opts.Web = htmlHandler("text/html; charset=utf-8")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/a/demo")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(string(body), "<!doctype html>") {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	assertSecurityHeaders(t, resp.Header, "frame-ancestors 'self' https://harness.acme.internal")

	// 管理页与其它 HTML 只允许同站：配置的宿主 origin 不能框后台。
	adminResp, err := http.Get(ts.URL + "/admin/menus")
	if err != nil {
		t.Fatal(err)
	}
	defer adminResp.Body.Close()
	if got := adminResp.Header.Get("Content-Security-Policy"); got != "frame-ancestors 'self'" {
		t.Fatalf("管理页 CSP = %q，希望只允许同站", got)
	}

	// 同一进程里的 JSON 接口不加这些头，嵌入式页面之外的响应保持原样。
	apiResp, err := http.Get(ts.URL + "/api/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer apiResp.Body.Close()
	if got := apiResp.Header.Get("Content-Security-Policy"); got != "" {
		t.Fatalf("JSON 接口不应该带 CSP，实际 = %q", got)
	}
}

// ---- 可配置 CSP（DSH_EE_CSP=off|report|enforce）----

// 真实 index.html 首屏主题脚本的哈希；这里用同一个值，让测试和线上配置一致。
const testInlineScriptHash = "sha256-6w+Q1AnbMrhQAI1rS4IDFxHocEfsLlQb97aeZpxCPJs="

const testCSPBase = "default-src 'self'; script-src 'self' 'sha256-6w+Q1AnbMrhQAI1rS4IDFxHocEfsLlQb97aeZpxCPJs='; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; font-src 'self' data:; " +
	"connect-src 'self'; base-uri 'self'; form-action 'self'"

func serveHTML(s *Server, path string) http.Header {
	rec := httptest.NewRecorder()
	s.securityHeaders(htmlHandler("text/html; charset=utf-8")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result().Header
}

// 默认 off：行为和 #118 合入时完全一致，只有 frame-ancestors，没有 Report-Only。
func TestSecurityHeadersCSPOffByDefault(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{
		EmbedOrigins:          []string{"https://harness.acme.internal"},
		CSPInlineScriptHashes: []string{testInlineScriptHash},
	})
	h := serveHTML(s, "/a/demo")
	assertSecurityHeaders(t, h, "frame-ancestors 'self' https://harness.acme.internal")
	if got := h.Get("Content-Security-Policy-Report-Only"); got != "" {
		t.Fatalf("off 模式不应有 Report-Only，实际 = %q", got)
	}
	if got := h.Get("Content-Security-Policy"); strings.Contains(got, "default-src") {
		t.Fatalf("off 模式不应加完整策略，实际 = %q", got)
	}
}

// 内部调用方传了非法模式时按 off 处理（config 层已经 fail closed）。
func TestSecurityHeadersCSPInvalidModeFallsBackToOff(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{CSP: "banana"})
	h := serveHTML(s, "/")
	assertSecurityHeaders(t, h, "frame-ancestors 'self'")
	if got := h.Get("Content-Security-Policy-Report-Only"); got != "" {
		t.Fatalf("非法模式不应产生 Report-Only，实际 = %q", got)
	}
}

// report：完整策略进 Report-Only，只报告；frame-ancestors 收口仍放在会执行的 CSP 里，
// 否则开报告模式就把嵌入边界放开了。
func TestSecurityHeadersCSPReportMode(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{
		EmbedOrigins:          []string{"https://harness.acme.internal"},
		CSP:                   "report",
		CSPReportURI:          "https://ee.acme.internal/api/csp-report",
		CSPInlineScriptHashes: []string{testInlineScriptHash},
	})

	app := serveHTML(s, "/a/demo")
	assertSecurityHeaders(t, app, "frame-ancestors 'self' https://harness.acme.internal")
	want := testCSPBase + "; frame-ancestors 'self' https://harness.acme.internal; report-uri https://ee.acme.internal/api/csp-report"
	if got := app.Get("Content-Security-Policy-Report-Only"); got != want {
		t.Fatalf("Report-Only = %q\n希望 %q", got, want)
	}

	admin := serveHTML(s, "/admin/menus")
	assertSecurityHeaders(t, admin, "frame-ancestors 'self'")
	wantAdmin := testCSPBase + "; frame-ancestors 'self'; report-uri https://ee.acme.internal/api/csp-report"
	if got := admin.Get("Content-Security-Policy-Report-Only"); got != wantAdmin {
		t.Fatalf("管理页 Report-Only = %q\n希望 %q", got, wantAdmin)
	}
}

// enforce：完整策略进 Content-Security-Policy；frame-ancestors 合并进去而不是被覆盖。
func TestSecurityHeadersCSPEnforceMode(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{
		EmbedOrigins:          []string{"https://harness.acme.internal", "http://localhost:5173"},
		CSP:                   "enforce",
		CSPInlineScriptHashes: []string{testInlineScriptHash, "sha256-BBBB="},
	})
	h := serveHTML(s, "/a/demo?embed=1")
	want := strings.Replace(testCSPBase, "'sha256-6w+Q1AnbMrhQAI1rS4IDFxHocEfsLlQb97aeZpxCPJs='",
		"'sha256-6w+Q1AnbMrhQAI1rS4IDFxHocEfsLlQb97aeZpxCPJs=' 'sha256-BBBB='", 1) +
		"; frame-ancestors 'self' https://harness.acme.internal http://localhost:5173"
	assertSecurityHeaders(t, h, want)
	if got := h.Get("Content-Security-Policy-Report-Only"); got != "" {
		t.Fatalf("enforce 模式不应有 Report-Only，实际 = %q", got)
	}

	admin := serveHTML(s, "/admin/menus")
	if got := admin.Get("Content-Security-Policy"); !strings.HasSuffix(got, "form-action 'self'; frame-ancestors 'self'") {
		t.Fatalf("管理页策略不能放行嵌入来源：%q", got)
	}
}

// report-uri 直接拼进响应头：非法字符（换行、分号、引号）必须丢弃，不能破坏策略或造成头注入。
func TestSecurityHeadersCSPSanitizesReportURI(t *testing.T) {
	for _, uri := range []string{
		"https://ee.acme.internal/a;b",
		"https://ee.acme.internal/a\r\nX-Injected: 1",
		"https://ee.acme.internal/\"x\"",
	} {
		s := newSecurityHeadersServerWithOptions(Options{CSP: "report", CSPReportURI: uri})
		h := serveHTML(s, "/")
		reportOnly := h.Get("Content-Security-Policy-Report-Only")
		if strings.Contains(reportOnly, "report-uri") {
			t.Errorf("CSPReportURI=%q 不应被写进策略，实际 = %q", uri, reportOnly)
		}
		if got := h.Get("X-Injected"); got != "" {
			t.Errorf("CSPReportURI=%q 造成了头注入：%q", uri, got)
		}
	}
}

// 非 HTML 响应（JSON、静态资源、纯文本）不加任何 CSP，也不加 Report-Only。
func TestSecurityHeadersCSPNonHTMLUnaffected(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{
		CSP:                   "enforce",
		CSPInlineScriptHashes: []string{testInlineScriptHash},
	})
	for _, c := range []struct{ name, contentType, body string }{
		{"JSON", "application/json; charset=utf-8", `{"ok":true}`},
		{"静态资源", "text/javascript; charset=utf-8", "console.log(1)"},
		{"纯文本错误页", "text/plain; charset=utf-8", "method not allowed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", c.contentType)
				_, _ = w.Write([]byte(c.body))
			})
			rec := httptest.NewRecorder()
			s.securityHeaders(h).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
			for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
				if got := rec.Result().Header.Get(key); got != "" {
					t.Errorf("非 HTML 响应不应该带 %s，实际 = %q", key, got)
				}
			}
		})
	}
}

// 嵌入页在 enforce 模式下仍能正常返回：响应体不变、frame-ancestors 来源合并保留。
func TestSecurityHeadersCSPKeepsEmbedPageWorking(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{
		EmbedOrigins:          []string{"https://harness.acme.internal"},
		CSP:                   "enforce",
		CSPInlineScriptHashes: []string{testInlineScriptHash},
	})
	rec := httptest.NewRecorder()
	s.securityHeaders(htmlHandler("text/html; charset=utf-8")).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/a/demo?embed=1", nil))
	if rec.Body.String() != "<!doctype html><html></html>" {
		t.Fatalf("响应体被改动：%q", rec.Body.String())
	}
	h := rec.Result().Header
	if got := h.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'self' https://harness.acme.internal") {
		t.Fatalf("嵌入页的 frame-ancestors 来源丢了：%q", got)
	}
}

// Handler() 集成：enforce 模式下 HTML 出口带完整策略，JSON 接口不受影响。
func TestHandlerAddsCSPToHTML(t *testing.T) {
	s := newSecurityHeadersServerWithOptions(Options{
		EmbedOrigins:          []string{"https://harness.acme.internal"},
		CSP:                   "enforce",
		CSPInlineScriptHashes: []string{testInlineScriptHash},
	})
	s.opts.Web = htmlHandler("text/html; charset=utf-8")
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := resp.Header.Get("Content-Security-Policy")
	if !strings.HasPrefix(got, "default-src 'self'") || !strings.Contains(got, "'"+testInlineScriptHash+"'") {
		t.Fatalf("登录页 CSP = %q", got)
	}
	if strings.Contains(got, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("script-src 不能用 unsafe-inline 兜底：%q", got)
	}
	if got := resp.Header.Get("Content-Security-Policy-Report-Only"); got != "" {
		t.Fatalf("enforce 模式不应有 Report-Only，实际 = %q", got)
	}

	apiResp, err := http.Get(ts.URL + "/api/does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer apiResp.Body.Close()
	for _, key := range []string{"Content-Security-Policy", "Content-Security-Policy-Report-Only"} {
		if got := apiResp.Header.Get(key); got != "" {
			t.Fatalf("JSON 接口不应该带 %s，实际 = %q", key, got)
		}
	}
}
