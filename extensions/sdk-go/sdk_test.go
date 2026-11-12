package sdk

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeDshEE 是一台只会发 JWKS 和签令牌的假 dsh-ee。
type fakeDshEE struct {
	ts  *httptest.Server
	key *rsa.PrivateKey
}

func newFakeDshEE(t *testing.T) *fakeDshEE {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDshEE{key: key}
	f.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "kid": "k1", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.ts.Close)
	return f
}

func (f *fakeDshEE) sign(key *rsa.PrivateKey, claims map[string]any) string {
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	body, _ := json.Marshal(claims)
	signed := base64.RawURLEncoding.EncodeToString(head) + "." + base64.RawURLEncoding.EncodeToString(body)
	sum := sha256.Sum256([]byte(signed))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	return signed + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeDshEE) token(aud string, extra map[string]any) string {
	c := map[string]any{"iss": f.ts.URL, "aud": aud, "sub": "7", "preferred_username": "zhangsan", "name": "张三", "exp": time.Now().Add(time.Minute).Unix()}
	for k, v := range extra {
		c[k] = v
	}
	return f.sign(f.key, c)
}

func post(t *testing.T, url, token string, header map[string]string, body any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func rpc(t *testing.T, base, token string, header map[string]string, method string, params any) map[string]any {
	t.Helper()
	code, out := post(t, base+"/mcp", token, header, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	if code != 200 {
		t.Fatalf("%s：HTTP %d %v", method, code, out)
	}
	return out
}

func testExtension(ee *fakeDshEE) *Extension {
	ext := NewWith(Manifest{ID: "acme/oa", Version: "1.2.0", Name: T("Acme OA", ""), Description: T("查审批单", ""),
		Credential: &Credential{Mode: "user_secret", Hint: T("在 OA 里生成", "")},
		Config:     []ConfigField{{Key: "tenant", Type: "text", Label: T("租户", ""), Required: true}}},
		Options{DshEEURL: ee.ts.URL})
	ext.Tool(Tool{Name: "list_forms", ReadOnly: true, Description: T("列出待办", ""),
		Params: []Param{{Name: "status", Type: "string", Enum: []any{"pending", "done"}}, {Name: "form_type", Type: "string", Fill: "app", Label: T("单据类型", "")}},
		Handler: func(ctx context.Context, c *Call) (any, error) {
			if c.Credential == "" {
				return nil, Errorf(CodeAuthorize, "请先填 OA 的 token")
			}
			return map[string]any{"user": c.User.Username, "dept": c.User.Dept, "tenant": c.Config["tenant"], "type": c.Args["form_type"], "cred": c.Credential}, nil
		}})
	ext.Tool(Tool{Name: "submit_leave", Params: []Param{{Name: "days", Type: "number", Required: true}},
		Handler: func(ctx context.Context, c *Call) (any, error) { return "已提交", nil }})
	ext.Validate(func(ctx context.Context, c *Call) error {
		if c.Config["tenant"] == "" {
			return Errorf(CodeInvalid, "租户不能为空")
		}
		return nil
	})
	docs := map[string]string{"d1": "# 报销制度\n\n住宿每晚不超过 600 元。"}
	ext.Knowledge(Knowledge{
		Changes: func(ctx context.Context, c *Call, scope []string, cursor string) (*Changes, error) {
			if cursor == "1" {
				return &Changes{Cursor: "1"}, nil
			}
			return &Changes{Items: []Change{{ID: "d1", Title: "报销制度"}}, Cursor: "1"}, nil
		},
		Document: func(ctx context.Context, c *Call, id string) (*Document, error) {
			if docs[id] == "" {
				return nil, Errorf(CodeNotFound, "没有 %s", id)
			}
			return &Document{ID: id, Title: "报销制度", Content: docs[id]}, nil
		},
	})
	ext.Endpoint(func(ctx context.Context, c *Call, req *EndpointRequest) (*EndpointResponse, error) {
		return &EndpointResponse{Body: append([]byte(req.Method+" "+req.Path+" "), req.Body...)}, nil
	})
	return ext
}

func TestExtension(t *testing.T) {
	ee := newFakeDshEE(t)
	ext := testExtension(ee)
	ts := httptest.NewServer(ext.Handler())
	defer ts.Close()
	good := ee.token("acme/oa", map[string]any{"dept": "研发部"})

	// 清单：能力按注册了什么自动生成；服务地址没配时用访问它的地址
	resp, err := http.Get(ts.URL + "/.well-known/dsh-ee-extension.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		ID, Kind, Protocol string
		Service            struct{ URL string }
		Capabilities       []string
		Permissions        map[string]any
		Tools              []struct {
			Name     string
			ReadOnly bool
		}
	}
	_ = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if m.ID != "acme/oa" || m.Kind != "service" || m.Protocol != "1" || m.Service.URL != ts.URL ||
		strings.Join(m.Capabilities, ",") != "endpoint,health,knowledge.documents,tools,validate" || m.Permissions["endpoint"] != true ||
		len(m.Tools) != 2 || !m.Tools[0].ReadOnly || m.Tools[1].ReadOnly {
		t.Fatalf("清单不对：%+v", m)
	}

	// 身份：不带、aud 不对、过期、别人签的，都拒绝
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	for name, tok := range map[string]string{
		"不带令牌":   "",
		"aud 不对": ee.token("acme/other", nil),
		"过期":     ee.token("acme/oa", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}),
		"签名不对":   ee.sign(other, map[string]any{"aud": "acme/oa", "sub": "7", "exp": time.Now().Add(time.Minute).Unix()}),
	} {
		if code, out := post(t, ts.URL+"/ee/health", tok, nil, nil); code != 401 {
			t.Fatalf("%s 应被拒绝：%d %v", name, code, out)
		}
		if code, _ := post(t, ts.URL+"/mcp", tok, nil, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}); code != 401 {
			t.Fatalf("%s 调 MCP 应被拒绝：%d", name, code)
		}
	}

	// 工具定义：应用固定的参数不给模型看
	list := rpc(t, ts.URL, good, nil, "tools/list", nil)
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "form_type") || !strings.Contains(string(raw), `"status"`) || !strings.Contains(string(raw), `"readOnlyHint":true`) {
		t.Fatalf("工具定义不对：%s", raw)
	}

	// 调用：没带员工凭据时返回约定的错误码
	res := rpc(t, ts.URL, good, nil, "tools/call", map[string]any{"name": "list_forms", "arguments": map[string]any{}})["result"].(map[string]any)
	if res["isError"] != true || res["_meta"].(map[string]any)[MetaError].(map[string]any)["code"] != CodeAuthorize {
		t.Fatalf("应返回 authorize：%v", res)
	}
	cfg := base64.StdEncoding.EncodeToString([]byte(`{"tenant":"acme"}`))
	res = rpc(t, ts.URL, good, map[string]string{HeaderCredential: "tok-zhangsan", HeaderConfig: cfg}, "tools/call",
		map[string]any{"name": "list_forms", "arguments": map[string]any{"form_type": "leave"}})["result"].(map[string]any)
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	var got map[string]any
	_ = json.Unmarshal([]byte(text), &got)
	if got["user"] != "zhangsan" || got["dept"] != "研发部" || got["tenant"] != "acme" || got["type"] != "leave" || got["cred"] != "tok-zhangsan" {
		t.Fatalf("处理函数拿到的不对：%s", text)
	}
	// 必填参数缺了
	res = rpc(t, ts.URL, good, nil, "tools/call", map[string]any{"name": "submit_leave", "arguments": map[string]any{}})["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("缺必填参数应报错：%v", res)
	}

	// 约定接口
	if code, out := post(t, ts.URL+"/ee/health", good, nil, nil); code != 200 || out["ok"] != true || out["version"] != "1.2.0" {
		t.Fatalf("health：%d %v", code, out)
	}
	if code, out := post(t, ts.URL+"/ee/validate", good, nil, map[string]any{"config": map[string]any{"tenant": ""}}); code != 200 || out["ok"] != false || out["message"] != "租户不能为空" {
		t.Fatalf("validate：%d %v", code, out)
	}
	if code, out := post(t, ts.URL+"/ee/options", good, nil, map[string]any{"field": "x"}); code != 404 || out["error"].(map[string]any)["code"] != CodeNotImplemented {
		t.Fatalf("没实现的接口应返回 not_implemented：%d %v", code, out)
	}
	_, ch := post(t, ts.URL+"/ee/kb/changes", good, nil, map[string]any{"cursor": ""})
	if len(ch["items"].([]any)) != 1 || ch["cursor"] != "1" {
		t.Fatalf("changes：%v", ch)
	}
	if _, ch = post(t, ts.URL+"/ee/kb/changes", good, nil, map[string]any{"cursor": "1"}); len(ch["items"].([]any)) != 0 || ch["cursor"] != "1" {
		t.Fatalf("没有新变化时应返回空列表和同一个 cursor：%v", ch)
	}
	if code, out := post(t, ts.URL+"/ee/kb/document", good, nil, map[string]any{"id": "nope"}); code != 404 || out["error"].(map[string]any)["code"] != CodeNotFound {
		t.Fatalf("不存在的文档：%d %v", code, out)
	}
	if code, out := post(t, ts.URL+"/ee/kb/search", good, nil, map[string]any{"query": "x"}); code != 404 {
		t.Fatalf("没实现联邦检索：%d %v", code, out)
	}
	_, ep := post(t, ts.URL+"/ee/endpoint", good, nil, map[string]any{"method": "POST", "path": "/cb", "body": []byte("hi")})
	body, _ := base64.StdEncoding.DecodeString(ep["body"].(string))
	if ep["status"] != float64(200) || string(body) != "POST /cb hi" {
		t.Fatalf("endpoint：%v %s", ep, body)
	}
}
