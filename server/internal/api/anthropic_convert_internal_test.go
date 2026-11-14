package api

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// ---- 请求转换 ----

func TestAnthropicToChatRequest(t *testing.T) {
	raw := `{
		"model":"chat",
		"max_tokens":256,
		"system":[{"type":"text","text":"你是助手"},{"type":"text","text":"回答简短"}],
		"messages":[
			{"role":"user","content":[{"type":"text","text":"北京天气"},{"type":"text","text":"顺便看看"}]},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"要查天气","signature":"sig"},
				{"type":"text","text":"我查一下"},
				{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"北京"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","tool_use_id":"toolu_1","content":[{"type":"text","text":"晴 25℃"}]},
				{"type":"text","text":"总结"}
			]}
		],
		"tools":[
			{"name":"weather","description":"查天气","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}},
			{"type":"web_search_20250305","name":"web_search"}
		],
		"tool_choice":{"type":"tool","name":"weather","disable_parallel_tool_use":true},
		"stop_sequences":["END"],
		"temperature":0.3,
		"top_p":0.9,
		"top_k":40,
		"thinking":{"type":"enabled","budget_tokens":1024},
		"reasoning_effort":"high",
		"metadata":{"user_id":"u1"},
		"stream":true
	}`
	req := requestBody(t, raw)
	got, err := anthropicToChat(req)
	if err != nil {
		t.Fatalf("转换请求失败：%v", err)
	}
	if got["model"] != "chat" {
		t.Fatalf("model = %v", got["model"])
	}
	if n, ok := got["max_tokens"].(int64); !ok || n != 256 {
		t.Fatalf("max_tokens = %v (%T)", got["max_tokens"], got["max_tokens"])
	}
	if temp, _ := got["temperature"].(float64); temp != 0.3 {
		t.Fatalf("temperature = %v", got["temperature"])
	}
	if topP, _ := got["top_p"].(float64); topP != 0.9 {
		t.Fatalf("top_p = %v", got["top_p"])
	}
	if _, exists := got["top_k"]; exists {
		t.Fatalf("top_k 应被忽略：%v", got["top_k"])
	}
	if stop := got["stop"].([]string); len(stop) != 1 || stop[0] != "END" {
		t.Fatalf("stop = %v", got["stop"])
	}
	if stream, _ := got["stream"].(bool); !stream {
		t.Fatalf("stream 没有透传")
	}
	if user, _ := got["user"].(string); user != "u1" {
		t.Fatalf("metadata.user_id 没有映射成 user：%v", got["user"])
	}
	if thinking := jsonMap(got["thinking"]); jsonStr(thinking["type"]) != "enabled" {
		t.Fatalf("thinking 映射不对：%v", got["thinking"])
	}
	if effort, _ := got["reasoning_effort"].(string); effort != "high" {
		t.Fatalf("reasoning_effort = %v", got["reasoning_effort"])
	}

	messages := jsonSlice(got["messages"])
	if len(messages) != 5 {
		t.Fatalf("messages 数量 = %d，期望 5；%#v", len(messages), messages)
	}
	system := jsonMap(messages[0])
	if system["role"] != "system" || system["content"] != "你是助手\n回答简短" {
		t.Fatalf("system 转换不对：%v", system)
	}
	user := jsonMap(messages[1])
	if user["role"] != "user" || user["content"] != "北京天气\n顺便看看" {
		t.Fatalf("user 文本块合并不对：%v", user)
	}
	assistant := jsonMap(messages[2])
	if assistant["role"] != "assistant" || assistant["content"] != "我查一下" {
		t.Fatalf("assistant 转换不对：%v", assistant)
	}
	if assistant["reasoning_content"] != "要查天气" {
		t.Fatalf("thinking 应映射成 reasoning_content：%v", assistant["reasoning_content"])
	}
	calls := jsonSlice(assistant["tool_calls"])
	if len(calls) != 1 {
		t.Fatalf("tool_calls 数量 = %d", len(calls))
	}
	call := jsonMap(calls[0])
	if call["id"] != "toolu_1" || call["type"] != "function" {
		t.Fatalf("tool_call 不对：%v", call)
	}
	fn := jsonMap(call["function"])
	if fn["name"] != "weather" || fn["arguments"] != `{"city":"北京"}` {
		t.Fatalf("tool_call function 不对：%v", fn)
	}
	toolMsg := jsonMap(messages[3])
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "toolu_1" || toolMsg["content"] != "晴 25℃" {
		t.Fatalf("tool_result 转换不对：%v", toolMsg)
	}
	if followUp := jsonMap(messages[4]); followUp["role"] != "user" || followUp["content"] != "总结" {
		t.Fatalf("tool_result 后的文本应单独成 user 消息：%v", followUp)
	}

	tools := jsonSlice(got["tools"])
	if len(tools) != 1 {
		t.Fatalf("客户端工具数量 = %d，服务端工具应被忽略：%#v", len(tools), tools)
	}
	tool := jsonMap(tools[0])
	toolFn := jsonMap(tool["function"])
	if tool["type"] != "function" || toolFn["name"] != "weather" || jsonMap(toolFn["parameters"]) == nil {
		t.Fatalf("工具转换不对：%v", tool)
	}
	choice := jsonMap(got["tool_choice"])
	if choice["type"] != "function" || jsonMap(choice["function"])["name"] != "weather" {
		t.Fatalf("tool_choice 转换不对：%v", choice)
	}
	if parallel, _ := got["parallel_tool_calls"].(bool); parallel {
		t.Fatalf("disable_parallel_tool_use 应映射成 parallel_tool_calls=false")
	}
}

