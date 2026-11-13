package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// dsh-ee package check：把本地 npm 包交给服务端预检（POST /api/me/packages/check）。
// 本机只读文件、不做检查；解包、自动检查和发布冲突判断都在服务端，与网页「发布」页同一份代码。

const packageUsage = `用法：dsh-ee package check <文件.tgz> [--server 地址] [--token 令牌] [--json]

把 npm pack 生成的 .tgz 交给 dsh-ee 做发布前预检：自动检查、发布冲突都看一遍，
不落库、不占版本号。文件要先在本机 pack 好，命令只上传和展示结果。

连接参数（也可以用环境变量）：
  --server 地址   或 DSH_EE_URL     dsh-ee 的地址，例如 https://ee.acme.com
  --token 令牌    或 DSH_EE_TOKEN   带 registry:publish（或 agent）范围的令牌

没给地址或令牌时，会再读 .npmrc（NPM_CONFIG_USERCONFIG）与 $DSH_HOME/dsh-ee/token，
setup.sh 写过其中任意一处就不用重复配置。

退出码：
  0  预检通过：没有发布冲突，也没有自动检查失败项
  1  预检发现阻塞问题：包不被接受、有发布冲突或自动检查失败项
  2  用法或本地配置不对：参数、文件、服务地址、令牌
  3  调用服务失败：网络错误、令牌无效或没有权限、服务端错误
`

const (
	exitOK       = 0
	exitProblems = 1
	exitUsage    = 2
	exitService  = 3
)

func cliPackage(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, packageUsage)
		return exitUsage
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, packageUsage)
		return exitOK
	}
	switch args[0] {
	case "check":
		return cliPackageCheck(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "不认识的命令 package %s\n\n", args[0])
	fmt.Fprint(stderr, packageUsage)
	return exitUsage
}

// packageCheck 与 server/internal/api.packageCheck 的 JSON 字段一一对应；这里只声明要展示的部分。
type packageCheck struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	IsPlugin       bool   `json:"is_plugin"`
	CanSubmit      bool   `json:"can_submit"`
	WillPublish    bool   `json:"will_publish"`
	RequiresReview bool   `json:"requires_review"`
	Scan           []struct {
		Key    string         `json:"key"`
		Level  string         `json:"level"`
		Params map[string]any `json:"params,omitempty"`
	} `json:"scan"`
	Conflicts []struct {
		Key    string         `json:"key"`
		Params map[string]any `json:"params,omitempty"`
	} `json:"conflicts"`
}

func cliPackageCheck(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, packageUsage)
		return exitOK
	}
	o, err := parseOpts(args, "server", "token")
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, "用法：dsh-ee package check <文件.tgz> [--json]")
		return exitUsage
	}
	if len(o.pos) != 1 {
		fmt.Fprintln(stderr, "用法：dsh-ee package check <文件.tgz> [--json]")
		return exitUsage
	}
	path := o.pos[0]
	if st, err := os.Stat(path); err != nil {
		fmt.Fprintf(stderr, "读不了 %s：%v\n", path, err)
		return exitUsage
	} else if st.IsDir() {
		fmt.Fprintf(stderr, "%s 是一个目录；这里要 npm pack 生成的 .tgz 文件\n", path)
		return exitUsage
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(stderr, "读不了 %s：%v\n", path, err)
		return exitUsage
	}
	if len(raw) == 0 {
		fmt.Fprintf(stderr, "%s 是空文件\n", path)
		return exitUsage
	}

	ep := resolveEndpoint(o)
	c, err := ep.client("registry:publish（或 agent）范围", 5*time.Minute)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return exitUsage
	}
	status, body, err := c.request("POST", "/api/me/packages/check", "application/octet-stream", raw)
	if err != nil {
		fmt.Fprintf(stderr, "连不上 %s：%v\n", c.base, err)
		fmt.Fprintln(stderr, "检查地址、网络或 VPN；也可以先用 dsh-ee doctor 看接入状态")
		return exitService
	}
	switch {
	case status == http.StatusOK:
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		fmt.Fprintf(stderr, "预检接口拒绝了这个令牌（HTTP %d）：%s\n", status, serverErrText(body))
		fmt.Fprintf(stderr, "令牌要在 dsh-ee 的「我的令牌」里建，范围至少包含 registry:publish 或 agent：%s/tokens\n", c.base)
		return exitService
	case status >= 400 && status < 500:
		fmt.Fprintf(stderr, "服务端不接受这个包（HTTP %d）：%s\n", status, serverErrText(body))
		return exitProblems
	default:
		fmt.Fprintf(stderr, "调用预检接口失败（HTTP %d）：%s\n", status, serverErrText(body))
		return exitService
	}

	var res packageCheck
	// 用 json.Number 保留数字的原始写法：scan 里 size 之类的参数直接展示，不能变成科学计数法。
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&res); err != nil {
		fmt.Fprintf(stderr, "服务端返回了看不懂的结果：%v\n", err)
		return exitService
	}
	if o.asJSON {
		printRawJSON(stdout, body)
	} else {
		printPackageCheck(stdout, &res)
	}
	if !res.CanSubmit || hasScanFail(&res) {
		return exitProblems
	}
	return exitOK
}

