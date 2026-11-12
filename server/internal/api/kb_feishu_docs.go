package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/feishu"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 飞书文档：内置知识源（docs/plugins.md §4.3、§6.2）。
//
// 实现和服务扩展同一套约定接口（kb/browse、kb/changes、kb/document），只是不走网络，直接在 dsh-ee 里调飞书开放平台。
// 只做「同步」：用应用身份（tenant token）读管理员勾选的知识空间，内容进 dsh-ee 的知识库，谁能看由库的权限决定。
// 用的是登录设置里的飞书应用（App ID / App Secret），不另外配凭据。
//
// 知识源表里用 asset_id = builtinFeishuDocs 标出它，不对应任何连接。
//
// 已知的限制：kb/changes 按「最后编辑时间」做增量，源系统里删掉的文档不会自动从知识库里删掉；
// 需要时在知识库页面上摘掉知识源（选「连同文档一起删」）再挂一次。只同步新版文档（docx），表格、多维表格等跳过。

const (
	builtinFeishuDocs   int64 = -1
	builtinFeishuDocsID       = "builtin/feishu-docs"
	extKindBuiltin            = "builtin"

	feishuDocsMaxDepth = 12
	feishuDocsMaxNodes = 10000
)

func isBuiltinSource(assetID int64) bool { return assetID == builtinFeishuDocs }

// feishuDocsClient 取登录设置里的飞书应用；没配好时返回 nil 和原因。
func (s *Server) feishuDocsClient(ctx context.Context) (*feishu.Client, string) {
	cfg, err := s.loadOIDC(ctx)
	if err != nil {
		return nil, "读不了登录设置"
	}
	if !cfg.Enabled || cfg.Preset != "feishu" {
		return nil, "没有开启飞书登录"
	}
	if cfg.FeishuAPIBase == "" || cfg.ClientSecret == "" {
		return nil, "飞书登录设置里没有填开放平台 API 地址或 App Secret"
	}
	return feishu.NewClient(cfg.FeishuAPIBase, cfg.ClientID, cfg.ClientSecret, s.oidcHTTP().HTTP), ""
}

// builtinFeishuDocsSource 是内置飞书文档在知识源里扮演的「扩展」。
func builtinFeishuDocsSource() (*store.Asset, *assetSpec) {
	a := &store.Asset{ID: builtinFeishuDocs, Kind: store.KindConnection, Slug: "feishu-docs", Title: "飞书文档（内置）", Status: store.AssetActive, LiveVersion: 1}
	return a, &assetSpec{Title: a.Title, Extension: &connExtension{ID: builtinFeishuDocsID, Version: "1.0.0", Kind: extKindBuiltin}}
}

