package api_test

// Anthropic Messages 兼容端点（POST /v1/messages）的集成测试：
// 用 fake 上游同时验证「Anthropic 请求 → OpenAI chat」与「OpenAI 响应/SSE → Anthropic」
// 两个方向，以及鉴权、额度、限流、错误壳和用量记账。

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// newAnthropicFixture 和 newPrincipalFixture 一样建库、登录管理员、建上游，
// 但允许传自定义 handler（fake 上游要按用例返回思考/工具/流式/错误响应）并调整 Options（限流）。
func newAnthropicFixture(t *testing.T, upstream http.Handler, mutate func(*api.Options)) *principalFixture {
	t.Helper()
	return newAnthropicFixtureVendor(t, upstream, "", mutate)
}

// newAnthropicFixtureVendor 同上，但可指定上游 provider 的 vendor（原生透传用例要 deepseek）。
func newAnthropicFixtureVendor(t *testing.T, upstream http.Handler, vendor string, mutate func(*api.Options)) *principalFixture {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(upstream)
	t.Cleanup(up.Close)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	opts := api.Options{DataDir: t.TempDir(), Upstream: up.Client()}
	if mutate != nil {
		mutate(&opts)
	}
	srv := api.New(st, box, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	f := &principalFixture{st: st, srv: srv, ts: ts, admin: newClient(t, ts.URL)}
	code, body := f.admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	providerReq := map[string]any{
		"name": "anthropic-upstream", "base_url": up.URL, "api_key": upstreamSecret, "models": []string{"chat"},
	}
	if vendor != "" {
		providerReq["vendor"] = vendor
	}
	code, body = f.admin.do("POST", "/api/admin/providers", providerReq, nil)
	expect(t, "创建模型上游", code, 201, body)
	return f
}

// chatUpstreamHandler 校验上游鉴权并解码请求体；返回 false 表示已经回过错误。
func chatUpstreamHandler(t *testing.T, w http.ResponseWriter, r *http.Request, body *map[string]any) bool {
	t.Helper()
	if r.URL.Path != "/chat/completions" {
		http.NotFound(w, r)
		return false
	}
	if r.Header.Get("Authorization") != "Bearer "+upstreamSecret {
		t.Errorf("上游收到的 Authorization 不对：%q", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(body); err != nil {
		t.Errorf("上游收到的请求体不是 JSON：%v", err)
		w.WriteHeader(http.StatusBadRequest)
		return false
	}
	return true
}

func postAnthropic(t *testing.T, base, token string, body any) (int, []byte) {
	t.Helper()
	return postAnthropicPath(t, base, "/v1/messages", token, body)
}

// postAnthropicPath 允许换端点路径（/v1/messages 或 DeepSeek 风格别名 /anthropic/v1/messages）。
func postAnthropicPath(t *testing.T, base, path, token string, body any) (int, []byte) {
	t.Helper()
	return newClient(t, base).do("POST", path, body, map[string]string{"X-Api-Key": token})
}

// doRawAnthropic 直接发字符串请求体（带自定义头），用来测非法 JSON 这类客户端用例。
func doRawAnthropic(c *client, raw string, header map[string]string) (int, []byte) {
	c.t.Helper()
	req, err := http.NewRequest("POST", c.base+"/v1/messages", strings.NewReader(raw))
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("POST /v1/messages: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

type anthropicErrorEnvelope struct {
	Type  string `json:"type"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// assertAnthropicErr 断言响应是 Anthropic 错误壳，并检查状态码、错误类型与文案片段。
func assertAnthropicErr(t *testing.T, what string, status, wantStatus int, body []byte, wantType, wantMsgPart string) {
	t.Helper()
	if status != wantStatus {
		t.Fatalf("%s：状态码 %d，期望 %d；响应 %s", what, status, wantStatus, body)
	}
	var env anthropicErrorEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("%s：错误体不是 JSON：%v；%s", what, err, body)
	}
	if env.Type != "error" || env.Error.Type != wantType {
		t.Fatalf("%s：错误壳不对：%s", what, body)
	}
	if env.Error.Message == "" || (wantMsgPart != "" && !strings.Contains(env.Error.Message, wantMsgPart)) {
		t.Fatalf("%s：错误文案 %q 不包含 %q", what, env.Error.Message, wantMsgPart)
	}
}

// TestAnthropicMessagesConversionAndUsage 覆盖请求/响应两个方向的完整转换和用量记账：
// system、text、tool_use、tool_result、thinking、tools/tool_choice、采样参数。
func TestAnthropicMessagesConversionAndUsage(t *testing.T) {
	upstreamBody := make(chan map[string]any, 1)
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		upstreamBody <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id":"chatcmpl-1","model":"chat",
			"choices":[{"message":{
				"role":"assistant","content":"这是答案","reasoning_content":"先想一想",
				"tool_calls":[{"id":"call_1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"上海\"}"}}]
			},"finish_reason":"tool_calls"}],
			"usage":{"prompt_tokens":100,"completion_tokens":20,"total_tokens":120,"prompt_tokens_details":{"cached_tokens":40}}
		}`)
	})
	f := newAnthropicFixture(t, upstream, nil)
	u := f.user(t, "anthropic-convert", nil)
	f.token(t, u.ID, "ee_at_anthropic", []string{rbac.ScopeModels}, nil, nil)

	reqBody := map[string]any{
		"model": "chat", "max_tokens": 256, "system": "你是助手",
		"messages": []map[string]any{
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "北京天气怎么样"},
			}},
			{"role": "assistant", "content": []map[string]any{
				{"type": "thinking", "thinking": "要查天气", "signature": "sig"},
				{"type": "text", "text": "我查一下"},
				{"type": "tool_use", "id": "toolu_1", "name": "weather", "input": map[string]any{"city": "北京"}},
			}},
			{"role": "user", "content": []map[string]any{
				{"type": "tool_result", "tool_use_id": "toolu_1", "content": []map[string]any{
					{"type": "text", "text": "晴 25℃"},
				}},
			}},
		},
		"tools": []map[string]any{
			{"name": "weather", "description": "查天气", "input_schema": map[string]any{"type": "object"}},
		},
		"tool_choice":      map[string]any{"type": "tool", "name": "weather"},
		"temperature":      0.2,
		"top_p":            0.8,
		"stop_sequences":   []string{"END"},
		"thinking":         map[string]any{"type": "enabled", "budget_tokens": 1024},
		"reasoning_effort": "high",
		"metadata":         map[string]any{"user_id": "u1"},
	}

	code, body := postAnthropic(t, f.ts.URL, "ee_at_anthropic", reqBody)
	expect(t, "Anthropic 非流式调用", code, 200, body)

	// 上游实际收到的应是转换后的 OpenAI chat 请求。
	got := <-upstreamBody
	if got["model"] != "chat" {
		t.Fatalf("上游模型 = %v", got["model"])
	}
	if n, _ := got["max_tokens"].(json.Number); n.String() != "256" {
		t.Fatalf("max_tokens = %v (%T)", got["max_tokens"], got["max_tokens"])
	}
	if temp, _ := got["temperature"].(json.Number); temp.String() != "0.2" {
		t.Fatalf("temperature = %v", got["temperature"])
	}
	if topP, _ := got["top_p"].(json.Number); topP.String() != "0.8" {
		t.Fatalf("top_p = %v", got["top_p"])
	}
	if user, _ := got["user"].(string); user != "u1" {
		t.Fatalf("metadata.user_id 没有映射成 user：%v", got["user"])
	}
	if thinking, _ := got["thinking"].(map[string]any); thinking["type"] != "enabled" {
		t.Fatalf("thinking 没有转换：%v", got["thinking"])
	}
	if effort, _ := got["reasoning_effort"].(string); effort != "high" {
		t.Fatalf("reasoning_effort = %v", got["reasoning_effort"])
	}
	msgs, _ := got["messages"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("上游 messages 数量 = %d，期望 4；%#v", len(msgs), msgs)
	}
	system, _ := msgs[0].(map[string]any)
	user, _ := msgs[1].(map[string]any)
	assistant, _ := msgs[2].(map[string]any)
	toolMsg, _ := msgs[3].(map[string]any)
	if system["role"] != "system" || !strings.Contains(system["content"].(string), "你是助手") {
		t.Fatalf("system 消息不对：%v", system)
	}
	if user["role"] != "user" || user["content"] != "北京天气怎么样" {
		t.Fatalf("user 消息不对：%v", user)
	}
	if assistant["role"] != "assistant" || assistant["content"] != "我查一下" || assistant["reasoning_content"] != "要查天气" {
		t.Fatalf("assistant 消息不对：%v", assistant)
	}
	calls, _ := assistant["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %v", assistant["tool_calls"])
	}
	call, _ := calls[0].(map[string]any)
	if call["id"] != "toolu_1" || call["type"] != "function" {
		t.Fatalf("tool_call 不对：%v", call)
	}
	fn, _ := call["function"].(map[string]any)
	if fn["name"] != "weather" || fn["arguments"] != `{"city":"北京"}` {
		t.Fatalf("tool_call function 不对：%v", fn)
	}
	if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "toolu_1" || toolMsg["content"] != "晴 25℃" {
		t.Fatalf("tool 消息不对：%v", toolMsg)
	}
	tools, _ := got["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("上游 tools 数量 = %d", len(tools))
	}
	choice, _ := got["tool_choice"].(map[string]any)
	if choice["type"] != "function" {
		t.Fatalf("tool_choice 不对：%v", got["tool_choice"])
	}
	if stop, _ := got["stop"].([]any); len(stop) != 1 || stop[0] != "END" {
		t.Fatalf("stop_sequences 没有映射成 stop：%v", got["stop"])
	}

	// 客户端拿到的是 Anthropic message：thinking + text + tool_use 三类内容块。
	var resp struct {
		ID         string `json:"id"`
		Type       string `json:"type"`
		Role       string `json:"role"`
		Model      string `json:"model"`
		StopReason string `json:"stop_reason"`
		Content    []struct {
			Type      string         `json:"type"`
			Text      string         `json:"text"`
			Thinking  string         `json:"thinking"`
			Signature string         `json:"signature"`
			ID        string         `json:"id"`
			Name      string         `json:"name"`
			Input     map[string]any `json:"input"`
		} `json:"content"`
		Usage struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			CacheReadTokens  int `json:"cache_read_input_tokens"`
			CacheCreateToken int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("解析 Anthropic 响应失败：%v；%s", err, body)
	}
	if resp.Type != "message" || resp.Role != "assistant" || resp.Model != "chat" {
		t.Fatalf("响应基本字段不对：%+v", resp)
	}
	if !strings.HasPrefix(resp.ID, "msg_") {
		t.Fatalf("message id 前缀不对：%q", resp.ID)
	}
	if resp.StopReason != "tool_use" {
		t.Fatalf("stop_reason = %q", resp.StopReason)
	}
	if len(resp.Content) != 3 {
		t.Fatalf("content 块数量 = %d：%s", len(resp.Content), body)
	}
	if resp.Content[0].Type != "thinking" || resp.Content[0].Thinking != "先想一想" || resp.Content[0].Signature == "" {
		t.Fatalf("thinking 块不对：%+v", resp.Content[0])
	}
	if resp.Content[1].Type != "text" || resp.Content[1].Text != "这是答案" {
		t.Fatalf("text 块不对：%+v", resp.Content[1])
	}
	if resp.Content[2].Type != "tool_use" || resp.Content[2].ID != "call_1" || resp.Content[2].Name != "weather" {
		t.Fatalf("tool_use 块不对：%+v", resp.Content[2])
	}
	if city, _ := resp.Content[2].Input["city"].(string); city != "上海" {
		t.Fatalf("tool_use input 不对：%v", resp.Content[2].Input)
	}
	if resp.Usage.InputTokens != 60 || resp.Usage.OutputTokens != 20 || resp.Usage.CacheReadTokens != 40 || resp.Usage.CacheCreateToken != 0 {
		t.Fatalf("usage 映射不对：%+v", resp.Usage)
	}

	// 用量按上游口径记账（prompt 含缓存命中），记进账号月度额度。
	used, err := f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 120 {
		t.Fatalf("记账用量 = %d，期望 120", used)
	}
}

