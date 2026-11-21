package api_test

// LLM 月度额度的 check-then-use 并发回归测试。
//
// 原实现是「先 SUM(usage_logs) 与 limit 比较、放行，响应写完后才插入 usage_logs」：
// 并发请求会读到同一个旧用量，全部通过准入后各自记账，实际用量可以越过月度额度
// 一个数量级（N 个 max_tokens=32768 的请求就能吃掉十几倍额度）。这里覆盖修完后的
// 三条不变量：
//  1. 放行时把「在途请求声明的 max_tokens」计入额度，超出的请求在转发上游之前 429；
//  2. 每用户有在途并发上限，超出的请求 429；
//  3. 预留只存在进程内，请求结束（成功 / 上游失败 / 客户端断开）都会释放，
//     不写进 usage_logs，也不影响 admin/me 的月度余额展示（MonthTokensByUser /
//     TokenMonthUsage 仍只统计真实用量）。
//
// 修前这些用例里的并发用例会得到 10 个 200（而不是 ≤ limit/max_tokens 个），
// 单请求超大 max_tokens 用例会得到 200（而不是 429）。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

type quotaCallResult struct {
	status int
	body   []byte
	err    error
}

// quotaRequestBody 构造一条 /v1/chat/completions 请求；maxTokens=0 表示不带 max_tokens。
func quotaRequestBody(maxTokens int64) map[string]any {
	body := map[string]any{
		"model":    "chat",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	return body
}

// startQuotaCalls 并发发起 n 条 chat 请求。结果先收进带缓冲的 channel，
// 测试在放行上游之后统一收集，避免 t.Fatal 时 goroutine 泄漏。
// nonOK 累计「已经拿到非 200 响应」的请求数，和上游收到的请求数相加正好是
// 已完成准入判定的请求数（在途的 200 要等测试放行上游才返回）。
func startQuotaCalls(base, token string, n int, maxTokens int64, nonOK *atomic.Int32) <-chan quotaCallResult {
	out := make(chan quotaCallResult, n)
	payload, _ := json.Marshal(quotaRequestBody(maxTokens))
	client := &http.Client{Timeout: 60 * time.Second}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := http.NewRequest("POST", base+"/v1/chat/completions", bytes.NewReader(payload))
			if err != nil {
				out <- quotaCallResult{err: err}
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			resp, err := client.Do(req)
			if err != nil {
				out <- quotaCallResult{err: err}
				return
			}
			defer resp.Body.Close()
			data, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK && nonOK != nil {
				nonOK.Add(1)
			}
			out <- quotaCallResult{status: resp.StatusCode, body: data}
		}()
	}
	go func() {
		wg.Wait()
		close(out)
	}()
	return out
}

func collectQuotaCalls(t *testing.T, results <-chan quotaCallResult) []quotaCallResult {
	t.Helper()
	var out []quotaCallResult
	for r := range results {
		if r.err != nil {
			t.Fatalf("并发请求失败：%v", r.err)
		}
		out = append(out, r)
	}
	return out
}

func countQuotaStatus(t *testing.T, results []quotaCallResult, status int) int {
	t.Helper()
	n := 0
	for _, r := range results {
		if r.status == status {
			n++
		}
	}
	return n
}

func writeQuotaUsage(w http.ResponseWriter, prompt, completion int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "x",
		"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		"usage": map[string]any{
			"prompt_tokens": prompt, "completion_tokens": completion, "total_tokens": prompt + completion,
		},
	})
}

