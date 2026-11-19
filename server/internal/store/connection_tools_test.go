package store

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/openapi"
)

// mcpTools 照 discoverTools 的做法把上游 MCP 的工具名变成工具：对外的名字规范、去重，和上游不一样时记下上游原名。
func mcpTools(raws ...string) []*ConnectionTool {
	names := openapi.MCPToolNames(raws)
	out := make([]*ConnectionTool, len(raws))
	for i, raw := range raws {
		var op json.RawMessage
		if names[i] != raw {
			op, _ = json.Marshal(MCPToolOp{Name: raw})
		}
		out[i] = &ConnectionTool{Name: names[i], Description: raw, InputSchema: json.RawMessage(`{"type":"object"}`), Operation: op, ReadOnly: true, Enabled: true}
	}
	return out
}

// byUpstream 按上游原名取同步后的工具。
func byUpstream(t *testing.T, st *Store, assetID int64) map[string]*ConnectionTool {
	t.Helper()
	tools, err := st.ConnectionTools(context.Background(), assetID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*ConnectionTool{}
	for _, tl := range tools {
		out[tl.UpstreamName()] = tl
	}
	return out
}

func replaceTools(t *testing.T, st *Store, assetID int64, tools []*ConnectionTool) {
	t.Helper()
	if err := st.ReplaceConnectionTools(context.Background(), assetID, tools); err != nil {
		t.Fatal(err)
	}
}

func wantTool(t *testing.T, tl *ConnectionTool, name string, enabled bool, roles []int64) {
	t.Helper()
	if tl == nil {
		t.Fatalf("缺工具 %s", name)
	}
	if tl.Name != name || tl.Enabled != enabled || !slices.Equal(tl.Roles, roles) {
		t.Fatalf("上游 %s：name=%s enabled=%v roles=%v，应为 name=%s enabled=%v roles=%v",
			tl.UpstreamName(), tl.Name, tl.Enabled, tl.Roles, name, enabled, roles)
	}
}

// 上游新增一个规范后和已有工具同名的工具（get-issue 对 get_issue），重新同步后管理员的角色限定
// 必须还在真正的 get_issue 上，新工具拿默认设置；否则真正的 get_issue 换了个名字、没了限制，就是越权。
func TestReplaceConnectionToolsKeepsSettingsByUpstreamName(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	const asset = 9001

	replaceTools(t, st, asset, mcpTools("get_issue", "list_issues"))
	if err := st.SetConnectionTool(ctx, asset, "get_issue", true, []int64{7}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConnectionTool(ctx, asset, "list_issues", false, nil); err != nil {
		t.Fatal(err)
	}

	// 上游新增的 get-issue 排在最前面
	replaceTools(t, st, asset, mcpTools("get-issue", "get_issue", "list_issues"))
	got := byUpstream(t, st, asset)
	wantTool(t, got["get_issue"], "get_issue", true, []int64{7})
	wantTool(t, got["get-issue"], "get_issue_2", true, []int64{})
	wantTool(t, got["list_issues"], "list_issues", false, []int64{})

	// 就算对外的名字换了主人（比如按旧的「先到先得」起名），设置也跟着上游身份走
	tools := mcpTools("get_issue", "get-issue", "list_issues")
	tools[0].Name, tools[0].Operation = "get_issue_2", json.RawMessage(`{"mcp_name":"get_issue"}`)
	tools[1].Name, tools[1].Operation = "get_issue", json.RawMessage(`{"mcp_name":"get-issue"}`)
	replaceTools(t, st, asset, tools)
	got = byUpstream(t, st, asset)
	wantTool(t, got["get_issue"], "get_issue_2", true, []int64{7})
	wantTool(t, got["get-issue"], "get_issue", true, []int64{})
}

// 还没记上游原名时存下的旧行：名字是规范过的（上游的 files.read 存成 files_read，operation 为空）。
// 重新同步时要按规范后的名字把设置对回来；但同名的合规上游工具优先，旧行不会被别的工具拿走。
func TestReplaceConnectionToolsLegacyRows(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	const asset = 9002

	legacy := []*ConnectionTool{
		{Name: "get_issue", InputSchema: json.RawMessage(`{}`), ReadOnly: true, Enabled: true},
		{Name: "files_read", InputSchema: json.RawMessage(`{}`), ReadOnly: true, Enabled: true},
	}
	replaceTools(t, st, asset, legacy)
	if err := st.SetConnectionTool(ctx, asset, "get_issue", true, []int64{3}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetConnectionTool(ctx, asset, "files_read", false, []int64{4}); err != nil {
		t.Fatal(err)
	}

	// files_read 原来是上游的 files.read；get_issue 原来就是上游的 get_issue，现在上游又多了 get-issue
	replaceTools(t, st, asset, mcpTools("files.read", "get-issue", "get_issue"))
	got := byUpstream(t, st, asset)
	wantTool(t, got["files.read"], "files_read", false, []int64{4})
	wantTool(t, got["get_issue"], "get_issue", true, []int64{3})
	wantTool(t, got["get-issue"], "get_issue_2", true, []int64{})
}
