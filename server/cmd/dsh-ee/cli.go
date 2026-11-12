package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/extension"
)

// 不启动服务、不连数据库的子命令：写扩展时用的检查和规格（docs/plugins.md §10、docs/agent-dev.md §3～§4）。

const cliUsage = `用法：
  dsh-ee                                 启动服务
  dsh-ee version                         打印版本
  dsh-ee extension init <目录> --id 作者/名字 [--kind declarative|service]
                                         新建一个扩展（带 AGENTS.md），复制后照着改
  dsh-ee extension check <目录> [--json]  检查一个扩展：清单、接口文档、只读标记、凭据写法
  dsh-ee extension test <目录|地址> [--run "启动命令"] [--credential 值] [--config JSON] [--write] [--json]
                                         把服务扩展跑起来按协议逐项测；全部通过才算合格
  dsh-ee extension dev [--listen 地址]    起一个模拟的 dsh-ee（不要数据库），写扩展时用
  dsh-ee extension call <地址> <工具> [--as 员工] [--args JSON] [--credential 值] [--config JSON]
                                         以某位员工的身份调一次扩展的工具
  dsh-ee extension pack <目录> [-o 文件]   把声明式扩展打成 zip，算出校验和
  dsh-ee extension report <目录|地址>      生成给管理员看的交付说明（Markdown）
  dsh-ee config export|plan|test|apply|explain|simulate …
                                         把权限配置（角色、字段、访问规则）当文件来改，见 dsh-ee config help
  dsh-ee package check <文件.tgz> [--json]
                                         把 npm 包交给服务端做发布前预检，不发布、不占版本号
  dsh-ee doctor [--json]                 只读诊断发布链路：服务连通、令牌范围、npmrc、Node/dsh 版本
  dsh-ee admin recover                   在服务器上生成一个 1 小时有效的恢复账号（把自己锁在外面时用）
  dsh-ee schema list [--json]            列出规格
  dsh-ee schema get <名字>                打印一份规格（manifest：清单；errors：检查项）

extension check 有错误时退出码为 1。加 --json 输出给程序和 Agent 看的结果：
每一条都有 code、severity、where（文件和位置）、message、fix（具体改法）。
package check 和 doctor 的地址/令牌也可以用 DSH_EE_URL、DSH_EE_TOKEN，或读 .npmrc 与 $DSH_HOME/dsh-ee/token。
`

// runCLI 处理子命令。不是子命令时 handled 为 false，由调用方启动服务。
func runCLI(args []string, stdout, stderr io.Writer) (handled bool, code int) {
	if len(args) == 0 {
		return false, 0
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, cliUsage)
		return true, 0
	case "extension":
		return true, cliExtension(args[1:], stdout, stderr)
	case "schema":
		return true, cliSchema(args[1:], stdout, stderr)
	case "config":
		return true, cliConfig(args[1:], stdout, stderr)
	case "package":
		return true, cliPackage(args[1:], stdout, stderr)
	case "doctor":
		return true, cliDoctor(args[1:], stdout, stderr)
	case "admin":
		return true, cliAdmin(args[1:], stdout, stderr)
	}
	return false, 0
}

// flags 把参数分成普通参数和 --json 开关；不认识的开关返回错误。
func flags(args []string) (rest []string, asJSON bool, err error) {
	for _, a := range args {
		switch {
		case a == "--json":
			asJSON = true
		case strings.HasPrefix(a, "-"):
			return nil, false, fmt.Errorf("不认识的参数 %s", a)
		default:
			rest = append(rest, a)
		}
	}
	return rest, asJSON, nil
}

func cliExtension(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if cmd, ok := extCommands[args[0]]; ok {
			return cmd(args[1:], stdout, stderr)
		}
	}
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprint(stderr, cliUsage)
		return 2
	}
	if slices.ContainsFunc(args[1:], func(a string) bool { return a == "-h" || a == "--help" || a == "help" }) {
		fmt.Fprint(stdout, cliUsage)
		return 0
	}
	rest, asJSON, err := flags(args[1:])
	if err != nil || len(rest) != 1 {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		fmt.Fprintln(stderr, "用法：dsh-ee extension check <目录> [--json]")
		return 2
	}
	dir := rest[0]
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		fmt.Fprintf(stderr, "%s 不是一个目录\n", dir)
		return 2
	}
	res := extension.Check(os.DirFS(dir))
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		_ = enc.Encode(res)
	} else {
		printCheck(stdout, res)
	}
	if !res.OK {
		return 1
	}
	return 0
}