func hasScanFail(res *packageCheck) bool {
	for _, item := range res.Scan {
		if item.Level == "fail" {
			return true
		}
	}
	return false
}

var scanLevelLabel = map[string]string{"pass": "通过", "warn": "提醒", "fail": "失败"}

func printPackageCheck(w io.Writer, res *packageCheck) {
	kind := "普通 npm 包"
	if res.IsPlugin {
		kind = "DSH 插件"
	}
	fmt.Fprintf(w, "预检 %s@%s（%s）\n", res.Name, res.Version, kind)

	pass, warn, fail := 0, 0, 0
	for _, item := range res.Scan {
		switch item.Level {
		case "pass":
			pass++
		case "warn":
			warn++
		case "fail":
			fail++
		}
	}
	fmt.Fprintf(w, "自动检查：%d 项通过", pass)
	if warn > 0 {
		fmt.Fprintf(w, "，%d 项提醒", warn)
	}
	if fail > 0 {
		fmt.Fprintf(w, "，%d 项失败", fail)
	}
	fmt.Fprintln(w)
	for _, item := range res.Scan {
		if item.Level == "pass" {
			continue
		}
		label := scanLevelLabel[item.Level]
		if label == "" {
			label = item.Level
		}
		fmt.Fprintf(w, "  · %s  %s\n", label, scanText(item.Level, item.Key, item.Params))
	}

	if len(res.Conflicts) > 0 {
		fmt.Fprintln(w, "\n发布冲突：")
		for _, c := range res.Conflicts {
			fmt.Fprintf(w, "  · %s\n", conflictText(c.Key, c.Params))
		}
	}

	fmt.Fprintln(w)
	switch {
	case !res.CanSubmit:
		fmt.Fprintln(w, "结论：不能提交，服务端会直接拒绝；先解决上面的发布冲突。")
	case fail > 0:
		fmt.Fprintln(w, "结论：不能自动上架；提交后会进入人工审核，审核员会看这些失败项。")
	case res.WillPublish:
		fmt.Fprintln(w, "结论：可以直接上架（管理员开了开放模式，且没有阻塞项）。")
	case res.RequiresReview:
		fmt.Fprintln(w, "结论：可以提交，会进入人工审核。")
	default:
		fmt.Fprintln(w, "结论：可以提交。")
	}
}

