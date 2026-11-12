package exttest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/extension"
	"gopkg.in/yaml.v3"
)

// 协议一致性测试：把跑起来的服务扩展按协议逐项调一遍，判断它是不是一个合格的扩展（docs/agent-dev.md §4.2）。

// Case 是一项测试的结果。
type Case struct {
	Group string `json:"group"`
	Name  string `json:"name"`
	// pass / fail / skip
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
	Fix     string `json:"fix,omitempty"`
}

// Report 是整次测试的结果。OK 为 true 才算合格。
type Report struct {
	OK       bool   `json:"ok"`
	Identity string `json:"identity,omitempty"`
	URL      string `json:"url"`
	Passed   int    `json:"passed"`
	Failed   int    `json:"failed"`
	Skipped  int    `json:"skipped"`
	Cases    []Case `json:"cases"`
}

// Options 是测试的参数。
type Options struct {
	// 扩展的服务地址
	URL string
	// 员工在源系统的凭据（X-DSH-EE-Credential）。不给时用一个假的，扩展多半会返回 authorize
	Credential string
	// 管理员的配置
	Config map[string]any
	// 是否真的调写工具（会改数据）。默认只用不合法的参数调写工具
	Write bool
	// 作者写的场景测试所在的目录（里面的 tests/*.yaml）；可以为 nil
	Scenarios fs.FS
	// 扩展进程的输出（用 --run 启动时有），用来查凭据有没有被打进日志
	Logs func() string
}

// Codes 是 `dsh-ee extension test` 会报的检查项。
var Codes = []extension.CodeInfo{
	{Code: "test.manifest_unreachable", Severity: extension.SevError, Meaning: "取不到 /.well-known/dsh-ee-extension.json"},
	{Code: "test.manifest_invalid", Severity: extension.SevError, Meaning: "清单没有通过 extension check"},
	{Code: "test.tools_mismatch", Severity: extension.SevError, Meaning: "清单里的工具和 MCP 的工具列表不一致"},
	{Code: "test.readonly_mismatch", Severity: extension.SevError, Meaning: "同一个工具是不是只读，清单和 MCP 说的不一样"},
	{Code: "test.fill_leaked", Severity: extension.SevError, Meaning: "不由模型填的参数（fill: app / admin）出现在了给模型的工具定义里"},
	{Code: "test.capability_mismatch", Severity: extension.SevError, Meaning: "清单声明的能力和实际实现的约定接口不一致"},
	{Code: "test.auth_not_enforced", Severity: extension.SevError, Meaning: "不带令牌、令牌过期、aud 不对或签名不对的请求没有被拒绝"},
	{Code: "test.tool_crashed", Severity: extension.SevError, Meaning: "调工具时扩展返回了 5xx、断开了连接，或返回的不是合法的 MCP 结果"},
	{Code: "test.tool_bad_args", Severity: extension.SevError, Meaning: "用不合法的参数调工具，扩展没有报错"},
	{Code: "test.error_code_missing", Severity: extension.SevError, Meaning: "工具报错了但没有带约定的错误码"},
	{Code: "test.authorize_expected", Severity: extension.SevError, Meaning: "凭据模式要员工凭据，但没带凭据时扩展没有返回 authorize"},
	{Code: "test.kb_changes", Severity: extension.SevError, Meaning: "kb/changes 翻页走不完、cursor 续不上，或同一个 cursor 再问时漏了变化"},
	{Code: "test.kb_document", Severity: extension.SevError, Meaning: "kb/document 取不到增量里列出的文档，或对不存在的 id 没有返回 not_found"},
	{Code: "test.kb_browse", Severity: extension.SevError, Meaning: "kb/browse 的树遍历不了"},
	{Code: "test.kb_search", Severity: extension.SevError, Meaning: "kb/search 返回的不是约定的结构"},
	{Code: "test.credential_leaked", Severity: extension.SevError, Meaning: "凭据出现在了扩展的响应或日志里"},
	{Code: "test.unstable", Severity: extension.SevError, Meaning: "并发或重复调用时出错"},
	{Code: "test.scenario_failed", Severity: extension.SevError, Meaning: "作者写的场景测试没通过"},
	{Code: "test.scenario_invalid", Severity: extension.SevError, Meaning: "场景测试文件写得不对"},
}

type runner struct {
	env  *Env
	opt  Options
	hc   *http.Client
	rep  *Report
	m    *extension.Manifest
	aud  string
	user User
	// 测试时带的可识别的假凭据：响应和日志里都不该出现
	canaryCred, canaryShared, canarySecret string
	bodiesMu                               sync.Mutex
	bodies                                 []string
}

