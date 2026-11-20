package api

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestUpstreamArtifactCredentialSelection(t *testing.T) {
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := box.Seal([]byte("private-token"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		registry, mode, auth, encrypted string
		want                            bool
		wantError                       bool
	}{
		{"custom", "online", "bearer", base64.StdEncoding.EncodeToString(sealed), true, false},
		{"npmjs", "online", "bearer", base64.StdEncoding.EncodeToString(sealed), false, false},
		{"npmmirror", "online", "basic", base64.StdEncoding.EncodeToString(sealed), false, false},
		{"custom", "offline", "bearer", base64.StdEncoding.EncodeToString(sealed), false, false},
		{"custom", "online", "none", "", false, false},
		{"custom", "online", "bearer", "invalid", false, true},
		{"custom", "online", "basic", base64.StdEncoding.EncodeToString([]byte("bad")), false, true},
	} {
		set := store.DefaultSettings()
		set.Upstream.Registry, set.Upstream.Mode, set.Upstream.CustomRegistry = tc.registry, tc.mode, "https://npm.corp/repository/npm/"
		set.Upstream.Auth.Type, set.Upstream.Auth.SecretEnc = tc.auth, tc.encrypted
		s := &Server{box: box, settings: settingsCache{v: set, at: time.Now()}}
		base, auth, err := s.upstreamCredentials(context.Background())
		if (err != nil) != tc.wantError || (auth != "") != tc.want || (base != "") != tc.want {
			t.Fatalf("%+v: base=%q auth set=%v err=%v", tc, base, auth != "", err)
		}
	}
}