// TestAnthropicMessagesAlias 覆盖 DeepSeek 风格的 base URL 别名 /anthropic/v1/messages：
// 与 /v1/messages 是同一 handler（鉴权、转换、错误壳、未匹配路径兜底都一致）。
func TestAnthropicMessagesAlias(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	})
	f := newAnthropicFixture(t, upstream, nil)
	u := f.user(t, "anthropic-alias", nil)
	f.token(t, u.ID, "ee_at_anthropic_alias", []string{rbac.ScopeModels}, nil, nil)
	reqBody := map[string]any{
		"model": "chat", "max_tokens": 16,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	decodeText := func(t *testing.T, body []byte) string {
		t.Helper()
		var resp struct {
			Type    string `json:"type"`
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("解析 Anthropic 响应失败：%v；%s", err, body)
		}
		if resp.Type != "message" || resp.Role != "assistant" || len(resp.Content) != 1 || resp.Content[0].Type != "text" {
			t.Fatalf("别名响应不是 Anthropic message：%s", body)
		}
		return resp.Content[0].Text
	}

	// x-api-key / Bearer 两种鉴权在别名上同样可用，返回同形状的 Anthropic message。
	code, body := postAnthropicPath(t, f.ts.URL, "/anthropic/v1/messages", "ee_at_anthropic_alias", reqBody)
	expect(t, "别名 + x-api-key", code, 200, body)
	if text := decodeText(t, body); text != "pong" {
		t.Fatalf("别名返回文本 = %q", text)
	}
	code, body = newClient(t, f.ts.URL).do("POST", "/anthropic/v1/messages", reqBody, bearer("ee_at_anthropic_alias"))
	expect(t, "别名 + Bearer", code, 200, body)
	if text := decodeText(t, body); text != "pong" {
		t.Fatalf("别名 Bearer 返回文本 = %q", text)
	}

	// anthropic-version / anthropic-beta（含 Files API beta）被忽略、不影响调用；
	// Files API 本身不实现，带这个头也不会改变行为。
	code, body = newClient(t, f.ts.URL).do("POST", "/anthropic/v1/messages", reqBody, map[string]string{
		"X-Api-Key":         "ee_at_anthropic_alias",
		"Anthropic-Version": "2023-06-01",
		"Anthropic-Beta":    "files-api-2025-04-14",
	})
	expect(t, "忽略 anthropic-version/anthropic-beta", code, 200, body)
	if text := decodeText(t, body); text != "pong" {
		t.Fatalf("忽略 beta 头后返回文本 = %q", text)
	}

	// 鉴权错误也走 Anthropic 壳。
	code, body = postAnthropicPath(t, f.ts.URL, "/anthropic/v1/messages", "ee_at_bad", reqBody)
	assertAnthropicErr(t, "别名无效 key", code, 401, body, "authentication_error", "无效")

	// count_tokens 别名与 /v1/messages/count_tokens 一样：鉴权后 404。
	code, body = postAnthropicPath(t, f.ts.URL, "/anthropic/v1/messages/count_tokens", "ee_at_anthropic_alias", reqBody)
	assertAnthropicErr(t, "别名 count_tokens", code, 404, body, "not_found_error", "count_tokens")

	// 别名前缀下的未匹配路径返回 Anthropic 404，而不是 OpenAI 壳或前端 SPA。
	code, body = postAnthropicPath(t, f.ts.URL, "/anthropic/v1/batches", "ee_at_anthropic_alias", reqBody)
	assertAnthropicErr(t, "别名兜底", code, 404, body, "not_found_error", "/v1/messages")
}

