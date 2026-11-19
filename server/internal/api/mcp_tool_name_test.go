package api_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// 上游 MCP 工具名里常有 - . / 这类字符（resolve-library-id、files.read）。dsh-ee 对外把名字
// 规范成 字母数字下划线，但转发给上游时必须用上游自己的名字，否则每次调用都是「没有这个工具」。
func TestMCPToolKeepsUpstreamName(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	var mu sync.Mutex
	var called []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&m)
		if m.ID == nil {
			w.WriteHeader(202)
			return
		}
		var result any
		switch m.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "issues"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "get-issue", "description": "Get one issue", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}},
				{"name": "files.read", "description": "Read a file", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}},
				{"name": "search", "description": "Search", "inputSchema": map[string]any{"type": "object"}, "annotations": map[string]any{"readOnlyHint": true}},
			}}
		case "tools/call":
			mu.Lock()
			called = append(called, m.Params.Name)
			mu.Unlock()
			known := m.Params.Name == "get-issue" || m.Params.Name == "files.read" || m.Params.Name == "search"
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "called " + m.Params.Name}}, "isError": !known}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": *m.ID, "result": result})
	}))
	defer up.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("POST", "/api/hub", map[string]any{"kind": "connection", "slug": "issues", "delivery": "auto",
		"spec": map[string]any{"title": "Issues", "description": "issue tracker", "type": "mcp", "url": up.URL}}, nil)
	expect(t, "接入 MCP 连接", code, 201, body)
	code, body = admin.do("POST", "/api/me/npm-tokens", map[string]any{"name": "dsh", "access": "read"}, nil)
	expect(t, "接入令牌", code, 201, body)
	d := dshClient{t: t, base: ts.URL, token: decodeAs[struct{ Token string }](t, body).Token}

	if got := d.toolNames(); !slices.Equal(got, []string{"issues__files_read", "issues__get_issue", "issues__search"}) {
		t.Fatalf("对外的工具名：%v", got)
	}
	for tool, want := range map[string]string{"issues__get_issue": "get-issue", "issues__files_read": "files.read", "issues__search": "search"} {
		res := d.rpc("tools/call", map[string]any{"name": tool, "arguments": map[string]any{}})
		r, _ := res["result"].(map[string]any)
		if r == nil || r["isError"] == true || !strings.Contains(fmt.Sprint(r), "called "+want) {
			t.Fatalf("调 %s 应以上游名字 %s 转发：%v", tool, want, res)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Contains(called, "get-issue") || !slices.Contains(called, "files.read") || slices.Contains(called, "get_issue") {
		t.Fatalf("上游收到的工具名：%v", called)
	}
}