func (r *runner) add(group, name, status, code, msg, fix string) {
	r.rep.Cases = append(r.rep.Cases, Case{Group: group, Name: name, Status: status, Code: code, Message: msg, Fix: fix})
}
func (r *runner) pass(group, name string)      { r.add(group, name, "pass", "", "", "") }
func (r *runner) skip(group, name, why string) { r.add(group, name, "skip", "", why, "") }
func (r *runner) fail(group, name, code, msg, fix string) {
	r.add(group, name, "fail", code, msg, fix)
}

type callOpt struct {
	token      string
	noToken    bool
	credential *string
}

// post 调扩展的一个地址，返回状态码和响应内容。
func (r *runner) post(ctx context.Context, path string, body any, o callOpt) (int, []byte, error) {
	raw, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.opt.URL+path, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if !o.noToken {
		tok := o.token
		if tok == "" {
			tok = r.env.Token(r.aud, r.user)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	cred := r.canaryCred
	if o.credential != nil {
		cred = *o.credential
	}
	if cred != "" {
		req.Header.Set("X-DSH-EE-Credential", cred)
	}
	req.Header.Set("X-DSH-EE-Shared-Credential", r.canaryShared)
	cfg := map[string]any{}
	for k, v := range r.opt.Config {
		cfg[k] = v
	}
	for _, f := range r.m.Config {
		if _, ok := cfg[f.Key]; !ok && f.Type == "secret" {
			cfg[f.Key] = r.canarySecret
		}
	}
	if len(cfg) > 0 {
		b, _ := json.Marshal(cfg)
		req.Header.Set("X-DSH-EE-Config", base64.StdEncoding.EncodeToString(b))
	}
	resp, err := r.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	r.bodiesMu.Lock()
	r.bodies = append(r.bodies, string(out))
	r.bodiesMu.Unlock()
	return resp.StatusCode, out, err
}

// ee 调一个约定接口，返回状态码、解析后的响应和约定的错误码。
func (r *runner) ee(ctx context.Context, name string, body any, o callOpt) (int, map[string]any, string, error) {
	status, raw, err := r.post(ctx, "/ee/"+name, body, o)
	if err != nil {
		return 0, nil, "", err
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	code := ""
	if e, _ := out["error"].(map[string]any); e != nil {
		code, _ = e["code"].(string)
	}
	return status, out, code, nil
}

type toolResult struct {
	IsError bool
	Text    string
	Code    string
}

// rpc 调一次 MCP。
func (r *runner) rpc(ctx context.Context, method string, params any, o callOpt) (map[string]any, error) {
	status, raw, err := r.post(ctx, "/mcp", map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, o)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d：%s", status, cut(string(raw), 200))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("响应不是 JSON：%s", cut(string(raw), 200))
	}
	return out, nil
}

func (r *runner) callTool(ctx context.Context, name string, args map[string]any, o callOpt) (*toolResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	out, err := r.rpc(ctx, "tools/call", map[string]any{"name": name, "arguments": args}, o)
	if err != nil {
		return nil, err
	}
	if e, _ := out["error"].(map[string]any); e != nil {
		return &toolResult{IsError: true, Text: fmt.Sprint(e["message"]), Code: "rpc"}, nil
	}
	res, _ := out["result"].(map[string]any)
	content, ok := res["content"].([]any)
	if res == nil || !ok {
		return nil, fmt.Errorf("结果里没有 content：%v", out)
	}
	tr := &toolResult{}
	tr.IsError, _ = res["isError"].(bool)
	for _, c := range content {
		if m, _ := c.(map[string]any); m != nil && m["type"] == "text" {
			tr.Text += fmt.Sprint(m["text"])
		}
	}
	if meta, _ := res["_meta"].(map[string]any); meta != nil {
		if e, _ := meta["dsh-ee/error"].(map[string]any); e != nil {
			tr.Code, _ = e["code"].(string)
		}
	}
	return tr, nil
}

func cut(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// Run 对一个跑着的服务扩展做一遍协议测试。
func Run(ctx context.Context, env *Env, opt Options) *Report {
	opt.URL = strings.TrimRight(opt.URL, "/")
	r := &runner{env: env, opt: opt, hc: &http.Client{Timeout: 20 * time.Second}, rep: &Report{URL: opt.URL, Cases: []Case{}}, user: Users[0],
		canaryCred: "dsh-ee-test-credential-7f3a9c", canaryShared: "dsh-ee-test-shared-5e1b2d", canarySecret: "dsh-ee-test-secret-9a4c6e", m: &extension.Manifest{}}
	if opt.Credential != "" {
		r.canaryCred = opt.Credential
	}
	if r.manifest(ctx) {
		r.identity(ctx)
		tools := r.tools(ctx)
		r.capabilities(ctx)
		r.knowledge(ctx)
		r.stability(ctx, tools)
		r.scenarios(ctx)
		r.leaks()
	}
	for _, c := range r.rep.Cases {
		switch c.Status {
		case "pass":
			r.rep.Passed++
		case "fail":
			r.rep.Failed++
		default:
			r.rep.Skipped++
		}
	}
	r.rep.OK = r.rep.Failed == 0 && r.rep.Passed > 0
	return r.rep
}

func (r *runner) manifest(ctx context.Context) bool {
	const g = "清单"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.opt.URL+"/.well-known/dsh-ee-extension.json", nil)
	resp, err := r.hc.Do(req)
	if err != nil {
		r.fail(g, "能取到清单", "test.manifest_unreachable", "连不上扩展："+err.Error(), "确认扩展已经启动，地址是 "+r.opt.URL)
		return false
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		r.fail(g, "能取到清单", "test.manifest_unreachable", fmt.Sprintf("/.well-known/dsh-ee-extension.json 返回 HTTP %d", resp.StatusCode), "用 SDK 的 ext.Handler() 会自动提供这个地址；自己实现协议的话要加上")
		return false
	}
	r.pass(g, "能取到清单")
	res := extension.CheckFiles(map[string][]byte{extension.ManifestFile: raw})
	if res.Manifest != nil {
		r.m = res.Manifest
		r.rep.Identity = res.Identity
		r.aud = res.Manifest.ID
	}
	if !res.OK {
		for _, d := range res.Results {
			if d.Severity == extension.SevError {
				r.fail(g, "清单符合规格", "test.manifest_invalid", d.Code+"（"+d.Where+"）："+d.Message, d.Fix)
			}
		}
		return false
	}
	if res.Manifest.Kind != extension.KindService {
		r.fail(g, "清单符合规格", "test.manifest_invalid", "这个地址上的清单不是服务扩展（kind 是 "+res.Manifest.Kind+"）", "声明式扩展用 `dsh-ee extension check <目录>` 检查，不用跑起来测")
		return false
	}
	r.pass(g, "清单符合规格")
	return true
}

func (r *runner) identity(ctx context.Context) {
	const g = "身份"
	bad, err := r.env.badTokens(r.aud)
	if err != nil {
		r.skip(g, "拒绝不合格的令牌", err.Error())
		return
	}
	names := make([]string, 0, len(bad))
	for n := range bad {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		o := callOpt{token: bad[name], noToken: bad[name] == ""}
		ok := true
		for _, path := range []string{"/mcp", "/ee/health"} {
			var body any = map[string]any{}
			if path == "/mcp" {
				body = map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"}
			}
			status, _, err := r.post(ctx, path, body, o)
			if err != nil || status != http.StatusUnauthorized {
				ok = false
				r.fail(g, name+"的请求被拒绝（"+path+"）", "test.auth_not_enforced", fmt.Sprintf("%s 时 %s 返回了 HTTP %d，应该是 401", name, path, status),
					"每个请求都要用 dsh-ee 的 JWKS 验签并核对 exp 和 aud（aud 是扩展的 id）；用 SDK 的话确认没有设 DSH_EE_INSECURE=1")
			}
		}
		if ok {
			r.pass(g, name+"的请求被拒绝")
		}
	}
}

type mcpTool struct {
	Name     string
	ReadOnly bool
	Schema   map[string]any
}

func (r *runner) tools(ctx context.Context) []mcpTool {
	const g = "工具"
	if _, err := r.rpc(ctx, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "dsh-ee-test", "version": "1"}}, callOpt{}); err != nil {
		r.fail(g, "MCP 能初始化", "test.tool_crashed", err.Error(), "扩展要在 /mcp 上用 Streamable HTTP 提供 MCP：POST 一条 JSON-RPC，回一条 JSON")
		return nil
	}
	out, err := r.rpc(ctx, "tools/list", nil, callOpt{})
	if err != nil {
		r.fail(g, "能列出工具", "test.tool_crashed", err.Error(), "")
		return nil
	}
	res, _ := out["result"].(map[string]any)
	list, _ := res["tools"].([]any)
	var tools []mcpTool
	for _, x := range list {
		m, _ := x.(map[string]any)
		if m == nil {
			continue
		}
		t := mcpTool{Name: fmt.Sprint(m["name"])}
		t.Schema, _ = m["inputSchema"].(map[string]any)
		if ann, _ := m["annotations"].(map[string]any); ann != nil {
			t.ReadOnly, _ = ann["readOnlyHint"].(bool)
		}
		tools = append(tools, t)
	}
	// 清单和 MCP 一致
	byName := map[string]mcpTool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	same := true
	for _, mt := range r.m.Tools {
		t, ok := byName[mt.Name]
		if !ok {
			same = false
			r.fail(g, "清单和 MCP 的工具一致", "test.tools_mismatch", "清单里有工具 "+mt.Name+"，MCP 的 tools/list 里没有", "两边要一致：用 SDK 的话清单是自动生成的，不要另外手写一份")
			continue
		}
		if t.ReadOnly != mt.ReadOnly {
			same = false
			r.fail(g, "清单和 MCP 的工具一致", "test.readonly_mismatch", fmt.Sprintf("工具 %s：清单里 readOnly 是 %v，MCP 的 readOnlyHint 是 %v", mt.Name, mt.ReadOnly, t.ReadOnly), "dsh-ee 以清单为准；把 MCP 的 readOnlyHint 改成和清单一样")
		}
		props, _ := t.Schema["properties"].(map[string]any)
		for _, p := range mt.Params {
			if _, leaked := props[p.Name]; leaked && (p.Fill == extension.FillApp || p.Fill == extension.FillAdmin) {
				same = false
				r.fail(g, "不由模型填的参数不在工具定义里", "test.fill_leaked", fmt.Sprintf("工具 %s 的参数 %s 是 fill: %s，却出现在 MCP 的 inputSchema 里", mt.Name, p.Name, p.Fill), "从 inputSchema 里拿掉它：这类参数由 dsh-ee 在调用时补上，模型不该看到")
			}
		}
		delete(byName, mt.Name)
	}
	for name := range byName {
		same = false
		r.fail(g, "清单和 MCP 的工具一致", "test.tools_mismatch", "MCP 的 tools/list 里有工具 "+name+"，清单里没有", "把它加进清单的 tools（清单是给管理员看、给权限和确认用的），否则 dsh-ee 不会收这个工具")
	}
	if same {
		r.pass(g, "清单和 MCP 的工具一致")
	}
	needsCred := r.m.Credential != nil && (r.m.Credential.Mode == extension.CredUserSecret || r.m.Credential.Mode == extension.CredOAuth)
	for _, t := range tools {
		mt := r.m.Tool(t.Name)
		if mt == nil {
			continue
		}
		args := sampleArgs(mt)
		// 不合法的参数：必填的缺了，或类型不对
		if bad, why := badArgs(mt); bad != nil {
			tr, err := r.callTool(ctx, t.Name, bad, callOpt{})
			switch {
			case err != nil:
				r.fail(g, t.Name+"：参数不合法时报错而不是崩掉", "test.tool_crashed", why+"时："+err.Error(), "处理函数里先检查参数，不合法就返回 sdk.Errorf(sdk.CodeInvalid, …)")
			case !tr.IsError:
				r.fail(g, t.Name+"：参数不合法时报错", "test.tool_bad_args", why+"时扩展没有报错，返回了："+cut(tr.Text, 120), "参数不合法要返回 isError 的结果，错误码用 invalid")
			default:
				r.pass(g, t.Name+"：参数不合法时报错")
			}
		}
		if !mt.ReadOnly && !r.opt.Write {
			r.skip(g, t.Name+"：用合法的参数调用", "写工具默认不真的调（会改数据）；要测加 --write")
			continue
		}
		tr, err := r.callTool(ctx, t.Name, args, callOpt{})
		switch {
		case err != nil:
			r.fail(g, t.Name+"：用合法的参数调用", "test.tool_crashed", err.Error(), "工具出错要返回 isError 的 MCP 结果，不能让请求 5xx 或断开")
		case tr.IsError && tr.Code == "":
			r.fail(g, t.Name+"：报错时带错误码", "test.error_code_missing", "工具报错了但没有带错误码："+cut(tr.Text, 120), "用 sdk.Errorf(sdk.CodeXxx, …) 返回错误；dsh-ee 靠错误码决定怎么告诉员工（authorize / setup / forbidden / unavailable / invalid / not_found）")
		default:
			r.pass(g, t.Name+"：用合法的参数调用")
		}
		if needsCred && mt.ReadOnly {
			none := ""
			tr, err := r.callTool(ctx, t.Name, args, callOpt{credential: &none})
			switch {
			case err != nil:
				r.fail(g, t.Name+"：没带员工凭据时提示授权", "test.tool_crashed", err.Error(), "")
			case !tr.IsError || tr.Code != "authorize":
				r.fail(g, t.Name+"：没带员工凭据时提示授权", "test.authorize_expected", fmt.Sprintf("凭据模式是 %s，没带员工凭据时应返回错误码 authorize，实际是 isError=%v code=%q", r.m.Credential.Mode, tr.IsError, tr.Code),
					"处理函数里发现 c.Credential 为空（或源系统说它失效了）就返回 sdk.Errorf(sdk.CodeAuthorize, …)，dsh-ee 会让员工去授权")
			default:
				r.pass(g, t.Name+"：没带员工凭据时提示授权")
			}
		}
	}
	return tools
}

