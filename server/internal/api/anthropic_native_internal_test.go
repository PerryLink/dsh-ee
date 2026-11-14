package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestAnthropicNativeURL(t *testing.T) {
	cases := map[string]string{
		"https://api.deepseek.com":             "https://api.deepseek.com/anthropic/v1/messages",
		"https://api.deepseek.com/":            "https://api.deepseek.com/anthropic/v1/messages",
		" https://api.deepseek.com ":           "https://api.deepseek.com/anthropic/v1/messages",
		"https://api.deepseek.com/anthropic":   "https://api.deepseek.com/anthropic/v1/messages",
		"https://api.deepseek.com/anthropic/":  "https://api.deepseek.com/anthropic/v1/messages",
		"http://127.0.0.1:8080":                "http://127.0.0.1:8080/anthropic/v1/messages",
		"https://llm-gateway.acme.internal/v1": "https://llm-gateway.acme.internal/v1/anthropic/v1/messages",
	}
	for base, want := range cases {
		if got := anthropicNativeURL(base); got != want {
			t.Errorf("anthropicNativeURL(%q) = %q，期望 %q", base, got, want)
		}
	}
}

func TestAnthropicNativeVendorAllowed(t *testing.T) {
	for _, tc := range []struct {
		vendor string
		want   bool
	}{
		{"deepseek", true},
		{"DeepSeek", true},
		{" deepseek ", true},
		{"custom", false},
		{"", false},
		{"deepseek-ai", false},
	} {
		if got := anthropicNativeVendorAllowed(&store.Provider{Vendor: tc.vendor}); got != tc.want {
			t.Errorf("vendor %q → %v，期望 %v", tc.vendor, got, tc.want)
		}
	}
	if anthropicNativeVendorAllowed(nil) {
		t.Fatal("nil provider 不应走原生透传")
	}
}

func TestSplitAnthropicCandidates(t *testing.T) {
	cands := []store.Candidate{
		{Provider: &store.Provider{Vendor: "custom"}, UpstreamModel: "a"},
		{Provider: &store.Provider{Vendor: "deepseek"}, UpstreamModel: "b"},
		{Provider: &store.Provider{Vendor: "custom"}, UpstreamModel: "c"},
		{Provider: &store.Provider{Vendor: "DeepSeek"}, UpstreamModel: "d"},
	}
	native, converted := splitAnthropicCandidates(cands)
	if len(native) != 2 || native[0].UpstreamModel != "b" || native[1].UpstreamModel != "d" {
		t.Fatalf("native 分组/顺序不对：%+v", native)
	}
	if len(converted) != 2 || converted[0].UpstreamModel != "a" || converted[1].UpstreamModel != "c" {
		t.Fatalf("converted 分组/顺序不对：%+v", converted)
	}
}

const nativeSSEWithUsage = `event: message_start
data: {"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"deepseek-flash","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"你好世界"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":7}}

event: message_stop
data: {"type":"message_stop"}

`

// TestRelayAnthropicNativeSSE 覆盖原生 SSE 的字节级转发 + usage 旁路；输入输出必须完全一致。
func TestRelayAnthropicNativeSSE(t *testing.T) {
	rec := httptest.NewRecorder()
	usage, streamed := relayAnthropicNativeSSE(rec, strings.NewReader(nativeSSEWithUsage), 42)
	if rec.Body.String() != nativeSSEWithUsage {
		t.Fatalf("原生 SSE 被改写：\n%s", rec.Body.String())
	}
	// 记账口径：prompt = input + cache_creation + cache_read = 15，completion = message_delta.output_tokens = 7
	if usage.PromptTokens != 15 || usage.CompletionTokens != 7 || usage.TotalTokens != 22 {
		t.Fatalf("SSE usage = %+v，期望 input 15 / output 7", usage)
	}
	if streamed <= 0 {
		t.Fatalf("text_delta 的估算应为正数：%d", streamed)
	}
}

// 上游没给 usage（原生 SSE 只发正文）时按请求估算输入、按已转发增量估算输出，字节仍原样转发。
func TestRelayAnthropicNativeSSEWithoutUsage(t *testing.T) {
	sse := `event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello world"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"先想一下"}}

`
	rec := httptest.NewRecorder()
	usage, streamed := relayAnthropicNativeSSE(rec, strings.NewReader(sse), 9)
	if rec.Body.String() != sse {
		t.Fatalf("原生 SSE 被改写：\n%s", rec.Body.String())
	}
	if usage.PromptTokens != 9 {
		t.Fatalf("输入估算 = %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != streamed || streamed <= 0 {
		t.Fatalf("输出估算 = %d（streamed=%d）", usage.CompletionTokens, streamed)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total = %d", usage.TotalTokens)
	}
}

func TestNativeResponseUsage(t *testing.T) {
	data := []byte(`{"type":"message","content":[{"type":"text","text":"hi"}],
		"usage":{"input_tokens":100,"cache_creation_input_tokens":5,"cache_read_input_tokens":20,"output_tokens":30}}`)
	usage := nativeResponseUsage(data)
	if usage.PromptTokens != 125 || usage.CompletionTokens != 30 || usage.TotalTokens != 155 {
		t.Fatalf("usage = %+v，期望 125/30/155", usage)
	}
	if got := nativeResponseUsage([]byte(`{"type":"message","content":[]}`)); got != (tokenUsage{}) {
		t.Fatalf("没有 usage 时应返回零值：%+v", got)
	}
	if n := estimateAnthropicNativeContent(data); n <= 0 {
		t.Fatalf("无 usage 时正文估算应为正数：%d", n)
	}
}