func TestAnthropicToChatTextHelpers(t *testing.T) {
	// 字符串 content / 字符串 system / 字符串 tool_choice / output_config.effort / reasoning_effort=none。
	req := requestBody(t, `{
		"model":"chat","max_tokens":32,"system":"系统提示",
		"messages":[
			{"role":"user","content":"你好"},
			{"role":"assistant","content":"在"},
			{"role":"user","content":"继续"}
		],
		"tool_choice":"any",
		"output_config":{"effort":"max"}
	}`)
	got, err := anthropicToChat(req)
	if err != nil {
		t.Fatal(err)
	}
	messages := jsonSlice(got["messages"])
	if len(messages) != 4 {
		t.Fatalf("messages = %#v", messages)
	}
	if jsonMap(messages[0])["content"] != "系统提示" || jsonMap(messages[1])["content"] != "你好" {
		t.Fatalf("字符串 content 转换不对：%v", messages)
	}
	if got["tool_choice"] != "required" {
		t.Fatalf(`tool_choice "any" 应映射成 required：%v`, got["tool_choice"])
	}
	if got["reasoning_effort"] != "high" {
		t.Fatalf("output_config.effort=max 应归一成 high：%v", got["reasoning_effort"])
	}

	// 明确 thinking.type=disabled 时不再给上游 reasoning_effort（避免矛盾参数）。
	req = requestBody(t, `{"model":"chat","max_tokens":32,"thinking":{"type":"disabled"},"reasoning_effort":"high",
		"messages":[{"role":"user","content":"hi"}]}`)
	got, err = anthropicToChat(req)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := got["reasoning_effort"]; exists {
		t.Fatalf("thinking 已关闭时不应透传 reasoning_effort：%v", got["reasoning_effort"])
	}

	// reasoning_effort=none 关思考，并且不再透传 reasoning_effort。
	req = requestBody(t, `{"model":"chat","max_tokens":32,"reasoning_effort":"none",
		"messages":[{"role":"user","content":"hi"}]}`)
	got, err = anthropicToChat(req)
	if err != nil {
		t.Fatal(err)
	}
	if jsonStr(jsonMap(got["thinking"])["type"]) != "disabled" {
		t.Fatalf("reasoning_effort=none 应关闭思考：%v", got["thinking"])
	}
	if _, exists := got["reasoning_effort"]; exists {
		t.Fatalf("reasoning_effort=none 不应透传：%v", got["reasoning_effort"])
	}

	// image 块转成 OpenAI 的 image_url 内容段。
	req = requestBody(t, `{"model":"chat","max_tokens":32,"messages":[{"role":"user","content":[
		{"type":"text","text":"看图"},
		{"type":"image","source":{"type":"base64","media_type":"image/png","data":"aGk="}}
	]}]}`)
	got, err = anthropicToChat(req)
	if err != nil {
		t.Fatal(err)
	}
	user := jsonMap(jsonSlice(got["messages"])[0])
	parts := jsonSlice(user["content"])
	if len(parts) != 2 {
		t.Fatalf("图文 user content 应是数组：%v", user["content"])
	}
	if jsonStr(jsonMap(parts[1])["type"]) != "image_url" {
		t.Fatalf("image 块转换不对：%v", parts[1])
	}
	url := jsonStr(jsonMap(jsonMap(parts[1])["image_url"])["url"])
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("image url = %q", url)
	}
}

func TestAnthropicToChatRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string // 期望错误文案里的片段
	}{
		{name: "缺 max_tokens", raw: `{"model":"chat","messages":[{"role":"user","content":"hi"}]}`, want: "max_tokens"},
		{name: "max_tokens 为 0", raw: `{"model":"chat","max_tokens":0,"messages":[{"role":"user","content":"hi"}]}`, want: "max_tokens"},
		{name: "max_tokens 超过上限", raw: `{"model":"chat","max_tokens":32769,"messages":[{"role":"user","content":"hi"}]}`, want: "上限"},
		{name: "max_tokens 是小数", raw: `{"model":"chat","max_tokens":1.5,"messages":[{"role":"user","content":"hi"}]}`, want: "max_tokens"},
		{name: "messages 为空", raw: `{"model":"chat","max_tokens":8,"messages":[]}`, want: "messages"},
		{name: "未知 role", raw: `{"model":"chat","max_tokens":8,"messages":[{"role":"system","content":"x"}]}`, want: "role"},
		{name: "未知块类型", raw: `{"model":"chat","max_tokens":8,"messages":[{"role":"user","content":[{"type":"document"}]}]}`, want: "document"},
		{name: "tool_use 缺 name", raw: `{"model":"chat","max_tokens":8,"messages":[{"role":"assistant","content":[{"type":"tool_use","input":{}}]}]}`, want: "name"},
		{name: "thinking.type 未知", raw: `{"model":"chat","max_tokens":8,"thinking":{"type":"weird"},"messages":[{"role":"user","content":"hi"}]}`, want: "thinking.type"},
		{name: "reasoning_effort 未知", raw: `{"model":"chat","max_tokens":8,"reasoning_effort":"extreme","messages":[{"role":"user","content":"hi"}]}`, want: "reasoning_effort"},
		{name: "tool_choice.type 未知", raw: `{"model":"chat","max_tokens":8,"tool_choice":{"type":"weird"},"messages":[{"role":"user","content":"hi"}]}`, want: "tool_choice"},
		{name: "system 块类型未知", raw: `{"model":"chat","max_tokens":8,"system":[{"type":"image"}],"messages":[{"role":"user","content":"hi"}]}`, want: "system"},
		{name: "stream 不是布尔", raw: `{"model":"chat","max_tokens":8,"stream":"true","messages":[{"role":"user","content":"hi"}]}`, want: "stream"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := anthropicToChat(requestBody(t, tc.raw))
			if err == nil {
				t.Fatal("应返回错误")
			}
			var br anthropicBadRequest
			if !errors.As(err, &br) {
				t.Fatalf("错误类型不对：%T %v", err, err)
			}
			if !strings.Contains(br.msg, tc.want) {
				t.Fatalf("错误文案 %q 不包含 %q", br.msg, tc.want)
			}
		})
	}
}

// ---- 响应转换 ----

func TestChatToAnthropicMessage(t *testing.T) {
	data := []byte(`{
		"id":"chatcmpl-1","model":"chat",
		"choices":[{"message":{
			"role":"assistant","content":"这是答案","reasoning_content":"先想一想",
			"tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"上海\"}"}}]
		},"finish_reason":"tool_calls"}],
		"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":40}}
	}`)
	msg, usage, err := chatToAnthropicMessage(data, "对外模型")
	if err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "message" || msg["role"] != "assistant" || msg["model"] != "对外模型" {
		t.Fatalf("message 基本字段不对：%v", msg)
	}
	if id, _ := msg["id"].(string); !strings.HasPrefix(id, "msg_") {
		t.Fatalf("message id 前缀不对：%q", id)
	}
	if msg["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %v", msg["stop_reason"])
	}
	content := jsonSlice(msg["content"])
	if len(content) != 3 {
		t.Fatalf("content 数量 = %d：%#v", len(content), content)
	}
	thinking := jsonMap(content[0])
	if thinking["type"] != "thinking" || thinking["thinking"] != "先想一想" {
		t.Fatalf("thinking 块不对：%v", thinking)
	}
	if sig, _ := thinking["signature"].(string); sig == "" {
		t.Fatalf("thinking 块缺少 signature：%v", thinking)
	}
	if text := jsonMap(content[1]); text["type"] != "text" || text["text"] != "这是答案" {
		t.Fatalf("text 块不对：%v", text)
	}
	toolUse := jsonMap(content[2])
	if toolUse["type"] != "tool_use" || toolUse["id"] != "call_1" || toolUse["name"] != "weather" {
		t.Fatalf("tool_use 块不对：%v", toolUse)
	}
	if jsonStr(jsonMap(toolUse["input"])["city"]) != "上海" {
		t.Fatalf("tool_use input 不对：%v", toolUse["input"])
	}
	u := jsonMap(msg["usage"])
	if n, _ := u["input_tokens"].(int); n != 60 {
		t.Fatalf("input_tokens = %v（应扣掉缓存命中 40）", u["input_tokens"])
	}
	if n, _ := u["output_tokens"].(int); n != 20 {
		t.Fatalf("output_tokens = %v", u["output_tokens"])
	}
	if n, _ := u["cache_read_input_tokens"].(int); n != 40 {
		t.Fatalf("cache_read_input_tokens = %v", u["cache_read_input_tokens"])
	}
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("记账用量不对：%+v", usage)
	}
}