// sampleArgs 按清单里的参数造一组合法的参数。
func sampleArgs(t *extension.Tool) map[string]any {
	args := map[string]any{}
	for _, p := range t.Params {
		if len(p.Default) > 0 {
			var v any
			if json.Unmarshal(p.Default, &v) == nil && v != nil {
				args[p.Name] = v
				continue
			}
		}
		if !p.Required && len(p.Enum) == 0 {
			continue
		}
		args[p.Name] = sampleValue(p)
	}
	return args
}

func sampleValue(p extension.Param) any {
	if len(p.Enum) > 0 {
		return p.Enum[0]
	}
	switch p.Type {
	case "number", "integer":
		return 1
	case "boolean":
		return true
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	}
	return "test"
}

// badArgs 造一组不合法的参数：缺一个必填的，没有必填的就把一个参数的类型弄错。
func badArgs(t *extension.Tool) (map[string]any, string) {
	for _, p := range t.Params {
		if p.Required && (p.Fill == "" || p.Fill == extension.FillModel) {
			args := sampleArgs(t)
			delete(args, p.Name)
			return args, "缺少必填参数 " + p.Name + " "
		}
	}
	return nil, ""
}

func (r *runner) capabilities(ctx context.Context) {
	const g = "能力"
	// 能力 → 用来探测的约定接口和请求
	probes := []struct {
		capability, name string
		body             any
	}{
		{"health", "health", map[string]any{}},
		{"validate", "validate", map[string]any{"config": r.opt.Config}},
		{"options", "options", map[string]any{"field": "__probe__"}},
		{"knowledge.documents", "kb/changes", map[string]any{"cursor": ""}},
		{"knowledge.search", "kb/search", map[string]any{"query": "test", "limit": 1}},
	}
	ok := true
	for _, p := range probes {
		declared := slices.Contains(r.m.Capabilities, p.capability)
		status, _, code, err := r.ee(ctx, p.name, p.body, callOpt{token: r.env.SystemToken(r.aud)})
		if p.capability == "knowledge.search" {
			status, _, code, err = r.ee(ctx, p.name, p.body, callOpt{})
		}
		implemented := err == nil && code != "not_implemented" && !(status == http.StatusNotFound && code == "")
		switch {
		case err != nil:
			ok = false
			r.fail(g, "声明的能力和实现一致", "test.capability_mismatch", "调 /ee/"+p.name+" 出错："+err.Error(), "")
		case declared && !implemented:
			ok = false
			r.fail(g, "声明的能力和实现一致", "test.capability_mismatch", "清单声明了能力 "+p.capability+"，但 /ee/"+p.name+" 没有实现", "实现这个接口，或从 capabilities 里去掉它（用 SDK 时 capabilities 是自动生成的，不用手写）")
		case !declared && implemented && p.capability != "health":
			ok = false
			r.fail(g, "声明的能力和实现一致", "test.capability_mismatch", "/ee/"+p.name+" 实现了，但清单的 capabilities 里没有 "+p.capability, "加进 capabilities，dsh-ee 才会用它")
		case status >= 500:
			ok = false
			r.fail(g, "声明的能力和实现一致", "test.capability_mismatch", fmt.Sprintf("/ee/%s 返回了 HTTP %d", p.name, status), "约定接口出错要返回带错误码的 JSON，不能 5xx")
		}
	}
	if ok {
		r.pass(g, "声明的能力和实现一致")
	}
}

