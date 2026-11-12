// Package sdk 是 dsh-ee 服务扩展的 Go SDK（docs/plugins.md §4.2、§10，协议见 docs/extension-protocol.md）。
//
// 一个服务扩展是企业自己部署的 HTTP 服务。用这个包只要注册处理函数：
//
//	ext := sdk.New(sdk.Manifest{ID: "acme/oa", Version: "1.0.0", Name: sdk.T("Acme OA", "")})
//	ext.Tool(sdk.Tool{Name: "list_forms", ReadOnly: true, Handler: listForms})
//	log.Fatal(ext.ListenAndServe(":9000"))
//
// SDK 负责：在 /.well-known/dsh-ee-extension.json 提供清单（capabilities 按注册了什么自动生成）、
// 在 /mcp 提供工具、在 /ee/* 提供约定接口、用 dsh-ee 的 JWKS 验证每个请求并把员工身份交给处理函数。
// 只用标准库。
package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// ProtocolVersion 是这份 SDK 实现的协议版本，清单里的 protocol 字段。
const ProtocolVersion = "1"

// dsh-ee 调扩展时用的请求头。
const (
	// 这位员工在源系统的凭据（user_secret 的值，或 oauth 的 access token）。只在这一次请求里有效，不要落盘、不要打日志
	HeaderCredential = "X-DSH-EE-Credential"
	// 管理员配的共享凭据
	HeaderShared = "X-DSH-EE-Shared-Credential"
	// 管理员安装时填的配置，base64 的 JSON 对象
	HeaderConfig = "X-DSH-EE-Config"
)

// Text 是给人看的文字，中英两种语言；只写一种时清单里就是一个字符串。
type Text struct {
	ZH string `json:"zh,omitempty"`
	EN string `json:"en,omitempty"`
}

// T 写一段双语文字，en 可以留空。
func T(zh, en string) Text { return Text{ZH: zh, EN: en} }

func (t Text) MarshalJSON() ([]byte, error) {
	if t.EN == "" {
		return json.Marshal(t.ZH)
	}
	type both Text
	return json.Marshal(both(t))
}

func (t Text) any() string {
	if t.ZH != "" {
		return t.ZH
	}
	return t.EN
}

// Manifest 是扩展的清单。Kind、Capabilities、Tools 由 SDK 填，不用写。
type Manifest struct {
	ID          string `json:"id"`
	Version     string `json:"version"`
	MinVersion  string `json:"minVersion,omitempty"`
	Name        Text   `json:"name"`
	Description Text   `json:"description,omitempty"`
	Repo        string `json:"repo,omitempty"`
	// 服务对外的地址（dsh-ee 能访问到的）。空则取环境变量 EXTENSION_URL
	URL string `json:"-"`

	Permissions map[string]any `json:"permissions,omitempty"`
	Config      []ConfigField  `json:"config,omitempty"`
	Credential  *Credential    `json:"credential,omitempty"`
	OAuth       *OAuthConfig   `json:"oauth,omitempty"`
	Fields      []Field        `json:"fields,omitempty"`
}

// ConfigField 是管理员安装时填的一项配置。Type：text / secret / url / select / boolean。
type ConfigField struct {
	Key      string `json:"key"`
	Type     string `json:"type"`
	Label    Text   `json:"label"`
	Help     Text   `json:"help,omitempty"`
	Required bool   `json:"required,omitempty"`
	Default  any    `json:"default,omitempty"`
	// select 的选项：[]string，或 "dynamic"（由 Options 处理函数给出）
	Options any `json:"options,omitempty"`
}

// Credential 是扩展调源系统时要用谁的身份。Mode：shared / user_secret / ee_jwt / oauth。
// 不管哪种，dsh-ee 调扩展的每个请求都带着 ee_jwt（处理函数里的 Call.User）。
type Credential struct {
	Mode string `json:"mode"`
	Hint Text   `json:"hint,omitempty"`
	// ee_jwt 里额外带的员工信息：dept / roles / idp_sub
	Claims []string `json:"claims,omitempty"`
}

// OAuthConfig 是标准 OAuth 2.0 授权码流程的参数；流程不标准时不写它，改为实现 OAuth 处理函数。
type OAuthConfig struct {
	AuthorizeURL string   `json:"authorizeUrl"`
	TokenURL     string   `json:"tokenUrl"`
	Scopes       []string `json:"scopes,omitempty"`
}

// Field 是扩展带的一个扩展字段（docs/access-control.md §6.0）。
type Field struct {
	Key    string `json:"key"`
	Object string `json:"object"` // document / kbase / user
	Type   string `json:"type"`   // text / number / date / select / tags / level
	Label  Text   `json:"label"`
	// select / tags 的选项，level 从低到高的等级
	Options []string `json:"options,omitempty"`
	Access  bool     `json:"access,omitempty"`
}

