package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

type dingTalkTestTransport struct {
	target  *url.URL
	wrapped http.RoundTripper
}

func (tr dingTalkTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.dingtalk.com" && req.URL.Host != "oapi.dingtalk.com" {
		return nil, fmt.Errorf("unexpected DingTalk request host: %s", req.URL.Host)
	}
	r := req.Clone(req.Context())
	r.URL.Scheme, r.URL.Host = tr.target.Scheme, tr.target.Host
	r.Host = req.URL.Host
	return tr.wrapped.RoundTrip(r)
}

func TestDingTalkLogin(t *testing.T) {
	var exchanges atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST api.dingtalk.com/v1.0/oauth2/userAccessToken", func(w http.ResponseWriter, r *http.Request) {
		exchanges.Add(1)
		var in map[string]string
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["clientId"] != "ding-app" ||
			in["clientSecret"] != "ding-app-secret" || in["grantType"] != "authorization_code" {
			http.Error(w, "unexpected user token request", 400)
			return
		}
		switch in["code"] {
		case "employee":
			fmt.Fprint(w, `{"accessToken":"employee-token","corpId":"ding-corp"}`)
		case "external":
			fmt.Fprint(w, `{"accessToken":"external-token","corpId":"ding-corp"}`)
		case "wrong-corp":
			fmt.Fprint(w, `{"accessToken":"employee-token","corpId":"ding-other"}`)
		default:
			http.Error(w, "unrecognized code", 400)
		}
	})
	mux.HandleFunc("GET api.dingtalk.com/v1.0/contact/users/me", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("x-acs-dingtalk-access-token") {
		case "employee-token":
			fmt.Fprint(w, `{"unionId":"union-employee","nick":"吴员工","email":"pretend@example.com"}`)
		case "external-token":
			fmt.Fprint(w, `{"unionId":"union-external"}`)
		default:
			http.Error(w, "invalid user token", 401)
		}
	})
	mux.HandleFunc("POST api.dingtalk.com/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["appKey"] != "ding-app" || in["appSecret"] != "ding-app-secret" {
			http.Error(w, "invalid app credentials", 401)
			return
		}
		fmt.Fprint(w, `{"accessToken":"app-token"}`)
	})
	mux.HandleFunc("POST oapi.dingtalk.com/topapi/user/getbyunionid", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if r.URL.Query().Get("access_token") != "app-token" || json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "invalid employee lookup", 401)
			return
		}
		switch in["unionid"] {
		case "union-employee":
			fmt.Fprint(w, `{"errcode":"0","result":{"userid":"staff-42","contact_type":"0"}}`)
		case "union-external":
			fmt.Fprint(w, `{"errcode":0,"result":{"userid":"external-42","contact_type":1}}`)
		default:
			http.Error(w, "unexpected unionId", 400)
		}
	})
	fake := httptest.NewServer(mux)
	t.Cleanup(fake.Close)
	target, err := url.Parse(fake.URL)
	if err != nil {
		t.Fatal(err)
	}
	hc := fake.Client()
	hc.Transport = dingTalkTestTransport{target, hc.Transport}

	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: hc}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "admin login", code, 200, body)
	cfg := map[string]any{
		"enabled": true, "preset": "dingtalk", "name": "钉钉", "autoCreate": true,
		"clientId": "ding-app", "clientSecret": "ding-app-secret", "dingTalkCorpId": "ding-corp", "issuer": "https://ignored.invalid",
	}
	code, body = admin.do("PUT", "/api/admin/auth/oidc", cfg, nil)
	expect(t, "save DingTalk", code, 200, body)
	if strings.Contains(string(body), "ding-app-secret") || !strings.Contains(string(body), "https://login.dingtalk.com") {
		t.Fatalf("unsafe or unexpected settings response: %s", body)
	}
	code, body = admin.do("POST", "/api/admin/auth/oidc/test", cfg, nil)
	expect(t, "test DingTalk app credentials", code, 200, body)
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("app credentials check failed: %s", body)
	}
	code, body = admin.do("GET", "/api/auth/methods", nil, nil)
	expect(t, "advertise DingTalk login", code, 200, body)
	if !strings.Contains(string(body), `"oidc":{"name":"钉钉"}`) {
		t.Fatalf("DingTalk login not advertised: %s", body)
	}

	start := func(t *testing.T, employee *client) (*http.Client, string) {
		t.Helper()
		browser := *employee.hc
		browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		res, err := browser.Get(ts.URL + "/api/auth/oidc/start?next=/build")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		u, err := res.Location()
		if err != nil || res.StatusCode != 302 || u.Host != "login.dingtalk.com" || u.Path != "/oauth2/auth" {
			t.Fatalf("unexpected authorization redirect: %v (%v)", u, err)
		}
		q := u.Query()
		if q.Get("scope") != "openid corpid" || q.Get("corpId") != "ding-corp" || q.Get("client_id") != "ding-app" ||
			q.Get("prompt") != "consent" || q.Get("redirect_uri") != ts.URL+"/api/auth/oidc/callback" || q.Get("state") == "" {
			t.Fatalf("wrong authorization query: %v", q)
		}
		return &browser, q.Get("state")
	}
	callback := func(t *testing.T, browser *http.Client, state, code string) string {
		t.Helper()
		q := url.Values{"authCode": {code}, "state": {state}}
		res, err := browser.Get(ts.URL + "/api/auth/oidc/callback?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != 302 {
			t.Fatalf("callback status: %d", res.StatusCode)
		}
		return res.Header.Get("Location")
	}
	employee := newClient(t, ts.URL)
	browser, state := start(t, employee)
	if loc := callback(t, browser, state, "employee"); loc != "/build" {
		t.Fatalf("employee login failed: %s", loc)
	}
	user, err := st.GetUserByExternalDN(ctx, store.AuthOIDC, "dingtalk:ding-corp:staff-42")
	if err != nil || user.Username != "staff-42" || user.DisplayName != "吴员工" || user.Email != "" {
		t.Fatalf("unexpected employee binding: %+v, %v", user, err)
	}
	code, body = employee.do("GET", "/api/auth/me", nil, nil)
	expect(t, "employee session", code, 200, body)
	if !strings.Contains(string(body), "staff-42") {
		t.Fatalf("callback did not issue employee session: %s", body)
	}
	if loc := callback(t, browser, state, "employee"); !strings.HasPrefix(loc, "/login?oidc_error=") || exchanges.Load() != 1 {
		t.Fatalf("replayed state accepted: %s (exchanges: %d)", loc, exchanges.Load())
	}
	for _, code := range []string{"wrong-corp", "external"} {
		t.Run(code, func(t *testing.T) {
			browser, state := start(t, newClient(t, ts.URL))
			if loc := callback(t, browser, state, code); !strings.HasPrefix(loc, "/login?oidc_error=") {
				t.Fatalf("nonemployee login accepted: %s", loc)
			}
		})
	}
}
