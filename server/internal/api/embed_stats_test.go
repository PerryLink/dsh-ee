package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

// 管理端嵌入运行概览（GET /api/admin/embed/stats）：权限、空态、聚合、时区、不泄露
// 用户/IP/令牌，以及 /metrics 的两个低基数嵌入指标、终态会话的保留期。

// embedStatsPayload 是接口响应的形状；字段多了会在这里的严格解析里暴露。
type embedStatsPayload struct {
	GeneratedAt    string `json:"generated_at"`
	DayStart       string `json:"day_start"`
	ActiveSessions int    `json:"active_sessions"`
	CreatedToday   int    `json:"created_today"`
	ExpiredToday   int    `json:"expired_today"`
	RevokedToday   int    `json:"revoked_today"`
	Denied24h      int    `json:"denied_24h"`
	TopApps        []struct {
		App      string `json:"app"`
		Sessions int    `json:"sessions"`
	} `json:"top_apps"`
}

func (f *embedFixture) embedStats(t *testing.T, query string) (int, []byte) {
	t.Helper()
	return f.admin.do("GET", "/api/admin/embed/stats"+query, nil, nil)
}

func decodeEmbedStats(t *testing.T, body []byte) embedStatsPayload {
	t.Helper()
	var out embedStatsPayload
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("解析嵌入概览响应：%v\n%s", err, body)
	}
	return out
}

// insertEmbedSession 直接往库里写一条会话，createdAt/expiresAt/revokedAt 完全可控。
func (f *embedFixture) insertEmbedSession(t *testing.T, hash, app string, created, expires time.Time, revoked *time.Time) {
	t.Helper()
	var revokedAt any
	if revoked != nil {
		revokedAt = *revoked
	}
	_, err := f.st.DB().ExecContext(context.Background(),
		`INSERT INTO embed_sessions (token_hash, user_id, app_slug, host_origin, expires_at, created_at, revoked_at)
		 VALUES (?, 1, ?, ?, ?, ?, ?)`,
		hash, app, embedHost, expires, created, revokedAt)
	if err != nil {
		t.Fatalf("写入嵌入会话：%v", err)
	}
}

// insertEmbedDeny 往审计里写一条 embed.deny（统计只看动作名与时间，不看详情）。
func (f *embedFixture) insertEmbedDeny(t *testing.T, at time.Time) {
	t.Helper()
	if _, err := f.st.DB().ExecContext(context.Background(),
		`INSERT INTO audit_logs (action, created_at) VALUES ('embed.deny', ?)`, at); err != nil {
		t.Fatalf("写入 embed.deny 审计：%v", err)
	}
}

func TestEmbedStatsPermission(t *testing.T) {
	f := newEmbedFixture(t, nil)
	emp := f.user(t, "embed-stats-emp")

	// 普通员工：没有 settings.manage，读不到运行概览。
	empClient := newClient(t, f.ts.URL)
	code, body := empClient.do("POST", "/api/auth/login", map[string]string{"username": emp.Username, "password": "employee-password-1"}, nil)
	expect(t, "员工登录", code, 200, body)
	if code, body := empClient.do("GET", "/api/admin/embed/stats", nil, nil); code != http.StatusForbidden {
		t.Fatalf("非管理员读嵌入概览应 403：%d %s", code, body)
	}

	// 匿名：401。
	anon := newClient(t, f.ts.URL)
	if code, _ := anon.do("GET", "/api/admin/embed/stats", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("匿名读嵌入概览应 401，得到 %d", code)
	}

	// 管理员：200；tz 超出范围是 400。
	code, body = f.embedStats(t, "")
	expect(t, "管理员读嵌入概览", code, 200, body)
	if code, body := f.embedStats(t, "?tz=9999"); code != http.StatusBadRequest {
		t.Fatalf("非法 tz 应 400：%d %s", code, body)
	}
}

func TestEmbedStatsEmpty(t *testing.T) {
	f := newEmbedFixture(t, nil)
	code, body := f.embedStats(t, "")
	expect(t, "空库读嵌入概览", code, 200, body)

	got := decodeEmbedStats(t, body)
	if got.ActiveSessions != 0 || got.CreatedToday != 0 || got.ExpiredToday != 0 || got.RevokedToday != 0 || got.Denied24h != 0 {
		t.Fatalf("空库所有计数应为 0：%+v", got)
	}
	if got.TopApps == nil || len(got.TopApps) != 0 {
		t.Fatalf("空库 top_apps 应是空数组：%s", body)
	}
	if !strings.Contains(string(body), `"top_apps":[]`) {
		t.Fatalf("空库 top_apps 应序列化为 []，不能是 null：%s", body)
	}
	if got.GeneratedAt == "" || got.DayStart == "" {
		t.Fatalf("概览应带 generated_at / day_start：%s", body)
	}
}