// TestAnthropicMessagesAuthAndLimits 覆盖鉴权方式与 401/403/429/404 错误壳。
func TestAnthropicMessagesAuthAndLimits(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`)
	})
	f := newAnthropicFixture(t, upstream, nil)
	c := newClient(t, f.ts.URL)
	u := f.user(t, "anthropic-auth", nil)
	f.token(t, u.ID, "ee_at_anthropic_auth", []string{rbac.ScopeModels}, nil, nil)
	okBody := map[string]any{
		"model": "chat", "max_tokens": 16,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}

	// 1. 不带任何凭证 → 401 authentication_error。
	code, body := c.do("POST", "/v1/messages", okBody, nil)
	assertAnthropicErr(t, "缺少凭证", code, 401, body, "authentication_error", "API key")

	// 2. 错误的 x-api-key → 401。
	code, body = c.do("POST", "/v1/messages", okBody, map[string]string{"X-Api-Key": "ee_at_nope"})
	assertAnthropicErr(t, "无效 x-api-key", code, 401, body, "authentication_error", "无效")

	// 3. Authorization: Bearer 与 x-api-key 都能用。
	code, body = c.do("POST", "/v1/messages", okBody, bearer("ee_at_anthropic_auth"))
	expect(t, "Bearer 调用", code, 200, body)
	code, body = c.do("POST", "/v1/messages", okBody, map[string]string{"X-Api-Key": "ee_at_anthropic_auth"})
	expect(t, "x-api-key 调用", code, 200, body)

	// 4. 令牌缺 models scope / 账号缺 llm.use → 403 permission_error。
	f.token(t, u.ID, "ee_at_anthropic_hub", []string{rbac.ScopeHub}, nil, nil)
	code, body = postAnthropic(t, f.ts.URL, "ee_at_anthropic_hub", okBody)
	assertAnthropicErr(t, "缺 models scope", code, 403, body, "permission_error", "没有调用大模型")

	// 5. 模型不在令牌可用范围 → 403 permission_error。
	f.token(t, u.ID, "ee_at_anthropic_model", []string{rbac.ScopeModels}, nil, []string{"other"})
	code, body = postAnthropic(t, f.ts.URL, "ee_at_anthropic_model", okBody)
	assertAnthropicErr(t, "模型不在范围", code, 403, body, "permission_error", "不能使用模型")

	// 6. 账号月度额度为 0 → 429 rate_limit_error。
	noQuota := f.user(t, "anthropic-no-quota", new(int64))
	f.token(t, noQuota.ID, "ee_at_anthropic_no_quota", []string{rbac.ScopeModels}, nil, nil)
	code, body = postAnthropic(t, f.ts.URL, "ee_at_anthropic_no_quota", okBody)
	assertAnthropicErr(t, "未分配额度", code, 429, body, "rate_limit_error", "额度")

	// 7. 账号额度过期 → 403 permission_error。
	expiredUser := f.user(t, "anthropic-expired", nil)
	f.token(t, expiredUser.ID, "ee_at_anthropic_expired", []string{rbac.ScopeModels}, nil, nil)
	past := time.Now().Add(-time.Hour)
	if _, err := f.st.DB().ExecContext(context.Background(),
		"UPDATE users SET quota_expires_at = ? WHERE id = ?", past, expiredUser.ID); err != nil {
		t.Fatal(err)
	}
	code, body = postAnthropic(t, f.ts.URL, "ee_at_anthropic_expired", okBody)
	assertAnthropicErr(t, "额度过期", code, 403, body, "permission_error", "额度已过期")

	// 8. 没有可用上游的模型 → 404 not_found_error。
	noModel := map[string]any{"model": "missing-model", "max_tokens": 16,
		"messages": []map[string]any{{"role": "user", "content": "hi"}}}
	code, body = postAnthropic(t, f.ts.URL, "ee_at_anthropic_auth", noModel)
	assertAnthropicErr(t, "模型没有上游", code, 404, body, "not_found_error", "上游")

	// 9. count_tokens 不在 v0 范围：鉴权后返回明确的 404 错误壳。
	code, body = c.do("POST", "/v1/messages/count_tokens", okBody, map[string]string{"X-Api-Key": "ee_at_anthropic_auth"})
	assertAnthropicErr(t, "count_tokens", code, 404, body, "not_found_error", "count_tokens")
}

// TestAnthropicMessagesRequestValidation 覆盖请求体层面的 400 invalid_request_error。
func TestAnthropicMessagesRequestValidation(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}]}`)
	})
	f := newAnthropicFixture(t, upstream, nil)
	u := f.user(t, "anthropic-validate", nil)
	f.token(t, u.ID, "ee_at_anthropic_validate", []string{rbac.ScopeModels}, nil, nil)
	header := map[string]string{"X-Api-Key": "ee_at_anthropic_validate"}

	cases := []struct {
		name    string
		raw     string
		msgPart string
	}{
		{name: "不是 JSON", raw: `not-json`, msgPart: "JSON"},
		{name: "缺少 model", raw: `{"max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, msgPart: "model"},
		{name: "缺少 max_tokens", raw: `{"model":"chat","messages":[{"role":"user","content":"hi"}]}`, msgPart: "max_tokens"},
		{name: "max_tokens 超上限", raw: `{"model":"chat","max_tokens":32769,"messages":[{"role":"user","content":"hi"}]}`, msgPart: "上限"},
		{name: "messages 为空", raw: `{"model":"chat","max_tokens":8,"messages":[]}`, msgPart: "messages"},
		{name: "不支持的块类型", raw: `{"model":"chat","max_tokens":8,"messages":[{"role":"user","content":[{"type":"document"}]}]}`, msgPart: "document"},
	}
	c := newClient(t, f.ts.URL)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doRawAnthropic(c, tc.raw, header)
			assertAnthropicErr(t, tc.name, code, 400, body, "invalid_request_error", tc.msgPart)
		})
	}
}

// TestAnthropicMessagesStream 覆盖流式：思考 → 正文 → 工具参数三类内容块与事件收尾。
func TestAnthropicMessagesStream(t *testing.T) {
	upstreamBody := make(chan map[string]any, 1)
	chunks := []string{
		`{"choices":[{"delta":{"reasoning_content":"先想"}}]}`,
		`{"choices":[{"delta":{"reasoning_content":"一下"}}]}`,
		`{"choices":[{"delta":{"content":"你好"}}]}`,
		`{"choices":[{"delta":{"content":"，世界"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_9","type":"function","function":{"name":"weather","arguments":"{\"city\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"北京\"}"}}]},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":6,"total_tokens":17}}`,
		`[DONE]`,
	}
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		upstreamBody <- body
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	})
	f := newAnthropicFixture(t, upstream, nil)
	u := f.user(t, "anthropic-stream", nil)
	f.token(t, u.ID, "ee_at_anthropic_stream", []string{rbac.ScopeModels}, nil, nil)

	body := map[string]any{
		"model": "chat", "max_tokens": 64, "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "查北京天气"}},
		"tools":    []map[string]any{{"name": "weather", "input_schema": map[string]any{"type": "object"}}},
	}
	code, raw := postAnthropic(t, f.ts.URL, "ee_at_anthropic_stream", body)
	expect(t, "Anthropic 流式调用", code, 200, raw)

	got := <-upstreamBody
	if stream, _ := got["stream"].(bool); !stream {
		t.Fatalf("上游没有收到 stream=true：%v", got["stream"])
	}
	opts, _ := got["stream_options"].(map[string]any)
	if opts == nil || opts["include_usage"] != true {
		t.Fatalf("上游没有收到 stream_options.include_usage=true：%v", got["stream_options"])
	}

	events := parseAnthropicEvents(t, string(raw))
	if events[0].name != "message_start" || events[len(events)-1].name != "message_stop" {
		t.Fatalf("事件首尾不对：%v ... %v", events[0].name, events[len(events)-1].name)
	}
	if strings.Contains(string(raw), "[DONE]") {
		t.Fatalf("不应把 OpenAI 的 [DONE] 转发给 Anthropic 客户端")
	}
	if !strings.Contains(string(raw), "event: message_start") {
		t.Fatalf("SSE 事件缺少 event 行：%s", raw)
	}

	type blockInfo struct {
		typ  string
		id   string
		name string
	}
	var blocks []blockInfo
	var argDeltas []string
	var messageDelta map[string]any
	for _, ev := range events {
		switch ev.name {
		case "content_block_start":
			block := mapStr(ev.data["content_block"])
			blocks = append(blocks, blockInfo{jsonStrOr(block["type"]), jsonStrOr(block["id"]), jsonStrOr(block["name"])})
		case "content_block_delta":
			delta := mapStr(ev.data["delta"])
			switch jsonStrOr(delta["type"]) {
			case "input_json_delta":
				argDeltas = append(argDeltas, jsonStrOr(delta["partial_json"]))
			case "thinking_delta", "text_delta", "signature_delta":
			default:
				t.Fatalf("未知 delta：%v", delta)
			}
		case "message_delta":
			messageDelta = ev.data
		}
	}
	if len(blocks) != 3 || blocks[0].typ != "thinking" || blocks[1].typ != "text" || blocks[2].typ != "tool_use" {
		t.Fatalf("内容块顺序不对：%+v", blocks)
	}
	if blocks[2].id != "call_9" || blocks[2].name != "weather" {
		t.Fatalf("tool_use 块不对：%+v", blocks[2])
	}
	if strings.Join(argDeltas, "") != `{"city":"北京"}` {
		t.Fatalf("工具参数增量 = %q", strings.Join(argDeltas, ""))
	}
	if messageDelta == nil {
		t.Fatal("缺少 message_delta")
	}
	if stop := jsonStrOr(mapStr(messageDelta["delta"])["stop_reason"]); stop != "tool_use" {
		t.Fatalf("stop_reason = %q", stop)
	}
	usageMap := mapStr(messageDelta["usage"])
	if n, _ := usageMap["output_tokens"].(float64); int(n) != 6 {
		t.Fatalf("output_tokens = %v", usageMap["output_tokens"])
	}

	used, err := f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 17 {
		t.Fatalf("流式记账用量 = %d，期望 17", used)
	}
}