// waitQuotaSettled 等到「上游收到的请求数 + 已拿到非 200 响应的请求数」达到 total，
// 也就是所有请求都完成了准入判定（通过且在上游阻塞，或被 429 拒绝）。
func waitQuotaSettled(hits, nonOK *atomic.Int32, total int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if int(hits.Load()+nonOK.Load()) >= total {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return int(hits.Load()+nonOK.Load()) >= total
}

// TestLLMQuotaReservationRejectsOversizedRequest 覆盖「剩余额度装不下本次 max_tokens」
// 的确定性场景：limit=1000 时 max_tokens=1500 的请求应在转发前被 429，而不是先调用
// 上游、等响应后记账才越界。修前这条请求会得到 200。
func TestLLMQuotaReservationRejectsOversizedRequest(t *testing.T) {
	var hits atomic.Int32
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		writeQuotaUsage(w, 0, 15)
	})
	f := newAnthropicFixtureVendor(t, up, "", nil)
	limit := int64(1000)
	u := f.user(t, "quota-reserve-oversize", &limit)
	token := "ee_at_q_oversize"
	f.token(t, u.ID, token, []string{rbac.ScopeModels}, nil, nil)
	c := newClient(t, f.ts.URL)

	// 单次申请 1500 > 剩余额度 1000：修后 429，且一个请求都不该转发上游。
	code, body := c.do("POST", "/v1/chat/completions", quotaRequestBody(1500), bearer(token))
	if code != http.StatusTooManyRequests {
		t.Fatalf("max_tokens 超过剩余额度应 429，得到 %d；%s", code, body)
	}
	if got := openAIErrorCode(t, body); got != "monthly_quota_exceeded" {
		t.Fatalf("错误码 = %q，期望 monthly_quota_exceeded；%s", got, body)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("被额度拒绝的请求不应转发上游，实际收到 %d 次", n)
	}
	used, err := f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("被拒绝的请求不应记账，当前用量 %d", used)
	}

	// 请求装得下（500 ≤ 1000）时照常放行：正常单请求仍然 200。
	code, body = c.do("POST", "/v1/chat/completions", quotaRequestBody(500), bearer(token))
	expect(t, "预留装得下的请求", code, http.StatusOK, body)

	// 已用 15，再来一个 max_tokens=1000 的请求：15 + 1000 > 1000，仍应 429。
	code, body = c.do("POST", "/v1/chat/completions", quotaRequestBody(1000), bearer(token))
	if code != http.StatusTooManyRequests {
		t.Fatalf("已用 15 后再申请 max_tokens=1000 应 429，得到 %d；%s", code, body)
	}
	// 申请 900：15 + 900 ≤ 1000，照常放行。
	code, body = c.do("POST", "/v1/chat/completions", quotaRequestBody(900), bearer(token))
	expect(t, "剩余额度内的请求", code, http.StatusOK, body)
	used, err = f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 30 {
		t.Fatalf("两次成功调用的用量应为 30，实际 %d", used)
	}
	if used > limit {
		t.Fatalf("实际用量 %d 越过月度额度 %d", used, limit)
	}
}

// TestLLMQuotaConcurrentReservation 是本修复的主复现：limit=1000 的账号并发发 10 个
// max_tokens=300 的请求。修前 10 个全部通过检查（各自读到 used=0），记账后用量 3000，
// 越过额度 3 倍；修后放行数应满足 passed×300 ≤ 1000。
func TestLLMQuotaConcurrentReservation(t *testing.T) {
	var limit = int64(1000)
	const (
		maxTokens = int64(300)
		calls     = 10
	)
	var hits atomic.Int32
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		<-release
		// 每个响应记满 300 token，模拟生成到 max_tokens 上限。
		writeQuotaUsage(w, 0, int(maxTokens))
	})
	f := newAnthropicFixtureVendor(t, up, "", nil)
	u := f.user(t, "quota-toctou-concurrent", &limit)
	token := "ee_at_q_toctou"
	f.token(t, u.ID, token, []string{rbac.ScopeModels}, nil, nil)

	var nonOK atomic.Int32
	results := startQuotaCalls(f.ts.URL, token, calls, maxTokens, &nonOK)
	// 每个请求要么到达上游（在途、被阻塞），要么已拿到 429；两者相加等于总请求数时，
	// 全部准入判定都已落定。修前 10 个都会到上游；修后最多 floor(1000/300)=3 个。
	if !waitQuotaSettled(&hits, &nonOK, calls, 10*time.Second) {
		releaseAll()
		t.Fatalf("准入判定未落定：上游 %d 个 + 已响应 %d 个，共 %d 个请求", hits.Load(), nonOK.Load(), calls)
	}
	admitted := int(hits.Load())
	if got := int64(admitted) * maxTokens; got > limit {
		releaseAll()
		t.Fatalf("准入的在途用量 %d×%d=%d 超过月度额度 %d（修前：全部请求先通过检查再记账）",
			admitted, maxTokens, got, limit)
	}
	releaseAll()

	all := collectQuotaCalls(t, results)
	passed := countQuotaStatus(t, all, http.StatusOK)
	denied := countQuotaStatus(t, all, http.StatusTooManyRequests)
	if passed != admitted {
		t.Fatalf("成功请求 %d 个，但在途期间只看到 %d 个到达上游", passed, admitted)
	}
	if passed+denied != calls {
		t.Fatalf("成功 %d + 429 %d != 总请求 %d", passed, denied, calls)
	}
	if int64(passed)*maxTokens > limit {
		t.Fatalf("通过数 %d×%d 超过额度 %d", passed, maxTokens, limit)
	}
	for _, r := range all {
		if r.status != http.StatusOK && r.status != http.StatusTooManyRequests {
			t.Fatalf("非预期的状态码 %d：%s", r.status, r.body)
		}
	}
	used, err := f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(passed) * maxTokens; used != want {
		t.Fatalf("成功调用记账应合计 %d，实际 %d", want, used)
	}
	if used > limit {
		t.Fatalf("实际用量 %d 越过月度额度 %d", used, limit)
	}
}

