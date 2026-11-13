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
	return &Server{
		opts: Options{EmbedOrigins: origins},
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
