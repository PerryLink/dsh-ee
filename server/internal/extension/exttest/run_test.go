package exttest

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// badExtension 是一个故意写错的服务扩展：不验身份、把凭据放进返回、工具报错不带错误码、
// 清单和 MCP 对不上、把页码当 cursor。测试工具要把这些都报出来。
func badExtension(t *testing.T) *httptest.Server {
	manifest := func(self string) string {
		return `{"id":"acme/bad","version":"1.0.0","kind":"service","name":"坏扩展","description":"x","service":{"url":"` + self + `"},
		 "capabilities":["tools","health","knowledge.documents","validate"],
		 "credential":{"mode":"user_secret","hint":"x"},
		 "tools":[{"name":"list_things","readOnly":true,"params":[{"name":"project","type":"string","fill":"app","label":"项目"}]},
		          {"name":"missing_tool","readOnly":true}]}`
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		reply := func(v any) {
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": in["id"], "result": v})
		}
		switch r.URL.Path {
		case "/.well-known/dsh-ee-extension.json":
			_, _ = io.WriteString(w, manifest(ts.URL))
		case "/mcp":
			switch in["method"] {
			case "tools/list":
				reply(map[string]any{"tools": []map[string]any{
					{"name": "list_things", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{"project": map[string]any{"type": "string"}}}, "annotations": map[string]any{"readOnlyHint": false}},
					{"name": "extra_tool", "inputSchema": map[string]any{"type": "object"}},
				}})
			case "tools/call":
				// 把收到的凭据原样放进结果；不带错误码
				reply(map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": "failed with " + r.Header.Get("X-DSH-EE-Credential")}}})
			default:
				reply(map[string]any{})
			}
		case "/ee/health":
			_, _ = io.WriteString(w, `{"ok":true}`)
		case "/ee/kb/changes":
			// 把页码当 cursor，而且永远说还有
			_, _ = io.WriteString(w, `{"items":[{"id":"d1","title":"x"}],"cursor":"`+strings.TrimSpace(in["cursor"].(string))+`","hasMore":true}`)
		case "/ee/kb/document":
			_, _ = io.WriteString(w, `{"id":"x","title":"x","content":""}`)
		default:
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `{"error":{"code":"not_implemented","message":"x"}}`)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestRunCatchesBadExtension(t *testing.T) {
	env, err := Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer env.Close()
	ts := badExtension(t)
	rep := Run(context.Background(), env, Options{URL: ts.URL, Logs: func() string { return "boot\nheader X-DSH-EE-Shared-Credential=dsh-ee-test-shared-5e1b2d\n" }})
	if rep.OK {
		t.Fatalf("坏扩展不该通过：%+v", rep)
	}
	got := map[string]bool{}
	for _, c := range rep.Cases {
		if c.Status == "fail" {
			got[c.Code] = true
			if c.Message == "" {
				t.Fatalf("失败项要说清楚哪里错：%+v", c)
			}
		}
	}
	for _, want := range []string{
		"test.auth_not_enforced", "test.tools_mismatch", "test.readonly_mismatch", "test.fill_leaked", "test.capability_mismatch",
		"test.error_code_missing", "test.authorize_expected", "test.kb_changes", "test.kb_document", "test.credential_leaked",
	} {
		if !got[want] {
			t.Errorf("没有报出 %s；报出的有 %v", want, got)
		}
	}
	// 每个会报的检查项都在 Codes 里登记过
	known := map[string]bool{}
	for _, c := range Codes {
		known[c.Code] = true
	}
	for code := range got {
		if !known[code] {
			t.Errorf("检查项 %s 没有登记在 Codes 里", code)
		}
	}
}

func TestScenarioChecks(t *testing.T) {
	yes := true
	sc := &Scenario{}
	sc.Expect.OK = &yes
	sc.Expect.Count = map[string]int{"gte": 2}
	sc.Expect.Contains = []string{"F-1"}
	if msg := checkScenario(sc, &toolResult{Text: `{"items":[{"id":"F-1"},{"id":"F-2"}]}`}); msg != "" {
		t.Fatalf("应通过：%s", msg)
	}
	if msg := checkScenario(sc, &toolResult{Text: `[{"id":"F-1"}]`}); !strings.Contains(msg, "条数") {
		t.Fatalf("条数不够应报出来：%q", msg)
	}
	bad := &Scenario{}
	bad.Expect.Error = "authorize"
	if msg := checkScenario(bad, &toolResult{IsError: true, Code: "forbidden"}); msg == "" {
		t.Fatal("错误码不对应报出来")
	}
	// 场景文件写错了要说清楚
	env, _ := Start("127.0.0.1:0")
	defer env.Close()
	r := &runner{env: env, rep: &Report{}, opt: Options{Scenarios: fstest.MapFS{"tests/a.yaml": {Data: []byte("- name: x\n  as: nobody\n  call: t\n")}}}, user: Users[0]}
	r.scenarios(context.Background())
	if len(r.rep.Cases) != 1 || r.rep.Cases[0].Code != "test.scenario_invalid" {
		t.Fatalf("不存在的员工应报 scenario_invalid：%+v", r.rep.Cases)
	}
}
