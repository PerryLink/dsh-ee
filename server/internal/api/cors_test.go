package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const corsAllowedOrigin = "https://harness.example.com"

func newCORSServer(origins []string) *Server {
	return &Server{
		opts: Options{CORSOrigins: origins},
		log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// corsHandler 是 /v1、/mcp 业务处理函数的替身：记录是否被调用，返回一个 JSON 响应。
func corsHandler(called *bool, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"ok":true}`)
	})
}

func runCORS(t *testing.T, s *Server, method, target, origin string, next http.Handler) (*httptest.ResponseRecorder, bool) {
	t.Helper()
	called := false
	if next == nil {
		next = corsHandler(&called, http.StatusOK)
	}
	req := httptest.NewRequest(method, target, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.cors(next).ServeHTTP(rec, req)
	return rec, called
}

func varyHasOrigin(h http.Header) bool {
	for _, v := range h.Values("Vary") {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "Origin") {
				return true
			}
		}
	}
	return false
}

// assertCORSGrant 检查给白名单来源的响应头：精确回显 Origin、Vary 含 Origin、不发 credentials。
func assertCORSGrant(t *testing.T, h http.Header, origin string) {
	t.Helper()
	if got := h.Get("Access-Control-Allow-Origin"); got != origin {
		t.Errorf("Access-Control-Allow-Origin = %q，希望 %q", got, origin)
	}
	if !varyHasOrigin(h) {
		t.Errorf("Vary 里应该有 Origin，实际 %v", h.Values("Vary"))
	}
	if got := h.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Bearer 不是 cookie，不应该发 Access-Control-Allow-Credentials，实际 %q", got)
	}
}

// assertCORSExposeHeaders 检查实际响应放出了浏览器 JS 需要读取的非简单响应头。
func assertCORSExposeHeaders(t *testing.T, h http.Header) {
	t.Helper()
	if got := h.Get("Access-Control-Expose-Headers"); got != corsExposeHeaders {
		t.Errorf("Access-Control-Expose-Headers = %q，希望 %q", got, corsExposeHeaders)
	}
}

// assertNoCORSHeaders 检查没有加任何 CORS 相关头（未列来源、非目标路径、关闭时都应如此）。
func assertNoCORSHeaders(t *testing.T, h http.Header) {
	t.Helper()
	for _, k := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Credentials",
		"Access-Control-Max-Age",
		"Access-Control-Expose-Headers",
	} {
		if got := h.Get(k); got != "" {
			t.Errorf("不应该带 %s，实际 %q", k, got)
		}
	}
	if vary := h.Values("Vary"); len(vary) != 0 {
		t.Errorf("不应该带 Vary，实际 %v", vary)
	}
}

// 默认关闭：不配 DSH_EE_CORS_ORIGINS 时请求照原样透传，不加任何头，OPTIONS 也不拦截。
func TestCORSDisabledByDefault(t *testing.T) {
	s := newCORSServer(nil)

	rec, called := runCORS(t, s, http.MethodGet, "/v1/models", corsAllowedOrigin, nil)
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("关闭时应该原样透传：called=%v status=%d", called, rec.Code)
	}
	assertNoCORSHeaders(t, rec.Result().Header)

	// 关闭时 OPTIONS 不在中间件里处理，仍落到业务路由。
	rec, called = runCORS(t, s, http.MethodOptions, "/mcp", corsAllowedOrigin, nil)
	if !called {
		t.Fatal("关闭时 OPTIONS 不应该被 CORS 中间件拦截")
	}
	assertNoCORSHeaders(t, rec.Result().Header)
}

// 白名单来源：实际响应加精确 ACAO + Vary: Origin；/v1/* 与 /mcp 都覆盖。
func TestCORSAllowedOriginActualResponse(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	for _, target := range []string{"/v1/models", "/v1/chat/completions", "/v1/apps/demo", "/mcp"} {
		t.Run(target, func(t *testing.T) {
			rec, called := runCORS(t, s, http.MethodGet, target, corsAllowedOrigin, nil)
			if !called || rec.Code != http.StatusOK {
				t.Fatalf("业务处理函数应该照常执行：called=%v status=%d", called, rec.Code)
			}
			assertCORSGrant(t, rec.Result().Header, corsAllowedOrigin)
			assertCORSExposeHeaders(t, rec.Result().Header)
			if rec.Body.String() != `{"ok":true}` {
				t.Fatalf("响应体被改动：%q", rec.Body.String())
			}
		})
	}
}

// 多个来源各自精确回显，不能串用。
func TestCORSMultipleOriginsEchoExactly(t *testing.T) {
	origins := []string{"https://a.example.com", "https://b.example.com:8443"}
	s := newCORSServer(origins)
	for _, origin := range origins {
		t.Run(origin, func(t *testing.T) {
			rec, _ := runCORS(t, s, http.MethodGet, "/v1/models", origin, nil)
			assertCORSGrant(t, rec.Result().Header, origin)
			assertCORSExposeHeaders(t, rec.Result().Header)
		})
	}
}

// 未列出的来源：业务照常执行，但不加任何 CORS 头，浏览器会按同源策略拦掉。
func TestCORSUnlistedOriginGetsNoHeaders(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	for _, target := range []string{"/v1/models", "/mcp"} {
		t.Run(target, func(t *testing.T) {
			rec, called := runCORS(t, s, http.MethodGet, target, "https://evil.example.com", nil)
			if !called || rec.Code != http.StatusOK {
				t.Fatalf("业务处理函数应该照常执行：called=%v status=%d", called, rec.Code)
			}
			assertNoCORSHeaders(t, rec.Result().Header)
		})
	}
}

// 没有 Origin 头的请求（同源页面、非浏览器客户端）行为完全不变。
func TestCORSNoOriginHeaderUnchanged(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	rec, called := runCORS(t, s, http.MethodGet, "/v1/models", "", nil)
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("called=%v status=%d", called, rec.Code)
	}
	assertNoCORSHeaders(t, rec.Result().Header)
}

// 预检：204 + 精确 ACAO + 固定的方法/请求头白名单 + Max-Age，且不进入业务路由。
func TestCORSPreflight(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin, "http://localhost:5173"})
	for _, tc := range []struct {
		origin string
		target string
	}{
		{corsAllowedOrigin, "/v1/chat/completions"},
		{corsAllowedOrigin, "/mcp"},
		{"http://localhost:5173", "/v1/models"},
	} {
		t.Run(tc.origin+tc.target, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodOptions, tc.target, nil)
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "authorization,content-type")
			called := false
			rec := httptest.NewRecorder()
			s.cors(corsHandler(&called, http.StatusOK)).ServeHTTP(rec, req)

			if called {
				t.Fatal("预检不应该进入业务路由")
			}

			if rec.Code != http.StatusNoContent {
				t.Fatalf("预检应该返回 204，实际 %d", rec.Code)
			}
			if rec.Body.Len() != 0 {
				t.Fatalf("预检不应该有响应体，实际 %q", rec.Body.String())
			}
			h := rec.Result().Header
			assertCORSGrant(t, h, tc.origin)
			if got := h.Get("Access-Control-Allow-Methods"); got != corsAllowMethods {
				t.Errorf("Access-Control-Allow-Methods = %q，希望 %q", got, corsAllowMethods)
			}
			if got := h.Get("Access-Control-Allow-Headers"); got != corsAllowHeaders {
				t.Errorf("Access-Control-Allow-Headers = %q，希望 %q", got, corsAllowHeaders)
			}
			if got := h.Get("Access-Control-Max-Age"); got != "600" {
				t.Errorf("Access-Control-Max-Age = %q，希望 600", got)
			}
			// Expose-Headers 只对实际响应有意义：预检响应不加，浏览器也不会读。
			if got := h.Get("Access-Control-Expose-Headers"); got != "" {
				t.Errorf("预检不应该带 Access-Control-Expose-Headers，实际 %q", got)
			}
		})
	}
}

// 未列来源的预检：同样 204，但不给任何 CORS 头，也不进入业务路由。
func TestCORSPreflightUnlistedOrigin(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	rec, called := runCORS(t, s, http.MethodOptions, "/v1/models", "https://evil.example.com", nil)
	if called {
		t.Fatal("预检不应该进入业务路由")
	}
	if rec.Code != http.StatusNoContent {
		t.Fatalf("预检应该返回 204，实际 %d", rec.Code)
	}
	assertNoCORSHeaders(t, rec.Result().Header)
}

// 没有 Origin 的 OPTIONS 不是浏览器预检：保持原有行为，交给业务路由处理。
func TestCORSOptionsWithoutOriginPassesThrough(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	rec, called := runCORS(t, s, http.MethodOptions, "/mcp", "", nil)
	if !called {
		t.Fatal("没有 Origin 的 OPTIONS 不应该被 CORS 中间件拦截")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	assertNoCORSHeaders(t, rec.Result().Header)
}

// 只对 /v1/* 与 /mcp 生效：/api、/healthz、/mcp/xxx、/v1 本身都不加头。
func TestCORSOnlyTargetPaths(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	for _, target := range []string{"/api/catalog", "/api/auth/me", "/healthz", "/mcp/extra", "/v1", "/v10/models"} {
		t.Run("GET "+target, func(t *testing.T) {
			rec, called := runCORS(t, s, http.MethodGet, target, corsAllowedOrigin, nil)
			if !called {
				t.Fatal("非目标路径应该原样透传")
			}
			assertNoCORSHeaders(t, rec.Result().Header)
		})
	}
	// 非目标路径上的 OPTIONS 也不拦截，保持原来的 404/405 语义。
	rec, called := runCORS(t, s, http.MethodOptions, "/api/auth/login", corsAllowedOrigin, nil)
	if !called {
		t.Fatal("非目标路径的 OPTIONS 应该原样透传")
	}
	assertNoCORSHeaders(t, rec.Result().Header)
}

// 业务处理函数返回错误（如 401）时也要带 CORS 头，浏览器里的 Harness 才能读到错误原因。
func TestCORSErrorResponseKeepsHeaders(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	called := false
	rec, _ := runCORS(t, s, http.MethodGet, "/v1/models", corsAllowedOrigin, corsHandler(&called, http.StatusUnauthorized))
	if !called || rec.Code != http.StatusUnauthorized {
		t.Fatalf("called=%v status=%d", called, rec.Code)
	}
	assertCORSGrant(t, rec.Result().Header, corsAllowedOrigin)
	assertCORSExposeHeaders(t, rec.Result().Header)
}

// Expose-Headers 放出的头名必须与各 PR 的最终实现一致，且只出现在白名单来源的
// /v1/*、/mcp 实际响应上；预检、未列来源、非目标路径都不加。
func TestCORSExposeHeadersScope(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})

	for _, target := range []string{"/v1/models", "/v1/chat/completions", "/mcp"} {
		t.Run("actual "+target, func(t *testing.T) {
			rec, _ := runCORS(t, s, http.MethodGet, target, corsAllowedOrigin, nil)
			assertCORSExposeHeaders(t, rec.Result().Header)
			got := rec.Result().Header.Get("Access-Control-Expose-Headers")
			for _, name := range []string{"Mcp-Session-Id", "X-Request-Id", "Dsh-EE-API-Version", "Retry-After", "X-RateLimit-Limit"} {
				if !strings.Contains(got, name) {
					t.Errorf("Access-Control-Expose-Headers 缺 %s：%q", name, got)
				}
			}
		})
	}

	t.Run("预检不加", func(t *testing.T) {
		rec, _ := runCORS(t, s, http.MethodOptions, "/v1/models", corsAllowedOrigin, nil)
		if got := rec.Result().Header.Get("Access-Control-Expose-Headers"); got != "" {
			t.Errorf("预检不应该带 Access-Control-Expose-Headers，实际 %q", got)
		}
	})
	t.Run("未列来源不加", func(t *testing.T) {
		rec, _ := runCORS(t, s, http.MethodGet, "/v1/models", "https://evil.example.com", nil)
		if got := rec.Result().Header.Get("Access-Control-Expose-Headers"); got != "" {
			t.Errorf("未列来源不应该带 Access-Control-Expose-Headers，实际 %q", got)
		}
	})
	t.Run("非目标路径不加", func(t *testing.T) {
		rec, _ := runCORS(t, s, http.MethodGet, "/api/catalog", corsAllowedOrigin, nil)
		if got := rec.Result().Header.Get("Access-Control-Expose-Headers"); got != "" {
			t.Errorf("非目标路径不应该带 Access-Control-Expose-Headers，实际 %q", got)
		}
	})
	t.Run("关闭时不加", func(t *testing.T) {
		off := newCORSServer(nil)
		rec, _ := runCORS(t, off, http.MethodGet, "/v1/models", corsAllowedOrigin, nil)
		if got := rec.Result().Header.Get("Access-Control-Expose-Headers"); got != "" {
			t.Errorf("CORS 关闭时不应该带 Access-Control-Expose-Headers，实际 %q", got)
		}
	})
}

// 上游处理函数自己的 Vary 不能被覆盖。
func TestCORSKeepsExistingVary(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		w.WriteHeader(http.StatusOK)
	})
	rec, _ := runCORS(t, s, http.MethodGet, "/v1/models", corsAllowedOrigin, next)
	h := rec.Result().Header
	if !varyHasOrigin(h) {
		t.Errorf("Vary 里应该有 Origin，实际 %v", h.Values("Vary"))
	}
	found := false
	for _, v := range h.Values("Vary") {
		if strings.Contains(v, "Accept-Encoding") {
			found = true
		}
	}
	if !found {
		t.Errorf("业务处理函数写的 Vary: Accept-Encoding 被覆盖了：%v", h.Values("Vary"))
	}
}

// Handler() 必须挂上这层中间件：预检在路由之前被处理，/v1 的实际响应带 CORS 头，
// 非目标路径（/api）保持原样。
func TestCORSHandlerWiring(t *testing.T) {
	s := newCORSServer([]string{corsAllowedOrigin})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	preflight, err := http.NewRequest(http.MethodOptions, ts.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	preflight.Header.Set("Origin", corsAllowedOrigin)
	preflight.Header.Set("Access-Control-Request-Method", "GET")
	resp, err := http.DefaultClient.Do(preflight)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("预检 status = %d，希望 204", resp.StatusCode)
	}
	assertCORSGrant(t, resp.Header, corsAllowedOrigin)
	if got := resp.Header.Get("Access-Control-Expose-Headers"); got != "" {
		t.Errorf("预检不应该带 Access-Control-Expose-Headers，实际 %q", got)
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", corsAllowedOrigin)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// 测试 Server 没有路由表，/v1 的兜底 404 也带 CORS 头；状态码本身保持原样。
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/v1 兜底 status = %d，希望 404", resp.StatusCode)
	}
	assertCORSGrant(t, resp.Header, corsAllowedOrigin)
	assertCORSExposeHeaders(t, resp.Header)

	req, err = http.NewRequest(http.MethodGet, ts.URL+"/api/does-not-exist", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", corsAllowedOrigin)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/api 兜底 status = %d，希望 404", resp.StatusCode)
	}
	assertNoCORSHeaders(t, resp.Header)
}