// TestLLMTokenQuotaConcurrentReservation 同上，但额度挂在令牌上（TokenMonthUsage 口径）：
// 令牌额度同样要在放行时计入在途预留，不能靠事后 SUM。
func TestLLMTokenQuotaConcurrentReservation(t *testing.T) {
	var limit = int64(1000)
	const (
		maxTokens = int64(300)
		calls     = 10
	)
	var hits atomic.Int32
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		writeQuotaUsage(w, 0, int(maxTokens))
	})
	f := newAnthropicFixtureVendor(t, up, "", nil)
	// 账号额度不限额，只有令牌额度 1000。
	u := f.user(t, "quota-toctou-token", nil)
	token := "ee_at_q_toctou_tok"
	tok := f.token(t, u.ID, token, []string{rbac.ScopeModels}, &limit, nil)

	var nonOK atomic.Int32
	results := startQuotaCalls(f.ts.URL, token, calls, maxTokens, &nonOK)
	if !waitQuotaSettled(&hits, &nonOK, calls, 10*time.Second) {
		releaseAll()
		t.Fatalf("令牌准入判定未落定：上游 %d 个 + 已响应 %d 个，共 %d 个请求", hits.Load(), nonOK.Load(), calls)
	}
	admitted := int(hits.Load())
	if got := int64(admitted) * maxTokens; got > limit {
		releaseAll()
		t.Fatalf("令牌准入的在途用量 %d×%d=%d 超过令牌月度额度 %d",
			admitted, maxTokens, got, limit)
	}
	releaseAll()

	all := collectQuotaCalls(t, results)
	passed := countQuotaStatus(t, all, http.StatusOK)
	if passed+countQuotaStatus(t, all, http.StatusTooManyRequests) != calls {
		t.Fatalf("非 200/429 响应：%+v", all)
	}
	if int64(passed)*maxTokens > limit {
		t.Fatalf("通过数 %d×%d 超过令牌额度 %d", passed, maxTokens, limit)
	}
	used, err := f.st.TokenMonthUsage(context.Background(), tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(passed) * maxTokens; used != want {
		t.Fatalf("令牌记账应合计 %d，实际 %d", want, used)
	}
	if used > limit {
		t.Fatalf("令牌实际用量 %d 越过额度 %d", used, limit)
	}
}

