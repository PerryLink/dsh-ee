package sdk

import (
	"context"
	"net/http"
	"time"
)

// 知识源（docs/plugins.md §6.2）。实现 Changes + Document 就是「文档库」，可以同步进 dsh-ee 的知识库；
// 实现 Search 就是「联邦检索」，不复制内容，员工检索时实时去查（用这位员工自己的身份）。
type Knowledge struct {
	// 列出 parent 下面的空间、文件夹和文档；parent 为空是最上层。管理员据此勾选要同步的范围
	Browse func(ctx context.Context, c *Call, parent string) ([]Node, error)
	// 增量：scope 是管理员勾选的节点 id，cursor 是上一次返回的位置（第一次为空）。
	// cursor 要能续上：同一个 cursor 再问一次，不能漏掉之后的变化
	Changes func(ctx context.Context, c *Call, scope []string, cursor string) (*Changes, error)
	// 取一篇文档的正文。id 不存在时返回 sdk.Errorf(sdk.CodeNotFound, …)
	Document func(ctx context.Context, c *Call, id string) (*Document, error)
	// 联邦检索
	Search func(ctx context.Context, c *Call, query string, limit int) ([]Hit, error)
}

// Node 是知识源里的一个节点。
type Node struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// folder（空间、文件夹，可以再往下看）或 document
	Kind        string `json:"kind"`
	HasChildren bool   `json:"hasChildren,omitempty"`
}

// Change 是一篇文档的变化。Deleted 为 true 表示源系统里删掉了（或移出了范围）。
type Change struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Deleted   bool      `json:"deleted,omitempty"`
	UpdatedAt time.Time `json:"updatedAt,omitempty"`
	// 在知识库里放到哪个目录下，用 / 分隔；空则放在根目录
	Folder string `json:"folder,omitempty"`
}

// Changes 是一页增量。HasMore 为 true 时 dsh-ee 会带着 Cursor 接着问。
type Changes struct {
	Items   []Change `json:"items"`
	Cursor  string   `json:"cursor"`
	HasMore bool     `json:"hasMore,omitempty"`
}

// Document 是一篇文档的正文。
type Document struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// markdown（默认）或 text
	Format  string `json:"format,omitempty"`
	Content string `json:"content"`
	// 原文在源系统里的地址，显示成「查看原文」
	URL string `json:"url,omitempty"`
	// 扩展字段的值（清单 fields 里声明过的）
	Fields map[string]any `json:"fields,omitempty"`
}

// Hit 是联邦检索的一条结果。
type Hit struct {
	ID      string         `json:"id"`
	Title   string         `json:"title"`
	Snippet string         `json:"snippet,omitempty"`
	URL     string         `json:"url,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// Knowledge 注册知识源。
func (e *Extension) Knowledge(k Knowledge) *Extension {
	e.kb = &k
	return e
}

func (e *Extension) handleKB(w http.ResponseWriter, r *http.Request, c *Call, name string) {
	k := e.kb
	missing := func() { writeError(w, http.StatusNotFound, CodeNotImplemented, "这个扩展没有实现 kb/"+name) }
	if k == nil {
		missing()
		return
	}
	ctx := r.Context()
	switch name {
	case "browse":
		var req struct {
			Parent string `json:"parent"`
		}
		if !readBody(w, r, &req) {
			return
		}
		if k.Browse == nil {
			missing()
			return
		}
		items, err := k.Browse(ctx, c, req.Parent)
		if err != nil {
			writeErr(w, err)
			return
		}
		if items == nil {
			items = []Node{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	case "changes":
		var req struct {
			Scope  []string `json:"scope"`
			Cursor string   `json:"cursor"`
		}
		if !readBody(w, r, &req) {
			return
		}
		if k.Changes == nil {
			missing()
			return
		}
		out, err := k.Changes(ctx, c, req.Scope, req.Cursor)
		if err != nil {
			writeErr(w, err)
			return
		}
		if out == nil {
			out = &Changes{Cursor: req.Cursor}
		}
		if out.Items == nil {
			out.Items = []Change{}
		}
		writeJSON(w, http.StatusOK, out)
	case "document":
		var req struct {
			ID string `json:"id"`
		}
		if !readBody(w, r, &req) {
			return
		}
		if k.Document == nil {
			missing()
			return
		}
		doc, err := k.Document(ctx, c, req.ID)
		if err != nil {
			writeErr(w, err)
			return
		}
		if doc == nil {
			writeError(w, http.StatusNotFound, CodeNotFound, "没有这篇文档："+req.ID)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	case "search":
		var req struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if !readBody(w, r, &req) {
			return
		}
		if k.Search == nil {
			missing()
			return
		}
		if req.Limit <= 0 || req.Limit > 50 {
			req.Limit = 10
		}
		items, err := k.Search(ctx, c, req.Query, req.Limit)
		if err != nil {
			writeErr(w, err)
			return
		}
		if items == nil {
			items = []Hit{}
		}
		if len(items) > req.Limit {
			items = items[:req.Limit]
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	default:
		missing()
	}
}
