package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// dsh-ee doctor：只读诊断「发布一个包到 dsh-ee」这条链路。
// 不写文件、不改服务端状态；唯一的 POST 是向预检接口发一个空包探权限，
// 预检本身不落库（package_check.go），所以也算只读。

const doctorUsage = `用法：dsh-ee doctor [--server 地址] [--token 令牌] [--json]

只读诊断发布链路，不改任何配置：
  服务地址与令牌从哪读到（参数、环境变量、.npmrc、$DSH_HOME/dsh-ee/token）
  服务连通与版本（GET /healthz）
  令牌是否有效、能不能调发布预检（需要 registry:publish 或 agent 范围）
  .npmrc 的企业源与令牌是否和当前登录一致
  企业 npm 源是否可达
  本机 Node 与 dsh 的版本

连接参数与 dsh-ee package check 相同；--json 输出机器可读的结果。
退出码：0 没有失败项（可以有提醒），1 有失败项，2 用法错误。
`

type doctorCheck struct {
	ID     string `json:"id"`
	Status string `json:"status"` // ok / warn / fail
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type doctorReport struct {
	Summary string        `json:"summary"`
	OK      bool          `json:"ok"`
	Checks  []doctorCheck `json:"checks"`
}

// doctorCommand 跑本机的 node / dsh 取版本；测试里替换成假命令。
var doctorCommand = func(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out)), err
}

const doctorTimeout = 10 * time.Second

func cliDoctor(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && (args[0] == "help" || args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, doctorUsage)
		return exitOK
	}
	o, err := parseOpts(args, "server", "token")
	if err != nil {
		fmt.Fprintln(stderr, err)
		fmt.Fprintln(stderr, "用法：dsh-ee doctor [--server 地址] [--token 令牌] [--json]")
		return exitUsage
	}
	if len(o.pos) > 0 {
		fmt.Fprintln(stderr, "用法：dsh-ee doctor [--server 地址] [--token 令牌] [--json]")
		return exitUsage
	}
	rep := collectDoctor(resolveEndpoint(o))
	if o.asJSON {
		printJSON(stdout, rep)
	} else {
		printDoctor(stdout, rep)
	}
	if !rep.OK {
		return exitProblems
	}
	return exitOK
}

