package api_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// cdUpstream 模拟一条上游线路：hit 计数，status 非 0 时固定回该状态码（可带 Retry-After），
// 否则回一个最小的 OpenAI 兼容成功响应。
func cdUpstream(t *testing.T, key string, status int, retryAfter string, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(401)
			return
		}
		if status != 0 {
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			w.WriteHeader(status)
			_, _ = io.WriteString(w, `{"error":{"message":"stub"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"x","choices":[{"message":{"role":"assistant","content":"好"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
}

// TestGatewayProviderCooldown 同名模型挂多条线路时，一条失败后进入冷却：
// 后续请求直接走下一条，不再先撞一次墙；全部冷却时最后一条仍会兜底尝试。
func TestGatewayProviderCooldown(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if ok, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil || !ok {
		t.Fatalf("bootstrap: %v %v", ok, err)
	}

	var p1Hits, p2Hits, p3Hits atomic.Int32
	overloaded := cdUpstream(t, "sk-cd-1", 429, "60", &p1Hits) // 主线路：一直 429，带 Retry-After
	healthy := cdUpstream(t, "sk-cd-2", 0, "", &p2Hits)        // 备线路：正常
	solo := cdUpstream(t, "sk-cd-3", 429, "", &p3Hits)         // 单线路模型：一直 429
	defer overloaded.Close()
	defer healthy.Close()
	defer solo.Close()

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	// cd-chat 挂两条线路（同优先级，按创建顺序：overloaded 在前）；cd-solo 只挂 overloaded 和 solo
	for _, p := range []map[string]any{
		{"name": "overloaded", "base_url": overloaded.URL, "api_key": "sk-cd-1", "models": []string{"cd-chat", "cd-solo"}},
		{"name": "healthy", "base_url": healthy.URL, "api_key": "sk-cd-2", "models": []string{"cd-chat"}},
		{"name": "solo", "base_url": solo.URL, "api_key": "sk-cd-3", "models": []string{"cd-solo"}},
	} {
		code, body = admin.do("POST", "/api/admin/providers", p, nil)
		expect(t, "创建上游 "+p["name"].(string), code, 201, body)
	}

	// 员工与额度：走自助 key 调网关
	code, body = admin.do("POST", "/api/admin/users", map[string]any{
		"username": "cd-user", "password": "cd-password-1", "display_name": "冷却测试",
	}, nil)
	expect(t, "创建员工", code, 201, body)
	var user struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(body, &user)
	code, body = admin.do("PATCH", "/api/admin/users/"+itoa(user.ID)+"/quota", map[string]any{
		"monthly_token_limit": 10000,
	}, nil)
	expect(t, "分配额度", code, 200, body)

	uc := newClient(t, ts.URL)
	code, body = uc.do("POST", "/api/auth/login", map[string]string{"username": "cd-user", "password": "cd-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)
	code, body = uc.do("POST", "/api/me/keys", map[string]any{"name": "cd"}, nil)
	expect(t, "自助创建 key", code, 201, body)
	var issued struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(body, &issued)
	bearer := map[string]string{"Authorization": "Bearer " + issued.Key}

	chat := func(model string) (int, []byte) {
		return uc.do("POST", "/v1/chat/completions", map[string]any{
			"model": model, "messages": []map[string]string{{"role": "user", "content": "你好"}},
		}, bearer)
	}

	// 第一次：主线路 429 一次后换到备线路成功
	code, body = chat("cd-chat")
	expect(t, "第一次经备线路成功", code, 200, body)
	if p1Hits.Load() != 1 || p2Hits.Load() != 1 {
		t.Fatalf("第一次应各打一次：主 %d 备 %d", p1Hits.Load(), p2Hits.Load())
	}

	// 第二次：主线路在冷却期内被直接跳过，不再撞 429
	code, body = chat("cd-chat")
	expect(t, "第二次直接走备线路", code, 200, body)
	if p1Hits.Load() != 1 || p2Hits.Load() != 2 {
		t.Fatalf("主线路应被冷却跳过：主 %d 备 %d", p1Hits.Load(), p2Hits.Load())
	}

	// 冷却跳过也作用于其他模型共用同一线路的候选：cd-solo 的第一条候选是冷却中的 overloaded
	code, body = chat("cd-solo")
	expect(t, "单线路兜底透传 429", code, 429, body)
	if p1Hits.Load() != 1 || p3Hits.Load() != 1 {
		t.Fatalf("冷却中的主线路不应被打：主 %d，兜底 %d", p1Hits.Load(), p3Hits.Load())
	}

	// 最后一条不受冷却影响：每个请求仍会尝试它，客户端照常拿到 429
	code, body = chat("cd-solo")
	expect(t, "兜底线路仍被尝试", code, 429, body)
	if p3Hits.Load() != 2 {
		t.Fatalf("最后一条线路不应被冷却拦住：%d", p3Hits.Load())
	}

	// 冷却期内 429 的客户端错误应仍是 OpenAI 形状
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("错误响应应为 OpenAI 格式：%s", body)
	}
}
