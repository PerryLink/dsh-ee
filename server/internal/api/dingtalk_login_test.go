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
	"time"

	"github.com/gorilla/websocket"

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

func TestDingTalkLoginAndBotChat(t *testing.T) {
	var exchanges atomic.Int64
	type receivedReply struct{ robot, user, text string }
	replies := make(chan receivedReply, 8)
	type callbackFrame struct {
		id   string
		data map[string]any
		ack  chan int
	}
	incoming := make(chan callbackFrame)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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
	mux.HandleFunc("POST api.dingtalk.com/v1.0/gateway/connections/open", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ClientID      string              `json:"clientId"`
			ClientSecret  string              `json:"clientSecret"`
			Subscriptions []map[string]string `json:"subscriptions"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.ClientID != "ding-app" || in.ClientSecret != "ding-app-secret" ||
			len(in.Subscriptions) != 1 || in.Subscriptions[0]["topic"] != "/v1.0/im/bot/messages/get" ||
			in.Subscriptions[0]["type"] != "CALLBACK" {
			http.Error(w, "invalid Stream subscription", 401)
			return
		}
		fmt.Fprint(w, `{"endpoint":"wss://wss-test.dingtalk.com/connect","ticket":"test-ticket"}`)
	})
	mux.HandleFunc("GET /connect", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") != "test-ticket" {
			http.Error(w, "invalid Stream ticket", 401)
			return
		}
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case in := <-incoming:
				data, _ := json.Marshal(in.data)
				if err := conn.WriteJSON(map[string]any{"type": "CALLBACK", "specVersion": "1.0", "data": string(data),
					"headers": map[string]string{"topic": "/v1.0/im/bot/messages/get", "messageId": in.id}}); err != nil {
					return
				}
				var ack struct {
					Code    int
					Headers map[string]string
				}
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				if err := conn.ReadJSON(&ack); err != nil || ack.Headers["messageId"] != in.id {
					return
				}
				in.ack <- ack.Code
			}
		}
	})
	mux.HandleFunc("POST api.dingtalk.com/v1.0/robot/oToMessages/batchSend", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RobotCode string
			UserIDs   []string `json:"userIds"`
			MsgKey    string
			MsgParam  string
		}
		var params map[string]string
		if r.Header.Get("x-acs-dingtalk-access-token") != "app-token" || json.NewDecoder(r.Body).Decode(&in) != nil ||
			in.MsgKey != "sampleText" || len(in.UserIDs) != 1 || json.Unmarshal([]byte(in.MsgParam), &params) != nil {
			http.Error(w, "invalid bot reply", 400)
			return
		}
		replies <- receivedReply{in.RobotCode, in.UserIDs[0], params["content"]}
		fmt.Fprint(w, `{"processQueryKey":"sent"}`)
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
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context, raw string, header http.Header) (*websocket.Conn, *http.Response, error) {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "wss-test.dingtalk.com" {
			return nil, nil, fmt.Errorf("unexpected Stream endpoint")
		}
		u.Scheme, u.Host = "ws", target.Host
		return websocket.DefaultDialer.DialContext(ctx, u.String(), header)
	}
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: hc, DingTalkStreamDial: dial}, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	t.Logf("Browser OAuth2 → verified tenant and employee → dsh-ee session (%s)", user.ExternalDN)
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

	accessToken := func(u *store.User, scope string) string {
		t.Helper()
		token := "ee_at_" + secret.Token()
		scopes := []string{scope}
		if scope == "agent" {
			scopes = append(scopes, "connections", "hub")
		}
		err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: scope, Prefix: token[:12], Scopes: scopes,
			ClientKind: store.ClientKindDSH, CreatedBy: u.ID}, secret.Hash(token))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	token := accessToken(user, "agent")
	bearer := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	next := "/api/me/dingtalk-chat/next"
	dsh := newClient(t, ts.URL)
	code, body = dsh.do("GET", next, nil, bearer(token))
	expect(t, "bot off", code, 404, body)
	cfg["dingTalkBot"] = true
	code, body = admin.do("PUT", "/api/admin/auth/oidc", cfg, nil)
	expect(t, "enable bot", code, 200, body)
	go srv.RunBackground(ctx)
	streamSend := func(id, userID, corp, conversation, kind, text string) {
		t.Helper()
		frame := callbackFrame{id: id, ack: make(chan int, 1), data: map[string]any{
			"msgId": id, "chatbotCorpId": "ding-corp", "senderCorpId": corp, "robotCode": "ding-bot",
			"senderStaffId": userID, "conversationType": conversation, "msgtype": kind, "text": map[string]string{"content": text},
		}}
		select {
		case incoming <- frame:
		case <-time.After(5 * time.Second):
			t.Fatal("fake DingTalk Stream was not connected")
		}
		select {
		case code := <-frame.ack:
			if code != 200 {
				t.Fatalf("unexpected Stream ack: %d", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stream acknowledgement not received")
		}
	}
	wantReply := func(userID, fragment string) {
		t.Helper()
		select {
		case reply := <-replies:
			if reply.user != userID || reply.robot != "ding-bot" || !strings.Contains(reply.text, fragment) {
				t.Fatalf("unexpected DingTalk reply: %+v", reply)
			}
			t.Logf("DingTalk OpenAPI → %s: %s", reply.user, reply.text)
		case <-time.After(5 * time.Second):
			t.Fatal("DingTalk reply not received")
		}
	}
	streamSend("group", "staff-42", "ding-corp", "2", "text", "group should not be sent")
	streamSend("other-corp", "staff-42", "outside-corp", "1", "text", "other tenant should not be sent")
	code, body = dsh.do("GET", next, nil, bearer(token))
	expect(t, "ignore groups and external organizations", code, 204, body)
	streamSend("unbound", "staff-unknown", "ding-corp", "1", "text", "hello")
	wantReply("staff-unknown", "用钉钉登录")
	streamSend("non-text", "staff-42", "ding-corp", "1", "picture", "")
	wantReply("staff-42", "只支持文字消息")
	streamSend("msg-1", "staff-42", "", "1", "text", "What is 6 × 7?")
	t.Log("Fake DingTalk DM → authenticated Stream connection → persisted and acknowledged by dsh-ee")
	code, body = dsh.do("GET", next, nil, bearer(accessToken(user, "models")))
	expect(t, "model-only token cannot poll", code, 403, body)
	code, body = dsh.do("GET", next+"?wait=1", nil, bearer(token))
	expect(t, "employee DSH poll", code, 200, body)
	msg := decodeAs[store.DingTalkChat](t, body)
	if msg.ID == 0 || msg.Text != "What is 6 × 7?" || strings.Contains(string(body), "ding-bot") {
		t.Fatalf("incorrect message delivery: %s", body)
	}
	t.Logf("dsh-ee → simulated connected DSH via bearer long poll: %q", msg.Text)
	other := &store.User{Username: "another-staff", DisplayName: "Other", AuthSource: store.AuthOIDC, ExternalDN: "dingtalk:ding-corp:another-staff"}
	if err := st.CreateUser(ctx, other); err != nil {
		t.Fatal(err)
	}
	code, body = dsh.do("POST", fmt.Sprintf("/api/me/dingtalk-chat/%d/reply", msg.ID), map[string]string{"text": "bypass"}, bearer(accessToken(other, "agent")))
	expect(t, "another employee cannot reply", code, 404, body)
	a := agent{dshClient{t: t, base: ts.URL, token: token}}
	out := a.ok("ee_dingtalk", map[string]any{"action": "reply", "id": msg.ID, "text": "6 × 7 = 42."})
	if out["ok"] != true {
		t.Fatalf("DSH reply failed: %v", out)
	}
	t.Log("Simulated DSH → MCP ee_dingtalk.reply → dsh-ee")
	wantReply("staff-42", "6 × 7 = 42.")
	streamSend("msg-1", "staff-42", "", "1", "text", "redelivery")
	code, body = dsh.do("GET", next, nil, bearer(token))
	expect(t, "Stream retries are deduplicated", code, 204, body)
	code, body = employee.do("GET", "/api/me/dingtalk-chat", nil, nil)
	expect(t, "employee connection status", code, 200, body)
	for _, flag := range []string{`"available":true`, `"enabled":true`, `"bound":true`, `"online":true`} {
		if !strings.Contains(string(body), flag) {
			t.Fatalf("missing connection status %s: %s", flag, body)
		}
	}
	cfg["dingTalkBot"] = false
	code, body = admin.do("PUT", "/api/admin/auth/oidc", cfg, nil)
	expect(t, "disable bot", code, 200, body)
	code, body = dsh.do("GET", next, nil, bearer(token))
	expect(t, "poll denied after disable", code, 404, body)
}