// builtinKBCall 是内置知识源的约定接口：结果按服务扩展的 JSON 结构写进 out。
func (s *Server) builtinKBCall(ctx context.Context, name string, in, out any) error {
	c, why := s.feishuDocsClient(ctx)
	if c == nil {
		return &extError{Code: "setup", Message: "飞书文档用不了：" + why, Status: 400}
	}
	args := map[string]any{}
	if raw, err := json.Marshal(in); err == nil {
		_ = json.Unmarshal(raw, &args)
	}
	token, err := c.TenantToken(ctx)
	if err != nil {
		return &extError{Code: "unavailable", Message: "取飞书应用令牌失败：" + err.Error(), Status: 502}
	}
	var res any
	switch name {
	case "kb/browse":
		res, err = feishuBrowse(ctx, c, token, str(args["parent"]))
	case "kb/changes":
		var scope []string
		if xs, ok := args["scope"].([]any); ok {
			for _, x := range xs {
				scope = append(scope, str(x))
			}
		}
		res, err = feishuChanges(ctx, c, token, scope, str(args["cursor"]))
	case "kb/document":
		res, err = feishuDocument(ctx, c, token, str(args["id"]))
	default:
		return &extError{Code: "not_implemented", Message: "飞书文档没有 " + name, Status: 404}
	}
	if err != nil {
		var fe *feishu.APIError
		if errors.As(err, &fe) {
			return &extError{Code: "unavailable", Message: fe.Error(), Status: 502}
		}
		return err
	}
	if out == nil {
		return nil
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// 节点 id：s:<空间> 是一个知识空间，n:<空间>:<节点> 是空间里的一个节点；文档 id 是 d:<文档 token>。
func spaceNode(space string) string      { return "s:" + space }
func wikiNode(space, node string) string { return "n:" + space + ":" + node }
func parseNode(id string) (space, node string, ok bool) {
	switch {
	case strings.HasPrefix(id, "s:"):
		return strings.TrimPrefix(id, "s:"), "", strings.TrimPrefix(id, "s:") != ""
	case strings.HasPrefix(id, "n:"):
		space, node, ok = strings.Cut(strings.TrimPrefix(id, "n:"), ":")
		return space, node, ok && space != "" && node != ""
	}
	return "", "", false
}

func feishuBrowse(ctx context.Context, c *feishu.Client, token, parent string) (map[string]any, error) {
	items := []map[string]any{}
	if parent == "" {
		spaces, err := c.WikiSpaces(ctx, token)
		if err != nil {
			return nil, err
		}
		for _, sp := range spaces {
			items = append(items, map[string]any{"id": spaceNode(sp.ID), "title": sp.Name, "kind": "folder", "hasChildren": true})
		}
		return map[string]any{"items": items}, nil
	}
	space, node, ok := parseNode(parent)
	if !ok {
		return nil, &extError{Code: "not_found", Message: "没有这个节点", Status: 404}
	}
	nodes, err := c.WikiNodes(ctx, token, space, node)
	if err != nil {
		return nil, err
	}
	for _, n := range nodes {
		kind := "document"
		if n.HasChild {
			kind = "folder"
		}
		items = append(items, map[string]any{"id": wikiNode(space, n.NodeToken), "title": n.Title, "kind": kind, "hasChildren": n.HasChild})
	}
	return map[string]any{"items": items}, nil
}

// feishuChanges 走一遍勾选的范围，返回最后编辑时间晚于 cursor 的新版文档。cursor 是上一轮见到的最晚编辑时间（Unix 秒）。
func feishuChanges(ctx context.Context, c *feishu.Client, token string, scope []string, cursor string) (map[string]any, error) {
	since, _ := strconv.ParseInt(cursor, 10, 64)
	latest := since
	items := []map[string]any{}
	seen := map[string]bool{}
	count := 0
	var walk func(space, parent, folder string, depth int) error
	visit := func(space string, n feishu.WikiNode, folder string, depth int) error {
		if count++; count > feishuDocsMaxNodes {
			return fmt.Errorf("勾选的范围超过 %d 个节点；请勾得小一些", feishuDocsMaxNodes)
		}
		if n.ObjType == "docx" && n.ObjToken != "" && !seen[n.ObjToken] {
			seen[n.ObjToken] = true
			edited, _ := strconv.ParseInt(n.ObjEditTime, 10, 64)
			if edited > since || since == 0 {
				items = append(items, map[string]any{"id": "d:" + n.ObjToken, "title": n.Title, "folder": folder})
			}
			latest = max(latest, edited)
		}
		if n.HasChild && depth < feishuDocsMaxDepth {
			sub := strings.Trim(folder+"/"+safeFolderPart(n.Title), "/")
			return walk(space, n.NodeToken, sub, depth+1)
		}
		return nil
	}
	walk = func(space, parent, folder string, depth int) error {
		nodes, err := c.WikiNodes(ctx, token, space, parent)
		if err != nil {
			return err
		}
		for _, n := range nodes {
			if err := visit(space, n, folder, depth); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range scope {
		space, node, ok := parseNode(id)
		if !ok {
			continue
		}
		if node == "" {
			if err := walk(space, "", "", 0); err != nil {
				return nil, err
			}
			continue
		}
		// 勾的是一个节点：它自己（如果是文档）加上它下面的
		n, err := c.WikiNode(ctx, token, node)
		if err != nil {
			return nil, err
		}
		if err := visit(space, *n, "", 0); err != nil {
			return nil, err
		}
	}
	return map[string]any{"items": items, "cursor": strconv.FormatInt(latest, 10), "hasMore": false}, nil
}

func feishuDocument(ctx context.Context, c *feishu.Client, token, id string) (map[string]any, error) {
	doc, ok := strings.CutPrefix(id, "d:")
	if !ok || doc == "" {
		return nil, &extError{Code: "not_found", Message: "没有这篇文档", Status: 404}
	}
	text, err := c.DocxRawContent(ctx, token, doc)
	if err != nil {
		var fe *feishu.APIError
		if errors.As(err, &fe) && fe.Status == 404 {
			return nil, &extError{Code: "not_found", Message: "飞书里没有这篇文档了", Status: 404}
		}
		return nil, err
	}
	return map[string]any{"id": id, "format": "text", "content": text}, nil
}

// safeFolderPart 把节点标题变成知识库目录名的一段。
func safeFolderPart(title string) string {
	t := strings.NewReplacer("/", "／", "\\", "＼").Replace(strings.TrimSpace(title))
	return cutUTF8(t, 60)
}
