package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func toolsNamed(names []string) []map[string]any {
	tools := make([]map[string]any, 0, len(names))
	for _, name := range names {
		tools = append(tools, map[string]any{"name": name, "description": "描述 " + name, "inputSchema": map[string]any{"type": "object"}})
	}
	return tools
}

func namesOf(tools []map[string]any) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, mcpToolDefName(t))
	}
	return out
}

func rawJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

// pageAllLimit 按 limit 翻完所有页，返回拼起来的工具名与页数。
func pageAllLimit(t *testing.T, tools []map[string]any, limit int, asDSH bool) ([]string, int) {
	t.Helper()
	var all []string
	cursor := ""
	for page := 0; page < 1000; page++ {
		var raw json.RawMessage
		if cursor == "" {
			raw = rawJSON(map[string]any{"limit": limit})
		} else {
			raw = rawJSON(map[string]any{"limit": limit, "cursor": cursor})
		}
		out, next, err := mcpToolsListPage(tools, raw, asDSH)
		if err != nil {
			t.Fatalf("第 %d 页：%v", page+1, err)
		}
		if limit > 0 && len(out) > limit {
			t.Fatalf("第 %d 页给了 %d 个工具，超过 limit=%d", page+1, len(out), limit)
		}
		all = append(all, namesOf(out)...)
		if next == "" {
			return all, page + 1
		}
		cursor = next
	}
	t.Fatal("翻页没有结束")
	return nil, 0
}

