package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/feishu"
)

// 假的飞书开放平台：一个空间，根下一个目录（带一篇文档）和一篇表格。
func fakeFeishuWiki(t *testing.T) *httptest.Server {
	t.Helper()
	ok := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "ok", "data": data})
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/open-apis/auth/v3/tenant_access_token/internal" && r.Header.Get("Authorization") != "Bearer t-1" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "tenant_access_token": "t-1", "expire": 7200})
		case r.URL.Path == "/open-apis/wiki/v2/spaces":
			ok(w, map[string]any{"items": []any{map[string]any{"space_id": "sp1", "name": "产品手册"}}})
		case r.URL.Path == "/open-apis/wiki/v2/spaces/sp1/nodes" && r.URL.Query().Get("parent_node_token") == "":
			ok(w, map[string]any{"items": []any{
				map[string]any{"node_token": "n-dir", "obj_token": "doc-dir", "obj_type": "docx", "title": "入门/指南", "has_child": true, "obj_edit_time": "100"},
				map[string]any{"node_token": "n-sheet", "obj_token": "sheet1", "obj_type": "sheet", "title": "表格", "obj_edit_time": "300"},
			}})
		case r.URL.Path == "/open-apis/wiki/v2/spaces/sp1/nodes" && r.URL.Query().Get("parent_node_token") == "n-dir":
			ok(w, map[string]any{"items": []any{
				map[string]any{"node_token": "n-a", "obj_token": "doc-a", "obj_type": "docx", "title": "安装", "obj_edit_time": "200"},
			}})
		case r.URL.Path == "/open-apis/wiki/v2/spaces/get_node":
			ok(w, map[string]any{"node": map[string]any{"space_id": "sp1", "node_token": "n-dir", "obj_token": "doc-dir", "obj_type": "docx", "title": "入门/指南", "has_child": true, "obj_edit_time": "100"}})
		case strings.HasPrefix(r.URL.Path, "/open-apis/docx/v1/documents/doc-a/raw_content"):
			ok(w, map[string]any{"content": "第一步：下载\n"})
		default:
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 1770002, "msg": "not found"})
		}
	}))
}

func TestBuiltinFeishuDocs(t *testing.T) {
	srv := fakeFeishuWiki(t)
	defer srv.Close()
	ctx := context.Background()
	c := feishu.NewClient(srv.URL, "cli", "sec", srv.Client())
	tok, err := c.TenantToken(ctx)
	if err != nil {
		t.Fatal(err)
	}
	top, err := feishuBrowse(ctx, c, tok, "")
	if err != nil || len(top["items"].([]map[string]any)) != 1 || top["items"].([]map[string]any)[0]["id"] != "s:sp1" {
		t.Fatalf("最上层应是空间：%v %v", top, err)
	}
	all, err := feishuChanges(ctx, c, tok, []string{"s:sp1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	items := all["items"].([]map[string]any)
	if len(items) != 2 || all["cursor"] != "200" {
		t.Fatalf("整个空间应有两篇 docx（表格跳过），cursor 取 docx 里最晚的编辑时间：%v", all)
	}
	if items[1]["id"] != "d:doc-a" || items[1]["folder"] != "入门／指南" {
		t.Fatalf("子文档的目录应是父节点标题（/ 换成全角）：%v", items[1])
	}
	inc, err := feishuChanges(ctx, c, tok, []string{"s:sp1"}, "150")
	if err != nil || len(inc["items"].([]map[string]any)) != 1 {
		t.Fatalf("增量只应有 150 之后改过的那一篇：%v %v", inc, err)
	}
	node, err := feishuChanges(ctx, c, tok, []string{"n:sp1:n-dir"}, "")
	if err != nil || len(node["items"].([]map[string]any)) != 2 {
		t.Fatalf("勾一个节点应包括它自己和下面的文档：%v %v", node, err)
	}
	doc, err := feishuDocument(ctx, c, tok, "d:doc-a")
	if err != nil || doc["content"] != "第一步：下载\n" {
		t.Fatalf("取正文：%v %v", doc, err)
	}
	if _, err := feishuDocument(ctx, c, tok, "d:missing"); extCode(err) != "not_found" {
		t.Fatalf("飞书 404 应变成 not_found：%v", err)
	}
}
