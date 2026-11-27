package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

type testBotReply struct{ target, body string }

func mockDingTalkBot(t *testing.T, ctx context.Context, f *fakeDingTalk) (
	func(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error),
	func(string, string, string), <-chan testBotReply,
) {
	t.Helper()
	type event struct {
		id, sender, text string
		ack              chan int
	}
	incoming := make(chan event)
	replies := make(chan testBotReply, 20)
	f.mux.HandleFunc("POST api.dingtalk.com/v1.0/gateway/connections/open", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ClientID, ClientSecret string
			Subscriptions          []map[string]string
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.ClientID != "ding-app" || in.ClientSecret != "ding-app-secret" ||
			len(in.Subscriptions) != 1 || in.Subscriptions[0]["topic"] != "/v1.0/im/bot/messages/get" {
			http.Error(w, "invalid subscription", 401)
			return
		}
		fmt.Fprint(w, `{"endpoint":"wss://stream-test.dingtalk.com/connect","ticket":"test-ticket"}`)
	})
	f.mux.HandleFunc("GET /connect", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ticket") != "test-ticket" {
			http.Error(w, "invalid ticket", 401)
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
				data, _ := json.Marshal(map[string]any{
					"msgId": in.id, "chatbotCorpId": "ding-corp", "senderStaffId": in.sender,
					"robotCode": "dual-test-robot", "conversationType": "1", "msgtype": "text",
					"text": map[string]string{"content": in.text},
				})
				if err := conn.WriteJSON(map[string]any{"type": "CALLBACK", "specVersion": "1.0", "data": string(data),
					"headers": map[string]string{"topic": "/v1.0/im/bot/messages/get", "messageId": in.id}}); err != nil {
					return
				}
				var ack struct{ Code int }
				_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
				if err := conn.ReadJSON(&ack); err != nil {
					return
				}
				in.ack <- ack.Code
			}
		}
	})
	f.mux.HandleFunc("POST api.dingtalk.com/v1.0/robot/oToMessages/batchSend", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RobotCode, MsgParam string
			UserIDs             []string `json:"userIds"`
		}
		if r.Header.Get("x-acs-dingtalk-access-token") != "app-token" || json.NewDecoder(r.Body).Decode(&in) != nil ||
			in.RobotCode != "dual-test-robot" || len(in.UserIDs) != 1 {
			http.Error(w, "invalid bot reply", 400)
			return
		}
		replies <- testBotReply{in.UserIDs[0], in.MsgParam}
		fmt.Fprint(w, `{"processQueryKey":"sent"}`)
	})
	target, _ := url.Parse(f.URL)
	dial := func(ctx context.Context, raw string, headers http.Header) (*websocket.Conn, *http.Response, error) {
		u, err := url.Parse(raw)
		if err != nil || u.Host != "stream-test.dingtalk.com" {
			return nil, nil, fmt.Errorf("unexpected Stream endpoint")
		}
		u.Scheme, u.Host = "ws", target.Host
		return websocket.DefaultDialer.DialContext(ctx, u.String(), headers)
	}
	send := func(id, sender, text string) {
		t.Helper()
		in := event{id, sender, text, make(chan int, 1)}
		select {
		case incoming <- in:
		case <-time.After(5 * time.Second):
			t.Fatal("Stream did not connect")
		}
		select {
		case n := <-in.ack:
			if n != 200 {
				t.Fatalf("Stream ack: %d", n)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Stream did not acknowledge the event")
		}
	}
	return dial, send, replies
}