func (r *runner) knowledge(ctx context.Context) {
	const g = "知识源"
	sys := callOpt{token: r.env.SystemToken(r.aud)}
	if slices.Contains(r.m.Capabilities, "knowledge.documents") {
		cursor, ids, seen := "", []string{}, map[string]bool{}
		ok := true
		pages := 0
		for ; pages < 100; pages++ {
			status, out, code, err := r.ee(ctx, "kb/changes", map[string]any{"cursor": cursor}, sys)
			if err != nil || status != http.StatusOK {
				ok = false
				r.fail(g, "增量能翻到底", "test.kb_changes", fmt.Sprintf("kb/changes（cursor=%q）失败：HTTP %d %s %v", cursor, status, code, err), "")
				break
			}
			items, isList := out["items"].([]any)
			next, _ := out["cursor"].(string)
			more, _ := out["hasMore"].(bool)
			if !isList {
				ok = false
				r.fail(g, "增量能翻到底", "test.kb_changes", "kb/changes 的响应里没有 items 数组", "没有变化时返回 {\"items\": [], \"cursor\": \"…\"}")
				break
			}
			for _, x := range items {
				m, _ := x.(map[string]any)
				id, _ := m["id"].(string)
				if id == "" {
					ok = false
					r.fail(g, "增量能翻到底", "test.kb_changes", "kb/changes 的一项没有 id", "")
					continue
				}
				if deleted, _ := m["deleted"].(bool); !deleted && !seen[id] {
					seen[id] = true
					ids = append(ids, id)
				}
			}
			if more && (next == "" || next == cursor) {
				ok = false
				r.fail(g, "增量能翻到底", "test.kb_changes", fmt.Sprintf("hasMore 是 true，但 cursor 没有往前走（%q → %q），会一直翻同一页", cursor, next), "每一页都要返回新的 cursor；cursor 要单调往前")
				break
			}
			if next != "" {
				cursor = next
			}
			if !more {
				break
			}
		}
		if pages >= 100 {
			ok = false
			r.fail(g, "增量能翻到底", "test.kb_changes", "翻了 100 页还没到底", "测试用的数据量小一点，或检查 hasMore 是不是一直是 true")
		}
		if ok {
			// 到底之后用同一个 cursor 再问：不该再有变化，cursor 也不该回退
			_, out, _, err := r.ee(ctx, "kb/changes", map[string]any{"cursor": cursor}, sys)
			items, _ := out["items"].([]any)
			next, _ := out["cursor"].(string)
			if err != nil || len(items) != 0 || (next != "" && next != cursor) {
				ok = false
				r.fail(g, "同一个 cursor 再问不重复、不回退", "test.kb_changes", fmt.Sprintf("翻到底后带着最后的 cursor（%q）再问，返回了 %d 项，cursor 变成 %q", cursor, len(items), next),
					"cursor 表示「这之前的都给过了」：没有新变化时返回空列表和同一个 cursor。常见错误是把页码当 cursor")
			}
		}
		if ok {
			r.pass(g, "增量能翻到底，cursor 能续上")
		}
		docOK := true
		for i, id := range ids {
			if i == 5 {
				break
			}
			status, out, code, err := r.ee(ctx, "kb/document", map[string]any{"id": id}, sys)
			content, _ := out["content"].(string)
			if err != nil || status != http.StatusOK || strings.TrimSpace(content) == "" {
				docOK = false
				r.fail(g, "能取到增量里的文档", "test.kb_document", fmt.Sprintf("kb/document（id=%s）：HTTP %d %s，content 为空或取不到 %v", id, status, code, err), "kb/changes 里列出的、没标 deleted 的文档，kb/document 都要能取到正文")
			}
		}
		status, _, code, err := r.ee(ctx, "kb/document", map[string]any{"id": "dsh-ee-test-no-such-document"}, sys)
		if err != nil || code != "not_found" || status < 400 {
			docOK = false
			r.fail(g, "不存在的文档返回 not_found", "test.kb_document", fmt.Sprintf("kb/document 对不存在的 id 返回了 HTTP %d code=%q", status, code), "返回 sdk.Errorf(sdk.CodeNotFound, …)；dsh-ee 靠它区分「文档没了」和「扩展出错了」")
		}
		if docOK {
			r.pass(g, "文档能取到，不存在的返回 not_found")
		}
		// browse 可以不实现
		status, out, code, err := r.ee(ctx, "kb/browse", map[string]any{"parent": ""}, sys)
		switch {
		case code == "not_implemented":
			r.skip(g, "目录能遍历", "没有实现 kb/browse：管理员挂知识源时不能勾选范围，只能全部同步")
		case err != nil || status != http.StatusOK:
			r.fail(g, "目录能遍历", "test.kb_browse", fmt.Sprintf("kb/browse 最上层：HTTP %d %s %v", status, code, err), "")
		default:
			items, _ := out["items"].([]any)
			browseOK := true
			for i, x := range items {
				m, _ := x.(map[string]any)
				id, _ := m["id"].(string)
				kind, _ := m["kind"].(string)
				if id == "" || (kind != "folder" && kind != "document") {
					browseOK = false
					r.fail(g, "目录能遍历", "test.kb_browse", fmt.Sprintf("kb/browse 的第 %d 项缺 id，或 kind 不是 folder / document", i+1), "")
					break
				}
				if kind == "folder" && i < 3 {
					if st, _, c, err := r.ee(ctx, "kb/browse", map[string]any{"parent": id}, sys); err != nil || st != http.StatusOK {
						browseOK = false
						r.fail(g, "目录能遍历", "test.kb_browse", fmt.Sprintf("kb/browse（parent=%s）：HTTP %d %s %v", id, st, c, err), "最上层列出的每个 folder 都要能再往下看")
					}
				}
			}
			if browseOK {
				r.pass(g, "目录能遍历")
			}
		}
	} else {
		r.skip(g, "文档库同步", "清单没有声明 knowledge.documents")
	}
	if slices.Contains(r.m.Capabilities, "knowledge.search") {
		status, out, code, err := r.ee(ctx, "kb/search", map[string]any{"query": "test", "limit": 3}, callOpt{})
		items, isList := out["items"].([]any)
		switch {
		case err != nil || (status != http.StatusOK && code == ""):
			r.fail(g, "联邦检索返回约定的结构", "test.kb_search", fmt.Sprintf("kb/search：HTTP %d %v", status, err), "")
		case status == http.StatusOK && (!isList || len(items) > 3):
			r.fail(g, "联邦检索返回约定的结构", "test.kb_search", "kb/search 要返回 {\"items\": [...]}，而且不超过 limit 条", "")
		default:
			r.pass(g, "联邦检索返回约定的结构")
		}
	}
}

