package registry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestPackageSourcePaths(t *testing.T) {
	for _, path := range []string{"a/%2e%2e/b", "a/b%2Fc", "a/b%5Cc", "a//b", "a/%00b", "a/%FF"} {
		if _, ok := PackagePath(path); ok {
			t.Errorf("accepted invalid path %q", path)
		}
	}
	segments, ok := PackagePath("team/release/raw%20file.bin")
	if !ok || strings.Join(segments, "/") != "team/release/raw file.bin" {
		t.Fatalf("segments = %v, %t", segments, ok)
	}
	src := store.PackageSource{Name: "center", Protocol: "conan", Mode: "proxy"}
	if err := ValidatePackageSource(&src); err != nil || src.UpstreamURL != ConanCenter {
		t.Fatalf("ConanCenter default: source=%+v err=%v", src, err)
	}
	raw := store.PackageSource{Name: "remote", Protocol: "raw", Mode: "proxy", UpstreamURL: "https://user:pass@example.com/artifacts"}
	if err := ValidatePackageSource(&raw); err == nil {
		t.Fatal("accepted credentials in an upstream URL")
	}
}

func TestConanPathAndPublication(t *testing.T) {
	for _, tc := range []struct {
		name, version string
		want          bool
	}{
		{strings.Repeat("a", 101), "1.0", true},
		{strings.Repeat("a", 102), "1.0", false},
		{strings.Repeat("a", 101), strings.Repeat("1", 98), true},
		{strings.Repeat("a", 101), strings.Repeat("1", 99), false},
	} {
		if _, ok := ParseConanPath([]string{"v2", "conans", tc.name, tc.version, "_", "_", "latest"}); ok != tc.want {
			t.Errorf("name length %d, version length %d: accepted = %t, want %t", len(tc.name), len(tc.version), ok, tc.want)
		}
	}
	longName, user := strings.Repeat("a", 101), "team"
	for _, n := range []int{199, 200, 201} {
		version := strings.Repeat("1", n-len(longName)-len(user)-2)
		ref := longName + "/" + version + "@" + user
		route, ok := ParseConanPath([]string{"v2", "conans", longName, version, user, "_", "latest"})
		if ok != (n <= 200) || ok && ConanReference(route.Parent) != ref {
			t.Errorf("reference length %d with no channel: accepted = %t, reference = %q", n, ok, ConanReference(route.Parent))
		}
	}
	route, ok := ParseConanPath(strings.Split("v2/conans/lib/1.0/_/_/revisions/abc/packages/0123/revisions/def/files/conan_package.tgz", "/"))
	if !ok || !route.Binary || route.Operation != "file" || route.Namespace != "lib/_/_" || route.File != "conan_package.tgz" ||
		route.Parent != "conans/lib/1.0/_/_/revisions/abc/packages/0123" {
		t.Fatalf("route = %+v, %t", route, ok)
	}
	manifest := "1\nconanfile.py: 0123456789abcdef0123456789abcdef\n" +
		"nested/my: data.txt: 0123456789abcdef0123456789abcdef\n" +
		"export_source/src/header.h: 0123456789abcdef0123456789abcdef\n"
	required, archives, err := ConanPublicationFiles(strings.NewReader(manifest), false)
	if err != nil || strings.Join(required, ",") != "conanfile.py" || strings.Join(archives, ",") != "conan_export,conan_sources" {
		t.Fatalf("required=%v archives=%v err=%v", required, archives, err)
	}
	if !MatchConanReference("lib/*@", "lib/1.0", true) || MatchConanReference("lib/*@", "lib/1.0@team/dev", true) {
		t.Fatal("unscoped pattern did not respect the trailing @")
	}
}

type packageRoundTrip func(*http.Request) (*http.Response, error)

func (f packageRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPackageProxyRequest(t *testing.T) {
	p := NewPackageProxy()
	hits := 0
	p.client.Transport = packageRoundTrip(func(r *http.Request) (*http.Response, error) {
		hits++
		if r.URL.String() != "https://93.184.216.34/root/nested/file%20name?q=one" ||
			r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("Range") != "bytes=0-5" {
			t.Errorf("unexpected upstream request: %s", r.URL)
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://127.0.0.1/secret"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	h := http.Header{"Authorization": {"Bearer test"}, "Cookie": {"session=test"}, "Range": {"bytes=0-5"}}
	if _, err := p.Fetch(context.Background(), "GET", "https://93.184.216.34/root/", []string{"nested", "file name"}, "q=one", h); err == nil || hits != 1 {
		t.Fatalf("redirect wasn't rejected: hits=%d, err=%v", hits, err)
	}
	if _, err := p.Fetch(context.Background(), "GET", "http://127.0.0.1", []string{"file"}, "", nil); err == nil || hits != 1 {
		t.Fatalf("private address wasn't rejected: hits=%d, err=%v", hits, err)
	}
}