func TestFeishuDingTalkDualBotChat(t *testing.T) {
	for _, secondary := range []string{"dingtalk", "feishu"} {
		t.Run(secondary+"_secondary", func(t *testing.T) {
			st := openTestStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
				t.Fatal(err)
			}
			ding := newFakeDingTalk(t)
			dial, sendDing, dingReplies := mockDingTalkBot(t, ctx, ding)
			feishuReplies := make(chan testBotReply, 20)
			idp := newFakeIdP(t)
			idp.mux.HandleFunc("POST /open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
				var in map[string]string
				if json.NewDecoder(r.Body).Decode(&in) != nil || in["app_id"] != "dsh-ee" || in["app_secret"] != "idp-secret" {
					http.Error(w, "invalid app credentials", 401)
					return
				}
				fmt.Fprint(w, `{"code":0,"tenant_access_token":"feishu-tenant"}`)
			})
			idp.mux.HandleFunc("POST /open-apis/im/v1/messages/{id}/reply", func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer feishu-tenant" {
					http.Error(w, "invalid tenant token", 401)
					return
				}
				raw, _ := io.ReadAll(r.Body)
				feishuReplies <- testBotReply{r.PathValue("id"), string(raw)}
				fmt.Fprint(w, `{"code":0,"data":{"message_id":"om-reply"}}`)
			})
			box, _ := secret.NewBox(make([]byte, 32))
			srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: ding.client(t), DingTalkStreamDial: dial}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			ts := httptest.NewServer(srv.Handler())
			defer ts.Close()
			admin := newClient(t, ts.URL)
			n, b := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
			expect(t, "admin login", n, 200, b)
			cfgs := map[string]map[string]any{
				"feishu": {"enabled": true, "preset": "feishu", "name": "飞书", "issuer": idp.URL, "clientId": "dsh-ee", "clientSecret": "idp-secret",
					"feishuApiBase": idp.URL, "feishuBot": true, "feishuVerificationToken": feishuTestToken, "autoCreate": true},
				"dingtalk": {"enabled": true, "preset": "dingtalk", "name": "钉钉", "clientId": "ding-app", "clientSecret": "ding-app-secret",
					"dingTalkCorpId": "ding-corp", "dingTalkBot": true, "autoCreate": true},
			}
			primary := "feishu"
			if secondary == primary {
				primary = "dingtalk"
				cfgs["feishu"]["feishuNotifyActions"] = true
			}
			save := func(provider, slot string) {
				t.Helper()
				n, b := admin.do("PUT", "/api/admin/auth/oidc"+slot, cfgs[provider], nil)
				expect(t, "save "+provider, n, 200, b)
				if secondary == "feishu" && provider == secondary && !strings.Contains(string(b), `"feishuNotifyActions":false`) {
					t.Fatalf("secondary Feishu must not enable approval notices: %s", b)
				}
			}
			save(primary, "")
			save(secondary, "?slot=secondary")
			go srv.RunBackground(ctx)
			employee := newClient(t, ts.URL)
			signIn := func(provider, query, subject string) string {
				t.Helper()
				if provider == "dingtalk" {
					return dingTalkSignIn(t, employee, "provider=dingtalk&"+query, subject)
				}
				return idp.loginFrom(t, employee, "provider=feishu&"+query,
					map[string]any{"sub": subject, "preferred_username": "staff-home", "email": "staff-home@example.test"}, nil)
			}
			if loc := signIn(primary, "next=/build", "staff-home"); loc != "/build" {
				t.Fatalf("home sign-in failed: %s", loc)
			}
			bind := func(sender string) {
				t.Helper()
				if loc := signIn(secondary, "bind=1", sender); loc != "/account?identity_bound="+secondary {
					t.Fatalf("bind failed: %s", loc)
				}
			}
			bind("staff-linked")
			u, err := st.GetUserByUsername(ctx, whoAmI(t, employee))
			if err != nil {
				t.Fatal(err)
			}
			token := "ee_at_" + secret.Token()
			if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: u.ID, Name: "DSH", Prefix: token[:12], Scopes: []string{"agent", "connections"},
				ClientKind: store.ClientKindDSH, CreatedBy: u.ID}, secret.Hash(token)); err != nil {
				t.Fatal(err)
			}
			dsh := newClient(t, ts.URL)
			bearer := map[string]string{"Authorization": "Bearer " + token}
			sendFeishu := func(id, sender, text string) {
				t.Helper()
				event := map[string]any{
					"header": map[string]any{"token": feishuTestToken, "app_id": cfgs["feishu"]["clientId"], "event_type": "im.message.receive_v1"},
					"event": map[string]any{
						"sender":  map[string]any{"sender_id": map[string]string{"open_id": sender}},
						"message": map[string]any{"message_id": id, "chat_id": "test-p2p", "chat_type": "p2p", "message_type": "text", "content": fmt.Sprintf(`{"text":%q}`, text)},
					},
				}
				n, b := dsh.do("POST", "/api/auth/feishu/event", event, nil)
				expect(t, "verified Feishu chat event", n, 200, b)
			}
			send := map[string]func(string, string, string){"dingtalk": sendDing, "feishu": sendFeishu}
			replies := map[string]<-chan testBotReply{"dingtalk": dingReplies, "feishu": feishuReplies}
			poll := func(provider string, status int, text string) int64 {
				t.Helper()
				n, b := dsh.do("GET", "/api/me/"+provider+"-chat/next", nil, bearer)
				expect(t, provider+" DSH poll", n, status, b)
				if status != 200 {
					return 0
				}
				m := decodeAs[store.DingTalkChat](t, b)
				if m.ID == 0 || m.Text != text {
					t.Fatalf("wrong %s delivery: %s", provider, b)
				}
				return m.ID
			}
			wantReply := func(provider, target, text string) {
				t.Helper()
				select {
				case got := <-replies[provider]:
					if got.target != target || !strings.Contains(got.body, text) {
						t.Fatalf("wrong %s outbound reply: %+v", provider, got)
					}
				case <-time.After(5 * time.Second):
					t.Fatalf("no %s outbound reply", provider)
				}
			}
			relay := func(provider, sender, id, text string) int64 {
				t.Helper()
				poll(provider, 204, "")
				send[provider](id, sender, text)
				m := poll(provider, 200, text)
				a := agent{dshClient{t: t, base: ts.URL, token: token}}
				if out := a.ok("ee_"+provider, map[string]any{"action": "reply", "id": m, "text": "DSH answered " + text}); out["ok"] != true {
					t.Fatalf("MCP %s reply: %v", provider, out)
				}
				target := sender
				if provider == "feishu" {
					target = id
				}
				wantReply(provider, target, "DSH answered "+text)
				t.Logf("%s sender %s → DSH account %s (ID %d) → %s reply", provider, sender, u.Username, u.ID, provider)
				return m
			}
			other := &store.User{Username: "other", AuthSource: store.AuthOIDC, ExternalDN: "feishu:other"}
			if secondary == "dingtalk" {
				other.ExternalDN = "dingtalk:ding-corp:other"
			}
			if err := st.CreateUser(ctx, other); err != nil {
				t.Fatal(err)
			}
			member, err := st.GetRoleByCode(ctx, "member")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.SetUserRoles(ctx, other.ID, []int64{member.ID}); err != nil {
				t.Fatal(err)
			}
			otherToken := "ee_at_" + secret.Token()
			if err := st.CreateAccessToken(ctx, &store.AccessToken{UserID: other.ID, Name: "DSH", Prefix: otherToken[:12], Scopes: []string{"agent", "connections"},
				ClientKind: store.ClientKindDSH, CreatedBy: other.ID}, secret.Hash(otherToken)); err != nil {
				t.Fatal(err)
			}
			otherAuth := map[string]string{"Authorization": "Bearer " + otherToken}
			relay(primary, "staff-home", "home-message", "home chat")
			old := relay(secondary, "staff-linked", "linked-message", "linked chat")
			send[secondary]("old-pending", "staff-linked", "pending old identity")
			n, b = dsh.do("GET", "/api/me/"+secondary+"-chat/next", nil, otherAuth)
			expect(t, "another account cannot receive the message", n, 204, b)
			n, b = dsh.do("POST", fmt.Sprintf("/api/me/%s-chat/%d/reply", secondary, old), map[string]string{"text": "must not send"}, otherAuth)
			expect(t, "another account cannot reply", n, 404, b)
			n, b = employee.do("DELETE", "/api/me/identities/"+secondary, nil, nil)
			expect(t, "explicit unlink", n, 200, b)
			unboundStatus := 204
			if secondary == "dingtalk" {
				unboundStatus = 404
			}
			poll(secondary, unboundStatus, "")
			deniedReply := func(id int64) {
				t.Helper()
				n, b := dsh.do("POST", fmt.Sprintf("/api/me/%s-chat/%d/reply", secondary, id), map[string]string{"text": "must not send"}, bearer)
				expect(t, "old reply is out of scope", n, 404, b)
			}
			deniedReply(old)
			send[secondary]("unbound-message", "staff-linked", "while unbound")
			unboundTarget := "staff-linked"
			if secondary == "feishu" {
				unboundTarget = "unbound-message"
			}
			wantReply(secondary, unboundTarget, "")
			var routedUser int64
			err = st.DB().QueryRowContext(ctx, "SELECT user_id FROM "+secondary+"_chat_messages WHERE message_id='unbound-message'").Scan(&routedUser)
			if secondary == "feishu" {
				// 审计 C1：飞书未绑定账号（含陌生员工）的入站不再入队，只回登录提示。
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("飞书未绑定账号的消息不应入队：user %d, err %v", routedUser, err)
				}
			} else if err != nil || routedUser != 0 {
				t.Fatalf("unbound chat routed to user %d: %v", routedUser, err)
			}
			bind("staff-replacement")
			deniedReply(old)
			replacement := relay(secondary, "staff-replacement", "new-message", "replacement chat")
			t.Logf("%s unlink/rebind: old messages could not be polled or replied to; replacement sender relayed", secondary)
			send[secondary]("current-app-pending", "staff-replacement", "pending from old app")
			cfgs[secondary]["clientId"] = "replacement-app"
			save(secondary, "?slot=secondary")
			poll(secondary, 204, "")
			deniedReply(replacement)
			t.Logf("%s app change: old app's queue is not delivered or replyable", secondary)
		})
	}
}
