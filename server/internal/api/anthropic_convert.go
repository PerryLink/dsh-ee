package api

// Anthropic Messages 与 OpenAI chat completions 之间的转换层。
//
// 只做纯函数转换，不碰 HTTP / 数据库，方便单测；HTTP 处理与 SSE 转换见 anthropic.go。
// 覆盖范围（v0）：system、messages 的 text / tool_use / tool_result / thinking、
// tools / tool_choice、max_tokens / temperature / top_p / stop_sequences、
// stream、thinking / reasoning_effort（含 output_config.effort）。
// 不支持的块类型返回 400；服务端工具（web_search 等）由 Anthropic 侧执行，这里忽略。

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// anthropicBadRequest 表示客户端请求不合法；处理函数按 Anthropic 的 invalid_request_error 返回。
type anthropicBadRequest struct{ msg string }

func (e anthropicBadRequest) Error() string { return e.msg }

func badAnthropicRequest(format string, args ...any) error {
	return anthropicBadRequest{msg: fmt.Sprintf(format, args...)}
}

// ---- 动态 JSON 小工具（请求体用 UseNumber 解码，未知字段保持原样） ----

func jsonMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func jsonSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func jsonStr(v any) string {
	s, _ := v.(string)
	return s
}

// floatParam 读数字参数。present 表示字段存在且不是 null；ok 表示能解析成数字。
func floatParam(body map[string]any, field string) (v float64, present, ok bool) {
	raw, exists := body[field]
	if !exists || raw == nil {
		return 0, false, false
	}
	switch n := raw.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, true, err == nil
	case float64:
		return n, true, true
	default:
		return 0, true, false
	}
}

func newAnthropicID(prefix string) string {
	b := make([]byte, 12)
	_, _ = rand.Read(b) // crypto/rand 基本不会失败；失败时也只是一串 0，不影响协议
	return prefix + hex.EncodeToString(b)
}

// anthropicThinkingSignature 给 thinking 块生成一个不透明签名。
// dsh-ee 不做签名校验，客户端回传时忽略；给一个稳定值是为了满足要求签名存在的客户端。
func anthropicThinkingSignature(text string) string {
	sum := sha256.Sum256([]byte(text))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// ---- 请求：Anthropic Messages → OpenAI chat completions ----

// anthropicToChat 把 Anthropic Messages 请求体转成 OpenAI chat completions 请求体。
// 返回的 body 里 model 仍是客户端请求的对外模型名，由 openChatUpstream 换成上游模型名。
func anthropicToChat(req map[string]any) (map[string]any, error) {
	out := map[string]any{"model": jsonStr(req["model"])}

	// max_tokens：Anthropic 必填，且同样受单次输出上限约束（与 OpenAI 网关共用额度口径）。
	maxTokens, present, ok := intParam(req, "max_tokens")
	switch {
	case !present:
		return nil, badAnthropicRequest("缺少 max_tokens：Anthropic Messages 请求必须带正整数 max_tokens")
	case !ok || maxTokens < 1:
		return nil, badAnthropicRequest("max_tokens 必须是正整数")
	case maxTokens > maxTokensPerRequest:
		return nil, badAnthropicRequest("max_tokens 超过单次上限 %d，请调小", maxTokensPerRequest)
	}
	out["max_tokens"] = maxTokens

	// messages：system 拼成一条 system 消息放在最前面，其余按角色转换。
	rawMessages := jsonSlice(req["messages"])
	if len(rawMessages) == 0 {
		return nil, badAnthropicRequest("messages 必须是非空数组")
	}
	messages := make([]any, 0, len(rawMessages)+1)
	if system, err := anthropicSystemMessage(req["system"]); err != nil {
		return nil, err
	} else if system != nil {
		messages = append(messages, system)
	}
	converted, err := anthropicMessages(rawMessages)
	if err != nil {
		return nil, err
	}
	messages = append(messages, converted...)
	out["messages"] = messages

	// 采样参数。top_k 在 OpenAI chat 里没有对应参数，忽略（同 DeepSeek 的 Anthropic 兼容层）。
	if v, present, ok := floatParam(req, "temperature"); present {
		if !ok {
			return nil, badAnthropicRequest("temperature 必须是数字")
		}
		out["temperature"] = v
	}
	if v, present, ok := floatParam(req, "top_p"); present {
		if !ok {
			return nil, badAnthropicRequest("top_p 必须是数字")
		}
		out["top_p"] = v
	}
	if raw, exists := req["stop_sequences"]; exists && raw != nil {
		stop, err := anthropicStopSequences(raw)
		if err != nil {
			return nil, err
		}
		if len(stop) > 0 {
			out["stop"] = stop
		}
	}

	// tools / tool_choice。
	tools, err := anthropicTools(req["tools"])
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		out["tools"] = tools
	}
	choice, parallel, err := anthropicToolChoice(req["tool_choice"])
	if err != nil {
		return nil, err
	}
	if choice != nil {
		out["tool_choice"] = choice
	}
	if parallel {
		out["parallel_tool_calls"] = false
	}

	// 思考开关与强度。
	if err := applyAnthropicThinking(req, out); err != nil {
		return nil, err
	}

	// metadata.user_id 映射到 OpenAI 的 user，便于上游按终端用户做隔离/限流。
	if meta := jsonMap(req["metadata"]); meta != nil {
		if user := jsonStr(meta["user_id"]); user != "" {
			out["user"] = user
		}
	}

	if raw, exists := req["stream"]; exists && raw != nil {
		stream, ok := raw.(bool)
		if !ok {
			return nil, badAnthropicRequest("stream 必须是布尔值")
		}
		if stream {
			out["stream"] = true
		}
	}
	return out, nil
}

