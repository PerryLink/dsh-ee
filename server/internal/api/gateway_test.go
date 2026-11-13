package api_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

// TestGatewayRequestLimits 从 HTTP 入口验证生成参数边界：超出单次上限或要求多候选时
// 返回 400 + OpenAI 兼容错误体，正常参数仍然放行。
func TestGatewayRequestLimits(t *testing.T) {
	f := newPrincipalFixture(t)
	u := f.user(t, "gateway-limits", nil)
	f.token(t, u.ID, "ee_at_gateway_limits", []string{rbac.ScopeModels}, nil, nil)
	c := newClient(t, f.ts.URL)
	header := bearer("ee_at_gateway_limits")

	call := func(extra map[string]any) (int, []byte) {
		body := map[string]any{
			"model":    "chat",
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		}
		for k, v := range extra {
			body[k] = v
		}
		return c.do("POST", "/v1/chat/completions", body, header)
	}
	errCode := func(t *testing.T, body []byte) string {
		t.Helper()
		var resp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("错误体不是合法 JSON：%v；%s", err, body)
		}
		if resp.Error.Message == "" {
			t.Fatalf("错误体缺少 message：%s", body)
		}
		return resp.Error.Code
	}

	cases := []struct {
		name     string
		extra    map[string]any
		status   int
		wantCode string
	}{
		{name: "正常请求", extra: nil, status: 200},
		{name: "max_tokens 在边界内", extra: map[string]any{"max_tokens": 4096}, status: 200},
		{name: "max_tokens 恰好等于上限", extra: map[string]any{"max_tokens": 32768}, status: 200},
		{name: "max_completion_tokens 在边界内", extra: map[string]any{"max_completion_tokens": 2048, "n": 1}, status: 200},
		{name: "max_tokens 超限", extra: map[string]any{"max_tokens": 32769}, status: 400, wantCode: "max_tokens_too_large"},
		{name: "max_completion_tokens 超限", extra: map[string]any{"max_completion_tokens": 1000000}, status: 400, wantCode: "max_completion_tokens_too_large"},
		{name: "max_tokens 非正整数", extra: map[string]any{"max_tokens": 0}, status: 400, wantCode: "invalid_max_tokens"},
		{name: "n>1", extra: map[string]any{"n": 2}, status: 400, wantCode: "n_not_supported"},
		{name: "n 非正整数", extra: map[string]any{"n": 0}, status: 400, wantCode: "invalid_n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := call(tc.extra)
			if code != tc.status {
				t.Fatalf("状态码 = %d，期望 %d；%s", code, tc.status, body)
			}
			if tc.wantCode != "" && errCode(t, body) != tc.wantCode {
				t.Fatalf("错误码不对：%s", body)
			}
		})
	}

	// DSH 接入插件走 400 + 稳定错误码，且文案不能命中上游的鉴权/额度分类。
	dshHeader := map[string]string{
		"Authorization":  "Bearer ee_at_gateway_limits",
		"X-DshEE-Client": "dsh-connect/1.0.1",
	}
	dshBody := map[string]any{
		"model":    "chat",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"n":        2,
	}
	code, resp := c.do("POST", "/v1/chat/completions", dshBody, dshHeader)
	if code != 400 {
		t.Fatalf("DSH 客户端应返回 400，得到 %d；%s", code, resp)
	}
	assertDshErrorSafe(t, resp)
	if got := errCode(t, resp); got != "n_not_supported" {
		t.Fatalf("DSH 错误码 = %q，期望 n_not_supported；%s", got, resp)
	}

	// 被拒绝的请求既不转发上游、也不记账：换一个没有成功调用的用户验证额度仍为 0。
	rejectOnly := f.user(t, "gateway-limits-reject-only", nil)
	f.token(t, rejectOnly.ID, "ee_at_gateway_reject", []string{rbac.ScopeModels}, nil, nil)
	code, resp = newClient(t, f.ts.URL).do("POST", "/v1/chat/completions", map[string]any{
		"model":    "chat",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"n":        2,
	}, bearer("ee_at_gateway_reject"))
	if code != 400 {
		t.Fatalf("n>1 应返回 400，得到 %d；%s", code, resp)
	}
	used, err := f.st.MonthTokensByUser(context.Background(), rejectOnly.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("被拒绝的请求不应计入额度，当前用量 %d", used)
	}
}