func collectDoctor(ep cliEndpoint) *doctorReport {
	checks := []doctorCheck{}
	add := func(id, status, title, detail, fix string) {
		checks = append(checks, doctorCheck{ID: id, Status: status, Title: title, Detail: detail, Fix: fix})
	}
	npm := readNpmrc(npmrcPath())

	// 1. 服务地址
	baseOK := false
	switch {
	case ep.base == "":
		add("endpoint", "fail", "dsh-ee 地址", "没有找到服务地址",
			"加 --server https://… 或设置 DSH_EE_URL；也可以运行一次 setup.sh，让 .npmrc 指向企业源")
	case !validBaseURL(ep.base):
		add("endpoint", "fail", "dsh-ee 地址", fmt.Sprintf("%q 不是 http(s) 地址", ep.base),
			"改成浏览器能打开的地址，例如 https://ee.acme.com")
	default:
		baseOK = true
		add("endpoint", "ok", "dsh-ee 地址", ep.base+"（来自 "+ep.baseSource+"）", "")
	}

	if baseOK {
		c := &eeClient{base: ep.base, token: ep.token, hc: &http.Client{Timeout: doctorTimeout}}

		// 2. 服务连通与版本
		serverOK := false
		status, raw, err := c.request("GET", "/healthz", "", nil)
		switch {
		case err != nil:
			add("server", "fail", "服务连通", "连不上 "+ep.base+"："+err.Error(),
				"检查地址、网络或 VPN；用浏览器打开 "+ep.base+"/healthz 确认服务在跑")
		case status == http.StatusOK:
			serverOK = true
			var health struct {
				Version string `json:"version"`
			}
			_ = json.Unmarshal(raw, &health)
			v := health.Version
			if v == "" {
				v = "未知版本"
			}
			add("server", "ok", "服务连通", "版本 "+v, "")
		default:
			add("server", "fail", "服务连通", fmt.Sprintf("HTTP %d：%s", status, serverErrText(raw)),
				"确认这个地址是 dsh-ee 服务端；反向代理 / 证书有问题时看服务端日志")
		}

		// 3. 令牌：用 npm 源的 whoami 验有效性（不要求 hub 范围），能读 /api/dsh/whoami 时补充范围和到期时间
		tokenOK := false
		if ep.token == "" {
			add("token", "fail", "令牌", "没有找到令牌",
				fmt.Sprintf("在 %s/tokens 建一个带 agent 或 registry:publish 范围的令牌，用 --token 或 DSH_EE_TOKEN 传进来（也可以运行 setup.sh）", ep.base))
		} else {
			status, raw, err = c.request("GET", "/npm/-/whoami", "", nil)
			switch {
			case err != nil:
				add("token", "fail", "令牌", "读不到令牌身份（连不上 npm 源）："+err.Error(),
					"检查网络或 VPN 后重试；持续失败看服务端是否在维护")
			case status == http.StatusOK:
				tokenOK = true
				var who struct {
					Username string `json:"username"`
				}
				_ = json.Unmarshal(raw, &who)
				detail := "有效，用户名 " + who.Username + "（来自 " + ep.tokenSource + "）"
				state, fix := "ok", ""
				if scopes, expires, ok := dshTokenInfo(c); ok {
					if len(scopes) > 0 {
						detail += "；范围：" + strings.Join(scopes, "、")
					}
					if left, ok := daysLeft(expires); ok && left < 7 {
						state = "warn"
						fix = "令牌快到期了；到期前重新登录，或在「我的令牌」里换一个新的"
						detail += fmt.Sprintf("；%d 天内到期", left)
					}
				}
				add("token", state, "令牌", detail, fix)
			case status == http.StatusUnauthorized:
				add("token", "fail", "令牌", "无效、已过期或已吊销",
					fmt.Sprintf("在 %s/tokens 重新建一个带 agent 或 registry:publish 范围的令牌，再用 --token 或 DSH_EE_TOKEN 传进来", ep.base))
			default:
				add("token", "warn", "令牌", fmt.Sprintf("读不到令牌身份（HTTP %d）：%s", status, serverErrText(raw)),
					"稍后重试；也可以用 --server/--token 再跑一次 doctor 排除本机配置问题")
			}
		}

		// 4. 发布预检权限：空包探一下，400=有权限（空包被拒），403=范围不够，401=令牌无效
		if tokenOK && serverOK {
			status, raw, err = c.request("POST", "/api/me/packages/check", "application/octet-stream", nil)
			switch {
			case err != nil:
				add("publish-scope", "warn", "发布权限", "探不到预检接口："+err.Error(), "稍后重试")
			case status == http.StatusBadRequest:
				add("publish-scope", "ok", "发布权限", "令牌可以调用发布预检（registry.publish 或 agent 范围）", "")
			case status == http.StatusForbidden:
				add("publish-scope", "fail", "发布权限", "令牌没有 registry:publish 或 agent 范围",
					fmt.Sprintf("在 %s/tokens 建一个带 agent 或 registry:publish 范围的令牌，或让管理员确认账号有「发布能力」权限", ep.base))
			case status == http.StatusUnauthorized:
				add("publish-scope", "fail", "发布权限", "令牌无效", "重新建一个令牌后再跑一次 doctor")
			default:
				add("publish-scope", "warn", "发布权限", fmt.Sprintf("预检接口返回 HTTP %d：%s", status, serverErrText(raw)), "服务端异常；看服务端日志")
			}
		}

		// 5. 企业 npm 源
		status, raw, err = c.request("GET", "/npm/-/ping", "", nil)
		switch {
		case err != nil:
			add("npm-source", "fail", "企业 npm 源", "连不上 "+registryURL(ep.base)+"："+err.Error(),
				"检查地址或反向代理；npm publish / 装企业插件会走这个源")
		case status == http.StatusOK:
			add("npm-source", "ok", "企业 npm 源", registryURL(ep.base)+" 可达", "")
		default:
			add("npm-source", "fail", "企业 npm 源", fmt.Sprintf("HTTP %d：%s", status, serverErrText(raw)),
				"确认服务端 npm 源已启用、数据目录可写；看服务端日志")
		}
	}

	// 6. .npmrc：企业源地址和令牌是否与当前登录一致。只读，不打印令牌。
	registries := []string{}
	if ep.base != "" {
		registries = append(registries, registryURL(ep.base))
	}
	npmDetail, npmState, npmFix := doctorNpmrc(npm, ep, registries)
	add("npmrc", npmState, "npmrc", npmDetail, npmFix)

	// 7. Node
	if out, err := doctorCommand("node", "--version"); err != nil {
		add("node", "warn", "Node", "没有找到 node 命令",
			"装 Node 22+（npm pack 与构建插件都要它；上游 dsh-primer 脚手架要求 Node ≥24.9）")
	} else {
		major := versionMajor(out)
		switch {
		case major < 0:
			add("node", "warn", "Node", "版本读不懂："+out, "升级到 Node 22+")
		case major < 22:
			add("node", "warn", "Node", out+" 太旧",
				"升级到 Node 22+；上游 dsh-primer 脚手架要求 Node ≥24.9")
		case major < 24:
			add("node", "ok", "Node", out+"（dsh-ee 预检可用；用上游 dsh-primer 脚手架需要 ≥24.9）", "")
		default:
			add("node", "ok", "Node", out, "")
		}
	}

	// 8. dsh
	if out, err := doctorCommand("dsh", "--version"); err != nil {
		add("dsh", "warn", "DSH", "没有找到 dsh 命令",
			"只发布 npm 包不强制；要在本机安装、验证企业插件时装 DSH")
	} else if line := firstLine(out); line != "" {
		add("dsh", "ok", "DSH", line, "")
	} else {
		add("dsh", "warn", "DSH", "dsh --version 没有输出", "确认 DSH 安装完整")
	}

	fails, warns := 0, 0
	for _, c := range checks {
		switch c.Status {
		case "fail":
			fails++
		case "warn":
			warns++
		}
	}
	rep := &doctorReport{OK: fails == 0, Checks: checks}
	switch {
	case fails > 0:
		rep.Summary = fmt.Sprintf("%d 项失败，%d 项需要注意", fails, warns)
	case warns > 0:
		rep.Summary = fmt.Sprintf("%d 项需要注意", warns)
	default:
		rep.Summary = "全部正常"
	}
	return rep
}

