// demo-extension 是一个用假数据把服务扩展的每种能力都做了一遍的示例。复制它就能开始：
// 改 id、把假数据换成对你们系统的调用，然后 `dsh-ee extension test --run "go run ." .` 全部通过就算做完。
//
// 它做了：模型填的参数和管理员、应用固定的参数；动态选项；保存配置前的检查；一个只读工具和一个写工具；
// 文档库知识源（带浏览，能增量同步）；联邦检索；回调地址；user_secret 凭据；约定的错误码；一个扩展字段。
package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/hikariming/dsh-ee/extensions/sdk-go"
)

// ---- 假数据：一个小小的审批系统和它的制度文档 ----

type form struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Title  string `json:"title"`
	Owner  string `json:"owner"`
	Status string `json:"status"`
}

type page struct {
	ID, Space, Title, Body string
	Rev                    int
	Deleted                bool
}

var (
	mu    sync.Mutex
	forms = []form{
		{ID: "F-1001", Type: "leave", Title: "年假 3 天", Owner: "zhangsan", Status: "pending"},
		{ID: "F-1002", Type: "expense", Title: "上海出差报销", Owner: "zhangsan", Status: "done"},
		{ID: "F-1003", Type: "leave", Title: "病假 1 天", Owner: "lisi", Status: "pending"},
	}
	pages = []*page{
		{ID: "hr-leave", Space: "hr", Title: "请假制度", Body: "# 请假制度\n\n入职满一年有 5 天年假，满三年有 10 天年假。\n\n病假需要医院证明。", Rev: 1},
		{ID: "hr-attendance", Space: "hr", Title: "考勤制度", Body: "# 考勤制度\n\n每天 9:30 前打卡。", Rev: 2},
		{ID: "fin-travel", Space: "finance", Title: "差旅报销制度", Body: "# 差旅报销制度\n\n一线城市住宿每晚不超过 600 元。", Rev: 3},
	}
	rev = 3
)

var spaces = map[string]string{"hr": "人事", "finance": "财务"}

