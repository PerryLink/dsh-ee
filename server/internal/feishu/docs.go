package feishu

import (
	"context"
	"net/http"
	"net/url"
)

// 飞书知识库（wiki）和云文档：内置知识源用（docs/plugins.md §6.2「飞书文档作为内置知识源」）。
// 用应用身份（tenant token）读，应用要在飞书开放平台开通 wiki:wiki:readonly、docx:document:readonly，
// 并被加进要同步的知识空间（空间设置 → 成员 → 添加应用）。

// WikiSpace 是一个知识空间。
type WikiSpace struct {
	ID   string `json:"space_id"`
	Name string `json:"name"`
}

// WikiNode 是知识空间里的一个节点（一篇文档、表格、或者只是目录）。
type WikiNode struct {
	SpaceID   string `json:"space_id"`
	NodeToken string `json:"node_token"`
	ObjToken  string `json:"obj_token"`
	// docx、doc、sheet、bitable、mindnote、file、slides
	ObjType  string `json:"obj_type"`
	Title    string `json:"title"`
	HasChild bool   `json:"has_child"`
	// 文档最后编辑时间，Unix 秒（字符串）
	ObjEditTime string `json:"obj_edit_time"`
}

// TenantToken 取应用身份的令牌，调用方在一轮操作里复用它。
func (c *Client) TenantToken(ctx context.Context) (string, error) { return c.tenantToken(ctx) }

// WikiSpaces 列出应用能看到的知识空间。
func (c *Client) WikiSpaces(ctx context.Context, token string) ([]WikiSpace, error) {
	var out []WikiSpace
	page := ""
	for i := 0; i < 50; i++ {
		var resp struct {
			Items     []WikiSpace `json:"items"`
			PageToken string      `json:"page_token"`
			HasMore   bool        `json:"has_more"`
		}
		q := url.Values{"page_size": {"50"}}
		if page != "" {
			q.Set("page_token", page)
		}
		if err := c.call(ctx, http.MethodGet, "/open-apis/wiki/v2/spaces?"+q.Encode(), nil, &resp, token); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if !resp.HasMore || resp.PageToken == "" {
			break
		}
		page = resp.PageToken
	}
	return out, nil
}

// WikiNodes 列出一个空间里 parent 下面的一层节点；parent 为空是空间的最上层。
func (c *Client) WikiNodes(ctx context.Context, token, spaceID, parent string) ([]WikiNode, error) {
	var out []WikiNode
	page := ""
	for i := 0; i < 100; i++ {
		var resp struct {
			Items     []WikiNode `json:"items"`
			PageToken string     `json:"page_token"`
			HasMore   bool       `json:"has_more"`
		}
		q := url.Values{"page_size": {"50"}}
		if parent != "" {
			q.Set("parent_node_token", parent)
		}
		if page != "" {
			q.Set("page_token", page)
		}
		if err := c.call(ctx, http.MethodGet, "/open-apis/wiki/v2/spaces/"+url.PathEscape(spaceID)+"/nodes?"+q.Encode(), nil, &resp, token); err != nil {
			return nil, err
		}
		out = append(out, resp.Items...)
		if !resp.HasMore || resp.PageToken == "" {
			break
		}
		page = resp.PageToken
	}
	return out, nil
}

// DocxRawContent 取一篇新版文档（docx）的纯文本正文。
func (c *Client) DocxRawContent(ctx context.Context, token, docToken string) (string, error) {
	var resp struct {
		Content string `json:"content"`
	}
	if err := c.call(ctx, http.MethodGet, "/open-apis/docx/v1/documents/"+url.PathEscape(docToken)+"/raw_content", nil, &resp, token); err != nil {
		return "", err
	}
	return resp.Content, nil
}

// WikiNode 取一个节点本身。
func (c *Client) WikiNode(ctx context.Context, token, nodeToken string) (*WikiNode, error) {
	var resp struct {
		Node WikiNode `json:"node"`
	}
	if err := c.call(ctx, http.MethodGet, "/open-apis/wiki/v2/spaces/get_node?token="+url.QueryEscape(nodeToken), nil, &resp, token); err != nil {
		return nil, err
	}
	return &resp.Node, nil
}