func (r *runner) stability(ctx context.Context, tools []mcpTool) {
	const g = "稳定性"
	var wg sync.WaitGroup
	errs := make(chan string, 32)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				if _, err := r.rpc(ctx, "tools/list", nil, callOpt{}); err != nil {
					errs <- "tools/list：" + err.Error()
				}
				return
			}
			if status, _, _, err := r.ee(ctx, "health", map[string]any{}, callOpt{token: r.env.SystemToken(r.aud)}); err != nil || status != http.StatusOK {
				errs <- fmt.Sprintf("health：HTTP %d %v", status, err)
			}
		}(i)
	}
	// 只读工具重复调用
	for _, t := range tools {
		mt := r.m.Tool(t.Name)
		if mt == nil || !mt.ReadOnly {
			continue
		}
		for i := 0; i < 3; i++ {
			wg.Add(1)
			go func(name string, args map[string]any) {
				defer wg.Done()
				if _, err := r.callTool(ctx, name, args, callOpt{}); err != nil {
					errs <- name + "：" + err.Error()
				}
			}(t.Name, sampleArgs(mt))
		}
		break
	}
	wg.Wait()
	close(errs)
	var all []string
	for e := range errs {
		all = append(all, e)
	}
	if len(all) > 0 {
		r.fail(g, "并发和重复调用", "test.unstable", fmt.Sprintf("%d 次并发调用出错，例如：%s", len(all), all[0]), "处理函数会被并发调用：共享的数据要加锁，不要依赖调用顺序")
		return
	}
	r.pass(g, "并发和重复调用")
}