// Param 是工具的一个参数。Fill：model（默认，模型填）/ app（搭应用的人固定）/ admin（管理员统一设置）。
// app 和 admin 的参数不出现在给模型的工具定义里，由 dsh-ee 在调用时补进 Args。
type Param struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // string / number / integer / boolean / array / object
	Description Text   `json:"description,omitempty"`
	Label       Text   `json:"label,omitempty"`
	Required    bool   `json:"required,omitempty"`
	Enum        []any  `json:"enum,omitempty"`
	Fill        string `json:"fill,omitempty"`
	Default     any    `json:"default,omitempty"`
}

// Tool 是一个工具。只读的才标 ReadOnly；没标的一律当写操作，调用前要员工确认。
type Tool struct {
	Name        string  `json:"name"`
	Description Text    `json:"description,omitempty"`
	ReadOnly    bool    `json:"readOnly,omitempty"`
	Params      []Param `json:"params,omitempty"`
	// 返回字符串就是文本结果；返回别的值会编成 JSON。出错返回 sdk.Errorf(...) 带上错误码
	Handler func(ctx context.Context, c *Call) (any, error) `json:"-"`
}

// User 是发起这次调用的员工，来自验过签的 ee_jwt。System 为 true 时没有员工（健康检查、同步、回调）。
type User struct {
	ID       string
	Username string
	Name     string
	Email    string
	Dept     string
	Roles    []string
	IDPSub   string
	System   bool
}

// Call 是一次调用的上下文。
type Call struct {
	User User
	// 工具参数（约定接口没有）
	Args map[string]any
	// 这位员工在源系统的凭据；没有就是空
	Credential string
	// 管理员配的共享凭据
	Shared string
	// 管理员安装时填的配置
	Config map[string]any
	// 原始的 ee_jwt，源系统自己能验 JWKS 时可以直接转给它
	Token string
}

// String 取一个字符串参数。
func (c *Call) String(name string) string {
	s, _ := c.Args[name].(string)
	return s
}

// Option 是动态选项里的一项。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Extension 是一个服务扩展。
type Extension struct {
	m     Manifest
	tools []*Tool
	opts  Options

	validate func(ctx context.Context, c *Call) error
	options  func(ctx context.Context, c *Call, field string) ([]Option, error)
	kb       *Knowledge
	oauth    *OAuth
	endpoint func(ctx context.Context, c *Call, req *EndpointRequest) (*EndpointResponse, error)

	keys  *keySet
	start time.Time
}

// Options 是运行参数，零值时都从环境变量取。
type Options struct {
	// dsh-ee 的地址，JWKS 在它的 /.well-known/jwks.json。环境变量 DSH_EE_URL
	DshEEURL string
	// 要求令牌的签发方就是这个地址；空则不查（员工可能从几个域名访问同一台 dsh-ee）。环境变量 DSH_EE_ISSUER
	Issuer string
	// 令牌的 aud，空则用清单的 id
	Audience string
	// 本机开发时跳过验签（请求里的令牌只解析不校验）。环境变量 DSH_EE_INSECURE=1。绝不能在生产上打开
	Insecure bool
	HTTP     *http.Client
}

// New 建一个扩展。
func New(m Manifest) *Extension { return NewWith(m, Options{}) }

// NewWith 用指定的运行参数建一个扩展。
func NewWith(m Manifest, o Options) *Extension {
	if o.DshEEURL == "" {
		o.DshEEURL = os.Getenv("DSH_EE_URL")
	}
	if o.Issuer == "" {
		o.Issuer = os.Getenv("DSH_EE_ISSUER")
	}
	if !o.Insecure {
		o.Insecure = os.Getenv("DSH_EE_INSECURE") == "1"
	}
	if o.Audience == "" {
		o.Audience = m.ID
	}
	if m.URL == "" {
		m.URL = os.Getenv("EXTENSION_URL")
	}
	e := &Extension{m: m, opts: o, start: time.Now()}
	if o.DshEEURL != "" {
		e.keys = &keySet{url: strings.TrimRight(o.DshEEURL, "/") + "/.well-known/jwks.json", hc: o.HTTP}
	}
	return e
}

// Tool 注册一个工具。
func (e *Extension) Tool(t Tool) *Extension {
	if t.Handler == nil {
		panic("sdk: 工具 " + t.Name + " 没有 Handler")
	}
	for _, old := range e.tools {
		if old.Name == t.Name {
			panic("sdk: 工具 " + t.Name + " 注册了两次")
		}
	}
	e.tools = append(e.tools, &t)
	return e
}

// Validate 注册配置检查：管理员保存配置或共享凭据前调用，c.Config 是要保存的配置。返回错误就不让保存。
func (e *Extension) Validate(f func(ctx context.Context, c *Call) error) *Extension {
	e.validate = f
	return e
}

