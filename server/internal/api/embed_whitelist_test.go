package api

import (
	"context"
	"testing"
	"time"
)

func TestEmbedRouteWhitelist(t *testing.T) {
	allowed := []struct{ method, path string }{
		{"GET", "/api/auth/me"},
		{"GET", "/api/auth/methods"},
		{"GET", "/api/settings"},
		{"GET", "/api/me/menus"},
		{"GET", "/api/me/apps/signup/web"},
		{"POST", "/api/me/apps/signup/source"},
		{"GET", "/api/apps/signup/data"},
		{"GET", "/api/apps/signup/data/notes"},
		{"POST", "/api/apps/signup/data/notes"},
		{"PATCH", "/api/apps/signup/data/notes/12"},
		{"DELETE", "/api/apps/signup/data/notes/12"},
		{"GET", "/api/apps/signup/data/notes/export"},
		{"POST", "/api/auth/embed/logout"},
	}
	for _, c := range allowed {
		if !embedRouteAllowed(c.method, c.path) {
			t.Errorf("%s %s 应该在嵌入白名单里", c.method, c.path)
		}
	}

	blocked := []struct{ method, path string }{
		{"GET", "/api/admin/users"},
		{"POST", "/api/admin/users"},
		{"GET", "/api/me/tokens"},
		{"POST", "/api/me/tokens"},
		{"GET", "/api/hub"},
		{"PUT", "/api/admin/settings"},
		{"GET", "/api/auth/me/extra"},
		{"POST", "/api/settings"},
		{"GET", "/api/apps/signup/data/notes/12"},
		{"POST", "/api/apps/signup/data/notes/12"},
		{"GET", "/api/me/apps/signup/other"},
		{"GET", "/api/auth/embed/logout"},
		{"POST", "/mcp"},
	}
	for _, c := range blocked {
		if embedRouteAllowed(c.method, c.path) {
			t.Errorf("%s %s 不应该在嵌入白名单里", c.method, c.path)
		}
	}
}

func TestEmbedNextMatches(t *testing.T) {
	for _, ok := range []string{"/a/signup", "/a/signup/", "/a/signup?tab=main", "/a/signup#x"} {
		if !embedNextMatches(ok, "signup") {
			t.Errorf("%q 应该允许", ok)
		}
	}
	for _, bad := range []string{"/a/other", "/a/signupx", "/admin/users", "/api/admin/users", "/a/signup/../admin"} {
		if embedNextMatches(bad, "signup") {
			t.Errorf("%q 应该拒绝", bad)
		}
	}
}

func TestEmbedCodesOneTimeAndExpired(t *testing.T) {
	var codes embedCodes
	codes.put("h1", embedCodePending{userID: 1, exp: time.Now().Add(time.Minute)})
	if _, found, expired := codes.take("h1"); !found || expired {
		t.Fatalf("第一次 take 应命中且未过期：found=%v expired=%v", found, expired)
	}
	if _, found, _ := codes.take("h1"); found {
		t.Fatal("同一个 code 不能取第二次")
	}

	codes.put("h2", embedCodePending{userID: 1, exp: time.Now().Add(-time.Second)})
	if _, found, expired := codes.take("h2"); !found || !expired {
		t.Fatalf("过期 code 应标记 expired：found=%v expired=%v", found, expired)
	}
}

func TestEmbedConfigEnvBaseline(t *testing.T) {
	s := newBareServer(t)
	ctx := context.Background()
	if cfg, err := s.embedConfig(ctx); err != nil || cfg.enabled() {
		t.Fatalf("默认应关闭：%+v %v", cfg, err)
	}
	s.opts.EmbedCrossSite = true
	if cfg, _ := s.embedConfig(ctx); cfg.enabled() {
		t.Fatal("没有来源时仍应关闭")
	}
	s.opts.EmbedOrigins = []string{"https://harness.example.com"}
	cfg, err := s.embedConfig(ctx)
	if err != nil || !cfg.enabled() || !cfg.allowsApp("any-app") {
		t.Fatalf("开关 + 来源都配好后应开启：%+v %v", cfg, err)
	}
}
