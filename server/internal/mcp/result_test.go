package mcp

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

// 写操作确认的结构化结果：文本与 isError 和旧版完全一致，只多一个 _meta 给第三方 Harness。
func TestTextResultWithMeta(t *testing.T) {
	const text = "这个操作会修改「CRM」里的数据，要先由你本人在网页上确认，现在还没有执行。\n" +
		"请打开 https://ee.example/confirm/42 核对参数并点「同意」，然后让我用完全相同的参数再调用一次。\n" +
		"同意后 10 分钟内有效，只能执行一次；参数变了要重新确认。不要改用别的工具绕过。"
	meta := map[string]any{
		"dsh-ee/confirmation": map[string]any{
			"id":         int64(42),
			"url":        "https://ee.example/confirm/42",
			"expires_at": "2026-11-12T16:00:00Z",
		},
	}
	raw := TextResultWithMeta(text, true, meta)
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("结果不是合法 JSON：%v", err)
	}
	var old map[string]any
	if err := json.Unmarshal(TextResult(text, true), &old); err != nil {
		t.Fatal(err)
	}
	// 旧客户端只读这两个字段：必须一字不差。
	if !reflect.DeepEqual(got["content"], old["content"]) {
		t.Fatalf("content 应与旧结果一致：%#v", got["content"])
	}
	if got["isError"] != true {
		t.Fatalf("isError 应保持 true：%#v", got["isError"])
	}
	// _meta 结构：只有 id/url/expires_at，Harness 不用解析中文文本。
	top, _ := got["_meta"].(map[string]any)
	confirmation, _ := top["dsh-ee/confirmation"].(map[string]any)
	if confirmation == nil {
		t.Fatalf("缺少 _meta['dsh-ee/confirmation']：%s", raw)
	}
	if len(confirmation) != 3 {
		t.Fatalf("确认 _meta 只该有 id/url/expires_at：%#v", confirmation)
	}
	if confirmation["id"] != float64(42) || confirmation["url"] != "https://ee.example/confirm/42" || confirmation["expires_at"] != "2026-11-12T16:00:00Z" {
		t.Fatalf("确认 _meta 内容不对：%#v", confirmation)
	}
	// 不该把参数、摘要、凭据混进结果（连文本里也不该出现这些词）。
	for _, leak := range []string{"args", "sha256", "token", "password", "user_id", "secret"} {
		if bytes.Contains(raw, []byte(leak)) {
			t.Fatalf("结果里不该出现 %q：%s", leak, raw)
		}
	}
}

func TestTextResultWithoutMeta(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal(TextResult("已完成", false), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["_meta"]; ok {
		t.Fatalf("普通结果不该多出 _meta：%#v", got)
	}
	if got["isError"] != false {
		t.Fatalf("isError：%#v", got["isError"])
	}
}