// scanText 把服务端 scan 项的 key/level/params 翻成一句人话。
// 文案与网页 i18n 是两份（CLI 不能读前端资源），但 key 和语义以服务端为准。
func scanText(level, key string, params map[string]any) string {
	str := func(k string) string {
		if v, ok := params[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	path := func() string {
		if p := str("path"); p != "" {
			return p + "："
		}
		return ""
	}
	switch key {
	case "bundle":
		if level == "warn" {
			return "没有声明 dsh.bundle：这是普通 npm 包，不会出现在能力市场和 DSH 索引里"
		}
		switch str("reason") {
		case "no-patch":
			return "声明了 dsh.bundle，但缺少 patch 字段"
		case "unsafe":
			return "dsh.bundle.patch 不是安全的相对路径：" + str("path")
		case "file-missing":
			return "dsh.bundle.patch 指向的 " + str("path") + " 不在包里（是不是没有 build 或 files 漏了？）"
		}
		return "组合包声明有问题（" + str("reason") + "）"
	case "patch":
		detail := patchReason(str("reason"), params)
		return path() + detail
	case "skills":
		return fmt.Sprintf("包内含 %s 个技能：%s。它们由插件运行时注入，不会出现在能力中心的技能 Tab，也不能按技能单独授权或下架。", str("count"), str("files"))
	case "install-scripts":
		return "安装时会执行脚本：" + str("scripts") + "；确认它不在安装时下载或执行外部代码"
	case "secrets":
		return fmt.Sprintf("发现 %s 处疑似密钥或敏感文件：%s", str("count"), str("files"))
	case "license":
		return "没有声明 license"
	case "engines":
		return "没有声明 engines.dsh，DSH 会按 peer 依赖判断兼容性"
	case "cordis":
		return "包把 " + str("packages") + " 放进了 dependencies：DSH 已提供 Cordis，双份会导致服务注入和生命周期错乱；请移到 peerDependencies（仅构建用可放 devDependencies）"
	case "cordis-peer":
		return "peerDependencies 的 Cordis 范围 " + str("range") + " 不包含当前 DSH 使用的 " + str("want") + "；如果还要兼容旧宿主，请确认范围"
	case "size":
		return fmt.Sprintf("包 %s（解包 %s，%s 个文件），偏大", humanBytes(str("size")), humanBytes(str("unpacked")), str("files"))
	}
	return key + "（" + paramsText(params) + "）"
}

func patchReason(reason string, params map[string]any) string {
	str := func(k string) string {
		if v, ok := params[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	row, insert := str("row"), str("insert")
	switch reason {
	case "yaml":
		return "不是合法 YAML（" + str("error") + "）"
	case "not-array":
		return "顶层必须是数组"
	case "empty":
		return "没有任何 patch 行"
	case "row-not-object":
		if insert != "" {
			return "第 " + row + " 行 insert 第 " + insert + " 项不是对象"
		}
		return "第 " + row + " 行不是对象"
	case "insert-not-array":
		return "第 " + row + " 行的 insert 必须是数组"
	case "missing-id":
		if insert != "" {
			return "第 " + row + " 行 insert 第 " + insert + " 项缺少 row id"
		}
		return "第 " + row + " 行缺少 row id"
	case "missing-name":
		return "第 " + row + " 行 insert 第 " + insert + " 项缺少 name"
	case "duplicate-id":
		return "row id " + str("id") + " 重复（第 " + row + " 行与第 " + str("first") + " 行）"
	case "name-mismatch":
		return "没有任何一行引用本包名 " + str("name") + "；DSH 会找不到插件"
	case "config-not-map":
		return str("name") + " 的 config 不是 mapping"
	case "unreadable":
		return "patch 文件太大或未能读取"
	}
	return reason
}

func conflictText(key string, params map[string]any) string {
	str := func(k string) string {
		if v, ok := params[k]; ok && v != nil {
			return fmt.Sprint(v)
		}
		return ""
	}
	switch key {
	case "name":
		return "包名 " + str("name") + " 不是合法的 npm 包名"
	case "scope":
		return "包名不在企业作用域内；这里能发布的包以企业作用域开头（允许：" + str("scope") + "）"
	case "version":
		return "版本号 " + str("version") + " 不合法（要像 1.0.0 这样）"
	case "upstream":
		return str("name") + " 是从上游预载的包，不能发布同名包"
	case "owner":
		return str("name") + " 属于别人，不能发布它的新版本"
	case "exists":
		return str("name") + "@" + str("version") + " 已经存在：同一个版本号不能重复发布（被驳回或下架过的也不行）"
	}
	return key + "（" + paramsText(params) + "）"
}

func paramsText(params map[string]any) string {
	if len(params) == 0 {
		return "无参数"
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%v", k, params[k]))
	}
	return strings.Join(parts, "，")
}

func humanBytes(s string) string {
	if s == "" || s == "0" {
		return "0 B"
	}
	var n int64
	if _, err := fmt.Sscan(s, &n); err != nil {
		return s
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}

// ---- 连接信息：地址与令牌 ----

// cliEndpoint 是 package check 和 doctor 共用的服务地址与令牌。
// 记下来源，doctor 才能说清楚「这个地址是从哪读到的」。
type cliEndpoint struct {
	base, token             string
	baseSource, tokenSource string
}

// client 校验地址与令牌后建一个带认证的客户端；tokenHint 用于缺令牌时提示需要什么范围。
func (ep cliEndpoint) client(tokenHint string, timeout time.Duration) (*eeClient, error) {
	if ep.base == "" {
		return nil, errors.New("不知道 dsh-ee 的地址：加 --server https://…、设置 DSH_EE_URL，或先运行一次 setup.sh 写好 .npmrc")
	}
	if u, err := url.Parse(ep.base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("dsh-ee 的地址 %q 不对，要像 https://ee.acme.com 这样", ep.base)
	}
	if ep.token == "" {
		return nil, fmt.Errorf("没有令牌：用 --token 或 DSH_EE_TOKEN 传一个带 %s 的令牌（网页「我的令牌」：%s/tokens）", tokenHint, ep.base)
	}
	return &eeClient{base: ep.base, token: ep.token, hc: &http.Client{Timeout: timeout}}, nil
}

// resolveEndpoint 按「参数 → 环境变量 → 本机配置」找 dsh-ee 的地址与令牌：
// 地址还看 .npmrc 里的企业源，令牌还看 $DSH_HOME/dsh-ee/token 与 .npmrc 的 _authToken。
// 找不到不报错，由调用方决定算用法错误（package check）还是诊断结果（doctor）。
func resolveEndpoint(o *cliOpts) cliEndpoint {
	ep := cliEndpoint{}
	switch {
	case o.vals["server"] != "":
		ep.base, ep.baseSource = o.vals["server"], "参数 --server"
	case os.Getenv("DSH_EE_URL") != "":
		ep.base, ep.baseSource = os.Getenv("DSH_EE_URL"), "环境变量 DSH_EE_URL"
	}
	switch {
	case o.vals["token"] != "":
		ep.token, ep.tokenSource = o.vals["token"], "参数 --token"
	case os.Getenv("DSH_EE_TOKEN") != "":
		ep.token, ep.tokenSource = os.Getenv("DSH_EE_TOKEN"), "环境变量 DSH_EE_TOKEN"
	}

	npm := readNpmrc(npmrcPath())
	if ep.base == "" {
		if v := npm.server(); v != "" {
			ep.base, ep.baseSource = v, "npmrc "+npm.path
		}
	}
	if ep.token == "" {
		if v, path := readDSHToken(); v != "" {
			ep.token, ep.tokenSource = v, "文件 "+path
		} else if v := npm.anyAuthToken(); v != "" {
			ep.token, ep.tokenSource = v, "npmrc "+npm.path
		}
	}
	ep.base = strings.TrimRight(strings.TrimSpace(ep.base), "/")
	return ep
}

// request 发一次请求，把 HTTP 状态码和响应体原样交回；只有网络层失败才返回错误。
// doctor 要根据 400/401/403 区分「有权限」「令牌不对」，不能像 do 一样把非 2xx 都变成错误。
func (c *eeClient) request(method, path, contentType string, body []byte) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.base+path, rd)
	if err != nil {
		return 0, nil, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("X-DshEE-Client", "dsh-ee-cli/"+version)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, raw, nil
}

func serverErrText(raw []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && strings.TrimSpace(e.Error) != "" {
		return strings.TrimSpace(e.Error)
	}
	return strings.TrimSpace(string(raw))
}

// ---- 本机配置：.npmrc 与 $DSH_HOME/dsh-ee/token ----

// npmrcFile 是 .npmrc 里与本命令有关的部分：企业源地址与对应令牌。
// 只解析、不写文件；doctor 只读诊断，package check 也不会改用户的配置。
type npmrcFile struct {
	path       string
	exists     bool
	registries map[string]string // "@scope" / ""（默认 registry）→ 地址
	authTokens map[string]string // "//host/npm/:_authToken" → 令牌
}

func npmrcPath() string {
	if p := strings.TrimSpace(os.Getenv("NPM_CONFIG_USERCONFIG")); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".npmrc"
	}
	return filepath.Join(home, ".npmrc")
}

func dshHomeDir() string {
	if d := strings.TrimSpace(os.Getenv("DSH_HOME")); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".dsh")
}

// readDSHToken 读 setup / 接入插件在凭据库不可用时留下的本地令牌文件。
func readDSHToken() (string, string) {
	home := dshHomeDir()
	if home == "" {
		return "", ""
	}
	path := filepath.Join(home, "dsh-ee", "token")
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	return strings.TrimSpace(string(raw)), path
}

func readNpmrc(path string) *npmrcFile {
	f := &npmrcFile{path: path, registries: map[string]string{}, authTokens: map[string]string{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	f.exists = true
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch {
		case strings.HasSuffix(key, ":registry"):
			f.registries[strings.TrimSuffix(key, ":registry")] = value
		case key == "registry":
			f.registries[""] = value
		case strings.HasSuffix(key, ":_authToken"):
			f.authTokens[key] = value
		}
	}
	return f
}

// server 从 .npmrc 的企业源里取出 dsh-ee 地址。setup.sh 写的是 <地址>/npm/。
func (f *npmrcFile) server() string {
	keys := make([]string, 0, len(f.registries))
	for k := range f.registries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := strings.TrimRight(strings.TrimSpace(f.registries[k]), "/")
		if base, ok := strings.CutSuffix(v, "/npm"); ok && base != "" {
			return base
		}
	}
	return ""
}

// anyAuthToken 返回 .npmrc 里第一条 dsh-ee 企业源的令牌（order 稳定：按 registry 键排序）。
func (f *npmrcFile) anyAuthToken() string {
	keys := make([]string, 0, len(f.authTokens))
	for k := range f.authTokens {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.Contains(k, "/npm") && strings.TrimSpace(f.authTokens[k]) != "" {
			return strings.TrimSpace(f.authTokens[k])
		}
	}
	return ""
}

// registryURL 返回与 base 对应的企业源地址，例如 https://ee.acme.com/npm/。
func registryURL(base string) string { return strings.TrimRight(base, "/") + "/npm/" }

// authTokenKey 与 setup.sh / npmrcEntries 的写法一致：//host/npm/:_authToken。
func authTokenKey(registry string) string {
	u, err := url.Parse(registry)
	if err != nil || u.Host == "" {
		return ""
	}
	return "//" + u.Host + u.Path + ":_authToken"
}