// TestAnthropicMessagesUpstreamErrors 覆盖上游 5xx/429/坏响应 → Anthropic 错误壳。
func TestAnthropicMessagesUpstreamErrors(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		text := lastUserText(t, body)
		switch {
		case strings.Contains(text, "boom500"):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"上游炸了"}}`)
		case strings.Contains(text, "boom429"):
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, `{"error":{"message":"上游限流"}}`)
		case strings.Contains(text, "badjson"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `not-json`)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
		}
	})
	f := newAnthropicFixture(t, upstream, nil)
	u := f.user(t, "anthropic-upstream-errors", nil)
	f.token(t, u.ID, "ee_at_anthropic_upstream", []string{rbac.ScopeModels}, nil, nil)
	call := func(text string) (int, []byte) {
		return postAnthropic(t, f.ts.URL, "ee_at_anthropic_upstream", map[string]any{
			"model": "chat", "max_tokens": 16,
			"messages": []map[string]any{{"role": "user", "content": text}},
		})
	}

	code, body := call("boom500")
	assertAnthropicErr(t, "上游 500", code, 500, body, "api_error", "上游炸了")

	code, body = call("boom429")
	assertAnthropicErr(t, "上游 429", code, 429, body, "rate_limit_error", "上游限流")

	code, body = call("badjson")
	assertAnthropicErr(t, "上游坏响应", code, 502, body, "api_error", "无法解析")
}

// TestAnthropicMessagesRateLimit 覆盖 /v1/messages 的每令牌限流也是 Anthropic 错误壳，
// 并且只带 x-api-key 的请求也按令牌计额度（而不是退回 IP）。
func TestAnthropicMessagesRateLimit(t *testing.T) {
	upstream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if !chatUpstreamHandler(t, w, r, &body) {
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	})
	f := newAnthropicFixture(t, upstream, func(opts *api.Options) {
		opts.RateLimitPerMinute = 1
		opts.RateLimitWindow = time.Minute
	})
	u := f.user(t, "anthropic-ratelimit", nil)
	f.token(t, u.ID, "ee_at_anthropic_rl", []string{rbac.ScopeModels}, nil, nil)

	reqBody := map[string]any{
		"model": "chat", "max_tokens": 16,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	}
	if code, body := postAnthropic(t, f.ts.URL, "ee_at_anthropic_rl", reqBody); code != 200 {
		t.Fatalf("第一次调用应成功：%d %s", code, body)
	}
	code, body := postAnthropic(t, f.ts.URL, "ee_at_anthropic_rl", reqBody)
	assertAnthropicErr(t, "超限", code, 429, body, "rate_limit_error", "频繁")

	// 别名与 /v1/messages 共用同一份每令牌限流额度。
	f.token(t, u.ID, "ee_at_anthropic_rl_alias", []string{rbac.ScopeModels}, nil, nil)
	if code, body := postAnthropicPath(t, f.ts.URL, "/anthropic/v1/messages", "ee_at_anthropic_rl_alias", reqBody); code != 200 {
		t.Fatalf("别名第一次调用应成功：%d %s", code, body)
	}
	code, body = postAnthropicPath(t, f.ts.URL, "/anthropic/v1/messages", "ee_at_anthropic_rl_alias", reqBody)
	assertAnthropicErr(t, "别名超限", code, 429, body, "rate_limit_error", "频繁")
}

// ---- 小工具 ----

type anthropicEvent struct {
	name string
	data map[string]any
}

// parseAnthropicEvents 把 SSE 响应拆成事件列表并校验 event 名与 data.type 一致。
func parseAnthropicEvents(t *testing.T, raw string) []anthropicEvent {
	t.Helper()
	var events []anthropicEvent
	for _, block := range strings.Split(strings.TrimSpace(raw), "\n\n") {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		var name, payload string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				payload = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if name == "" || payload == "" {
			t.Fatalf("事件缺少 event 或 data：%q", block)
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			t.Fatalf("事件 data 不是 JSON：%v；%s", err, payload)
		}
		if jsonStrOr(data["type"]) != name {
			t.Fatalf("事件名 %q 与 data.type %q 不一致", name, data["type"])
		}
		events = append(events, anthropicEvent{name: name, data: data})
	}
	return events
}

func mapStr(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func jsonStrOr(v any) string {
	s, _ := v.(string)
	return s
}

// lastUserText 取转换后 OpenAI 请求最后一条 user 文本，用来区分 fake 上游返回哪种响应。
func lastUserText(t *testing.T, body map[string]any) string {
	t.Helper()
	msgs, _ := body["messages"].([]any)
	for i := len(msgs) - 1; i >= 0; i-- {
		msg, _ := msgs[i].(map[string]any)
		if msg["role"] != "user" {
			continue
		}
		if text, ok := msg["content"].(string); ok {
			return text
		}
	}
	return ""
}