// leaks 查测试时带的假凭据有没有出现在响应或日志里。
func (r *runner) leaks() {
	const g = "凭据"
	canaries := map[string]string{"共享凭据": r.canaryShared, "密钥类型的配置项": r.canarySecret}
	if r.opt.Credential == "" {
		canaries["员工凭据"] = r.canaryCred
	}
	ok := true
	r.bodiesMu.Lock()
	bodies := strings.Join(r.bodies, "\n")
	r.bodiesMu.Unlock()
	logs := ""
	if r.opt.Logs != nil {
		logs = r.opt.Logs()
	}
	for what, c := range canaries {
		if strings.Contains(bodies, c) {
			ok = false
			r.fail(g, "凭据不出现在响应里", "test.credential_leaked", what+"出现在了扩展的响应里（会进到模型的上下文）", "不要把凭据、请求头、配置原样放进工具结果或错误信息")
		}
		if logs != "" && strings.Contains(logs, c) {
			ok = false
			r.fail(g, "凭据不出现在日志里", "test.credential_leaked", what+"被打进了扩展的日志", "不要打印 c.Credential、c.Shared、c.Config 里的密钥，也不要整个打印请求头")
		}
	}
	if ok {
		if logs == "" {
			r.pass(g, "凭据不出现在响应里（没有用 --run 启动，日志没法查）")
		} else {
			r.pass(g, "凭据不出现在响应和日志里")
		}
	}
}