// DSH 接入插件不跟 nextCursor：不带 cursor/limit 时必须还是全量、保持原有顺序。
func TestMCPToolsListPageDSHLegacyFullList(t *testing.T) {
	tools := toolsNamed([]string{"z__export", "a__lookup", "m__update"})
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{}`), json.RawMessage(`null`), rawJSON(map[string]any{"unused": 1})} {
		page, next, err := mcpToolsListPage(tools, raw, true)
		if err != nil {
			t.Fatalf("params=%s：%v", raw, err)
		}
		if next != "" {
			t.Fatalf("DSH 全量不该有 nextCursor：%s", next)
		}
		if got, want := namesOf(page), namesOf(tools); !slices.Equal(got, want) {
			t.Fatalf("DSH 全量应保持原有顺序：got %v want %v", got, want)
		}
	}
}

// 第三方 Harness 不带参数时按默认页大小分页；DSH 同一批工具仍是全量。
func TestMCPToolsListPageThirdPartyDefaultSize(t *testing.T) {
	names := make([]string, 0, 150)
	for i := 0; i < 150; i++ {
		names = append(names, fmt.Sprintf("t%03d__tool", i))
	}
	tools := toolsNamed(names)
	page, next, err := mcpToolsListPage(tools, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != mcpToolsPageSize || next == "" {
		t.Fatalf("第三方第一页应为默认页大小 %d 且带 cursor：%d %q", mcpToolsPageSize, len(page), next)
	}
	// 后续只回传 cursor、不带 limit 时，默认页大小同样生效。
	page2, next2, err := mcpToolsListPage(tools, rawJSON(map[string]any{"cursor": next}), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != len(tools)-mcpToolsPageSize || next2 != "" {
		t.Fatalf("第二页：%d 个，next=%q", len(page2), next2)
	}
	if len(page)+len(page2) != len(tools) {
		t.Fatalf("两页应正好拼成全量：%d", len(page)+len(page2))
	}
}

func TestMCPToolsListPageLimitBoundaries(t *testing.T) {
	// 5 个工具按 limit=2 翻页：2、2、1，最后一页没有 nextCursor。
	all, pages := pageAllLimit(t, toolsNamed([]string{"e__5", "d__4", "c__3", "b__2", "a__1"}), 2, false)
	want := []string{"a__1", "b__2", "c__3", "d__4", "e__5"}
	if !slices.Equal(all, want) || pages != 3 {
		t.Fatalf("分页结果不对：pages=%d all=%v", pages, all)
	}
	// 正好整除：4 个 limit=2 → 2 页，最后一页也不该多给空页。
	all, pages = pageAllLimit(t, toolsNamed([]string{"d", "c", "b", "a"}), 2, false)
	if !slices.Equal(all, []string{"a", "b", "c", "d"}) || pages != 2 {
		t.Fatalf("整除边界不对：pages=%d all=%v", pages, all)
	}
	// limit 比工具总数大：全给，不带 cursor。
	page, next, err := mcpToolsListPage(toolsNamed([]string{"b", "a"}), rawJSON(map[string]any{"limit": 10}), false)
	if err != nil || len(page) != 2 || next != "" {
		t.Fatalf("limit 大于总数：%v %d %q", err, len(page), next)
	}
	// limit=0：明确不分页。
	page, next, err = mcpToolsListPage(toolsNamed([]string{"c", "b", "a"}), rawJSON(map[string]any{"limit": 0}), false)
	if err != nil || len(page) != 3 || next != "" {
		t.Fatalf("limit=0 应返回全量：%v %d %q", err, len(page), next)
	}
	// 单页最多 mcpToolsPageMax 个。
	many := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		many = append(many, fmt.Sprintf("m%03d", i))
	}
	big := toolsNamed(many)
	page, next, err = mcpToolsListPage(big, rawJSON(map[string]any{"limit": 10000}), false)
	if err != nil || len(page) != mcpToolsPageMax || next == "" {
		t.Fatalf("单页应封顶 %d：%v %d %q", mcpToolsPageMax, err, len(page), next)
	}
	page2, next2, err := mcpToolsListPage(big, rawJSON(map[string]any{"limit": 10000, "cursor": next}), false)
	if err != nil || len(page2) != len(big)-mcpToolsPageMax || next2 != "" {
		t.Fatalf("封顶后的下一页：%v %d %q", err, len(page2), next2)
	}
	// 空清单：返回 []，不是 null，也没有 cursor。
	empty, next, err := mcpToolsListPage(nil, nil, false)
	if err != nil || empty == nil || len(empty) != 0 || next != "" {
		t.Fatalf("空清单：%v %v %q", err, empty, next)
	}
	if b, _ := json.Marshal(empty); string(b) != "[]" {
		t.Fatalf("空清单应序列化成 []：%s", b)
	}
}

func TestMCPToolsListPageCursor(t *testing.T) {
	tools := toolsNamed([]string{"tool-echo", "tool-delta", "tool-charlie", "tool-bravo", "tool-alpha"})
	first, cursor, err := mcpToolsListPage(tools, rawJSON(map[string]any{"limit": 2}), false)
	if err != nil || cursor == "" {
		t.Fatalf("第一页：%v %q", err, cursor)
	}
	// cursor 不透明：不能是明文下标，也不能带工具名。
	if cursor == "2" || strings.Contains(cursor, "tool") || strings.Contains(cursor, "alpha") {
		t.Fatalf("cursor 不该泄漏明文内容：%q", cursor)
	}
	if _, err := base64.RawURLEncoding.DecodeString(cursor); err != nil {
		t.Fatalf("cursor 应是 base64url：%v", err)
	}
	// 同一份清单两次翻页结果与 cursor 完全一致（稳定）。
	firstAgain, cursorAgain, err := mcpToolsListPage(tools, rawJSON(map[string]any{"limit": 2}), false)
	if err != nil || !slices.Equal(namesOf(first), namesOf(firstAgain)) || cursor != cursorAgain {
		t.Fatalf("同样参数应得到稳定结果：%v %v %q %q", err, namesOf(firstAgain), cursor, cursorAgain)
	}
	// 未知 cursor（乱写、以及合法 base64 但格式不对）都应该被拒绝。
	for _, bad := range []string{
		"not-a-cursor",
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":1}`)),
		base64.RawURLEncoding.EncodeToString([]byte(`{"v":99,"o":1,"d":"0123456789abcdef"}`)),
		encodeMCPToolsCursor(-1, mcpToolsDigest(tools)),
		encodeMCPToolsCursor(0, "deadbeefdeadbeef"),
	} {
		if _, _, err := mcpToolsListPage(tools, rawJSON(map[string]any{"cursor": bad}), false); err == nil {
			t.Fatalf("未知 cursor 应该报错：%q", bad)
		}
	}
	// 清单在翻页途中变了（工具增删）：旧 cursor 作废，不能悄悄漏工具。
	changed := toolsNamed([]string{"tool-echo", "tool-delta", "tool-charlie", "tool-bravo", "tool-alpha", "tool-foxtrot"})
	if _, _, err := mcpToolsListPage(changed, rawJSON(map[string]any{"cursor": cursor}), false); !errors.Is(err, errMCPToolsListChanged) {
		t.Fatalf("清单变化后旧 cursor 应作废：%v", err)
	}
	// 走到末尾后再用旧 cursor：空页而不是 500。
	last := encodeMCPToolsCursor(len(tools), mcpToolsDigest(sortedTools(tools)))
	page, next, err := mcpToolsListPage(tools, rawJSON(map[string]any{"cursor": last}), false)
	if err != nil || len(page) != 0 || next != "" {
		t.Fatalf("末尾 cursor 应返回空页：%v %d %q", err, len(page), next)
	}
}

// sortedTools 返回按名字稳定排序后的副本（测试里造 cursor 用）。
func sortedTools(tools []map[string]any) []map[string]any {
	out := append([]map[string]any(nil), tools...)
	slices.SortFunc(out, func(a, b map[string]any) int { return strings.Compare(mcpToolDefName(a), mcpToolDefName(b)) })
	return out
}

func TestMCPToolsListParams(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage(``), json.RawMessage(`null`), json.RawMessage(`{}`)} {
		p, err := parseMCPToolsListParams(raw)
		if err != nil || p.Cursor != "" || p.Limit != nil {
			t.Fatalf("空参数：%v %+v", err, p)
		}
	}
	p, err := parseMCPToolsListParams(rawJSON(map[string]any{"cursor": "abc", "limit": 0}))
	if err != nil || p.Cursor != "abc" || p.Limit == nil || *p.Limit != 0 {
		t.Fatalf("正常参数：%v %+v", err, p)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"cursor":1}`),
		json.RawMessage(`{"limit":"many"}`),
		json.RawMessage(`{"limit":-1}`),
		json.RawMessage(`[1,2]`),
		json.RawMessage(`"x"`),
	} {
		if _, err := parseMCPToolsListParams(raw); err == nil {
			t.Fatalf("坏参数应该报错：%s", raw)
		}
	}
}
