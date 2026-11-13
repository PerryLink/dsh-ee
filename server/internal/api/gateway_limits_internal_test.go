package api

import (
	"bytes"
	"encoding/json"
	"testing"
)

// requestBody 按网关的方式解析请求体：数字保留成 json.Number。
func requestBody(t *testing.T, raw string) map[string]any {
	t.Helper()
	var body map[string]any
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("解析请求体失败：%v", err)
	}
	return body
}

func TestCompletionLimitError(t *testing.T) {
	cases := []struct {
		name string
		body string
		code string // 空表示合法
	}{
		{name: "不传生成参数", body: `{"model":"chat","messages":[]}`},
		{name: "显式 null", body: `{"model":"chat","max_tokens":null,"max_completion_tokens":null,"n":null}`},
		{name: "max_tokens 最小值", body: `{"model":"chat","max_tokens":1}`},
		{name: "max_tokens 恰好等于上限", body: `{"model":"chat","max_tokens":32768}`},
		{name: "max_completion_tokens 恰好等于上限", body: `{"model":"chat","max_completion_tokens":32768}`},
		{name: "max_tokens 超过上限", body: `{"model":"chat","max_tokens":32769}`, code: "max_tokens_too_large"},
		{name: "max_completion_tokens 超过上限", body: `{"model":"chat","max_completion_tokens":1000000}`, code: "max_completion_tokens_too_large"},
		{name: "max_tokens 为 0", body: `{"model":"chat","max_tokens":0}`, code: "invalid_max_tokens"},
		{name: "max_tokens 为负数", body: `{"model":"chat","max_tokens":-1}`, code: "invalid_max_tokens"},
		{name: "max_tokens 为小数", body: `{"model":"chat","max_tokens":1.5}`, code: "invalid_max_tokens"},
		{name: "max_tokens 为字符串", body: `{"model":"chat","max_tokens":"100"}`, code: "invalid_max_tokens"},
		{name: "max_completion_tokens 为 0", body: `{"model":"chat","max_completion_tokens":0}`, code: "invalid_max_completion_tokens"},
		{name: "n=1", body: `{"model":"chat","n":1}`},
		{name: "n>1", body: `{"model":"chat","n":2}`, code: "n_not_supported"},
		{name: "n 很大", body: `{"model":"chat","n":100000}`, code: "n_not_supported"},
		{name: "n=0", body: `{"model":"chat","n":0}`, code: "invalid_n"},
		{name: "n 为负数", body: `{"model":"chat","n":-2}`, code: "invalid_n"},
		{name: "n 为小数", body: `{"model":"chat","n":1.0}`, code: "invalid_n"},
		{name: "n 为字符串", body: `{"model":"chat","n":"2"}`, code: "invalid_n"},
		{name: "先报输出上限再报 n", body: `{"model":"chat","max_tokens":32769,"n":2}`, code: "max_tokens_too_large"},
		{name: "两个输出参数都超限", body: `{"model":"chat","max_tokens":40000,"max_completion_tokens":40000}`, code: "max_tokens_too_large"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, msg := completionLimitError(requestBody(t, tc.body))
			if code != tc.code {
				t.Fatalf("错误码 = %q，期望 %q（msg=%q）", code, tc.code, msg)
			}
			if code == "" && msg != "" {
				t.Fatalf("合法参数不应带错误文案：%q", msg)
			}
			if code != "" && msg == "" {
				t.Fatal("错误码必须带可读文案")
			}
		})
	}
}

// 上限至少要比「最小典型月度额度」（管理端按万分配，示例 10 万）低一个量级，
// 保证一次请求烧不穿整个月额度；同时也不能小到误伤正常长输出。
func TestMaxTokensPerRequestSanity(t *testing.T) {
	if maxTokensPerRequest < 8192 {
		t.Fatalf("单次输出上限 %d 过小，会误伤正常长输出", maxTokensPerRequest)
	}
	if maxTokensPerRequest > 100000 {
		t.Fatalf("单次输出上限 %d 过大，单次请求就可能烧穿一个月的典型额度", maxTokensPerRequest)
	}
	if maxChoicesPerRequest != 1 {
		t.Fatalf("额度和记账按一次请求统计，只能接受 n=1，当前 %d", maxChoicesPerRequest)
	}
}
