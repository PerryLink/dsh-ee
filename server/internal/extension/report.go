package extension

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Report 生成给管理员看的交付说明（docs/agent-dev.md §6.1）：这个扩展做什么、有哪些工具、哪些是写操作、
// 用哪种凭据、会交给扩展什么、申请了哪些权限、安装时要填什么。作者（或 Agent）在后面补上测试结果和说明。
func Report(res *Result) string {
	m := res.Manifest
	var b strings.Builder
	fmt.Fprintf(&b, "# %s 交付说明\n\n", m.Name.Any())
	fmt.Fprintf(&b, "- 身份：`%s`\n", res.Identity)
	kinds := map[string]string{KindDeclarative: "声明式扩展（只有清单和接口文档，没有代码；请求由 dsh-ee 自己发）",
		KindService: "服务扩展（企业自己部署的服务，dsh-ee 按协议调它）", KindLocal: "端侧扩展（跑在员工电脑上的 npm 包）"}
	fmt.Fprintf(&b, "- 形式：%s\n", kinds[m.Kind])
	if res.Digest != "" {
		fmt.Fprintf(&b, "- 清单摘要（sha256）：`%s`\n", res.Digest)
	}
	if m.MinVersion != "" {
		fmt.Fprintf(&b, "- 要求 dsh-ee %s 以上\n", m.MinVersion)
	}
	if d := m.Description.Any(); d != "" {
		fmt.Fprintf(&b, "\n%s\n", d)
	}

	b.WriteString("\n## 请求发往哪里\n\n")
	switch m.Kind {
	case KindDeclarative:
		for _, s := range m.Servers {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n请求只会发往上面这些地址，重定向到别的域名时不带凭据。\n")
	case KindService:
		if m.Service != nil {
			fmt.Fprintf(&b, "- 扩展自己的服务：%s（安装时由管理员填实际的地址）\n", m.Service.URL)
		}
		b.WriteString("\ndsh-ee 只调扩展的服务；扩展再去调哪些系统由它的代码决定，请对照代码或让部署的人确认。\n")
	case KindLocal:
		fmt.Fprintf(&b, "- 员工本机上的 npm 包 `%s`，从员工的电脑直接访问源系统\n", m.Package)
	}

	b.WriteString("\n## 用谁的身份\n\n")
	cr := m.Credential
	switch {
	case cr == nil && m.Kind == KindService:
		b.WriteString("每次调用带一个 5 分钟有效的身份令牌（ee_jwt），扩展据此知道是哪位员工。不交给扩展别的凭据。\n")
	case cr == nil:
		b.WriteString("不带凭据。\n")
	default:
		line := map[string]string{
			CredShared:     "管理员配的一份共享凭据：所有员工共用，源系统分不清是谁。适合只读的公共数据。",
			CredUserSecret: "员工各自在「我的凭据」里填自己的凭据，按人加密存放；员工离职时一起删掉。",
			CredEEJWT:      "dsh-ee 为每次调用现签一个 5 分钟有效的身份令牌（ee_jwt），对方验签就知道是谁；dsh-ee 不保存员工在对方系统里的密码。",
			CredOAuth:      "员工在「我的凭据」里授权一次，dsh-ee 按人保存令牌、到期前刷新。",
		}[cr.Mode]
		b.WriteString(line + "\n")
		if cr.Mode == CredEEJWT && len(cr.Claims) > 0 {
			fmt.Fprintf(&b, "\n令牌里除了 id、用户名、姓名、邮箱，还带：%s。\n", strings.Join(cr.Claims, "、"))
		}
		if cr.Shared != nil && cr.Mode != CredShared {
			b.WriteString("\n另外还带一份管理员配的共享凭据（比如服务账号的 API Key）。\n")
		}
		if m.Kind == KindService {
			b.WriteString("\n**交给扩展的凭据**：")
			switch cr.Mode {
			case CredShared:
				b.WriteString("共享凭据原样交给扩展（这类凭据没有短期形式）。")
			case CredUserSecret:
				b.WriteString("员工填的凭据原样交给扩展（这类凭据没有短期形式）。")
			case CredOAuth:
				b.WriteString("只给 access token；refresh token 和 client secret 留在 dsh-ee。")
			case CredEEJWT:
				b.WriteString("只有短期的身份令牌。")
			}
			b.WriteString("\n")
		}
	}
	if m.OAuth != nil {
		fmt.Fprintf(&b, "\nOAuth：授权地址 %s，令牌地址 %s", m.OAuth.AuthorizeURL, m.OAuth.TokenURL)
		if len(m.OAuth.Scopes) > 0 {
			fmt.Fprintf(&b, "，scope：%s", strings.Join(m.OAuth.Scopes, " "))
		}
		b.WriteString("。管理员要先在对方系统的开放平台上建一个应用，把 dsh-ee 给出的回调地址登记上去。\n")
	}

	b.WriteString("\n## 工具\n\n| 工具 | 读 / 写 | 说明 |\n|---|---|---|\n")
	desc := map[string]string{}
	for _, t := range m.Tools {
		desc[t.Name] = t.Description.Any()
	}
	writes := 0
	for _, t := range res.Tools {
		kind := "**写**（调用前要员工确认，装好后默认不启用）"
		if t.ReadOnly {
			kind = "只读"
		} else {
			writes++
		}
		extra := desc[t.Name]
		for _, p := range t.Fixed {
			who := map[string]string{FillApp: "搭应用的人固定", FillAdmin: "管理员统一设置"}[p.Fill]
			extra += fmt.Sprintf("；参数 `%s` 由%s，模型看不到", p.Name, who)
		}
		if len(t.Keep) > 0 {
			extra += "；返回只保留 " + strings.Join(t.Keep, "、")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", t.Name, kind, strings.TrimPrefix(extra, "；"))
	}
	fmt.Fprintf(&b, "\n共 %d 个工具，其中 %d 个写操作。\n", len(res.Tools), writes)

	if len(m.Capabilities) > 0 {
		caps := map[string]string{"validate": "保存配置前检查", "options": "动态选项", "health": "健康检查", "knowledge.documents": "知识源：文档库同步",
			"knowledge.files": "知识源：网盘", "knowledge.search": "知识源：联邦检索（不复制，按员工权限实时查）", "endpoint": "回调地址（第三方系统可以打进来）", "oauth": "OAuth 钩子"}
		var list []string
		for _, c := range m.Capabilities {
			if c != "tools" && caps[c] != "" {
				list = append(list, caps[c])
			}
		}
		if len(list) > 0 {
			b.WriteString("\n## 工具之外的能力\n\n")
			for _, c := range list {
				fmt.Fprintf(&b, "- %s\n", c)
			}
		}
	}
	var perms map[string]any
	_ = json.Unmarshal(m.Permissions, &perms)
	if len(perms) > 0 {
		names := map[string]string{"kb": "以当前员工的身份检索和读取知识库", "approval": "发起写操作确认", "audit": "回报调用摘要", "notify": "给员工发通知", "endpoint": "注册回调地址"}
		keys := make([]string, 0, len(perms))
		for k := range perms {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\n## 申请的权限（扩展反过来调 dsh-ee）\n\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "- `%s`：%s。为什么需要：（请补充）\n", k, names[k])
		}
	}
	if len(m.Fields) > 0 {
		b.WriteString("\n## 带的扩展字段\n\n")
		for _, f := range m.Fields {
			access := ""
			if f.Access {
				access = "，**用于访问控制**"
			}
			fmt.Fprintf(&b, "- `%s`（%s，加在%s上%s）\n", f.Label.Any(), f.Type, map[string]string{"document": "文档", "kbase": "知识库", "user": "用户"}[f.Object], access)
		}
	}

	b.WriteString("\n## 安装时要填什么\n\n")
	n := 0
	if m.Kind == KindService {
		n++
		fmt.Fprintf(&b, "%d. 扩展部署好之后的服务地址。\n", n)
	}
	for _, f := range m.Config {
		n++
		req := ""
		if f.Required {
			req = "（必填）"
		}
		fmt.Fprintf(&b, "%d. %s%s", n, f.Label.Any(), req)
		if h := f.Help.Any(); h != "" {
			fmt.Fprintf(&b, "：%s", h)
		}
		b.WriteString("\n")
	}
	for _, p := range m.AdminParams() {
		n++
		fmt.Fprintf(&b, "%d. 参数「%s」的值（用于工具 %s）。\n", n, p.Label, strings.Join(p.Tools, "、"))
	}
	if cr != nil && (cr.Mode == CredShared || cr.Shared != nil) {
		n++
		fmt.Fprintf(&b, "%d. 共享凭据：去哪里拿（请补充）。\n", n)
	}
	if m.OAuth != nil {
		n++
		fmt.Fprintf(&b, "%d. OAuth 客户端的 client_id 和 client_secret。\n", n)
	}
	if n == 0 {
		b.WriteString("不用填。\n")
	}

	b.WriteString("\n## 检查和测试结果\n\n")
	if res.OK {
		fmt.Fprintf(&b, "- `dsh-ee extension check`：通过（0 个错误，%d 个提醒）\n", res.Warnings)
	} else {
		fmt.Fprintf(&b, "- `dsh-ee extension check`：**没通过**（%d 个错误）\n", res.Errors)
	}
	for _, d := range res.Results {
		if d.Severity != SevInfo {
			fmt.Fprintf(&b, "  - %s `%s`：%s\n", map[string]string{SevError: "错误", SevWarning: "提醒"}[d.Severity], d.Code, d.Message)
		}
	}
	if m.Kind == KindService {
		b.WriteString("- `dsh-ee extension test`：（把输出贴在这里；全部通过才能交付）\n")
	}
	b.WriteString("\n## 补充说明\n\n（这个扩展解决什么问题、已知的限制、出问题找谁）\n")
	return b.String()
}
