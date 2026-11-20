package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReplyText(t *testing.T) {
	fail := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tenant_access_token") {
			_, _ = w.Write([]byte(`{"code":0,"tenant_access_token":"tenant"}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/open-apis/im/v1/messages/om-original/reply" || r.Header.Get("Authorization") != "Bearer tenant" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Type    string `json:"msg_type"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Type != "post" || !json.Valid([]byte(body.Content)) || len(body.Content) > 28000 || !strings.Contains(body.Content, `"tag":"md"`) {
			t.Errorf("invalid rich text payload %s", body.Content)
		}
		if fail {
			http.Error(w, "unavailable", 503)
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"data":{"message_id":"om-reply"}}`))
	}))
	defer ts.Close()
	c := NewClient(ts.URL, "app", "secret", ts.Client())
	if id, err := c.ReplyText(context.Background(), "om-original", strings.Repeat("界", 9000)); err != nil || id != "om-reply" {
		t.Fatalf("reply %q %v", id, err)
	}
	if _, err := c.ReplyText(context.Background(), "", "answer"); err == nil {
		t.Fatal("empty message id accepted")
	}
	fail = true
	if _, err := c.ReplyText(context.Background(), "om-original", "answer"); err == nil {
		t.Fatal("upstream error accepted")
	}
}
