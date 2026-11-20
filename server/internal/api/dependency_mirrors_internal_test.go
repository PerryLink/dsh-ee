package api

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestDependencyMirrorsBundleAndErrors(t *testing.T) {
	f := newReleaseFixture(t, "notify")
	f.s.box, _ = secret.NewBox(bytes.Repeat([]byte{1}, 32))
	ctx := context.Background()
	_, _, before, err := f.s.buildConnect(ctx, "https://ee.corp", "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	set, err := f.st.GetSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	set.Mirrors.PyPI = store.DependencyMirror{URL: "https://private-mirror.corp/simple", Username: "mirror-reader", Secret: "private-mirror-password"}
	if err := f.s.sealDependencyMirrors(&set.Mirrors); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveSettings(ctx, set, 0); err != nil {
		t.Fatal(err)
	}
	f.s.settings.mu.Lock()
	f.s.settings.v = nil
	f.s.settings.mu.Unlock()
	_, files, after, err := f.s.buildConnect(ctx, "https://ee.corp", "1.0.1")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("mirrors must not change downloadable bundle")
	}
	for name, data := range files {
		for _, value := range []string{"private-mirror.corp", "mirror-reader", "private-mirror-password", set.Mirrors.PyPI.SecretEnc} {
			if strings.Contains(string(data), value) {
				t.Fatalf("mirror config leaked into %s", name)
			}
		}
	}
	if _, err := f.s.dependencyMirrors(set.Mirrors); err != nil {
		t.Fatal(err)
	}
	wrongBox, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&Server{box: wrongBox}).dependencyMirrors(set.Mirrors); err == nil {
		t.Fatal("wrong key must fail closed")
	}
	set.Mirrors.PyPI.SecretEnc = "not-base64"
	if _, err := f.s.dependencyMirrors(set.Mirrors); err == nil {
		t.Fatal("corrupt secret must fail closed")
	}
}