func TestChatToAnthropicMessageEdgeCases(t *testing.T) {
	// finish_reason=length、没有 usage、工具参数不是合法 JSON。
	data := []byte(`{"choices":[{"message":{"content":"","tool_calls":[
		{"id":"c1","function":{"name":"x","arguments":"not-json"}}]},"finish_reason":"length"}]}`)
	msg, usage, err := chatToAnthropicMessage(data, "m")
	if err != nil {
		t.Fatal(err)
	}
	if msg["stop_reason"] != "tool_use" {
		t.Fatalf("有工具调用时 stop_reason 应是 tool_use：%v", msg["stop_reason"])
	}
	toolUse := jsonMap(jsonSlice(msg["content"])[0])
	if len(jsonMap(toolUse["input"])) != 0 {
		t.Fatalf("非法参数应退化成空对象：%v", toolUse["input"])
	}
	if usage != (tokenUsage{}) {
		t.Fatalf("没有 usage 时不应凭空记用量：%+v", usage)
	}

	// 没有工具时按 finish_reason 映射 stop_reason。
	for finish, want := range map[string]string{
		"stop": "end_turn", "length": "max_tokens", "content_filter": "refusal",
	} {
		data := []byte(`{"choices":[{"message":{"content":"hi"},"finish_reason":"` + finish + `"}]}`)
		msg, _, err := chatToAnthropicMessage(data, "m")
		if err != nil {
			t.Fatal(err)
		}
		if msg["stop_reason"] != want {
			t.Fatalf("finish_reason=%s 映射成 %v，期望 %s", finish, msg["stop_reason"], want)
		}
	}

	if _, _, err := chatToAnthropicMessage([]byte(`{"choices":[]}`), "m"); err == nil {
		t.Fatal("没有 choices 应返回错误")
	}
}

// ---- 流式事件 ----

type sseEvent struct {
	name string
	data map[string]any
}

// parseSSEEvents 把 Anthropic SSE 响应拆成事件列表；用于断言事件顺序与内容。
func parseSSEEvents(t *testing.T, raw string) []sseEvent {
	t.Helper()
	var events []sseEvent
	for _, block := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var name string
		var data map[string]any
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if err := json.Unmarshal([]byte(payload), &data); err != nil {
					t.Fatalf("事件 data 不是合法 JSON：%v；%s", err, payload)
				}
			}
		}
		if name == "" || data == nil {
			t.Fatalf("事件缺少 event 或 data：%q", block)
		}
		if typ := jsonStr(data["type"]); typ != name {
			t.Fatalf("事件名 %q 与 data.type %q 不一致", name, typ)
		}
		events = append(events, sseEvent{name: name, data: data})
	}
	return events
}

