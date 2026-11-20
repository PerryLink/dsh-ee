package openapi

import (
	"fmt"
	"strings"
	"testing"
)

func TestMCPToolNames(t *testing.T) {
	long := strings.Repeat("a", 70)
	for _, c := range []struct {
		name string
		raws []string
		want []string
	}{
		// 合规的原名先占住，不管上游先列谁
		{"合规的名字不被抢", []string{"get-issue", "get_issue"}, []string{"get_issue_2", "get_issue"}},
		{"顺序无关", []string{"get_issue", "get-issue"}, []string{"get_issue", "get_issue_2"}},
		{"只差大小写", []string{"GetIssue", "getissue"}, []string{"GetIssue", "getissue_2"}},
		{"规范后重名加后缀", []string{"files.read", "files/read", "files-read"}, []string{"files_read", "files_read_2", "files_read_3"}},
		// 后缀不能撞上后面才出现的合规原名
		{"后缀避开合规原名", []string{"a.b", "a_b", "a_b_2"}, []string{"a_b_3", "a_b", "a_b_2"}},
		// 超过 64 字符的要截断，不算合规；截断后撞上合规的 64 字符名字时加后缀并仍在 64 以内
		{"超长截断", []string{long, long[:64]}, []string{long[:62] + "_2", long[:64]}},
		{"超长两个都截断", []string{long, long + "b"}, []string{long[:64], long[:62] + "_2"}},
	} {
		got := MCPToolNames(c.raws)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s：MCPToolNames(%q) = %q，应为 %q", c.name, c.raws, got, c.want)
		}
		for _, n := range got {
			if len(n) > 64 || ToolName(n) != n {
				t.Errorf("%s：名字不合规 %q", c.name, n)
			}
		}
	}
}

func TestUniqueToolNameBoundsAndCase(t *testing.T) {
	used := map[string]bool{}
	base := strings.Repeat("a", 64)
	first := UniqueToolName(base, used)
	if first != base {
		t.Fatalf("首个名字应保持原样：%q", first)
	}
	for i := 0; i < 1200; i++ {
		name := UniqueToolName(base, used)
		if len(name) > 64 {
			t.Fatalf("工具名超过 64 字节：%q", name)
		}
		if strings.ToLower(name) == strings.ToLower(first) {
			t.Fatalf("工具名重复：%q", name)
		}
	}
	// 大小写不同也视为同一个名字
	used = map[string]bool{}
	_ = UniqueToolName("GetUser", used)
	if got := UniqueToolName("getuser", used); got == "getuser" {
		t.Fatalf("大小写不同应生成后缀：%q", got)
	}
}

func TestParseDeduplicatesLongOperationIDs(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{`)
	id := strings.Repeat("a", 64)
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&b, `"/p%d":{"get":{"operationId":%q}}`, i, id)
		if i < 1199 {
			b.WriteByte(',')
		}
	}
	b.WriteString("}}")
	spec, err := Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Tools) != 1200 {
		t.Fatalf("工具数 = %d", len(spec.Tools))
	}
	seen := map[string]bool{}
	for _, tool := range spec.Tools {
		if len(tool.Name) > 64 {
			t.Fatalf("工具名超过 64 字节：%q", tool.Name)
		}
		key := strings.ToLower(tool.Name)
		if seen[key] {
			t.Fatalf("工具名重复：%q", tool.Name)
		}
		seen[key] = true
	}
}
