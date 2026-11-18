package registry

import (
	"context"
	"crypto/sha1"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 这两个测试覆盖审计里的「npm 按需缓存无上限」：
//  1. 按需回源 tarball 有全局并发上限，超出的请求直接 503（不排队、不发起上游下载），
//     已缓存内容直接读盘、不占名额；
//  2. blobs 目录有总量上限，超限时在写盘之前拒绝新的回源，已缓存内容仍可读。

// fakeNpmUpstream 是只服务 packument 与 tarball 的假上游。
// gate 非空时 tarball 处理器先等 gate 关闭（用来把按需下载卡在「进行中」）。
type fakeNpmUpstream struct {
	*httptest.Server
	mu          sync.Mutex
	docs        map[string]map[string]any
	files       map[string][]byte // "<name>/<tarball 文件名>" → 内容
	tarballHits map[string]int
	gate        chan struct{}
	entered     chan struct{}
}

func newFakeNpmUpstream(t *testing.T) *fakeNpmUpstream {
	t.Helper()
	f := &fakeNpmUpstream{
		docs:        map[string]map[string]any{},
		files:       map[string][]byte{},
		tarballHits: map[string]int{},
		entered:     make(chan struct{}, 16),
	}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

// add 登记一个包的一个版本：元数据里的 dist.tarball 指向自己，内容是一个最小 tarball。
func (f *fakeNpmUpstream) add(t *testing.T, name, version string) int64 {
	t.Helper()
	raw := tgz(t, map[string]string{
		"package/package.json": fmt.Sprintf(`{"name":%q,"version":%q}`, name, version),
	})
	s512 := sha512.Sum512(raw)
	s1 := sha1.Sum(raw)
	file := TarballFile(name, version)
	doc := map[string]any{
		"name":      name,
		"dist-tags": map[string]string{"latest": version},
		"versions": map[string]any{version: map[string]any{
			"name": name, "version": version,
			"dist": map[string]any{
				"tarball":   f.URL + "/" + name + "/-/" + file,
				"integrity": "sha512-" + base64.StdEncoding.EncodeToString(s512[:]),
				"shasum":    hex.EncodeToString(s1[:]),
			},
		}},
		"time": map[string]string{version: time.Now().UTC().Format(time.RFC3339Nano)},
	}
	f.mu.Lock()
	f.docs[name] = doc
	f.files[name+"/"+file] = raw
	f.mu.Unlock()
	return int64(len(raw))
}

func (f *fakeNpmUpstream) serve(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if i := strings.Index(p, "/-/"); i >= 0 {
		name, file := p[:i], p[i+3:]
		f.mu.Lock()
		f.tarballHits[name]++
		gate := f.gate
		raw, ok := f.files[name+"/"+file]
		f.mu.Unlock()
		if gate != nil {
			select {
			case f.entered <- struct{}{}:
			default:
			}
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(raw)
		return
	}
	f.mu.Lock()
	doc, ok := f.docs[p]
	f.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(doc)
}

// setGate 设置（或清除，传 nil）tarball 的阻塞闸门。
func (f *fakeNpmUpstream) setGate(gate chan struct{}) {
	f.mu.Lock()
	f.gate = gate
	f.mu.Unlock()
}

func (f *fakeNpmUpstream) tarballCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tarballHits[name]
}

// newMirrorService 建一个「按需回源」配置的 Service：上游指向假 registry。
func newMirrorService(t *testing.T, upstream *fakeNpmUpstream) *Service {
	t.Helper()
	st := openRegistryTestStore(t)
	set := store.DefaultSettings()
	set.Upstream.Registry = "custom"
	set.Upstream.CustomRegistry = upstream.URL
	set.Registry.ProxyMode = "cache"
	if err := st.SaveSettings(context.Background(), set, 0); err != nil {
		t.Fatal(err)
	}
	blobs, err := NewBlobs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatal(err)
	}
	up, err := NewUpstream(&http.Client{Transport: rewriteTransport{target: target}}, t.TempDir(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return NewService(st, blobs, up, st.GetSettings, slog.New(slog.NewTextHandler(io.Discard, nil)), "test-instance")
}

func unblockChannel(ch chan struct{}) func() {
	var once sync.Once
	return func() { once.Do(func() { close(ch) }) }
}

func waitEntered(t *testing.T, upstream *fakeNpmUpstream) {
	t.Helper()
	select {
	case <-upstream.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("按需回源没有开始下载上游 tarball")
	}
}

// N+1 个按需 tarball 请求：只有 N 个能进入上游下载，多出来的立即 503；
// 已缓存内容直接读盘，不占按需回源名额。
func TestNpmOnDemandTarballConcurrencyLimit(t *testing.T) {
	up := newFakeNpmUpstream(t)
	up.add(t, "conc-a", "1.0.0")
	up.add(t, "conc-b", "1.0.0")
	svc := newMirrorService(t, up)
	svc.MaxOnDemandConcurrency = 1
	ctx := context.Background()

	gate := make(chan struct{})
	up.setGate(gate)
	unblock := unblockChannel(gate)
	t.Cleanup(unblock)

	type result struct {
		file *os.File
		err  error
	}
	first := make(chan result, 1)
	go func() {
		f, _, err := svc.Tarball(ctx, "conc-a", "1.0.0")
		first <- result{f, err}
	}()
	waitEntered(t, up)

	// 名额已满：另外两个按需请求（不同包、同一个包）都直接 503，且不会请求上游 tarball。
	for _, name := range []string{"conc-b", "conc-a"} {
		if _, _, err := svc.Tarball(ctx, name, "1.0.0"); err == nil {
			t.Fatalf("%s 在名额已满时应被拒绝", name)
		} else {
			wantErrStatus(t, err, 503)
		}
	}
	if got := up.tarballCount("conc-b"); got != 0 {
		t.Fatalf("被并发闸门拒绝的请求不该下载上游 tarball，实际请求了 %d 次", got)
	}
	t.Logf("修后：并发上限=1，占住 1 个名额时另外 2 个按需请求立即 503，且没有发起上游 tarball 下载")

	// 放行第一次下载：成功、名额释放。
	unblock()
	got := <-first
	if got.err != nil {
		t.Fatalf("占住名额的按需回源应成功：%v", got.err)
	}
	if got.file == nil {
		t.Fatal("成功时应有 tarball 文件")
	}
	got.file.Close()

	// 再卡住一个回源，同时读已缓存版本：已缓存内容不该被闸门影响。
	gate2 := make(chan struct{})
	up.setGate(gate2)
	unblock2 := unblockChannel(gate2)
	t.Cleanup(unblock2)
	second := make(chan result, 1)
	go func() {
		f, _, err := svc.Tarball(ctx, "conc-b", "1.0.0")
		second <- result{f, err}
	}()
	waitEntered(t, up)
	cachedFile, cachedVersion, err := svc.Tarball(ctx, "conc-a", "1.0.0")
	if err != nil {
		t.Fatalf("已缓存内容不该被回源闸门拒绝：%v", err)
	}
	if st, statErr := cachedFile.Stat(); statErr != nil || st.Size() != cachedVersion.Size {
		t.Fatalf("已缓存 tarball 读取不对：%v size=%d/%d", statErr, st.Size(), cachedVersion.Size)
	}
	cachedFile.Close()

	unblock2()
	gotSecond := <-second
	if gotSecond.err != nil {
		t.Fatalf("第二次按需回源应成功：%v", gotSecond.err)
	}
	gotSecond.file.Close()

	svc.onDemand.mu.Lock()
	running := svc.onDemand.running
	svc.onDemand.mu.Unlock()
	if running != 0 {
		t.Fatalf("所有请求结束后闸门名额应全部释放，仍有 %d 个", running)
	}
}

// 缓存总量超限：新的按需回源在写盘之前被拒（507），已缓存内容仍可读；0 表示不限。
func TestNpmCacheMaxBytesRejectsNewMirror(t *testing.T) {
	up := newFakeNpmUpstream(t)
	up.add(t, "cache-a", "1.0.0")
	up.add(t, "cache-b", "1.0.0")
	svc := newMirrorService(t, up)
	// 单 tarball 上限调小，让「按最坏情况预留」的边界在小测试里可精确计算。
	svc.MaxTarball = 1 << 20
	svc.MaxCacheBytes = 1 << 20
	ctx := context.Background()

	f, v, err := svc.Tarball(ctx, "cache-a", "1.0.0")
	if err != nil {
		t.Fatalf("上限内的第一次回源应成功：%v", err)
	}
	f.Close()
	used := svc.Blobs().Usage()
	if used <= 0 || used != v.Size {
		t.Fatalf("缓存用量统计不对：used=%d size=%d", used, v.Size)
	}

	// 收紧到「已用 + 单个 tarball 上限 - 1」：任何新的回源都可能越界，必须在下载前拒绝。
	svc.MaxCacheBytes = used + svc.MaxTarball - 1
	if _, _, err := svc.Tarball(ctx, "cache-b", "1.0.0"); err == nil {
		t.Fatal("缓存超限时新的回源应被拒绝")
	} else {
		wantErrStatus(t, err, 507)
	}
	if got := up.tarballCount("cache-b"); got != 0 {
		t.Fatalf("缓存超限应在下载前拒绝，不该请求上游 tarball（实际 %d 次）", got)
	}
	if after := svc.Blobs().Usage(); after != used {
		t.Fatalf("被拒绝的下载不该占用缓存空间：%d → %d", used, after)
	}
	t.Logf("修后：缓存上限=%d 字节（已用 %d + 单 tarball 上限 %d - 1），新回源返回 507 且未请求上游；已缓存 tarball 仍可读", svc.MaxCacheBytes, used, svc.MaxTarball)

	// 已缓存内容仍然可读：读取不经过缓存上限检查。
	cached, _, err := svc.Tarball(ctx, "cache-a", "1.0.0")
	if err != nil {
		t.Fatalf("缓存超限不该影响已缓存内容的读取：%v", err)
	}
	if st, statErr := cached.Stat(); statErr != nil || st.Size() != v.Size {
		t.Fatalf("已缓存 tarball 读取不对：%v", statErr)
	}
	cached.Close()

	// 0 = 不限：放开后能继续回源。
	svc.MaxCacheBytes = 0
	fb, _, err := svc.Tarball(ctx, "cache-b", "1.0.0")
	if err != nil {
		t.Fatalf("关闭上限后应能继续回源：%v", err)
	}
	fb.Close()
}

// blobs 用量统计：写入累加、重复内容只算一次、重启后从磁盘重建。
func TestBlobsUsageCountsExistingFiles(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put(strings.NewReader("hello"), 100); err != nil {
		t.Fatal(err)
	}
	if got := b.Usage(); got != 5 {
		t.Fatalf("写入 5 字节后用量应为 5，得到 %d", got)
	}
	// 同一内容重复写入（按内容寻址）不重复计费。
	if _, err := b.Put(strings.NewReader("hello"), 100); err != nil {
		t.Fatal(err)
	}
	if got := b.Usage(); got != 5 {
		t.Fatalf("重复写入同一内容不该增加用量，得到 %d", got)
	}
	if _, err := b.Put(strings.NewReader("world!"), 100); err != nil {
		t.Fatal(err)
	}
	if got := b.Usage(); got != 11 {
		t.Fatalf("再写 6 字节后用量应为 11，得到 %d", got)
	}
	// 模拟进程重启：新实例需要从磁盘重建用量，否则重启前的缓存不占额度。
	again, err := NewBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Usage(); got != 11 {
		t.Fatalf("重启后用量应从磁盘重建为 11，得到 %d", got)
	}
}

// publishTestDoc 组一个 npm publish 请求体（tarball 在 _attachments 里，base64 编码）。
func publishTestDoc(t *testing.T, name, version string) (*PublishDoc, []byte) {
	t.Helper()
	raw := tgz(t, map[string]string{
		"package/package.json": fmt.Sprintf(`{"name":%q,"version":%q,"license":"MIT"}`, name, version),
	})
	s512 := sha512.Sum512(raw)
	s1 := sha1.Sum(raw)
	vdoc := map[string]any{
		"name": name, "version": version, "license": "MIT",
		"dist": map[string]any{
			"integrity": "sha512-" + base64.StdEncoding.EncodeToString(s512[:]),
			"shasum":    hex.EncodeToString(s1[:]),
		},
	}
	body := map[string]any{
		"_id": name, "name": name,
		"dist-tags": map[string]string{"latest": version},
		"versions":  map[string]any{version: vdoc},
		"_attachments": map[string]any{TarballFile(name, version): map[string]any{
			"content_type": "application/octet-stream",
			"data":         base64.StdEncoding.EncodeToString(raw),
			"length":       len(raw),
		}},
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var doc PublishDoc
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return &doc, raw
}

// 发布也受磁盘总量上限约束：超限时在写盘前拒绝（507），不写 blob、不入库、
// 已发布版本仍可读；上限调大/关闭后同一条发布请求能成功（不会留下半装状态）。
func TestNpmCacheMaxBytesRejectsPublish(t *testing.T) {
	svc := newMirrorService(t, newFakeNpmUpstream(t))
	ctx := context.Background()
	pub := Publisher{Username: "tester", AutoPublish: true}

	docA, rawA := publishTestDoc(t, "@company/cache-pub-a", "1.0.0")
	docB, rawB := publishTestDoc(t, "@company/cache-pub-b", "1.0.0")
	svc.MaxCacheBytes = int64(len(rawA) + len(rawB) - 1)

	if _, err := svc.Publish(ctx, pub, docA); err != nil {
		t.Fatalf("上限内的发布应成功：%v", err)
	}
	used := svc.Blobs().Usage()
	if used != int64(len(rawA)) {
		t.Fatalf("发布后用量应等于 tarball 大小：%d != %d", used, len(rawA))
	}

	if _, err := svc.Publish(ctx, pub, docB); err == nil {
		t.Fatal("缓存超限时新的发布应被拒绝")
	} else {
		wantErrStatus(t, err, 507)
		var re *Error
		if !errors.As(err, &re) || !strings.Contains(re.Msg, "DSH_EE_NPM_CACHE_MAX_BYTES") {
			t.Fatalf("507 文案应说明如何调整上限：%v", err)
		}
	}
	if _, err := svc.store.GetVersion(ctx, "@company/cache-pub-b", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("被拒绝的发布不该入库：%v", err)
	}
	if got := svc.Blobs().Usage(); got != used {
		t.Fatalf("被拒绝的发布不该占用缓存空间：%d → %d", used, got)
	}

	// 已发布版本不受影响：读取走已有的 blob，不经过上限检查。
	f, v, err := svc.Tarball(ctx, "@company/cache-pub-a", "1.0.0")
	if err != nil {
		t.Fatalf("缓存超限不该影响已发布版本的读取：%v", err)
	}
	if st, statErr := f.Stat(); statErr != nil || st.Size() != int64(len(rawA)) || v.Size != int64(len(rawA)) {
		t.Fatalf("已发布 tarball 读取不对：%v size=%d/%d", statErr, v.Size, len(rawA))
	}
	f.Close()

	// 0 = 不限：正常发布不回归（同一条 B 请求现在能成功）。
	svc.MaxCacheBytes = 0
	if _, err := svc.Publish(ctx, pub, docB); err != nil {
		t.Fatalf("关闭上限后发布应成功：%v", err)
	}
	fb, _, err := svc.Tarball(ctx, "@company/cache-pub-b", "1.0.0")
	if err != nil {
		t.Fatalf("关闭上限后发布的版本应可读：%v", err)
	}
	fb.Close()
	t.Logf("修后：发布路径同样受上限约束——超限发布返回 507 且不写盘/不入库，已发布版本仍可读；0=不限时恢复")
}

// 周期性重扫：外部删除/新增 blob（模拟多实例共享同一块盘、或人工清理）后，
// 到间隔或强制 Rescan 时用量要回到磁盘实际值，而不是长期停在旧计数。
func TestBlobsRescanPicksUpExternalChanges(t *testing.T) {
	dir := t.TempDir()
	b, err := NewBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, err := b.Put(strings.NewReader("aaaaa"), 100)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Put(strings.NewReader("bbbbbb"), 100); err != nil {
		t.Fatal(err)
	}
	if got := b.Usage(); got != 11 {
		t.Fatalf("两个 blob 共 11 字节，得到 %d", got)
	}

	// 外部删除（不是通过本实例）：内存计数仍停在 11，间隔未到时不重扫。
	if err := os.Remove(b.path(a.Key)); err != nil {
		t.Fatal(err)
	}
	b.rescanEvery = time.Hour
	if got := b.RefreshIfStale(); got != 11 {
		t.Fatalf("扫描间隔未到，不该重扫：%d", got)
	}
	// 强制重扫后与磁盘一致。
	if got, err := b.Rescan(); err != nil || got != 6 {
		t.Fatalf("强制重扫后应为 6 字节：%d %v", got, err)
	}

	// 外部新增（另一个实例写同一块盘）：本实例不知道，直到周期重扫。
	other, err := NewBlobs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Put(strings.NewReader("ccccccc"), 100); err != nil {
		t.Fatal(err)
	}
	if got := b.Usage(); got != 6 {
		t.Fatalf("重扫前本实例计数不应变化：%d", got)
	}
	b.rescanEvery = time.Nanosecond
	if got := b.RefreshIfStale(); got != 13 {
		t.Fatalf("周期重扫应把外部写入算进来：%d", got)
	}
	if got := b.Usage(); got != 13 {
		t.Fatalf("重扫后 Usage 应与磁盘一致：%d", got)
	}
	// 重扫之后本实例的写入继续正常累加。
	if _, err := b.Put(strings.NewReader("dd"), 100); err != nil {
		t.Fatal(err)
	}
	if got := b.Usage(); got != 15 {
		t.Fatalf("重扫后的写入应继续累加：%d", got)
	}
	t.Logf("修后：Rescan/RefreshIfStale 后用量与磁盘一致（外部增删都能反映，最多滞后一个扫描周期）")
}

// 周期重扫真正接进上限判断：模拟另一个实例往共享盘写了本实例不知道的 blob，
// 重扫后发布即被 507 拒绝；外部文件清掉并重扫后恢复正常发布。
func TestNpmCacheRescanAppliedToPublish(t *testing.T) {
	svc := newMirrorService(t, newFakeNpmUpstream(t))
	ctx := context.Background()
	pub := Publisher{Username: "tester", AutoPublish: true}
	docA, rawA := publishTestDoc(t, "@company/rescan-a", "1.0.0")
	docB, rawB := publishTestDoc(t, "@company/rescan-b", "1.0.0")
	// A + B 本来都放得下；外部多出 2 KiB 后 B 就会越界。
	svc.MaxCacheBytes = int64(len(rawA)+len(rawB)) + 1024
	if _, err := svc.Publish(ctx, pub, docA); err != nil {
		t.Fatalf("发布 A：%v", err)
	}

	external := filepath.Join(svc.blobs.dir, "external-from-other-instance")
	if err := os.WriteFile(external, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	// 把重扫间隔调到 1ns，让下一次写盘前的用量检查立即重扫。
	svc.blobs.rescanEvery = time.Nanosecond
	if _, err := svc.Publish(ctx, pub, docB); err == nil {
		t.Fatal("重扫把外部写入算进来后，发布 B 应被拒绝")
	} else {
		wantErrStatus(t, err, 507)
	}
	if _, err := svc.store.GetVersion(ctx, "@company/rescan-b", "1.0.0"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("被拒绝的发布不该入库：%v", err)
	}

	// 外部文件清掉 + 重扫后恢复正常。
	if err := os.Remove(external); err != nil {
		t.Fatal(err)
	}
	if used, err := svc.blobs.Rescan(); err != nil || used != int64(len(rawA)) {
		t.Fatalf("清理后重扫用量应回到 A 的大小：%d %v", used, err)
	}
	if _, err := svc.Publish(ctx, pub, docB); err != nil {
		t.Fatalf("重扫后发布 B 应成功：%v", err)
	}
	t.Logf("修后：写盘前的周期重扫能感知外部实例写入的 blob——外部写入后发布 B 返回 507，清理并重扫后恢复")
}
