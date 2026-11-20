package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUpstreamArtifactSettingsStorage(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	set, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if set.Upstream.Auth.Type != "none" || set.Upstream.Auth.HasSecret {
		t.Fatal("unexpected defaults")
	}
	u := tokenTestUser(t, st, false)
	set.Upstream.Registry = "custom"
	set.Upstream.RepoKind = "artifactory"
	set.Upstream.Auth = UpstreamAuth{Type: "bearer", Secret: "do-not-store", SecretEnc: "opaque-ciphertext"}
	if err := st.SaveSettings(ctx, set, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Upstream.Auth.Secret != "" || got.Upstream.Auth.SecretEnc != "opaque-ciphertext" || !got.Upstream.Auth.HasSecret || got.Upstream.RepoKind != "artifactory" {
		t.Fatal("settings did not round trip")
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "opaque-ciphertext") || strings.Contains(string(b), "do-not-store") {
		t.Fatal("public settings leaked credentials")
	}
	var raw string
	if err := st.DB().QueryRowContext(ctx, "SELECT value FROM settings WHERE name = ?", appSettingsKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "do-not-store") || !strings.Contains(raw, "opaque-ciphertext") {
		t.Fatal("invalid credential storage")
	}
	got.Upstream.Auth = UpstreamAuth{Type: "none"}
	if err := st.SaveSettings(ctx, got, u.ID); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Upstream.Auth.HasSecret || got.Upstream.Auth.SecretEnc != "" {
		t.Fatal("secret not cleared")
	}
}