func TestRelayAnthropicSSE(t *testing.T) {
	upstream := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"先想"}}]}`,
		`data: {"choices":[{"delta":{"reasoning_content":"一下"}}]}`,
		`data: {"choices":[{"delta":{"content":"你好"}}]}`,
		`data: {"choices":[{"delta":{"content":"，世界"}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"weather","arguments":"{\"city\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"北京\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":6,"total_tokens":17}}`,
		`data: [DONE]`,
	}, "\n\n") + "\n\n"

	rec := httptest.NewRecorder()
	usage, streamed := relayAnthropicSSE(rec, strings.NewReader(upstream), "对外模型", 10)
	raw := rec.Body.String()
	if strings.Contains(raw, "[DONE]") {
		t.Fatalf("不应把 OpenAI 的 [DONE] 转发给 Anthropic 客户端：%s", raw)
	}
	events := parseSSEEvents(t, raw)

	if events[0].name != "message_start" {
		t.Fatalf("第一个事件应是 message_start：%v", events[0].name)
	}
	start := jsonMap(events[0].data["message"])
	if start["model"] != "对外模型" || start["role"] != "assistant" {
		t.Fatalf("message_start 内容不对：%v", start)
	}
	if last := events[len(events)-1]; last.name != "message_stop" {
		t.Fatalf("最后一个事件应是 message_stop：%v", last.name)
	}

	var blocks []struct {
		typ   string
		index int
		id    string
		name  string
	}
	var deltas []string
	for _, ev := range events {
		switch ev.name {
		case "content_block_start":
			block := jsonMap(ev.data["content_block"])
			blocks = append(blocks, struct {
				typ   string
				index int
				id    string
				name  string
			}{jsonStr(block["type"]), int(ev.data["index"].(float64)), jsonStr(block["id"]), jsonStr(block["name"])})
		case "content_block_delta":
			delta := jsonMap(ev.data["delta"])
			switch jsonStr(delta["type"]) {
			case "thinking_delta":
				if jsonStr(delta["thinking"]) == "" {
					t.Fatalf("thinking_delta 为空")
				}
			case "text_delta":
				if jsonStr(delta["text"]) == "" {
					t.Fatalf("text_delta 为空")
				}
			case "signature_delta":
				if jsonStr(delta["signature"]) == "" {
					t.Fatalf("signature_delta 为空")
				}
			case "input_json_delta":
				deltas = append(deltas, jsonStr(delta["partial_json"]))
			default:
				t.Fatalf("未知 delta 类型：%v", delta)
			}
		}
	}
	if len(blocks) != 3 {
		t.Fatalf("内容块数量 = %d：%#v", len(blocks), blocks)
	}
	if blocks[0].typ != "thinking" || blocks[1].typ != "text" || blocks[2].typ != "tool_use" {
		t.Fatalf("内容块顺序不对：%#v", blocks)
	}
	if blocks[0].index != 0 || blocks[1].index != 1 || blocks[2].index != 2 {
		t.Fatalf("内容块 index 应递增：%#v", blocks)
	}
	if blocks[2].id != "call_9" || blocks[2].name != "weather" {
		t.Fatalf("tool_use 块不对：%#v", blocks[2])
	}
	if strings.Join(deltas, "") != `{"city":"北京"}` {
		t.Fatalf("工具参数增量拼接 = %q", strings.Join(deltas, ""))
	}

	// 最后一个非收尾事件是 message_delta，带 stop_reason 与用量。
	var deltaEvent map[string]any
	for _, ev := range events {
		if ev.name == "message_delta" {
			deltaEvent = ev.data
		}
	}
	if deltaEvent == nil {
		t.Fatal("缺少 message_delta")
	}
	if stop := jsonStr(jsonMap(deltaEvent["delta"])["stop_reason"]); stop != "tool_use" {
		t.Fatalf("stop_reason = %q", stop)
	}
	u := jsonMap(deltaEvent["usage"])
	if n, _ := u["output_tokens"].(float64); int(n) != 6 {
		t.Fatalf("output_tokens = %v", u["output_tokens"])
	}
	if n, _ := u["input_tokens"].(float64); int(n) != 11 {
		t.Fatalf("input_tokens = %v", u["input_tokens"])
	}
	if usage != (tokenUsage{PromptTokens: 11, CompletionTokens: 6, TotalTokens: 17}) {
		t.Fatalf("记账用量不对：%+v", usage)
	}
	if streamed <= 0 {
		t.Fatalf("流式增量估算应为正数：%d", streamed)
	}
}

func TestRelayAnthropicSSEWithoutUsage(t *testing.T) {
	// 上游没给 usage（客户端中途停止时也会这样）：按请求估算输入、按已转发增量估算输出。
	upstream := "data: {\"choices\":[{\"delta\":{\"content\":\"hello world\"}}]}\n\n"
	rec := httptest.NewRecorder()
	usage, streamed := relayAnthropicSSE(rec, strings.NewReader(upstream), "m", 9)
	if usage.PromptTokens != 9 {
		t.Fatalf("输入估算 = %d", usage.PromptTokens)
	}
	if usage.CompletionTokens != streamed || streamed <= 0 {
		t.Fatalf("输出估算 = %d（streamed=%d）", usage.CompletionTokens, streamed)
	}
	if usage.TotalTokens != usage.PromptTokens+usage.CompletionTokens {
		t.Fatalf("total = %d", usage.TotalTokens)
	}
	// 上游提前结束也要补 message_delta / message_stop，客户端才知道结束原因。
	events := parseSSEEvents(t, rec.Body.String())
	if last := events[len(events)-1]; last.name != "message_stop" {
		t.Fatalf("缺少 message_stop：%v", last.name)
	}
	if stop := jsonStr(jsonMap(events[len(events)-2].data["delta"])["stop_reason"]); stop != "end_turn" {
		t.Fatalf("默认 stop_reason 应为 end_turn：%q", stop)
	}
}
