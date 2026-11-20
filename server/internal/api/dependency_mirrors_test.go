package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestDependencyMirrorsSettingsAndCatalog(t *testing.T) {
	f := newEmbedFixture(t, nil)
	f.user(t, "mirror-employee")
	emp := newClient(t, f.ts.URL)
	code, body := emp.do("POST", "/api/auth/login", map[string]string{"username": "mirror-employee", "password": "employee-password-1"}, nil)
	expect(t, "login", code, 200, body)
	code, body = emp.do("POST", "/api/me/npm-tokens", map[string]any{"name": "dsh", "access": "read"}, nil)
	expect(t, "token", code, 201, body)
	dsh := dshClient{t: t, base: f.ts.URL, token: decodeAs[struct{ Token string }](t, body).Token}
	type catalog struct {
		Mirrors map[string]struct {
			URL      string `json:"url"`
			Username string `json:"username"`
			Secret   string `json:"secret"`
		} `json:"mirrors"`
		Revision string `json:"revision"`
	}
	read := func() (catalog, string) {
		t.Helper()
		code, body, headers := dsh.do("GET", "/api/dsh/catalog", nil, nil)
		expect(t, "catalog", code, 200, body)
		if headers.Get("Cache-Control") != "private, no-store" {
			t.Fatal(headers)
		}
		return decodeAs[catalog](t, body), headers.Get("ETag")
	}
	save := func(mirrors map[string]any) store.AppSettings {
		t.Helper()
		code, body := f.admin.do("PUT", "/api/admin/settings", map[string]any{"mirrors": mirrors}, nil)
		expect(t, "save", code, 200, body)
		if strings.Contains(string(body), `"secret"`) || strings.Contains(string(body), "mirrorSecrets") || strings.Contains(string(body), "mirror-password") {
			t.Fatalf("settings leaked: %s", body)
		}
		return decodeAs[store.AppSettings](t, body)
	}
	empty, oldETag := read()
	if empty.Mirrors == nil || len(empty.Mirrors) != 0 {
		t.Fatal(empty)
	}
	code, body = newClient(t, f.ts.URL).do("GET", "/api/dsh/catalog", nil, nil)
	if code == http.StatusOK {
		t.Fatalf("anonymous catalog: %s", body)
	}
	set := save(map[string]any{
		"pypi":  map[string]any{"url": "https://nexus.corp/repository/pypi-group/simple", "username": "reader", "secret": "mirror-password-one"},
		"maven": map[string]any{"url": "https://nexus.corp/repository/maven-public/"},
	})
	if !set.Mirrors.PyPI.HasSecret || set.Mirrors.Maven.HasSecret {
		t.Fatal(set.Mirrors)
	}
	code, body = f.admin.do("GET", "/api/settings", nil, nil)
	expect(t, "get settings", code, 200, body)
	if strings.Contains(string(body), `"secret"`) || strings.Contains(string(body), "mirror-password") {
		t.Fatal("GET leaked credentials")
	}
	stored, err := f.st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := stored.Mirrors.PyPI.SecretEnc
	sealed, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := f.box.Open(sealed)
	if err != nil || string(plain) != "mirror-password-one" {
		t.Fatalf("encryption: %v", err)
	}
	var raw string
	if err := f.st.DB().QueryRow("SELECT value FROM settings WHERE name = 'app'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "mirror-password") {
		t.Fatal("plaintext in DB")
	}
	logs, err := f.st.ListAudit(context.Background(), store.AuditFilter{Action: "settings.update", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) == 0 {
		t.Fatal("missing audit")
	}
	encoded, _ := json.Marshal(logs)
	if strings.Contains(string(encoded), "mirror-password") || strings.Contains(string(encoded), ciphertext) {
		t.Fatal("credentials in audit")
	}
	current, etag := read()
	if len(current.Mirrors) != 2 || current.Mirrors["pypi"].Secret != "mirror-password-one" || current.Mirrors["pypi"].Username != "reader" || current.Mirrors["maven"].Secret != "" || etag == oldETag || current.Revision == empty.Revision {
		t.Fatal(current, etag)
	}
	code, body, _ = dsh.do("GET", "/api/dsh/catalog", nil, map[string]string{"If-None-Match": etag})
	expect(t, "not modified", code, 304, body)
	save(map[string]any{"pypi": map[string]any{"secret": ""}})
	kept, keptETag := read()
	if keptETag != etag || kept.Mirrors["pypi"].Secret != "mirror-password-one" {
		t.Fatal("empty secret changed credentials")
	}
	code, body = emp.do("PUT", "/api/admin/settings", map[string]any{"mirrors": map[string]any{"pypi": map[string]any{"url": "https://other.corp/simple"}}}, nil)
	expect(t, "employee cannot save", code, 403, body)
	for _, patch := range []map[string]any{
		{"secret": "mirror-password-two"}, {"username": "reader-two"}, {"url": "https://other.corp/simple"},
	} {
		save(map[string]any{"pypi": patch})
		next, nextETag := read()
		if nextETag == etag || next.Revision == current.Revision {
			t.Fatal("mirror change did not change revision")
		}
		code, body, _ = dsh.do("GET", "/api/dsh/catalog", nil, map[string]string{"If-None-Match": etag})
		expect(t, "changed catalog", code, 200, body)
		current, etag = next, nextETag
	}
	set = save(map[string]any{"pypi": map[string]any{"url": ""}})
	if set.Mirrors.PyPI.HasSecret || set.Mirrors.PyPI.Username != "" {
		t.Fatal(set.Mirrors)
	}
	configured, _ := read()
	if len(configured.Mirrors) != 1 || configured.Mirrors["maven"].URL == "" {
		t.Fatal(configured)
	}
	save(map[string]any{"maven": map[string]any{"url": ""}})
	cleared, _ := read()
	if len(cleared.Mirrors) != 0 {
		t.Fatal(cleared)
	}
	stored, err = f.st.GetSettings(context.Background())
	if err != nil || stored.Mirrors.PyPI.SecretEnc != "" || stored.Mirrors.Maven.SecretEnc != "" {
		t.Fatal("stored credentials not cleared", err)
	}
	for _, kind := range []string{"pypi", "maven"} {
		for _, entry := range []map[string]any{
			{"url": "ftp://mirror.corp/"}, {"url": "https:///simple"}, {"url": "https://user:pass@mirror.corp/"}, {"url": "https://mirror.corp/#fragment"},
			{"url": "https://mirror.corp/", "secret": "password"}, {"url": "https://mirror.corp/", "username": "reader\n", "secret": "password\n"},
		} {
			code, body = f.admin.do("PUT", "/api/admin/settings", map[string]any{"mirrors": map[string]any{kind: entry}}, nil)
			expect(t, "invalid mirror", code, 400, body)
		}
	}
}

func TestDependencyMirrorsRegistryManage(t *testing.T) {
	f := newEmbedFixture(t, nil)
	code, body := f.admin.do("PUT", "/api/admin/settings", map[string]any{"embed": map[string]any{"allowedOrigins": []string{"https://embed.corp"}}}, nil)
	expect(t, "configure embed", code, 200, body)
	for _, permission := range []string{"registry.manage", "upstream.manage"} {
		code, body := f.admin.do("POST", "/api/admin/roles", map[string]any{"code": permission, "name": permission, "permissions": []string{permission}}, nil)
		expect(t, "create role", code, 201, body)
		role := decodeAs[struct{ ID int64 }](t, body)
		code, body = f.admin.do("POST", "/api/admin/users", map[string]any{"username": permission, "password": "mirror-admin-password-1", "role_ids": []int64{role.ID}}, nil)
		expect(t, "create admin", code, 201, body)
		admin := newClient(t, f.ts.URL)
		code, body = admin.do("POST", "/api/auth/login", map[string]string{"username": permission, "password": "mirror-admin-password-1"}, nil)
		expect(t, "login", code, 200, body)
		if permission == "registry.manage" {
			code, body = admin.do("GET", "/api/settings", nil, nil)
			expect(t, "redacted settings", code, 200, body)
			redacted := decodeAs[store.AppSettings](t, body)
			if len(redacted.Embed.AllowedOrigins) != 0 {
				t.Fatal("embed settings not redacted")
			}
			code, body = admin.do("PUT", "/api/admin/settings", redacted, nil)
			expect(t, "full redacted settings cannot overwrite embed", code, 403, body)
		}
		code, body = admin.do("PUT", "/api/admin/settings", map[string]any{"mirrors": map[string]any{"pypi": map[string]any{"url": "https://mirror.corp/" + permission, "username": "reader", "secret": "mirror-password"}}}, nil)
		want := 403
		if permission == "registry.manage" {
			want = 200
		}
		expect(t, "mirror permission", code, want, body)
		code, body = admin.do("PUT", "/api/admin/settings", map[string]any{"orgName": permission}, nil)
		expect(t, "unrelated settings remain protected", code, 403, body)
	}
	stored, err := f.st.GetSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Embed.AllowedOrigins) != 1 || stored.Embed.AllowedOrigins[0] != "https://embed.corp" {
		t.Fatal("partial save changed embed configuration")
	}
}