func main() {
	ext := sdk.New(sdk.Manifest{
		ID:          "example/demo",
		Version:     "1.0.0",
		MinVersion:  "0.2.0",
		Name:        sdk.T("示例审批系统", "Demo approvals"),
		Description: sdk.T("用假数据演示服务扩展的每种能力：查和提交审批单、制度文档知识源、回调", "A fake approval system that exercises every extension capability"),
		Repo:        "https://github.com/hikariming/dsh-ee",
		Config: []sdk.ConfigField{
			{Key: "tenant", Type: "select", Label: sdk.T("租户", "Tenant"), Required: true, Options: "dynamic"},
			{Key: "webhook_secret", Type: "secret", Label: sdk.T("回调的校验密钥", "Webhook secret"), Help: sdk.T("审批系统回调时放在 X-Demo-Secret 里的值", "")},
		},
		// 员工各自在审批系统里有一个 token。真实系统能验 JWT 的话，优先用 ee_jwt（不用员工填任何东西）
		Credential: &sdk.Credential{Mode: "user_secret", Hint: sdk.T("示例里随便填一个以 demo- 开头的值，比如 demo-123", "Any value starting with demo-")},
		Fields: []sdk.Field{
			{Key: "space", Object: "document", Type: "select", Label: sdk.T("所属空间", "Space"), Options: []string{"人事", "财务"}},
		},
	})

	// 只读工具：status 由模型填；form_type 由搭应用的人固定（模型看不到，也改不了）
	ext.Tool(sdk.Tool{
		Name:        "list_forms",
		Description: sdk.T("列出当前员工自己的审批单", "List the caller's forms"),
		ReadOnly:    true,
		Params: []sdk.Param{
			{Name: "status", Type: "string", Enum: []any{"pending", "done"}, Description: sdk.T("只看这种状态的；不填看全部", "")},
			{Name: "form_type", Type: "string", Fill: "app", Label: sdk.T("单据类型", "Form type")},
		},
		Handler: listForms,
	})
	// 写工具：没标 ReadOnly，调用前要员工确认。approver 由管理员统一设置
	ext.Tool(sdk.Tool{
		Name:        "submit_leave",
		Description: sdk.T("替当前员工提交一张请假单", "Submit a leave request for the caller"),
		Params: []sdk.Param{
			{Name: "days", Type: "number", Required: true, Description: sdk.T("请几天", "")},
			{Name: "reason", Type: "string", Description: sdk.T("原因", "")},
			{Name: "approver", Type: "string", Fill: "admin", Label: sdk.T("默认审批人", "Default approver"), Default: "hr"},
		},
		Handler: submitLeave,
	})

	ext.Validate(func(ctx context.Context, c *sdk.Call) error {
		tenant, _ := c.Config["tenant"].(string)
		if tenant != "acme" && tenant != "globex" {
			return sdk.Errorf(sdk.CodeInvalid, "没有租户 %q：只有 acme 和 globex", tenant)
		}
		return nil
	})
	ext.Options(func(ctx context.Context, c *sdk.Call, field string) ([]sdk.Option, error) {
		if field == "tenant" {
			return []sdk.Option{{Value: "acme", Label: "Acme 公司"}, {Value: "globex", Label: "Globex 公司"}}, nil
		}
		return nil, sdk.Errorf(sdk.CodeNotFound, "没有 %s 这个选项字段", field)
	})

	ext.Knowledge(sdk.Knowledge{Browse: browse, Changes: changes, Document: document, Search: search})

	// 回调：审批系统在单子办完时打过来。验签要扩展自己做
	ext.Endpoint(func(ctx context.Context, c *sdk.Call, req *sdk.EndpointRequest) (*sdk.EndpointResponse, error) {
		want, _ := c.Config["webhook_secret"].(string)
		got := ""
		if vs := req.Header["X-Demo-Secret"]; len(vs) > 0 {
			got = vs[0]
		}
		if want != "" && got != want {
			return &sdk.EndpointResponse{Status: 403, Body: []byte("bad secret")}, nil
		}
		id := strings.TrimPrefix(req.Path, "/done/")
		mu.Lock()
		defer mu.Unlock()
		for i := range forms {
			if forms[i].ID == id {
				forms[i].Status = "done"
				return &sdk.EndpointResponse{Status: 200, Body: []byte("ok")}, nil
			}
		}
		return &sdk.EndpointResponse{Status: 404, Body: []byte("no such form")}, nil
	})

	log.Printf("示例扩展 %s 启动", "example/demo")
	log.Fatal(ext.ListenAndServe(""))
}

// needToken 检查员工的凭据。真实扩展里，这里是拿着它去调源系统。
func needToken(c *sdk.Call) error {
	switch {
	case c.Credential == "":
		return sdk.Errorf(sdk.CodeAuthorize, "还没有填审批系统的 token")
	case !strings.HasPrefix(c.Credential, "demo-"):
		return sdk.Errorf(sdk.CodeAuthorize, "审批系统不认这个 token，请重新填")
	case c.Credential == "demo-readonly-blocked":
		return sdk.Errorf(sdk.CodeForbidden, "你的账号在审批系统里被停用了")
	}
	return nil
}

func listForms(ctx context.Context, c *sdk.Call) (any, error) {
	if err := needToken(c); err != nil {
		return nil, err
	}
	status, typ := c.String("status"), c.String("form_type")
	mu.Lock()
	defer mu.Unlock()
	out := []form{}
	for _, f := range forms {
		// 只给当前员工看他自己的单子：身份来自验过签的 ee_jwt，不是模型说的
		if f.Owner == c.User.Username && (status == "" || f.Status == status) && (typ == "" || f.Type == typ) {
			out = append(out, f)
		}
	}
	return map[string]any{"items": out, "total": len(out)}, nil
}

func submitLeave(ctx context.Context, c *sdk.Call) (any, error) {
	if err := needToken(c); err != nil {
		return nil, err
	}
	days, _ := c.Args["days"].(float64)
	if days <= 0 || days > 30 {
		return nil, sdk.Errorf(sdk.CodeInvalid, "请假天数要在 1 到 30 之间")
	}
	mu.Lock()
	defer mu.Unlock()
	f := form{ID: "F-" + strconv.Itoa(1001+len(forms)), Type: "leave", Title: fmt.Sprintf("请假 %g 天", days), Owner: c.User.Username, Status: "pending"}
	forms = append(forms, f)
	return map[string]any{"id": f.ID, "status": f.Status, "approver": c.String("approver")}, nil
}