// ---- 场景测试 ----

// Scenario 是作者在 tests/*.yaml 里写的一条场景。
type Scenario struct {
	Name string `yaml:"name"`
	// 以谁的身份调用（模拟环境里预置的员工），默认 zhangsan
	As string `yaml:"as"`
	// 员工凭据：不写用 --credential 给的；none 表示不带
	Credential string         `yaml:"credential"`
	Call       string         `yaml:"call"`
	Args       map[string]any `yaml:"args"`
	Expect     struct {
		OK       *bool          `yaml:"ok"`
		Error    string         `yaml:"error"`
		Contains []string       `yaml:"contains"`
		Count    map[string]int `yaml:"count"`
	} `yaml:"expect"`
}

func (r *runner) scenarios(ctx context.Context) {
	const g = "场景"
	if r.opt.Scenarios == nil {
		return
	}
	files, _ := fs.Glob(r.opt.Scenarios, "tests/*.yaml")
	more, _ := fs.Glob(r.opt.Scenarios, "tests/*.yml")
	files = append(files, more...)
	sort.Strings(files)
	for _, file := range files {
		raw, err := fs.ReadFile(r.opt.Scenarios, file)
		if err != nil {
			continue
		}
		var list []Scenario
		if err := yaml.Unmarshal(raw, &list); err != nil {
			r.fail(g, file, "test.scenario_invalid", "读不了："+err.Error(), "场景文件是一个 YAML 列表，每一项有 name、call、expect；见 docs/extension-protocol.md §8")
			continue
		}
		for _, sc := range list {
			name := file + "：" + sc.Name
			user := r.user
			if sc.As != "" {
				u, ok := FindUser(sc.As)
				if !ok {
					r.fail(g, name, "test.scenario_invalid", "模拟环境里没有员工 "+sc.As, "预置的员工有 zhangsan、lisi、wangwu、admin")
					continue
				}
				user = u
			}
			if sc.Call == "" {
				r.fail(g, name, "test.scenario_invalid", "没有写 call（要调的工具）", "")
				continue
			}
			o := callOpt{token: r.env.Token(r.aud, user)}
			if sc.Credential == "none" {
				none := ""
				o.credential = &none
			} else if sc.Credential != "" {
				c := sc.Credential
				o.credential = &c
			}
			tr, err := r.callTool(ctx, sc.Call, sc.Args, o)
			if err != nil {
				r.fail(g, name, "test.scenario_failed", err.Error(), "")
				continue
			}
			if msg := checkScenario(&sc, tr); msg != "" {
				r.fail(g, name, "test.scenario_failed", msg+"；实际返回："+cut(tr.Text, 200), "")
				continue
			}
			r.pass(g, name)
		}
	}
}

