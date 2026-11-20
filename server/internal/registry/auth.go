package registry

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

var ErrUpstreamAuth = errors.New("Upstream authentication failed (401/403); check the configured token, username/password and repository permissions")

type authTransport struct {
	base          http.RoundTripper
	origin        string
	authorization string
}

func authOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	port := u.Port()
	if port == "" {
		switch strings.ToLower(u.Scheme) {
		case "http":
			port = "80"
		case "https":
			port = "443"
		}
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Hostname()) + ":" + port
}

// AuthClient confines credentials to the configured origin, including every redirect hop.
func AuthClient(client *http.Client, base, authorization string) *http.Client {
	c := *client
	tr := c.Transport
	if tr == nil {
		tr = http.DefaultTransport
	}
	c.Transport = &authTransport{base: tr, origin: authOrigin(base), authorization: authorization}
	return &c
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Header.Del("Authorization")
	if t.origin != "" && authOrigin(r.URL.String()) == t.origin && t.authorization != "" {
		r.Header.Set("Authorization", t.authorization)
	}
	return t.base.RoundTrip(r)
}

func (u *Upstream) authenticatedClient(ctx context.Context) (*http.Client, error) {
	if u.Credentials == nil {
		return u.hc, nil
	}
	base, authorization, err := u.Credentials(ctx)
	if err != nil {
		return nil, err
	}
	return AuthClient(u.hc, base, authorization), nil
}