func validBaseURL(base string) bool {
	u, err := url.Parse(base)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// dshTokenInfo 是尽力而为：/api/dsh/whoami 要 hub 范围，只有 registry 范围的令牌读不到，
// 这时返回 ok=false，不影响 doctor 结论。
func dshTokenInfo(c *eeClient) (scopes []string, expires string, ok bool) {
	status, raw, err := c.request("GET", "/api/dsh/whoami", "", nil)
	if err != nil || status != http.StatusOK {
		return nil, "", false
	}
	var me struct {
		Scopes    []string `json:"scopes"`
		ExpiresAt string   `json:"expires_at"`
	}
	if json.Unmarshal(raw, &me) != nil {
		return nil, "", false
	}
	sort.Strings(me.Scopes)
	return me.Scopes, me.ExpiresAt, true
}

func doctorNpmrc(npm *npmrcFile, ep cliEndpoint, registries []string) (detail, state, fix string) {
	if !npm.exists {
		return "没有找到 " + npm.path + "（企业源装插件和 npm publish 会用到）", "warn",
			"运行一次 setup.sh 会为 DSH profile 写好企业源；需要全局 npm publish 时把 registry 与 _authToken 写到 " + npm.path
	}
	if len(registries) == 0 {
		return npm.path + "：不知道 dsh-ee 地址，没法核对企业源", "warn", "先配置 dsh-ee 地址再跑一次 doctor"
	}
	want := registries[0]
	found := false
	for _, v := range npm.registries {
		if strings.TrimRight(strings.TrimSpace(v), "/") == strings.TrimRight(want, "/") {
			found = true
			break
		}
	}
	if !found {
		return npm.path + " 里没有指向 " + want + " 的企业源", "warn",
			"运行 setup.sh 自动同步；或手动加一行 registry=" + want + "（以及各 <作用域>:registry=" + want + "）"
	}
	key := authTokenKey(want)
	token, ok := npm.authTokens[key]
	if key == "" || !ok || strings.TrimSpace(token) == "" {
		return npm.path + " 里有企业源，但缺少 " + key, "warn",
			"运行 setup.sh 会自动写这一行；或者重新登录让接入插件同步"
	}
	if ep.token != "" && strings.TrimSpace(token) != strings.TrimSpace(ep.token) {
		return npm.path + " 的企业源令牌与当前令牌不一致（npm publish 会 401）", "warn",
			"重新登录或运行 setup.sh 同步令牌（只改 _authToken 那一行）"
	}
	return npm.path + "：企业源与令牌一致", "ok", ""
}

func printDoctor(w io.Writer, rep *doctorReport) {
	fmt.Fprintln(w, "dsh-ee doctor（只读诊断，不修改任何东西）")
	fmt.Fprintln(w)
	for _, c := range rep.Checks {
		fmt.Fprintf(w, "%s %s：%s\n", doctorMark(c.Status), c.Title, c.Detail)
	}
	fmt.Fprintf(w, "\n结论：%s\n", rep.Summary)
	var next []string
	seen := map[string]bool{}
	for _, c := range rep.Checks {
		if c.Status == "ok" || strings.TrimSpace(c.Fix) == "" || seen[c.Fix] {
			continue
		}
		seen[c.Fix] = true
		next = append(next, c.Fix)
	}
	if len(next) > 0 {
		fmt.Fprintln(w, "下一步：")
		for _, s := range next {
			fmt.Fprintln(w, "  · "+s)
		}
	}
}

func doctorMark(status string) string {
	switch status {
	case "ok":
		return "✓"
	case "warn":
		return "!"
	default:
		return "✗"
	}
}

// versionMajor 从 "v24.22.1" / "24.22.1" 这类输出里取主版本号；读不懂返回 -1。
func versionMajor(v string) int {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	first := strings.SplitN(v, ".", 2)[0]
	n, err := strconv.Atoi(first)
	if err != nil {
		return -1
	}
	return n
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// daysLeft 把 RFC3339 到期时间换算成剩余天数；没有到期时间返回 ok=false。
func daysLeft(expires string) (int, bool) {
	if strings.TrimSpace(expires) == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339, expires)
	if err != nil {
		return 0, false
	}
	left := int(time.Until(t).Hours() / 24)
	if left < 0 {
		left = 0
	}
	return left, true
}