func TestEmbedStatsAggregation(t *testing.T) {
	f := newEmbedFixture(t, nil)
	now := time.Now().UTC()
	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	if now.Sub(dayStart) < 2*time.Minute {
		t.Skip("刚好在 UTC 零点附近，按天聚合的结果不稳定")
	}
	todayCreated := dayStart.Add(time.Minute)
	inHour := now.Add(time.Hour)

	// 活跃：应用 a 两条、应用 b 一条，都是今天创建的。
	f.insertEmbedSession(t, "h-a1", "app-a", todayCreated, inHour, nil)
	f.insertEmbedSession(t, "h-a2", "app-a", todayCreated, inHour, nil)
	f.insertEmbedSession(t, "h-b1", "app-b", todayCreated, inHour, nil)
	// 今天过期（未吊销）：expires_at 落在本地今天之内。
	f.insertEmbedSession(t, "h-expired", "app-c", dayStart.Add(-time.Hour), now.Add(-time.Minute), nil)
	// 今天吊销：revoked_at 在本地今天之内，会话本身还没到期。
	f.insertEmbedSession(t, "h-revoked", "app-d", dayStart.Add(-time.Hour), inHour, ptrTime(dayStart.Add(30*time.Second)))
	// 昨天过期 / 昨天吊销：都不进今日计数。
	f.insertEmbedSession(t, "h-old-expired", "app-e", dayStart.Add(-3*time.Hour), dayStart.Add(-2*time.Hour), nil)
	f.insertEmbedSession(t, "h-old-revoked", "app-f", dayStart.Add(-3*time.Hour), inHour, ptrTime(dayStart.Add(-2*time.Hour)))

	// 24 小时拒绝：两条在窗口内（含 23 小时前），一条在窗口外。
	f.insertEmbedDeny(t, now.Add(-time.Hour))
	f.insertEmbedDeny(t, now.Add(-23*time.Hour))
	f.insertEmbedDeny(t, now.Add(-25*time.Hour))

	code, body := f.embedStats(t, "?tz=0")
	expect(t, "读嵌入概览", code, 200, body)
	got := decodeEmbedStats(t, body)
	if got.ActiveSessions != 3 || got.CreatedToday != 3 || got.ExpiredToday != 1 || got.RevokedToday != 1 || got.Denied24h != 2 {
		t.Fatalf("聚合结果不对：%+v（活跃 3、今日创建 3、今日过期 1、今日吊销 1、24h 拒绝 2）", got)
	}
	if len(got.TopApps) != 2 || got.TopApps[0].App != "app-a" || got.TopApps[0].Sessions != 2 || got.TopApps[1].App != "app-b" || got.TopApps[1].Sessions != 1 {
		t.Fatalf("top_apps 应按活跃数倒序且只含活跃应用：%+v", got.TopApps)
	}
	// day_start 就是 UTC 零点。
	parsed, err := time.Parse(time.RFC3339, got.DayStart)
	if err != nil || !parsed.Equal(dayStart) {
		t.Fatalf("day_start 应等于 UTC 零点：%q %v", got.DayStart, err)
	}
}

// ptrTime 给「可空的吊销时间」用。
func ptrTime(t time.Time) *time.Time { return &t }

