package registry

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUpstreamArtifactAuth(t *testing.T) {
	shasum := fmt.Sprintf("%x", sha1.Sum([]byte("artifact")))
	for _, authorization := range []string{"Bearer private-token", "Basic " + base64.StdEncoding.EncodeToString([]byte("reader:password"))} {
		t.Run(strings.Fields(authorization)[0], func(t *testing.T) {
			ctx := context.Background()
			var base string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != authorization {
					w.WriteHeader(401)
					return
				}
				if r.URL.Path == "/package.tgz" {
					_, _ = w.Write([]byte("artifact"))
					return
				}
				fmt.Fprintf(w, `{"name":"private-pkg","versions":{"1.0.0":{"name":"private-pkg","version":"1.0.0","dist":{"tarball":%q,"shasum":%q}}}}`, base+"/package.tgz", shasum)
			}))
			defer server.Close()
			base = server.URL
			u, err := NewUpstream(server.Client(), t.TempDir(), time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := u.Packument(ctx, base, "private-pkg", 0); !errors.Is(err, ErrUpstreamAuth) {
				t.Fatalf("missing credentials: %v", err)
			}
			blobs, err := NewBlobs(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := u.FetchTarball(ctx, blobs, base+"/package.tgz", "", "", 1024); !errors.Is(err, ErrUpstreamAuth) {
				t.Fatalf("anonymous tarball: %v", err)
			}
			u.Credentials = func(context.Context) (string, string, error) { return base, authorization, nil }
			doc, err := u.Packument(ctx, base, "private-pkg", 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := u.FetchTarball(ctx, blobs, doc.Version("1.0.0").Tarball, "", doc.Version("1.0.0").Shasum, 1024); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestUpstreamArtifactAuthOriginBoundary(t *testing.T) {
	t.Setenv("DSH_EE_UPSTREAM_ALLOW_PRIVATE", "1")
	ctx := context.Background()
	var received []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte("artifact"))
	}))
	defer other.Close()
	var sameReceived []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sameReceived = append(sameReceived, r.Header.Get("Authorization"))
		if r.URL.Path == "/redirect-host" {
			http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+"/artifact", 302)
			return
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, other.URL+"/artifact", 302)
			return
		}
		_, _ = w.Write([]byte(`{"plugins":[]}`))
	}))
	defer server.Close()
	u, err := NewUpstream(server.Client(), t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	u.Credentials = func(context.Context) (string, string, error) { return server.URL, "Bearer confidential", nil }
	blobs, err := NewBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{other.URL + "/artifact", server.URL + "/redirect", server.URL + "/redirect-host"} {
		if _, err := u.FetchTarball(ctx, blobs, raw, "", fmt.Sprintf("%x", sha1.Sum([]byte("artifact"))), 1024); err != nil {
			t.Fatal(err)
		}
	}
	// Feeds must stay anonymous even if hosted on the registry origin.
	s := &Service{up: u}
	if _, _, err := s.fetchFeed(ctx, server.URL+"/feed", true); err != nil {
		t.Fatal(err)
	}
	if len(received) != 3 || received[0] != "" || received[1] != "" || received[2] != "" {
		t.Fatalf("cross-origin credential leak: %v", received)
	}
	if len(sameReceived) != 3 || sameReceived[0] != "Bearer confidential" || sameReceived[1] != "Bearer confidential" || sameReceived[2] != "" {
		t.Fatalf("registry/feed credentials: %v", sameReceived)
	}
	for _, tc := range []struct {
		target string
		want   bool
	}{
		{"https://EXAMPLE.com/npm", true}, {"https://example.com:443/pkg", true},
		{"http://example.com/pkg", false}, {"https://example.com:444/pkg", false},
		{"https://sub.example.com/pkg", false}, {"https://example.org/pkg", false},
	} {
		if got := authOrigin(tc.target) == authOrigin("https://example.com"); got != tc.want {
			t.Errorf("origin %s = %v", tc.target, got)
		}
	}
}

func TestUpstreamArtifactAuthForbidden(t *testing.T) {
	u, err := NewUpstream(nil, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	if _, err := u.Packument(context.Background(), server.URL, "pkg", 0); !errors.Is(err, ErrUpstreamAuth) {
		t.Fatalf("403: %v", err)
	}
}