// anthropicSystemMessage 把 system（字符串或文本块数组）转成 OpenAI 的 system 消息。
func anthropicSystemMessage(raw any) (any, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, nil
		}
		return map[string]any{"role": "system", "content": v}, nil
	case []any:
		texts := make([]string, 0, len(v))
		for i, item := range v {
			block := jsonMap(item)
			if block == nil {
				return nil, badAnthropicRequest("system 第 %d 项不是对象", i)
			}
			typ := jsonStr(block["type"])
			if typ != "" && typ != "text" {
				return nil, badAnthropicRequest("system 不支持内容块类型 %q", typ)
			}
			texts = append(texts, jsonStr(block["text"]))
		}
		content := strings.Join(texts, "\n")
		if strings.TrimSpace(content) == "" {
			return nil, nil
		}
		return map[string]any{"role": "system", "content": content}, nil
	default:
		return nil, badAnthropicRequest("system 必须是字符串或文本块数组")
	}
}

// anthropicMessages 按角色转换 messages；一个 Anthropic user 消息可能展开成多条 OpenAI 消息
// （tool_result 要变成独立的 tool 消息），所以返回切片。
func anthropicMessages(raw []any) ([]any, error) {
	out := make([]any, 0, len(raw))
	for i, item := range raw {
		msg := jsonMap(item)
		if msg == nil {
			return nil, badAnthropicRequest("messages 第 %d 项不是对象", i)
		}
		switch role := jsonStr(msg["role"]); role {
		case "user":
			msgs, err := anthropicUserMessages(i, msg["content"])
			if err != nil {
				return nil, err
			}
			out = append(out, msgs...)
		case "assistant":
			m, err := anthropicAssistantMessage(i, msg["content"])
			if err != nil {
				return nil, err
			}
			out = append(out, m)
		default:
			return nil, badAnthropicRequest("messages 第 %d 项 role 不支持：%q（只支持 user / assistant）", i, role)
		}
	}
	return out, nil
}