// TestEmbedStatsFlowAndNoLeak 走真实的 mint/exchange/拒绝/注销，确认统计正确，
// 且响应里没有用户、IP、令牌、宿主 origin 或 code。
func TestEmbedStatsFlowAndNoLeak(t *testing.T) {
	f := newEmbedFixture(t, nil)
	emp := f.user(t, "embed-stats-secret-user")
	const tokenPlain = "ee_at_embed_stats_secret"
	f.token(t, emp.ID, tokenPlain, []string{rbac.ScopeHub, rbac.ScopeAgent})

	code, body := f.mint(t, tokenPlain, map[string]any{"app": f.slug, "host_origin": embedHost})
	expect(t, "mint", code, 200, body)
	oneTimeCode := decode[struct{ Code string }](t, body).Code
	resp, exchangeBody := f.exchange(t, oneTimeCode)
	if resp.StatusCode != 200 {
		t.Fatalf("exchange 应成功：%d %s", resp.StatusCode, exchangeBody)
	}
	embedToken := decode[struct{ Token string }](t, exchangeBody).Token

	// 来源不在白名单：制造一次 embed.deny。
	code, body = f.mint(t, tokenPlain, map[string]any{"app": f.slug, "host_origin": "https://evil.example.com"})
	if code != http.StatusForbidden {
		t.Fatalf("非法来源应 403：%d %s", code, body)
	}

	code, body = f.embedStats(t, "")
	expect(t, "读嵌入概览", code, 200, body)
	got := decodeEmbedStats(t, body)
	if got.ActiveSessions != 1 || got.CreatedToday < 1 || got.Denied24h < 1 {
		t.Fatalf("真实流程后的统计不对：%+v", got)
	}
	if len(got.TopApps) != 1 || got.TopApps[0].App != f.slug || got.TopApps[0].Sessions != 1 {
		t.Fatalf("top_apps 应只有被嵌入的应用：%+v", got.TopApps)
	}

	// 不泄密：响应体里不能出现令牌、code、用户名、宿主 origin。
	for _, secret := range []string{tokenPlain, embedToken, oneTimeCode, emp.Username, embedHost} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("响应里泄露了 %q：%s", secret, body)
		}
	}
	// 顶层只允许这几个键；top_apps 每条只有 app/sessions。
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"generated_at": true, "day_start": true, "active_sessions": true, "created_today": true,
		"expired_today": true, "revoked_today": true, "denied_24h": true, "top_apps": true,
	}
	for key := range top {
		if !allowed[key] {
			t.Fatalf("响应里有计划外的字段 %q：%s", key, body)
		}
	}
	for key := range decode[[]map[string]json.RawMessage](t, top["top_apps"])[0] {
		if key != "app" && key != "sessions" {
			t.Fatalf("top_apps 里不应有 %q：%s", key, body)
		}
	}

	// 注销后：活跃归零，今日吊销 +1。
	if code, _ := newClient(t, f.ts.URL).do("POST", "/api/auth/embed/logout", map[string]any{}, bearer(embedToken)); code != http.StatusNoContent {
		t.Fatalf("嵌入会话注销应 204，得到 %d", code)
	}
	code, body = f.embedStats(t, "")
	expect(t, "注销后读嵌入概览", code, 200, body)
	got = decodeEmbedStats(t, body)
	if got.ActiveSessions != 0 || len(got.TopApps) != 0 || got.RevokedToday < 1 {
		t.Fatalf("注销后的统计不对：%+v", got)
	}
}

// TestEmbedStatsRetention：终态会话在保留期内保留（当天按天统计不会丢），超期后物理删除。
func TestEmbedStatsRetention(t *testing.T) {
	f := newEmbedFixture(t, nil)
	now := time.Now().UTC()
	inHour := now.Add(time.Hour)
	// 保留期内：过期未吊销、已吊销。
	f.insertEmbedSession(t, "h-recent-expired", "app-a", now.Add(-2*time.Hour), now.Add(-time.Hour), nil)
	f.insertEmbedSession(t, "h-recent-revoked", "app-a", now.Add(-2*time.Hour), inHour, ptrTime(now.Add(-time.Hour)))
	// 超过保留期：过期、已吊销。
	f.insertEmbedSession(t, "h-old-expired", "app-a", now.Add(-9*24*time.Hour), now.Add(-8*24*time.Hour), nil)
	f.insertEmbedSession(t, "h-old-revoked", "app-a", now.Add(-9*24*time.Hour), inHour, ptrTime(now.Add(-8*24*time.Hour)))

	n, err := f.st.DeleteExpiredEmbedSessions(context.Background())
	if err != nil {
		t.Fatalf("清理：%v", err)
	}
	if n != 2 {
		t.Fatalf("应只清理超过保留期的 2 条，实际 %d", n)
	}
	var left int
	if err := f.st.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM embed_sessions").Scan(&left); err != nil || left != 2 {
		t.Fatalf("保留期内应剩 2 条：%d %v", left, err)
	}
}

// TestEmbedMetrics：/metrics 的嵌入指标低基数且口径正确（活跃会话来自数据库，拒绝计数进程内累计）。
func TestEmbedMetrics(t *testing.T) {
	f := newEmbedFixture(t, func(o *api.Options) { o.Metrics = true })
	emp := f.user(t, "embed-metrics-user")
	const tokenPlain = "ee_at_embed_metrics"
	f.token(t, emp.ID, tokenPlain, []string{rbac.ScopeHub, rbac.ScopeAgent})

	code, body := f.mint(t, tokenPlain, map[string]any{"app": f.slug, "host_origin": embedHost})
	expect(t, "mint", code, 200, body)
	oneTimeCode := decode[struct{ Code string }](t, body).Code
	if resp, exchangeBody := f.exchange(t, oneTimeCode); resp.StatusCode != 200 {
		t.Fatalf("exchange：%d %s", resp.StatusCode, exchangeBody)
	}
	code, _ = f.mint(t, tokenPlain, map[string]any{"app": f.slug, "host_origin": "https://evil.example.com"})
	if code != http.StatusForbidden {
		t.Fatalf("非法来源应被拒绝：%d", code)
	}

	resp, err := http.Get(f.ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	text := string(raw)
	for _, want := range []string{
		"# TYPE dsh_ee_embed_sessions_active gauge",
		"dsh_ee_embed_sessions_active 1",
		"# TYPE dsh_ee_embed_denied_total counter",
		"dsh_ee_embed_denied_total 1",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("/metrics 缺少 %q：\n%s", want, text)
		}
	}
}
