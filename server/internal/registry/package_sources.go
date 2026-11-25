package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

const (
	ConanCenter            = "https://center2.conan.io"
	PackageFileLimit int64 = 512 << 20
)

var packageSourceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func ValidPackageSourceName(name string) bool { return packageSourceName.MatchString(name) }

func ValidatePackageSource(v *store.PackageSource) error {
	if !ValidPackageSourceName(v.Name) || (v.Protocol != "raw" && v.Protocol != "conan") {
		return fmt.Errorf("invalid package source name or protocol")
	}
	switch v.Mode {
	case "hosted":
		if v.UpstreamURL != "" {
			return fmt.Errorf("hosted sources cannot have an upstream URL")
		}
	case "proxy":
		if v.Protocol == "conan" {
			if v.UpstreamURL != "" && v.UpstreamURL != ConanCenter {
				return fmt.Errorf("Conan proxy sources use ConanCenter")
			}
			v.UpstreamURL = ConanCenter
			return nil
		}
		u, err := url.Parse(v.UpstreamURL)
		if err != nil || len(v.UpstreamURL) > 2048 || !validPackageUpstream(u) || u.RawQuery != "" || u.ForceQuery {
			return fmt.Errorf("invalid upstream URL: use an HTTP(S) base URL without credentials, query or fragment")
		}
		v.UpstreamURL = strings.TrimRight(u.String(), "/")
	default:
		return fmt.Errorf("invalid package source mode")
	}
	return nil
}

func PackagePath(escaped string) ([]string, bool) {
	if len(escaped) > 3*1024 || escaped == "" {
		return nil, false
	}
	segs := strings.Split(escaped, "/")
	for i, seg := range segs {
		value, err := url.PathUnescape(seg)
		if err != nil || value == "" || value == "." || value == ".." || !utf8.ValidString(value) ||
			strings.ContainsAny(value, "/\\") || strings.ContainsFunc(value, unicode.IsControl) {
			return nil, false
		}
		segs[i] = value
	}
	return segs, len(strings.Join(segs, "/")) <= 1024
}

func validPackageUpstream(u *url.URL) bool {
	return u != nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

type PackageProxy struct {
	client *http.Client
	guard  *Upstream
}

func NewPackageProxy() *PackageProxy {
	guard := &Upstream{}
	client := guard.guardClient(&http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConnsPerHost:   16,
			DisableCompression:    true,
		},
	})
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !validPackageUpstream(req.URL) ||
			(via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https") {
			return fmt.Errorf("invalid package upstream redirect")
		}
		req.Header.Del("Referer")
		return guard.ValidateUpstreamURL(req.Context(), req.URL.String())
	}
	return &PackageProxy{client: client, guard: guard}
}

func (p *PackageProxy) Fetch(ctx context.Context, method, base string, segments []string, query string, headers http.Header) (*http.Response, error) {
	u, err := url.Parse(base)
	if err != nil || !validPackageUpstream(u) {
		return nil, fmt.Errorf("invalid package upstream")
	}
	var escaped []string
	for _, segment := range segments {
		escaped = append(escaped, url.PathEscape(segment))
	}
	u.RawPath = strings.TrimRight(u.EscapedPath(), "/") + "/" + strings.Join(escaped, "/")
	u.Path, err = url.PathUnescape(u.RawPath)
	if err != nil {
		return nil, err
	}
	u.RawQuery = query
	if err := p.guard.ValidateUpstreamURL(ctx, u.String()); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"} {
		if value := headers.Get(key); value != "" {
			req.Header.Set(key, value)
		}
	}
	return p.client.Do(req)
}