// anthropicUserMessages 转换 user 消息：text / image 是正文，tool_result 提成独立的 tool 消息
// （OpenAI 要求 tool 消息紧跟带 tool_calls 的 assistant 消息）。
func anthropicUserMessages(index int, raw any) ([]any, error) {
	if text, ok := raw.(string); ok {
		if text == "" {
			return nil, badAnthropicRequest("messages 第 %d 项 user 消息内容为空", index)
		}
		return []any{map[string]any{"role": "user", "content": text}}, nil
	}
	blocks := jsonSlice(raw)
	if blocks == nil {
		return nil, badAnthropicRequest("messages 第 %d 项 content 必须是字符串或内容块数组", index)
	}

	var toolMessages []any
	var parts []any
	hasImage := false
	for j, item := range blocks {
		block := jsonMap(item)
		if block == nil {
			return nil, badAnthropicRequest("messages 第 %d 项第 %d 个内容块不是对象", index, j)
		}
		switch jsonStr(block["type"]) {
		case "text":
			if text := jsonStr(block["text"]); text != "" {
				parts = append(parts, map[string]any{"type": "text", "text": text})
			}
		case "image":
			part, err := anthropicImagePart(block)
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
			hasImage = true
		case "tool_result":
			toolMsg, err := anthropicToolMessage(index, j, block)
			if err != nil {
				return nil, err
			}
			toolMessages = append(toolMessages, toolMsg)
		case "thinking", "redacted_thinking":
			// user 消息里出现思考块不符合 Anthropic 协议；忽略，避免把脏数据转发给上游。
		default:
			return nil, badAnthropicRequest("messages 第 %d 项第 %d 个内容块类型 %q 暂不支持", index, j, jsonStr(block["type"]))
		}
	}

	out := toolMessages
	if len(parts) > 0 {
		var content any
		if hasImage {
			// 有图片时用 OpenAI 的多段 content；text 段保持原始顺序。
			content = parts
		} else {
			texts := make([]string, 0, len(parts))
			for _, part := range parts {
				texts = append(texts, jsonStr(jsonMap(part)["text"]))
			}
			content = strings.Join(texts, "\n")
		}
		out = append(out, map[string]any{"role": "user", "content": content})
	}
	if len(out) == 0 {
		return nil, badAnthropicRequest("messages 第 %d 项 user 消息没有可用内容", index)
	}
	return out, nil
}

// anthropicToolMessage 把 tool_result 块转成 OpenAI 的 tool 消息。
func anthropicToolMessage(index, blockIndex int, block map[string]any) (any, error) {
	id := jsonStr(block["tool_use_id"])
	if id == "" {
		return nil, badAnthropicRequest("messages 第 %d 项第 %d 个 tool_result 缺少 tool_use_id", index, blockIndex)
	}
	content, err := anthropicToolResultContent(block["content"])
	if err != nil {
		return nil, err
	}
	return map[string]any{"role": "tool", "tool_call_id": id, "content": content}, nil
}

// anthropicToolResultContent 把 tool_result 的内容压成文本。
// OpenAI 的 tool 消息不支持图片，图片用占位符标出来，避免静默丢内容。
func anthropicToolResultContent(raw any) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []any:
		var b strings.Builder
		for _, item := range v {
			block := jsonMap(item)
			if block == nil {
				return "", badAnthropicRequest("tool_result content 里有不是对象的内容块")
			}
			switch jsonStr(block["type"]) {
			case "text":
				b.WriteString(jsonStr(block["text"]))
			case "image":
				b.WriteString("[image]")
			default:
				b.WriteString("[" + jsonStr(block["type"]) + "]")
			}
		}
		return b.String(), nil
	default:
		return "", badAnthropicRequest("tool_result content 必须是字符串或内容块数组")
	}
}

// anthropicImagePart 把 Anthropic 的 image 块转成 OpenAI 的 image_url 内容段。
func anthropicImagePart(block map[string]any) (any, error) {
	source := jsonMap(block["source"])
	if source == nil {
		return nil, badAnthropicRequest("image 块缺少 source")
	}
	switch typ := jsonStr(source["type"]); typ {
	case "base64":
		mediaType := jsonStr(source["media_type"])
		data := jsonStr(source["data"])
		if mediaType == "" || data == "" {
			return nil, badAnthropicRequest("image base64 source 缺少 media_type 或 data")
		}
		return map[string]any{"type": "image_url", "image_url": map[string]any{
			"url": "data:" + mediaType + ";base64," + data,
		}}, nil
	case "url":
		url := jsonStr(source["url"])
		if url == "" {
			return nil, badAnthropicRequest("image url source 缺少 url")
		}
		return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}, nil
	default:
		return nil, badAnthropicRequest("image source.type %q 暂不支持（支持 base64 / url）", typ)
	}
}