// TestLLMInflightPerUserCap 覆盖每用户在途并发上限：不限额度时并发 8 个请求，
// 最多 4 个能到上游，其余 429；在途请求结束后名额释放，后续请求恢复 200。
func TestLLMInflightPerUserCap(t *testing.T) {
	const (
		inflight = 4
		calls    = 8
	)
	var hits atomic.Int32
	release := make(chan struct{})
	releaseAll := sync.OnceFunc(func() { close(release) })
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		<-release
		writeQuotaUsage(w, 0, 15)
	})
	f := newAnthropicFixtureVendor(t, up, "", nil)
	u := f.user(t, "quota-inflight-cap", nil) // nil：账号额度不限额
	token := "ee_at_q_inflight"
	f.token(t, u.ID, token, []string{rbac.ScopeModels}, nil, nil)

	var nonOK atomic.Int32
	results := startQuotaCalls(f.ts.URL, token, calls, 0, &nonOK)
	if !waitQuotaSettled(&hits, &nonOK, calls, 10*time.Second) {
		releaseAll()
		t.Fatalf("并发闸判定未落定：上游 %d 个 + 已响应 %d 个，共 %d 个请求", hits.Load(), nonOK.Load(), calls)
	}
	if got := hits.Load(); got != inflight {
		releaseAll()
		t.Fatalf("每用户同时只允许 %d 个在途请求，上游实际收到 %d 个", inflight, got)
	}
	releaseAll()

	all := collectQuotaCalls(t, results)
	passed := countQuotaStatus(t, all, http.StatusOK)
	denied := countQuotaStatus(t, all, http.StatusTooManyRequests)
	if passed != inflight || denied != calls-inflight {
		t.Fatalf("成功 %d / 429 %d，期望 %d / %d", passed, denied, inflight, calls-inflight)
	}
	for _, r := range all {
		if r.status == http.StatusTooManyRequests {
			// 并发闸沿用网关限流的错误壳：OpenAI 壳 rate_limit_error / rate_limit_exceeded。
			if got := openAIErrorCode(t, r.body); got != "rate_limit_exceeded" {
				t.Fatalf("并发闸 429 错误码 = %q，期望 rate_limit_exceeded；%s", got, r.body)
			}
		}
	}

	// 名额释放：在途请求都结束后，同一账号再发请求应恢复 200。
	code, body := newClient(t, f.ts.URL).do("POST", "/v1/chat/completions", quotaRequestBody(0), bearer(token))
	expect(t, "在途名额释放后的请求", code, http.StatusOK, body)
}

