package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
)

func TestUpstreamArtifactSettingsAndInstall(t *testing.T) {
	for _, authType := range []string{"bearer", "basic"} {
		t.Run(authType, func(t *testing.T) {
			st := openTestStore(t)
			ctx := context.Background()
			if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
				t.Fatal(err)
			}
			up := &fakeRegistry{docs: map[string]map[string]any{}, tgz: map[string][]byte{}, hits: map[string]int{}}
			want := "Bearer artifact-secret"
			if authType == "basic" {
				want = "Basic " + base64.StdEncoding.EncodeToString([]byte("reader:artifact-secret"))
			}
			up.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/feed.json" {
					if r.Header.Get("Authorization") != "" {
						t.Error("feed received credentials")
					}
					up.serve(w, r)
					return
				}
				if r.Header.Get("Authorization") != want {
					w.WriteHeader(401)
					return
				}
				up.serve(w, r)
			}))
			defer up.Close()
			up.add(t, map[string]any{"name": "artifact-dep", "version": "1.0.0"}, nil)
			ts := newRegistryServer(t, st, up)
			admin := newClient(t, ts.URL)
			code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
			expect(t, "login", code, 200, body)
			save := func(auth map[string]any, expected int) []byte {
				t.Helper()
				code, body := admin.do("PUT", "/api/admin/settings", map[string]any{
					"upstream": map[string]any{"registry": "custom", "customRegistry": up.URL, "repoKind": "nexus", "auth": auth},
					"registry": map[string]any{"proxyMode": "cache", "requireToken": false},
				}, nil)
				expect(t, "save", code, expected, body)
				if strings.Contains(string(body), "artifact-secret") || strings.Contains(string(body), "upstreamSecretEnc") {
					t.Fatalf("response leaked secret: %s", body)
				}
				return body
			}
			save(map[string]any{"type": "none"}, 200)
			code, body = admin.do("POST", "/api/admin/registry/check", nil, nil)
			expect(t, "unauthenticated check", code, 502, body)
			if !strings.Contains(string(body), `"code":"upstream.auth_failed"`) {
				t.Fatalf("missing auth error code: %s", body)
			}
			code, body = admin.do("GET", "/npm/artifact-dep", nil, nil)
			expect(t, "unauthenticated metadata", code, 502, body)
			if !strings.Contains(string(body), "authentication failed") {
				t.Fatalf("unclear failure: %s", body)
			}
			code, body = admin.do("GET", "/npm/artifact-dep/-/artifact-dep-1.0.0.tgz", nil, nil)
			expect(t, "unauthenticated install", code, 502, body)
			save(map[string]any{"type": authType, "username": "reader"}, 400)
			save(map[string]any{"type": authType, "username": "reader", "secret": "artifact-secret"}, 200)
			for _, auth := range []map[string]any{{"secret": ""}, {}} {
				save(auth, 200)
			}
			code, body = admin.do("GET", "/api/settings", nil, nil)
			expect(t, "settings", code, 200, body)
			if strings.Contains(string(body), "artifact-secret") || strings.Contains(string(body), "upstreamSecretEnc") || strings.Contains(string(body), `"secret":`) || !strings.Contains(string(body), `"hasSecret":true`) {
				t.Fatalf("unsafe settings: %s", body)
			}
			stored, err := st.GetSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Upstream.Auth.SecretEnc == "" || stored.Upstream.Auth.Secret != "" {
				t.Fatal("credentials not encrypted")
			}
			var raw map[string]any
			if _, err := st.GetSettingJSON(ctx, "app", &raw); err != nil {
				t.Fatal(err)
			}
			b, _ := json.Marshal(raw)
			if strings.Contains(string(b), "artifact-secret") {
				t.Fatal("plaintext persisted")
			}
			code, body = admin.do("POST", "/api/admin/registry/check", nil, nil)
			expect(t, "authenticated check", code, 200, body)
			if !strings.Contains(string(body), `"ok":true`) {
				t.Fatalf("check failed: %s", body)
			}
			code, body = admin.do("GET", "/npm/artifact-dep", nil, nil)
			expect(t, "authenticated metadata", code, 200, body)
			p := decodeAs[packument](t, body)
			if !strings.HasPrefix(p.Versions["1.0.0"].Dist.Tarball, ts.URL+"/npm/") {
				t.Fatal("tarball not rewritten")
			}
			code, body = admin.do("GET", "/npm/artifact-dep/-/artifact-dep-1.0.0.tgz", nil, nil)
			expect(t, "authenticated install", code, 200, body)
			code, body = admin.do("GET", "/api/admin/upstream/feed", nil, nil)
			expect(t, "anonymous catalog", code, 200, body)
			for _, patch := range []map[string]any{
				{"repoKind": "maven"}, {"auth": map[string]any{"type": "digest"}},
				{"auth": map[string]any{"type": "basic", "username": ""}},
				{"customRegistry": "https://user:password@example.com/npm"},
			} {
				code, body = admin.do("PUT", "/api/admin/settings", map[string]any{"upstream": patch}, nil)
				expect(t, "invalid config", code, 400, body)
			}
			save(map[string]any{"type": "none", "secret": "ignored"}, 200)
			stored, err = st.GetSettings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Upstream.Auth.SecretEnc != "" || stored.Upstream.Auth.HasSecret {
				t.Fatal("none did not clear credentials")
			}
			code, body = admin.do("GET", "/api/admin/audit?action=settings.update", nil, nil)
			expect(t, "audit", code, 200, body)
			if strings.Contains(string(body), "artifact-secret") || strings.Contains(string(body), "upstreamSecretEnc") {
				t.Fatal("secret in audit")
			}
		})
	}
}