func checkScenario(sc *Scenario, tr *toolResult) string {
	e := sc.Expect
	if e.Error != "" {
		if !tr.IsError || tr.Code != e.Error {
			return fmt.Sprintf("应返回错误码 %s，实际 isError=%v code=%q", e.Error, tr.IsError, tr.Code)
		}
		return ""
	}
	if e.OK != nil && *e.OK == tr.IsError {
		return fmt.Sprintf("应该 ok=%v，实际 isError=%v code=%q", *e.OK, tr.IsError, tr.Code)
	}
	for _, c := range e.Contains {
		if !strings.Contains(tr.Text, c) {
			return "结果里应包含 " + c
		}
	}
	if len(e.Count) > 0 {
		n, ok := countItems(tr.Text)
		if !ok {
			return "结果不是列表（或带 items 的对象），数不了条数"
		}
		for op, want := range e.Count {
			pass := map[string]bool{"eq": n == want, "gte": n >= want, "lte": n <= want, "gt": n > want, "lt": n < want}
			got, known := pass[op]
			if !known {
				return "count 只支持 eq / gte / lte / gt / lt"
			}
			if !got {
				return fmt.Sprintf("条数应 %s %d，实际 %d", op, want, n)
			}
		}
	}
	return ""
}

// countItems 数结果里有几条：结果是数组，或对象里的 items / records / data / list 数组。
func countItems(text string) (int, bool) {
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		return 0, false
	}
	switch x := v.(type) {
	case []any:
		return len(x), true
	case map[string]any:
		for _, k := range []string{"items", "records", "data", "list", "results"} {
			if arr, ok := x[k].([]any); ok {
				return len(arr), true
			}
		}
	}
	return 0, false
}
