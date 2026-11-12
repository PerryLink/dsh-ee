package api_test

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

type cooldownTransport func(*http.Request) (*http.Response, error)

func (f cooldownTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type cooldownGateway struct {
	t      *testing.T
	admin  *client
	router http.Handler
	key    string
}

func newCooldownGateway(t *testing.T, upstream func(host, model, message string) (*http.Response, error)) *cooldownGateway {
	t.Helper()
	st := openTestStore(t)
	if ok, err := api.Bootstrap(t.Context(), st, "admin", "admin-password-123"); err != nil || !ok {
		t.Fatalf("bootstrap: %v %v", ok, err)
	}

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	transport := cooldownTransport(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		if r.Header.Get("Authorization") != "Bearer sk-cooldown-test" {
			return cooldownResponse(http.StatusUnauthorized), nil
		}
		var body struct {
			Model    string
			Messages []struct{ Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 1 {
			t.Errorf("上游收到错误的请求体：%+v，%v", body, err)
			return cooldownResponse(http.StatusBadRequest), nil
		}
		return upstream(r.URL.Hostname(), body.Model, body.Messages[0].Content)
	})
	opts := api.Options{DataDir: t.TempDir(), Upstream: &http.Client{Transport: transport}}
	router := api.New(st, box, opts, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler()
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	admin := newClient(t, server.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	code, body = admin.do("POST", "/api/admin/users", map[string]string{
		"username": "cd-user", "password": "cd-password-1", "display_name": "冷却测试",
	}, nil)
	expect(t, "创建员工", code, 201, body)
	var user struct{ ID int64 }
	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatal(err)
	}
	code, body = admin.do("PATCH", "/api/admin/users/"+itoa(user.ID)+"/quota", map[string]int{
		"monthly_token_limit": 10000,
	}, nil)
	expect(t, "分配额度", code, 200, body)

	employee := newClient(t, server.URL)
	code, body = employee.do("POST", "/api/auth/login", map[string]string{"username": "cd-user", "password": "cd-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)
	code, body = employee.do("POST", "/api/me/keys", map[string]string{"name": "cooldown"}, nil)
	expect(t, "自助创建 key", code, 201, body)
	var issued struct{ Key string }
	if err := json.Unmarshal(body, &issued); err != nil {
		t.Fatal(err)
	}
	return &cooldownGateway{t: t, admin: admin, router: router, key: issued.Key}
}

func (g *cooldownGateway) addProvider(host string, models map[string]string) {
	g.t.Helper()
	code, body := g.admin.do("POST", "/api/admin/providers", map[string]string{
		"name": host, "base_url": "https://" + host, "api_key": "sk-cooldown-test",
	}, nil)
	expect(g.t, "创建上游 "+host, code, 201, body)
	var provider struct{ ID int64 }
	if err := json.Unmarshal(body, &provider); err != nil {
		g.t.Fatal(err)
	}
	items := make([]map[string]string, 0, len(models))
	for name, upstreamModel := range models {
		items = append(items, map[string]string{"name": name, "upstream_model": upstreamModel})
	}
	code, body = g.admin.do("POST", "/api/admin/models", map[string]any{"provider_id": provider.ID, "items": items}, nil)
	expect(g.t, "创建模型 "+host, code, 201, body)
}

func (g *cooldownGateway) chat(model, message string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]any{
		"model": model, "messages": []map[string]string{{"role": "user", "content": message}},
	})
	req := httptest.NewRequestWithContext(g.t.Context(), http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.key)
	rec := httptest.NewRecorder()
	g.router.ServeHTTP(rec, req)
	return rec
}

func cooldownResponse(status int) *http.Response {
	header := http.Header{"Content-Type": {"application/json"}}
	body := `{"error":{"message":"stub"}}`
	if status == http.StatusTooManyRequests {
		header.Set("Retry-After", "60")
	}
	if status == http.StatusOK {
		body = `{"choices":[{"message":{"role":"assistant","content":"好"}}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body))}
}

func TestGatewayProviderCooldown(t *testing.T) {
	var limitedHits, healthyHits, backupHits int
	g := newCooldownGateway(t, func(host, model, message string) (*http.Response, error) {
		if host == "primary.invalid" {
			if model == "vendor-limited" {
				limitedHits++
				return cooldownResponse(http.StatusTooManyRequests), nil
			}
			healthyHits++
			return cooldownResponse(http.StatusOK), nil
		}
		backupHits++
		if model == "vendor-healthy" {
			return cooldownResponse(http.StatusServiceUnavailable), nil
		}
		if message == "限流" {
			return cooldownResponse(http.StatusTooManyRequests), nil
		}
		return cooldownResponse(http.StatusOK), nil
	})
	g.addProvider("primary.invalid", map[string]string{
		"limited": "vendor-limited", "limited-alias": "vendor-limited", "healthy": "vendor-healthy",
	})
	g.addProvider("backup.invalid", map[string]string{
		"limited": "backup-chat", "limited-alias": "backup-chat", "backup-only": "backup-chat", "healthy": "vendor-healthy",
	})

	res := g.chat("limited", "第一次")
	expect(t, "经备线成功", res.Code, 200, res.Body.Bytes())
	res = g.chat("limited", "第二次")
	expect(t, "直接走备线", res.Code, 200, res.Body.Bytes())
	res = g.chat("limited-alias", "同一个上游模型")
	expect(t, "别名共享同一个上游模型的冷却", res.Code, 200, res.Body.Bytes())
	res = g.chat("healthy", "独立模型")
	expect(t, "同一上游的健康模型保持可用", res.Code, 200, res.Body.Bytes())
	if limitedHits != 1 || healthyHits != 1 || backupHits != 3 {
		t.Fatalf("上游调用次数：限流模型 %d，健康模型 %d，备线 %d", limitedHits, healthyHits, backupHits)
	}

	res = g.chat("backup-only", "限流")
	expect(t, "备线也进入冷却", res.Code, 429, res.Body.Bytes())
	res = g.chat("limited", "两条线路均在冷却")
	expect(t, "冷却中的最后一条线路继续兜底", res.Code, 200, res.Body.Bytes())
	if limitedHits != 1 || backupHits != 5 {
		t.Fatalf("主线应保持冷却，备线应兜底：主线 %d，备线 %d", limitedHits, backupHits)
	}
}

func TestGatewayProviderCooldownRecordsLastFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{"429", http.StatusTooManyRequests},
		{"5xx", http.StatusServiceUnavailable},
		{"connection", http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var primaryHits int
			g := newCooldownGateway(t, func(host, _, _ string) (*http.Response, error) {
				if host == "backup.invalid" {
					return cooldownResponse(http.StatusOK), nil
				}
				primaryHits++
				if tc.status == http.StatusBadGateway {
					return nil, errors.New("上游连接失败")
				}
				return cooldownResponse(tc.status), nil
			})
			pairedModel := "vendor-chat"
			if tc.status == http.StatusBadGateway {
				pairedModel = "vendor-other" // 连接故障影响整条线路。
			}
			g.addProvider("primary.invalid", map[string]string{"solo": "vendor-chat", "paired": pairedModel})
			g.addProvider("backup.invalid", map[string]string{"paired": "backup-chat"})

			for range 2 {
				res := g.chat("solo", "只有一个候选")
				expect(t, "保留兜底请求的错误响应", res.Code, tc.status, res.Body.Bytes())
				if !strings.Contains(res.Body.String(), `"error"`) {
					t.Fatalf("错误响应应为 OpenAI 格式：%s", res.Body.String())
				}
			}
			res := g.chat("paired", "有备线")
			expect(t, "跳过此前失败的线路", res.Code, 200, res.Body.Bytes())
			if primaryHits != 2 {
				t.Fatalf("冷却中仅有两次兜底请求应访问主线，实际 %d 次", primaryHits)
			}
		})
	}
}

type blockedCooldownBody struct {
	io.ReadCloser
	block func()
}

func (b blockedCooldownBody) Read(p []byte) (int, error) {
	b.block()
	return b.ReadCloser.Read(p)
}

func TestGatewayProviderCooldownConcurrent(t *testing.T) {
	for _, tc := range []struct {
		name     string
		firstOK  bool
		wantHits int32
	}{
		{"older_success_keeps_newer_failure", true, 2},
		{"cooldown_starts_before_reading_error_body", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			block := sync.OnceFunc(func() {
				close(started)
				<-release
			})
			var primaryHits atomic.Int32
			g := newCooldownGateway(t, func(host, _, message string) (*http.Response, error) {
				if host == "backup.invalid" {
					return cooldownResponse(http.StatusOK), nil
				}
				primaryHits.Add(1)
				res := cooldownResponse(http.StatusTooManyRequests)
				if message != "first" {
					return res, nil
				}
				if tc.firstOK {
					block()
					return cooldownResponse(http.StatusOK), nil
				}
				res.Body = blockedCooldownBody{ReadCloser: res.Body, block: block}
				return res, nil
			})
			g.addProvider("primary.invalid", map[string]string{"paired": "vendor-chat"})
			g.addProvider("backup.invalid", map[string]string{"paired": "backup-chat"})

			first := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				defer close(first)
				first <- g.chat("paired", "first")
			}()
			defer func() {
				unblock()
				select {
				case <-first:
				case <-time.After(10 * time.Second):
					t.Error("等待中的请求未退出")
				}
			}()
			select {
			case <-started:
			case <-time.After(10 * time.Second):
				t.Fatal("第一个请求未进入预期的等待点")
			}
			second := g.chat("paired", "second")
			expect(t, "第二个请求经备线成功", second.Code, 200, second.Body.Bytes())
			hitsWhileBlocked := primaryHits.Load()

			unblock()
			select {
			case res := <-first:
				expect(t, "第一个请求成功", res.Code, 200, res.Body.Bytes())
			case <-time.After(10 * time.Second):
				t.Fatal("第一个请求未完成")
			}
			third := g.chat("paired", "third")
			expect(t, "第三个请求经备线成功", third.Code, 200, third.Body.Bytes())
			if hits := primaryHits.Load(); hitsWhileBlocked != tc.wantHits || hits != tc.wantHits {
				t.Fatalf("等待中的主线请求数 %d，第三个请求后 %d，均期望 %d", hitsWhileBlocked, hits, tc.wantHits)
			}
		})
	}
}