// TestLLMQuotaReservationReleased 覆盖预留的释放路径：上游失败与客户端断开都不能把
// 预留永久留在在途记账里，否则后续请求会被误判为额度不足。
func TestLLMQuotaReservationReleased(t *testing.T) {
	var limit = int64(1000)
	const maxTokens = int64(900)

	t.Run("upstream_error", func(t *testing.T) {
		var calls atomic.Int32
		up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if calls.Add(1) == 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = io.WriteString(w, `{"error":{"message":"upstream boom"}}`)
				return
			}
			writeQuotaUsage(w, 0, 15)
		})
		f := newAnthropicFixtureVendor(t, up, "", nil)
		u := f.user(t, "quota-release-error", &limit)
		token := "ee_at_q_rel_err"
		f.token(t, u.ID, token, []string{rbac.ScopeModels}, nil, nil)
		c := newClient(t, f.ts.URL)

		code, body := c.do("POST", "/v1/chat/completions", quotaRequestBody(maxTokens), bearer(token))
		if code == http.StatusTooManyRequests {
			t.Fatalf("上游失败前应已通过额度准入（不是 429）：%s", body)
		}
		// 失败请求的预留必须已经释放：同样大的请求还能再次通过准入。
		code, body = c.do("POST", "/v1/chat/completions", quotaRequestBody(maxTokens), bearer(token))
		expect(t, "失败后预留释放", code, http.StatusOK, body)
		used, err := f.st.MonthTokensByUser(context.Background(), u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if used != 15 {
			t.Fatalf("失败请求不应记账，成功请求记 15；实际 %d", used)
		}
	})

	t.Run("client_cancel", func(t *testing.T) {
		started := make(chan struct{})
		releaseFirst := make(chan struct{})
		var releaseOnce sync.Once
		releaseUpstream := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
		defer releaseUpstream() // 断言失败时也要放行上游，避免 httptest.Server.Close 卡住
		var first atomic.Bool
		up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if first.CompareAndSwap(false, true) {
				close(started)
				// 模拟「客户端断开时上游还在生成」：取消后由测试放行这条上游响应。
				<-releaseFirst
				return
			}
			writeQuotaUsage(w, 0, 15)
		})
		f := newAnthropicFixtureVendor(t, up, "", nil)
		u := f.user(t, "quota-release-cancel", &limit)
		token := "ee_at_q_rel_cancel"
		f.token(t, u.ID, token, []string{rbac.ScopeModels}, nil, nil)

		ctx, cancel := context.WithCancel(context.Background())
		payload, _ := json.Marshal(quotaRequestBody(maxTokens))
		req, err := http.NewRequestWithContext(ctx, "POST", f.ts.URL+"/v1/chat/completions", bytes.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		done := make(chan struct{})
		go func() {
			defer close(done)
			resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("第一个请求没有到达上游")
		}
		cancel()
		releaseUpstream()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("客户端取消后请求没有退出")
		}

		// 预留释放是异步的（handler 收尾），轮询到恢复 200 为止；期间绝不能一直是 429。
		c := newClient(t, f.ts.URL)
		deadline := time.Now().Add(5 * time.Second)
		for {
			code, body := c.do("POST", "/v1/chat/completions", quotaRequestBody(maxTokens), bearer(token))
			if code == http.StatusOK {
				break
			}
			if code != http.StatusTooManyRequests {
				t.Fatalf("取消后请求状态码 = %d，期望 200 或 429；%s", code, body)
			}
			if time.Now().After(deadline) {
				t.Fatalf("客户端取消后预留没有释放，后续请求一直 429：%s", body)
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
}

// TestLLMQuotaReservationNotPersisted 覆盖月度余额展示：预留只在进程内，不写 usage_logs；
// 用完的预留额度不会永久占住 admin/me 看到的本月用量（MonthTokensByUser 与
// TokenMonthUsage 仍是实际记账值）。
func TestLLMQuotaReservationNotPersisted(t *testing.T) {
	var hits atomic.Int32
	up := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		writeQuotaUsage(w, 6, 15)
	})
	f := newAnthropicFixtureVendor(t, up, "", nil)
	limit := int64(100000)
	u := f.user(t, "quota-reserve-visible", &limit)
	token := "ee_at_q_visible"
	tok := f.token(t, u.ID, token, []string{rbac.ScopeModels}, nil, nil)
	c := newClient(t, f.ts.URL)

	// 单请求声明 32768（占满单次上限）但实际只用 21：应 200。
	code, body := c.do("POST", "/v1/chat/completions", quotaRequestBody(32768), bearer(token))
	expect(t, "声明大 max_tokens 的单请求", code, http.StatusOK, body)
	used, err := f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 21 {
		t.Fatalf("账号月度用量应只统计实际 21，实际 %d（预留被写进了账？）", used)
	}
	tokenUsed, err := f.st.TokenMonthUsage(context.Background(), tok.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tokenUsed != 21 {
		t.Fatalf("令牌月度用量应只统计实际 21，实际 %d（预留被写进了账？）", tokenUsed)
	}

	// 预留已释放：再来一次同样的请求仍然 200，累计 42，不会因为上一次申请了 32768 被拒。
	code, body = c.do("POST", "/v1/chat/completions", quotaRequestBody(32768), bearer(token))
	expect(t, "预留释放后的第二个大 max_tokens 请求", code, http.StatusOK, body)
	used, err = f.st.MonthTokensByUser(context.Background(), u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 42 {
		t.Fatalf("两次成功调用后账号月度用量应为 42，实际 %d", used)
	}
	if n := hits.Load(); n != 2 {
		t.Fatalf("两次请求都该到上游，实际 %d 次", n)
	}
}

// TestLLMQuotaReservationCoversAllEntrypoints 覆盖所有 LLM 入口共用同一套预留准入：
// /v1/chat/completions、/v1/messages（转换层与 DeepSeek 原生透传）、/v1/responses
// （转换层与原生透传）、/beta/completions。每个入口都用「max_tokens 超过剩余额度」的
// 请求，断言在转发上游之前就被 429，而不是先调上游、等响应后记账才越界。
func TestLLMQuotaReservationCoversAllEntrypoints(t *testing.T) {
	var limit = int64(1000)
	const maxTokens = int64(1500) // > limit：任何一个入口都装不下
	messagesBody := func() map[string]any {
		return map[string]any{
			"model": "chat", "max_tokens": maxTokens,
			"messages": []map[string]string{{"role": "user", "content": "hi"}},
		}
	}
	countUpstream := func(hits *atomic.Int32) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			writeQuotaUsage(w, 0, 15)
		})
	}

	// 转换层入口：vendor 为空（自定义上游），messages / responses 走 chat 转换。
	var convertedHits atomic.Int32
	converted := newAnthropicFixtureVendor(t, countUpstream(&convertedHits), "", nil)
	convertedUser := converted.user(t, "quota-entries-conv", &limit)
	convertedToken := "ee_at_q_entries_conv"
	converted.token(t, convertedUser.ID, convertedToken, []string{rbac.ScopeModels}, nil, nil)

	// 原生透传入口：vendor=deepseek。
	var nativeHits atomic.Int32
	native := newAnthropicFixtureVendor(t, countUpstream(&nativeHits), "deepseek", nil)
	nativeUser := native.user(t, "quota-entries-native", &limit)
	nativeToken := "ee_at_q_entries_native"
	native.token(t, nativeUser.ID, nativeToken, []string{rbac.ScopeModels}, nil, nil)

	// DeepSeek beta 透传入口。
	var betaHits atomic.Int32
	beta := newBetaFixture(t, func(w http.ResponseWriter, r *http.Request, _ []byte) {
		betaHits.Add(1)
		writeQuotaUsage(w, 0, 15)
	}, func(f *betaFixture, _ *api.Options) {
		f.addProvider("deepseek-main", "deepseek", f.url, []string{"deepseek-flash"})
	})
	betaUser, betaToken := beta.issueToken("beta-quota-entries", &limit, nil)

	openAICode := func(t *testing.T, body []byte) string {
		t.Helper()
		return openAIErrorCode(t, body)
	}
	responsesCode := func(t *testing.T, body []byte) string {
		t.Helper()
		var resp struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("Responses 错误体不是合法 JSON：%v；%s", err, body)
		}
		if resp.Error.Code == "" || resp.Error.Message == "" {
			t.Fatalf("Responses 错误体缺字段：%s", body)
		}
		return resp.Error.Code
	}
	anthropicType := func(t *testing.T, body []byte) string {
		t.Helper()
		var resp struct {
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatalf("Anthropic 错误体不是合法 JSON：%v；%s", err, body)
		}
		if resp.Error.Type == "" || resp.Error.Message == "" {
			t.Fatalf("Anthropic 错误体缺字段：%s", body)
		}
		return resp.Error.Type
	}

	cases := []struct {
		name     string
		base     string
		path     string
		token    string
		body     map[string]any
		wantCode string
		check    func(*testing.T, []byte) string
	}{
		{
			name: "chat_completions", base: converted.ts.URL, path: "/v1/chat/completions", token: convertedToken,
			body: map[string]any{
				"model": "chat", "max_tokens": maxTokens,
				"messages": []map[string]string{{"role": "user", "content": "hi"}},
			},
			wantCode: "monthly_quota_exceeded", check: openAICode,
		},
		{
			name: "messages_converted", base: converted.ts.URL, path: "/v1/messages", token: convertedToken,
			body: messagesBody(), wantCode: "rate_limit_error", check: anthropicType,
		},
		{
			name: "responses_converted", base: converted.ts.URL, path: "/v1/responses", token: convertedToken,
			body:     map[string]any{"model": "chat", "input": "hi", "max_output_tokens": maxTokens},
			wantCode: "monthly_quota_exceeded", check: responsesCode,
		},
		{
			name: "messages_native", base: native.ts.URL, path: "/v1/messages", token: nativeToken,
			body: messagesBody(), wantCode: "rate_limit_error", check: anthropicType,
		},
		{
			name: "responses_native", base: native.ts.URL, path: "/v1/responses", token: nativeToken,
			body:     map[string]any{"model": "chat", "input": "hi", "max_output_tokens": maxTokens},
			wantCode: "monthly_quota_exceeded", check: responsesCode,
		},
		{
			name: "beta_completions", base: beta.ts.URL, path: "/beta/completions", token: betaToken,
			body:     map[string]any{"model": "deepseek-flash", "prompt": "hi", "max_tokens": maxTokens},
			wantCode: "monthly_quota_exceeded", check: openAICode,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := newClient(t, tc.base).do("POST", tc.path, tc.body, bearer(tc.token))
			if code != http.StatusTooManyRequests {
				t.Fatalf("max_tokens 超过剩余额度应 429，得到 %d；%s", code, body)
			}
			if got := tc.check(t, body); got != tc.wantCode {
				t.Fatalf("错误标识 = %q，期望 %q；%s", got, tc.wantCode, body)
			}
		})
	}
	if n := convertedHits.Load() + nativeHits.Load() + betaHits.Load(); n != 0 {
		t.Fatalf("被额度拒绝的请求不应转发上游，实际收到 %d 次", n)
	}
	// beta 的用户额度也未被这些被拒请求消耗。
	used, err := beta.st.MonthTokensByUser(context.Background(), betaUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if used != 0 {
		t.Fatalf("被拒绝的 beta 请求不应记账，实际用量 %d", used)
	}
}