var severityLabel = map[string]string{extension.SevError: "错误", extension.SevWarning: "提醒", extension.SevInfo: "说明"}

func printCheck(w io.Writer, res *extension.Result) {
	if res.Identity != "" {
		fmt.Fprintf(w, "%s（%s）\n", res.Identity, res.Kind)
	}
	if len(res.Servers) > 0 {
		fmt.Fprintf(w, "请求只会发往：%s\n", strings.Join(res.Servers, "、"))
	}
	if cr := res.Credential; cr != nil {
		line := map[string]string{
			extension.CredShared:     "管理员配的一份共享凭据",
			extension.CredUserSecret: "员工自己填的凭据，放在 " + cr.Header,
			extension.CredEEJWT:      "dsh-ee 为当前员工现签的身份令牌（ee_jwt），放在 " + cr.Header,
			extension.CredOAuth:      "oauth",
		}[cr.Mode]
		if cr.Mode == extension.CredEEJWT {
			if cr.Audience != "" {
				line += "，aud 是 " + cr.Audience
			}
			if len(cr.Claims) > 0 {
				line += "，额外带 " + strings.Join(cr.Claims, "、")
			}
		}
		if cr.SharedHeader != "" {
			line += "；另带一份共享凭据，放在 " + cr.SharedHeader
		}
		fmt.Fprintf(w, "调用时的身份：%s\n", line)
	} else if res.Manifest != nil {
		fmt.Fprintln(w, "调用时的身份：不带凭据")
	}
	if len(res.Tools) > 0 {
		fmt.Fprintln(w, "工具：")
		for _, t := range res.Tools {
			kind := "写操作，调用前要员工确认"
			if t.ReadOnly {
				kind = "只读"
			}
			line := "  " + t.Name
			if t.Method != "" {
				line += fmt.Sprintf("  %s %s", t.Method, t.Path)
			}
			fmt.Fprintf(w, "%s  （%s）\n", line, kind)
			who := map[string]string{extension.FillApp: "搭应用的人固定", extension.FillAdmin: "管理员统一设置"}
			for _, p := range t.Fixed {
				extra := ""
				if len(p.Default) > 0 && string(p.Default) != "null" {
					extra = "，预先填 " + string(p.Default)
				}
				fmt.Fprintf(w, "      %s：%s，模型看不到%s\n", p.Name, who[p.Fill], extra)
			}
			if len(t.Keep) > 0 {
				fmt.Fprintf(w, "      返回只保留：%s\n", strings.Join(t.Keep, "、"))
			}
		}
	}
	if len(res.Results) > 0 {
		fmt.Fprintln(w)
	}
	for _, d := range res.Results {
		fmt.Fprintf(w, "%s  %s  %s\n      %s\n", severityLabel[d.Severity], d.Code, d.Where, d.Message)
		if d.Fix != "" {
			fmt.Fprintf(w, "      改法：%s\n", d.Fix)
		}
	}
	fmt.Fprintln(w)
	if res.OK {
		fmt.Fprintf(w, "通过：0 个错误，%d 个提醒\n", res.Warnings)
		if res.Digest != "" {
			fmt.Fprintf(w, "sha256：%s\n", res.Digest)
		}
	} else {
		fmt.Fprintf(w, "没通过：%d 个错误，%d 个提醒\n", res.Errors, res.Warnings)
	}
}

func cliSchema(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, cliUsage)
		return 2
	}
	rest, asJSON, err := flags(args[1:])
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	switch args[0] {
	case "list":
		if len(rest) != 0 {
			fmt.Fprintln(stderr, "用法：dsh-ee schema list [--json]")
			return 2
		}
		list := extension.Schemas()
		if asJSON {
			enc := json.NewEncoder(stdout)
			enc.SetIndent("", "  ")
			enc.SetEscapeHTML(false)
			_ = enc.Encode(list)
			return 0
		}
		for _, s := range list {
			fmt.Fprintf(stdout, "%-10s %s\n", s.Name, s.Title)
		}
		return 0
	case "get":
		if len(rest) != 1 {
			fmt.Fprintln(stderr, "用法：dsh-ee schema get <名字>")
			return 2
		}
		raw, ok := extension.Schema(rest[0])
		if !ok {
			fmt.Fprintf(stderr, "没有名为 %s 的规格；用 dsh-ee schema list 看有哪些\n", rest[0])
			return 1
		}
		_, _ = stdout.Write(raw)
		return 0
	}
	fmt.Fprint(stderr, cliUsage)
	return 2
}
