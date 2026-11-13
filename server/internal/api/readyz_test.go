package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// readyzBody 是 GET /readyz 的响应形状（与 api.readyResponse 一致；
// 这里单独定义一份，改坏形状时测试要能发现）。
type readyzBody struct {
	Status  string `json:"status"`
	Version string `json:"version"`
	Checks  map[string]struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"checks"`
}

// readyzServer 起一个只用于就绪检查的实例：数据目录用临时目录，版本号固定便于断言。
func readyzServer(t *testing.T, st *store.Store, dataDir string) *httptest.Server {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("生成主密钥: %v", err)
	}
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("主密钥: %v", err)
	}
	srv := api.New(st, box, api.Options{DataDir: dataDir, Version: "test-version"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

// getReadyz 发一次匿名 GET 请求（就绪探针不需要登录），返回状态码、响应头和响应体。
func getReadyz(t *testing.T, ts *httptest.Server) (int, http.Header, []byte) {
	t.Helper()
	return getProbe(t, ts.URL+"/readyz")
}

func getProbe(t *testing.T, url string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

func decodeReadyz(t *testing.T, body []byte) readyzBody {
	t.Helper()
	var got readyzBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("解析 /readyz 响应: %v\n%s", err, body)
	}
	return got
}

// 就绪检查的全部依赖正常时：200、status=ok、四项检查都是 ok，且响应里不带部署细节。
func TestReadyzReady(t *testing.T) {
	st := openTestStore(t)
	ts := readyzServer(t, st, t.TempDir())

	code, header, body := getReadyz(t, ts)
	expect(t, "就绪检查", code, 200, body)
	if ct := header.Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type 不对：%q", ct)
	}
	if cc := header.Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control 应为 no-store，实际 %q", cc)
	}
	got := decodeReadyz(t, body)
	if got.Status != "ok" {
		t.Fatalf("status 应为 ok：%s", body)
	}
	if got.Version != "test-version" {
		t.Fatalf("version 应为 test-version：%s", body)
	}
	for _, name := range []string{"database", "migrations", "data_dir", "secret_key"} {
		check, ok := got.Checks[name]
		if !ok {
			t.Fatalf("缺少检查项 %s：%s", name, body)
		}
		if check.Status != "ok" || check.Message != "" {
			t.Fatalf("检查项 %s 应为 ok：%s", name, body)
		}
	}
	// 探针可能被未授权的人访问：不能回 DSN、地址、主密钥或原始错误。
	if dsn := os.Getenv("DSH_EE_TEST_DSN"); dsn != "" && bytes.Contains(body, []byte(dsn)) {
		t.Fatalf("就绪响应泄露了 DSN：%s", body)
	}
	for _, banned := range []string{"tcp(", "password", "DSN", "root:"} {
		if bytes.Contains(bytes.ToLower(body), []byte(strings.ToLower(banned))) {
			t.Fatalf("就绪响应包含敏感内容 %q：%s", banned, body)
		}
	}
}

// 数据库关闭后：/readyz 变成 503 unready，数据库项失败；/healthz 仍然 200（只证明进程活）。
func TestReadyzDatabaseDown(t *testing.T) {
	st := openTestStore(t)
	ts := readyzServer(t, st, t.TempDir())

	code, _, body := getReadyz(t, ts)
	expect(t, "关闭前的就绪检查", code, 200, body)

	if err := st.Close(); err != nil {
		t.Fatalf("关闭数据库: %v", err)
	}

	code, _, body = getReadyz(t, ts)
	expect(t, "数据库关闭后的就绪检查", code, 503, body)
	got := decodeReadyz(t, body)
	if got.Status != "unready" {
		t.Fatalf("status 应为 unready：%s", body)
	}
	if check := got.Checks["database"]; check.Status != "fail" || check.Message == "" {
		t.Fatalf("database 检查应失败并带固定原因：%s", body)
	}

	// liveness 不查依赖：数据库断了进程还在，不该被重启。
	code, _, body = getProbe(t, ts.URL+"/healthz")
	expect(t, "数据库关闭后的存活检查", code, 200, body)
	if !bytes.Contains(body, []byte(`"status":"ok"`)) {
		t.Fatalf("/healthz 响应形状变了：%s", body)
	}
}

// 模拟一次没成功的迁移：schema_migrations 里没有最新版本时，/readyz 应为 503。
func TestReadyzMigrationPending(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	state, err := st.MigrationStatus(ctx)
	if err != nil {
		t.Fatalf("读迁移状态: %v", err)
	}
	if state.Latest == "" || len(state.Pending) != 0 {
		t.Fatalf("刚迁移完应无待执行项：latest=%q pending=%v", state.Latest, state.Pending)
	}
	// 相当于这次迁移执行到一半失败：行没写进 schema_migrations，下次启动才会重跑。
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = ?", state.Latest); err != nil {
		t.Fatalf("删除迁移记录: %v", err)
	}

	ts := readyzServer(t, st, t.TempDir())
	code, _, body := getReadyz(t, ts)
	expect(t, "迁移未执行完的就绪检查", code, 503, body)
	got := decodeReadyz(t, body)
	if got.Status != "unready" {
		t.Fatalf("status 应为 unready：%s", body)
	}
	if check := got.Checks["migrations"]; check.Status != "fail" || check.Message == "" {
		t.Fatalf("migrations 检查应失败并带固定原因：%s", body)
	}
	if check := got.Checks["database"]; check.Status != "ok" {
		t.Fatalf("数据库本身是通的，database 应仍为 ok：%s", body)
	}

	pending, err := st.MigrationStatus(ctx)
	if err != nil {
		t.Fatalf("再次读迁移状态: %v", err)
	}
	if len(pending.Pending) != 1 || pending.Pending[0] != state.Latest {
		t.Fatalf("pending 应只剩 %q：%v", state.Latest, pending.Pending)
	}
}

// 数据目录不是一个可写目录（这里用一个普通文件占位）：/readyz 应为 503，数据目录项失败。
func TestReadyzDataDirUnwritable(t *testing.T) {
	st := openTestStore(t)
	bad := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(bad, []byte("occupied"), 0o600); err != nil {
		t.Fatalf("占位文件: %v", err)
	}

	ts := readyzServer(t, st, bad)
	code, _, body := getReadyz(t, ts)
	expect(t, "数据目录不可写的就绪检查", code, 503, body)
	got := decodeReadyz(t, body)
	if got.Status != "unready" {
		t.Fatalf("status 应为 unready：%s", body)
	}
	if check := got.Checks["data_dir"]; check.Status != "fail" || check.Message == "" {
		t.Fatalf("data_dir 检查应失败并带固定原因：%s", body)
	}
	if check := got.Checks["database"]; check.Status != "ok" {
		t.Fatalf("数据库本身是通的，database 应仍为 ok：%s", body)
	}
}

// 只接受 GET（以及 Go 的 GET 隐式 HEAD）：POST 等其它方法返回 405。
func TestReadyzMethodNotAllowed(t *testing.T) {
	st := openTestStore(t)
	ts := readyzServer(t, st, t.TempDir())

	resp, err := http.Post(ts.URL+"/readyz", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /readyz: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	expect(t, "POST /readyz", resp.StatusCode, 405, body)
}