// ---- 知识源 ----

func browse(ctx context.Context, c *sdk.Call, parent string) ([]sdk.Node, error) {
	mu.Lock()
	defer mu.Unlock()
	if parent == "" {
		ids := make([]string, 0, len(spaces))
		for id := range spaces {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		out := []sdk.Node{}
		for _, id := range ids {
			out = append(out, sdk.Node{ID: id, Title: spaces[id], Kind: "folder", HasChildren: true})
		}
		return out, nil
	}
	if _, ok := spaces[parent]; !ok {
		return nil, sdk.Errorf(sdk.CodeNotFound, "没有空间 %s", parent)
	}
	out := []sdk.Node{}
	for _, p := range pages {
		if p.Space == parent && !p.Deleted {
			out = append(out, sdk.Node{ID: p.ID, Title: p.Title, Kind: "document"})
		}
	}
	return out, nil
}

// changes 用修订号做 cursor：单调递增，同一个 cursor 再问一次，返回的是它之后的全部变化，不会漏。
func changes(ctx context.Context, c *sdk.Call, scope []string, cursor string) (*sdk.Changes, error) {
	since := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil {
			return nil, sdk.Errorf(sdk.CodeInvalid, "cursor 不对")
		}
		since = n
	}
	inScope := func(p *page) bool {
		if len(scope) == 0 {
			return true
		}
		for _, s := range scope {
			if s == p.Space || s == p.ID {
				return true
			}
		}
		return false
	}
	mu.Lock()
	defer mu.Unlock()
	var changed []*page
	for _, p := range pages {
		if p.Rev > since && inScope(p) {
			changed = append(changed, p)
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].Rev < changed[j].Rev })
	// 每页 2 篇，演示翻页
	const pageSize = 2
	out := &sdk.Changes{Cursor: strconv.Itoa(since)}
	for i, p := range changed {
		if i == pageSize {
			out.HasMore = true
			break
		}
		out.Items = append(out.Items, sdk.Change{ID: p.ID, Title: p.Title, Deleted: p.Deleted, Folder: spaces[p.Space], UpdatedAt: time.Now()})
		out.Cursor = strconv.Itoa(p.Rev)
	}
	if !out.HasMore && rev > since && len(changed) == 0 {
		out.Cursor = strconv.Itoa(rev)
	}
	return out, nil
}

func document(ctx context.Context, c *sdk.Call, id string) (*sdk.Document, error) {
	mu.Lock()
	defer mu.Unlock()
	for _, p := range pages {
		if p.ID == id && !p.Deleted {
			return &sdk.Document{ID: p.ID, Title: p.Title, Content: p.Body, URL: "https://demo.example.com/wiki/" + p.ID, Fields: map[string]any{"space": spaces[p.Space]}}, nil
		}
	}
	return nil, sdk.Errorf(sdk.CodeNotFound, "没有文档 %s", id)
}

// search 是联邦检索：带着员工的身份查。示例里财务空间只有财务部的人能查到，演示「权限留在源系统」。
func search(ctx context.Context, c *sdk.Call, query string, limit int) ([]sdk.Hit, error) {
	mu.Lock()
	defer mu.Unlock()
	out := []sdk.Hit{}
	for _, p := range pages {
		if p.Deleted || (p.Space == "finance" && c.User.Dept != "财务部") {
			continue
		}
		if i := strings.Index(p.Body, query); i >= 0 || strings.Contains(p.Title, query) {
			out = append(out, sdk.Hit{ID: p.ID, Title: p.Title, Snippet: firstLineWith(p.Body, query), URL: "https://demo.example.com/wiki/" + p.ID})
		}
	}
	return out, nil
}

func firstLineWith(body, q string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, q) {
			return line
		}
	}
	return ""
}