// anthropicAssistantMessage 转换 assistant 消息：text / thinking / tool_use。
func anthropicAssistantMessage(index int, raw any) (any, error) {
	if text, ok := raw.(string); ok {
		if text == "" {
			return nil, badAnthropicRequest("messages 第 %d 项 assistant 消息内容为空", index)
		}
		return map[string]any{"role": "assistant", "content": text}, nil
	}
	blocks := jsonSlice(raw)
	if blocks == nil {
		return nil, badAnthropicRequest("messages 第 %d 项 content 必须是字符串或内容块数组", index)
	}

	var content strings.Builder
	var reasoning strings.Builder
	var toolCalls []any
	for j, item := range blocks {
		block := jsonMap(item)
		if block == nil {
			return nil, badAnthropicRequest("messages 第 %d 项第 %d 个内容块不是对象", index, j)
		}
		switch typ := jsonStr(block["type"]); typ {
		case "text":
			content.WriteString(jsonStr(block["text"]))
		case "thinking":
			// OpenAI 兼容生态（DeepSeek 等）用 reasoning_content 回传上一轮思考；
			// 带 tools 时必须完整回传，否则部分上游会 400。
			reasoning.WriteString(jsonStr(block["thinking"]))
		case "redacted_thinking":
			// 加密思考内容无法还原成文本，忽略（Claude Code 不会在带 tools 的续轮里依赖它）。
		case "tool_use":
			name := jsonStr(block["name"])
			if name == "" {
				return nil, badAnthropicRequest("messages 第 %d 项第 %d 个 tool_use 缺少 name", index, j)
			}
			input := block["input"]
			var args []byte
			if input == nil {
				args = []byte("{}")
			} else {
				if jsonMap(input) == nil {
					return nil, badAnthropicRequest("messages 第 %d 项第 %d 个 tool_use 的 input 必须是对象", index, j)
				}
				b, err := json.Marshal(input)
				if err != nil {
					return nil, badAnthropicRequest("messages 第 %d 项第 %d 个 tool_use 的 input 无法序列化", index, j)
				}
				args = b
			}
			id := jsonStr(block["id"])
			if id == "" {
				// Anthropic 的 tool_use 必须带 id；缺了补一个，保证下一轮 tool_result 能按 id 对上。
				id = newAnthropicID("toolu_")
			}
			toolCalls = append(toolCalls, map[string]any{
				"id": id, "type": "function",
				"function": map[string]any{"name": name, "arguments": string(args)},
			})
		case "server_tool_use", "web_search_tool_result", "web_fetch_tool_result",
			"code_execution_tool_result", "mcp_tool_use", "mcp_tool_result", "container_upload":
			return nil, badAnthropicRequest("messages 第 %d 项第 %d 个内容块类型 %q 暂不支持（服务端工具/文件块不在 v0 范围）", index, j, typ)
		default:
			return nil, badAnthropicRequest("messages 第 %d 项第 %d 个内容块类型 %q 暂不支持", index, j, typ)
		}
	}
	if content.Len() == 0 && reasoning.Len() == 0 && len(toolCalls) == 0 {
		return nil, badAnthropicRequest("messages 第 %d 项 assistant 消息没有可用内容", index)
	}

	msg := map[string]any{"role": "assistant", "content": content.String()}
	if reasoning.Len() > 0 {
		msg["reasoning_content"] = reasoning.String()
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	return msg, nil
}

// anthropicTools 转换客户端自定义工具。服务端工具（type=web_search_20250305 等）
// 由 Anthropic 侧执行，dsh-ee 不实现，直接忽略——报 400 会让整个 Claude Code 会话不可用。
func anthropicTools(raw any) ([]any, error) {
	if raw == nil {
		return nil, nil
	}
	blocks := jsonSlice(raw)
	if blocks == nil {
		return nil, badAnthropicRequest("tools 必须是数组")
	}
	out := make([]any, 0, len(blocks))
	for i, item := range blocks {
		tool := jsonMap(item)
		if tool == nil {
			return nil, badAnthropicRequest("tools 第 %d 项不是对象", i)
		}
		if typ := jsonStr(tool["type"]); typ != "" && typ != "custom" {
			continue
		}
		name := jsonStr(tool["name"])
		if name == "" {
			return nil, badAnthropicRequest("tools 第 %d 项缺少 name", i)
		}
		fn := map[string]any{"name": name}
		if desc := jsonStr(tool["description"]); desc != "" {
			fn["description"] = desc
		}
		if schema, exists := tool["input_schema"]; exists && schema != nil {
			if jsonMap(schema) == nil {
				return nil, badAnthropicRequest("tools 第 %d 项 input_schema 必须是对象", i)
			}
			fn["parameters"] = schema
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out, nil
}

// anthropicToolChoice 转换 tool_choice，返回 OpenAI 的 tool_choice 和是否禁用并行工具调用。
func anthropicToolChoice(raw any) (any, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	switch v := raw.(type) {
	case string:
		switch v {
		case "auto", "none":
			return v, false, nil
		case "any":
			return "required", false, nil
		default:
			return nil, false, badAnthropicRequest("tool_choice 不支持 %q", v)
		}
	case map[string]any:
		parallel, _ := v["disable_parallel_tool_use"].(bool)
		switch typ := jsonStr(v["type"]); typ {
		case "auto":
			return "auto", parallel, nil
		case "none":
			return "none", parallel, nil
		case "any":
			return "required", parallel, nil
		case "tool":
			name := jsonStr(v["name"])
			if name == "" {
				return nil, false, badAnthropicRequest("tool_choice 为 tool 时必须带 name")
			}
			return map[string]any{"type": "function", "function": map[string]any{"name": name}}, parallel, nil
		default:
			return nil, false, badAnthropicRequest("tool_choice.type %q 不支持", typ)
		}
	default:
		return nil, false, badAnthropicRequest("tool_choice 必须是字符串或对象")
	}
}

// anthropicEfforts 把 Anthropic 侧的思考强度归一到 OpenAI 常用的取值；
// 更细的 xhigh/max/ultra 统一按 high 处理，避免不认识的取值导致上游 400。
var anthropicEfforts = map[string]string{
	"minimal": "minimal",
	"low":     "low",
	"medium":  "medium",
	"high":    "high",
	"xhigh":   "high",
	"max":     "high",
	"ultra":   "high",
}

// applyAnthropicThinking 把 thinking 与 reasoning_effort 映射成 OpenAI 侧的
// thinking 开关（DeepSeek 等 OpenAI 兼容上游的扩展参数）与 reasoning_effort。
func applyAnthropicThinking(req, out map[string]any) error {
	thinkingDisabled := false
	if raw, exists := req["thinking"]; exists && raw != nil {
		block := jsonMap(raw)
		if block == nil {
			return badAnthropicRequest("thinking 必须是对象")
		}
		switch typ := jsonStr(block["type"]); typ {
		case "enabled", "adaptive":
			out["thinking"] = map[string]any{"type": "enabled"}
		case "disabled":
			out["thinking"] = map[string]any{"type": "disabled"}
			thinkingDisabled = true
		default:
			return badAnthropicRequest("thinking.type %q 不支持（支持 enabled / disabled / adaptive）", typ)
		}
	}

	// reasoning_effort 是 DeepSeek 风格的扩展字段；Anthropic 官方用 output_config.effort，两者都接受。
	effort := ""
	if raw, exists := req["reasoning_effort"]; exists && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return badAnthropicRequest("reasoning_effort 必须是字符串")
		}
		effort = s
	} else if oc := jsonMap(req["output_config"]); oc != nil {
		if s, ok := oc["effort"].(string); ok {
			effort = s
		}
	}
	if effort == "" {
		return nil
	}
	if effort == "none" {
		// 关掉思考：OpenAI 兼容上游用 thinking.type 表达，比 reasoning_effort=none 更通用。
		out["thinking"] = map[string]any{"type": "disabled"}
		return nil
	}
	if thinkingDisabled {
		// 客户端明确 thinking.type=disabled 时，effort 没有意义，避免给上游一对矛盾参数。
		return nil
	}
	normalized, ok := anthropicEfforts[effort]
	if !ok {
		return badAnthropicRequest("reasoning_effort 不支持 %q（支持 none / minimal / low / medium / high，xhigh/max/ultra 按 high）", effort)
	}
	out["reasoning_effort"] = normalized
	return nil
}

// anthropicStopSequences 校验 stop_sequences（Anthropic 最多 4 条，宽松处理）。
func anthropicStopSequences(raw any) ([]string, error) {
	items := jsonSlice(raw)
	if items == nil {
		return nil, badAnthropicRequest("stop_sequences 必须是字符串数组")
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		s, ok := item.(string)
		if !ok || s == "" {
			return nil, badAnthropicRequest("stop_sequences 第 %d 项必须是非空字符串", i)
		}
		out = append(out, s)
	}
	return out, nil
}

// ---- 响应：OpenAI chat completions → Anthropic message ----

// openAIUsage 是 OpenAI usage 里记账需要的字段。
type openAIUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// openAIToolCall 同时覆盖非流式响应与流式增量里的 tool_calls。
type openAIToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// openAIStreamChunk 是流式与非流式共用的最小响应结构（只取转换需要用的字段）。
type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			Reasoning        string           `json:"reasoning"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			Reasoning        string           `json:"reasoning"`
			ToolCalls        []openAIToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

// chatToAnthropicMessage 把一次非流式 OpenAI chat completions 响应转成 Anthropic message。
// 第二个返回值是记账用的 token 用量（prompt 含缓存命中，保持与 OpenAI 网关一致的口径）。
func chatToAnthropicMessage(data []byte, model string) (map[string]any, tokenUsage, error) {
	var resp openAIStreamChunk
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, tokenUsage{}, err
	}
	if len(resp.Choices) == 0 {
		return nil, tokenUsage{}, errNoChoices
	}
	msg := resp.Choices[0].Message

	reasoning := msg.ReasoningContent
	if reasoning == "" {
		reasoning = msg.Reasoning
	}
	content := make([]any, 0, 3)
	if reasoning != "" {
		content = append(content, map[string]any{
			"type": "thinking", "thinking": reasoning, "signature": anthropicThinkingSignature(reasoning),
		})
	}
	if msg.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, tc := range msg.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    anthropicToolUseID(tc.ID),
			"name":  tc.Function.Name,
			"input": anthropicToolInput(tc.Function.Arguments),
		})
	}

	usage := tokenUsage{}
	inputTokens, cacheRead := 0, 0
	if resp.Usage != nil {
		usage = tokenUsage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		}
		cacheRead = 0
		if d := resp.Usage.PromptTokensDetails; d != nil && d.CachedTokens > 0 {
			cacheRead = d.CachedTokens
		}
		inputTokens = resp.Usage.PromptTokens - cacheRead
		if inputTokens < 0 {
			inputTokens = 0
		}
	} else {
		usage = tokenUsage{CompletionTokens: 0}
	}

	out := map[string]any{
		"id":            newAnthropicID("msg_"),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   anthropicStopReason(resp.Choices[0].FinishReason, len(msg.ToolCalls) > 0),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               usage.CompletionTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     cacheRead,
		},
	}
	return out, usage, nil
}

// anthropicToolUseID 保留上游 tool_call id（客户端会原样带回），缺失时补一个 Anthropic 风格 id。
func anthropicToolUseID(id string) string {
	if id == "" {
		return newAnthropicID("toolu_")
	}
	return id
}

// anthropicToolInput 把 OpenAI 的 arguments JSON 字符串还原成 Anthropic tool_use 的 input 对象。
// 参数不是合法 JSON 对象时退化成空对象（协议要求 input 是对象），不因上游格式问题让整次调用失败。
func anthropicToolInput(arguments string) map[string]any {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}
	}
	var parsed any
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil {
		return map[string]any{}
	}
	if m, ok := parsed.(map[string]any); ok {
		return m
	}
	return map[string]any{"value": parsed}
}

// anthropicStopReason 把 OpenAI 的 finish_reason 映射成 Anthropic 的 stop_reason。
func anthropicStopReason(finish string, hasTools bool) string {
	if hasTools {
		return "tool_use"
	}
	switch finish {
	case "length":
		return "max_tokens"
	case "content_filter":
		return "refusal"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

var errNoChoices = fmt.Errorf("上游返回没有 choices")