// Options 注册动态选项：给 options 写了 "dynamic" 的配置项或参数提供选项。
func (e *Extension) Options(f func(ctx context.Context, c *Call, field string) ([]Option, error)) *Extension {
	e.options = f
	return e
}

// EndpointRequest 是第三方打到回调地址上的请求，由 dsh-ee 原样转过来。验签要扩展自己做。
type EndpointRequest struct {
	Method string              `json:"method"`
	Path   string              `json:"path"`
	Query  string              `json:"query,omitempty"`
	Header map[string][]string `json:"headers,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

// EndpointResponse 由 dsh-ee 原样回给第三方。
type EndpointResponse struct {
	Status int                 `json:"status"`
	Header map[string][]string `json:"headers,omitempty"`
	Body   []byte              `json:"body,omitempty"`
}

// Endpoint 注册回调处理函数（清单里要申请 endpoint 权限）。
func (e *Extension) Endpoint(f func(ctx context.Context, c *Call, req *EndpointRequest) (*EndpointResponse, error)) *Extension {
	e.endpoint = f
	return e
}

// Capabilities 按注册了哪些处理函数列出能力。
func (e *Extension) Capabilities() []string {
	out := []string{"health"}
	if len(e.tools) > 0 {
		out = append(out, "tools")
	}
	if e.validate != nil {
		out = append(out, "validate")
	}
	if e.options != nil {
		out = append(out, "options")
	}
	if k := e.kb; k != nil {
		if k.Changes != nil && k.Document != nil {
			out = append(out, "knowledge.documents")
		}
		if k.Search != nil {
			out = append(out, "knowledge.search")
		}
	}
	if e.endpoint != nil {
		out = append(out, "endpoint")
	}
	if e.oauth != nil {
		out = append(out, "oauth")
	}
	sort.Strings(out)
	return out
}

// ManifestJSON 是 /.well-known/dsh-ee-extension.json 的内容。
func (e *Extension) ManifestJSON() []byte { return e.manifestJSON("") }

// selfURL 是这次请求访问扩展用的地址：没配 EXTENSION_URL 时清单里的服务地址就用它。
func selfURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func (e *Extension) manifestJSON(fallbackURL string) []byte {
	type service struct {
		URL string `json:"url"`
	}
	out := struct {
		Manifest
		Kind         string   `json:"kind"`
		Protocol     string   `json:"protocol"`
		Service      service  `json:"service"`
		Capabilities []string `json:"capabilities"`
		Tools        []*Tool  `json:"tools,omitempty"`
	}{Manifest: e.m, Kind: "service", Protocol: ProtocolVersion, Service: service{URL: e.m.URL}, Capabilities: e.Capabilities(), Tools: e.tools}
	if out.Service.URL == "" {
		out.Service.URL = fallbackURL
	}
	if e.endpoint != nil {
		if out.Permissions == nil {
			out.Permissions = map[string]any{}
		} else {
			cp := map[string]any{}
			for k, v := range out.Permissions {
				cp[k] = v
			}
			out.Permissions = cp
		}
		out.Permissions["endpoint"] = true
	}
	raw, _ := json.MarshalIndent(out, "", "  ")
	return append(raw, '\n')
}

// Handler 是扩展的 HTTP 入口。
func (e *Extension) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/dsh-ee-extension.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(e.manifestJSON(selfURL(r)))
	})
	mux.HandleFunc("/mcp", e.handleMCP)
	mux.HandleFunc("/ee/", e.handleEE)
	return mux
}

// ListenAndServe 启动服务。addr 为空时取环境变量 EXTENSION_ADDR，再没有就是 :9000。
func (e *Extension) ListenAndServe(addr string) error {
	if addr == "" {
		addr = os.Getenv("EXTENSION_ADDR")
	}
	if addr == "" {
		addr = ":9000"
	}
	if e.keys == nil && !e.opts.Insecure {
		return errors.New("没有设置 DSH_EE_URL：扩展要用它取 dsh-ee 的公钥来验证请求（本机开发可以设 DSH_EE_INSECURE=1 跳过）")
	}
	srv := &http.Server{Addr: addr, Handler: e.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return srv.ListenAndServe()
}

// call 验证请求并取出调用上下文。失败时已经写了响应。
func (e *Extension) call(w http.ResponseWriter, r *http.Request) (*Call, bool) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	token = strings.TrimSpace(token)
	if !ok || token == "" {
		writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "请求没有带 dsh-ee 的身份令牌")
		return nil, false
	}
	var claims map[string]any
	if e.opts.Insecure {
		parts := strings.Split(token, ".")
		if len(parts) == 3 {
			if raw, err := base64.RawURLEncoding.DecodeString(parts[1]); err == nil {
				_ = json.Unmarshal(raw, &claims)
			}
		}
		if claims == nil {
			claims = map[string]any{"sub": "dev", "preferred_username": "dev", "name": "dev"}
		}
	} else {
		if e.keys == nil {
			writeError(w, http.StatusInternalServerError, CodeSetup, "扩展没有设置 DSH_EE_URL，验不了请求")
			return nil, false
		}
		var err error
		if claims, err = e.keys.verify(r.Context(), token, e.opts.Audience, e.opts.Issuer, time.Now()); err != nil {
			writeError(w, http.StatusUnauthorized, CodeUnauthenticated, "dsh-ee 的身份令牌没有通过校验："+err.Error())
			return nil, false
		}
	}
	str := func(k string) string { s, _ := claims[k].(string); return s }
	c := &Call{Token: token, Credential: r.Header.Get(HeaderCredential), Shared: r.Header.Get(HeaderShared), Config: map[string]any{}}
	c.User = User{ID: str("sub"), Username: str("preferred_username"), Name: str("name"), Email: str("email"), Dept: str("dept"), IDPSub: str("idp_sub")}
	if sys, _ := claims["sys"].(bool); sys {
		c.User.System = true
	}
	if roles, ok := claims["roles"].([]any); ok {
		for _, x := range roles {
			if s, ok := x.(string); ok {
				c.User.Roles = append(c.User.Roles, s)
			}
		}
	}
	if raw := r.Header.Get(HeaderConfig); raw != "" {
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil || json.Unmarshal(b, &c.Config) != nil {
			writeError(w, http.StatusBadRequest, CodeInvalid, HeaderConfig+" 不是 base64 的 JSON 对象")
			return nil, false
		}
	}
	return c, true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func writeErr(w http.ResponseWriter, err error) {
	var ee *Error
	if errors.As(err, &ee) {
		writeError(w, ee.status(), ee.Code, ee.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, CodeInternal, err.Error())
}

const maxBody = 8 << 20

func readBody(w http.ResponseWriter, r *http.Request, into any) bool {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, CodeInvalid, "只支持 POST")
		return false
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(raw) > maxBody {
		writeError(w, http.StatusBadRequest, CodeInvalid, "请求体读不了或太大")
		return false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	if err := json.Unmarshal(raw, into); err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalid, "请求体不是合法的 JSON："+err.Error())
		return false
	}
	return true
}

// handleEE 处理约定接口 POST /ee/<名字>。
func (e *Extension) handleEE(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/ee/")
	c, ok := e.call(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	missing := func() {
		writeError(w, http.StatusNotFound, CodeNotImplemented, fmt.Sprintf("这个扩展没有实现 %s", name))
	}
	switch name {
	case "health":
		var req struct{}
		if !readBody(w, r, &req) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "id": e.m.ID, "version": e.m.Version, "protocol": ProtocolVersion,
			"capabilities": e.Capabilities(), "uptimeSeconds": int(time.Since(e.start).Seconds()),
		})
	case "validate":
		var req struct {
			Config map[string]any `json:"config"`
		}
		if !readBody(w, r, &req) {
			return
		}
		if e.validate == nil {
			missing()
			return
		}
		if req.Config != nil {
			c.Config = req.Config
		}
		if err := e.validate(ctx, c); err != nil {
			var ee *Error
			if errors.As(err, &ee) && ee.Code != CodeInvalid {
				writeErr(w, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case "options":
		var req struct {
			Field  string         `json:"field"`
			Config map[string]any `json:"config"`
		}
		if !readBody(w, r, &req) {
			return
		}
		if e.options == nil {
			missing()
			return
		}
		if req.Config != nil {
			c.Config = req.Config
		}
		opts, err := e.options(ctx, c, req.Field)
		if err != nil {
			writeErr(w, err)
			return
		}
		if opts == nil {
			opts = []Option{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"options": opts})
	case "endpoint":
		var req EndpointRequest
		if !readBody(w, r, &req) {
			return
		}
		if e.endpoint == nil {
			missing()
			return
		}
		resp, err := e.endpoint(ctx, c, &req)
		if err != nil {
			writeErr(w, err)
			return
		}
		if resp == nil {
			resp = &EndpointResponse{Status: http.StatusOK}
		}
		if resp.Status == 0 {
			resp.Status = http.StatusOK
		}
		writeJSON(w, http.StatusOK, resp)
	default:
		switch {
		case strings.HasPrefix(name, "kb/"):
			e.handleKB(w, r, c, strings.TrimPrefix(name, "kb/"))
		case strings.HasPrefix(name, "oauth/"):
			e.handleOAuth(w, r, c, strings.TrimPrefix(name, "oauth/"))
		default:
			writeError(w, http.StatusNotFound, CodeNotImplemented, "没有这个约定接口："+name)
		}
	}
}
