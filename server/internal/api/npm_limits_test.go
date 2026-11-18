package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// npm 缓存总量上限端到端：GET /npm/... 触发的按需回源在超限后返回 507，
// 错误体是 npm 协议面的普通 JSON（writeErr / errf 风格），已缓存的 tarball 仍能下载。
//
// 上限按「已用 + 单个 tarball 上限（100 MiB）」计算预留空间，所以这里设成
// 「100 MiB + 第一个包的大小 - 1」：第一个包能进，第二个包必然越界。
func TestNpmCacheLimitHTTP(t *testing.T) {
	st := openTestStore(t)
	up := newFakeRegistry(t)
	pkgA := map[string]any{"name": "cache-http-a", "version": "1.0.0", "license": "MIT"}
	pkgB := map[string]any{"name": "cache-http-b", "version": "1.0.0", "license": "MIT"}
	up.add(t, pkgA, nil)
	up.add(t, pkgB, nil)
	sizeA := int64(len(makeTgz(t, pkgA, nil)))

	set := store.DefaultSettings()
	set.Upstream.Registry = "custom"
	set.Upstream.CustomRegistry = up.URL
	set.Registry.ProxyMode = "cache"
	// 免令牌：这个测试只关心缓存上限，不关心鉴权（鉴权有既有测试覆盖）。
	set.Registry.RequireToken = false
	if err := st.SaveSettings(context.Background(), set, 0); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(st, box, api.Options{
		DataDir:          t.TempDir(),
		NpmUpstream:      up.Client(),
		FeedURL:          up.URL + "/feed.json",
		NpmCacheMaxBytes: (100 << 20) + sizeA - 1,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	get := func(path string) (int, []byte) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("读 %s 响应: %v", path, err)
		}
		return resp.StatusCode, body
	}

	// packument 是元数据，不落 tarball，不受缓存上限影响。
	if code, body := get("/npm/cache-http-a"); code != http.StatusOK {
		t.Fatalf("packument 应 200：%d %s", code, body)
	}
	// 上限内的第一个 tarball 正常回源。
	code, body := get("/npm/cache-http-a/-/cache-http-a-1.0.0.tgz")
	if code != http.StatusOK {
		t.Fatalf("上限内第一次按需回源应 200：%d %s", code, body)
	}
	if int64(len(body)) != sizeA {
		t.Fatalf("下载的 tarball 大小不对：%d，期望 %d", len(body), sizeA)
	}

	// 第二个包：缓存预留空间不足，回源前拒绝，返回 507 + 明确的 JSON 错误文案。
	code, body = get("/npm/cache-http-b/-/cache-http-b-1.0.0.tgz")
	if code != http.StatusInsufficientStorage {
		t.Fatalf("缓存超限时新的按需回源应 507：%d %s", code, body)
	}
	var npmErr struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &npmErr); err != nil {
		t.Fatalf("507 应保持 npm 协议面的 JSON 错误体：%v %s", err, body)
	}
	if !strings.Contains(npmErr.Error, "cache") || !strings.Contains(npmErr.Error, "DSH_EE_NPM_CACHE_MAX_BYTES") {
		t.Fatalf("507 的错误文案应说明缓存上限与调整方式：%q", npmErr.Error)
	}

	// 已缓存的 tarball 仍可下载（不经过缓存上限检查）。
	code, body = get("/npm/cache-http-a/-/cache-http-a-1.0.0.tgz")
	if code != http.StatusOK || int64(len(body)) != sizeA {
		t.Fatalf("缓存超限不该影响已缓存内容的下载：%d %d", code, len(body))
	}
	t.Logf("修后：第一个包回源 200（%d 字节），第二个包因缓存上限 507，已缓存包再次下载仍 200", sizeA)
}

// 发布路径同样受 npm 缓存总量上限约束的 HTTP 端到端：
// 超限的 npm publish 返回 507（错误文案带调整方式），不写 blob、不入库；
// 已上架版本仍可下载；上限内的正常发布不回归。
func TestNpmPublishCacheLimitHTTP(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	up := newFakeRegistry(t)
	pkgA := map[string]any{"name": "@company/pub-cache-a", "version": "1.0.0", "license": "MIT"}
	pkgB := map[string]any{"name": "@company/pub-cache-b", "version": "1.0.0", "license": "MIT"}
	up.add(t, pkgA, nil)
	up.add(t, pkgB, nil)
	tgzA := makeTgz(t, pkgA, nil)
	tgzB := makeTgz(t, pkgB, nil)

	set := store.DefaultSettings()
	// 发布后直接上架，便于验证「超限时已上架内容仍可读」。
	set.AdmissionMode = "open"
	set.Registry.RequireToken = false
	if err := st.SaveSettings(ctx, set, 0); err != nil {
		t.Fatal(err)
	}
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	srv := api.New(st, box, api.Options{
		DataDir:          t.TempDir(),
		NpmUpstream:      up.Client(),
		FeedURL:          up.URL + "/feed.json",
		NpmCacheMaxBytes: int64(len(tgzA) + len(tgzB) - 1),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	// 上限内的发布正常成功（正常发布不回归）。
	code, body = admin.do("PUT", "/npm/@company%2fpub-cache-a", publishBody("@company/pub-cache-a", "1.0.0", tgzA, pkgA, ""), nil)
	expect(t, "上限内发布", code, 201, body)

	// 已上架版本可下载。
	code, body = admin.do("GET", "/npm/@company/pub-cache-a/-/pub-cache-a-1.0.0.tgz", nil, nil)
	expect(t, "已上架 tarball 可下载", code, 200, body)
	if len(body) != len(tgzA) {
		t.Fatalf("已上架 tarball 大小不对：%d != %d", len(body), len(tgzA))
	}

	// 第二个包超限：写盘前拒绝，507 + npm 协议面的 JSON 错误体。
	code, body = admin.do("PUT", "/npm/@company%2fpub-cache-b", publishBody("@company/pub-cache-b", "1.0.0", tgzB, pkgB, ""), nil)
	expect(t, "超限发布", code, http.StatusInsufficientStorage, body)
	var pubErr struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &pubErr); err != nil || !strings.Contains(pubErr.Error, "DSH_EE_NPM_CACHE_MAX_BYTES") {
		t.Fatalf("507 应返回说明调整上限的 JSON 错误：%v %s", err, body)
	}
	// 被拒绝的版本没有入库，也不可下载。
	code, _ = admin.do("GET", "/npm/@company/pub-cache-b/-/pub-cache-b-1.0.0.tgz", nil, nil)
	if code != http.StatusNotFound {
		t.Fatalf("被拒绝的发布不该可下载：%d", code)
	}
	// 超限后已上架版本仍可下载。
	code, body = admin.do("GET", "/npm/@company/pub-cache-a/-/pub-cache-a-1.0.0.tgz", nil, nil)
	expect(t, "超限后已上架 tarball 仍可下载", code, 200, body)
	t.Logf("修后端到端：上限内发布 201；第 2 个包发布 507（未入库/不可下载）；已上架 tarball 超限后仍 200")
}
