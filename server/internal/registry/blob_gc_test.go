package registry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestCollectBlobsAfterRawOverwrite(t *testing.T) {
	ctx := context.Background()
	st := openRegistryTestStore(t)
	dataDir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	blobs, err := NewBlobs(dataDir)
	must(err)
	put := func(content string) Digest {
		t.Helper()
		d, err := blobs.Put(strings.NewReader(content), 1024)
		must(err)
		return d
	}
	old, current := put("original Raw file"), put("replacement Raw file")
	must(st.CreatePackageSource(ctx, store.PackageSource{Name: "gc-raw", Protocol: "raw", Mode: "hosted"}))
	raw, err := st.PackageSource(ctx, "raw", "gc-raw")
	must(err)
	const file = "releases/artifact.bin"
	for _, d := range []Digest{old, current} {
		f := store.PackageSourceFile{Path: file, BlobKey: d.Key, Size: d.Size, UploaderID: 1}
		must(st.SavePackageSourceFile(ctx, raw.ID, file, "", "", f, true, "", nil, nil))
	}
	npm := put("unpublished npm blob")
	must(st.EnsureUpstreamPackage(ctx, &store.NpmPackage{Name: "gc-npm"}))
	must(st.InsertMirroredVersion(ctx, &store.NpmVersion{
		Name: "gc-npm", Version: "1.0.0", Manifest: []byte("{}"), Integrity: npm.Integrity,
		Shasum: npm.Shasum, BlobKey: npm.Key, Size: npm.Size, Status: "unpublished",
	}))
	kb := put("knowledge base original")
	base := &store.KBase{Slug: "gc-kb", Title: "GC test"}
	must(st.CreateKBase(ctx, base, 1))
	must(st.CreateKDoc(ctx, &store.KDoc{BaseID: base.ID, Title: "Original", BlobKey: kb.Key, Size: kb.Size}, "Text", "", 1))
	staged := put("unpublished Conan blob")
	must(st.CreatePackageSource(ctx, store.PackageSource{Name: "gc-conan", Protocol: "conan", Mode: "hosted"}))
	conan, err := st.PackageSource(ctx, "conan", "gc-conan")
	must(err)
	const parent = "conans/lib/1.0/_/_"
	const revision = parent + "/revisions/abc"
	f := store.PackageSourceFile{Path: revision + "/files/conanfile.py", BlobKey: staged.Key, Size: staged.Size, UploaderID: 1}
	must(st.SavePackageSourceFile(ctx, conan.ID, "lib/_/_", revision, parent, f, false, "", nil, nil))

	temp := filepath.Join(dataDir, "blobs", "tmp", old.Key)
	must(os.WriteFile(temp, []byte("upload in temp"), 0600))
	want := map[string]int64{old.Key: old.Size}

	// 预览：只收集候选，不删除。
	scan, err := ScanBlobs(ctx, dataDir, st, GCOptions{MinAge: 0})
	must(err)
	if found := candidateMap(scan); !reflect.DeepEqual(found, want) || !blobs.Has(old.Key) {
		t.Fatalf("dry run: found %v, old blob still present=%v", found, blobs.Has(old.Key))
	}
	must(scan.Close())

	// 删除：候选逐个删掉，被引用的 blob 保留。
	scan, err = ScanBlobs(ctx, dataDir, st, GCOptions{Remove: true, MinAge: 0})
	must(err)
	if found := candidateMap(scan); !reflect.DeepEqual(found, want) {
		t.Fatalf("delete scan: found %v", found)
	}
	res := scan.Delete(ctx)
	must(scan.Close())
	if res.Deleted != 1 || res.Failed != 0 {
		t.Fatalf("delete: %+v", res)
	}
	if blobs.Has(old.Key) {
		t.Errorf("unreferenced blob %s was not removed", old.Key)
	}
	for _, d := range []Digest{current, npm, kb, staged} {
		if !blobs.Has(d.Key) {
			t.Errorf("referenced blob %s was removed", d.Key)
		}
	}
	_, err = os.Stat(temp)
	must(err)
}

// TestBlobGCRefusesEmptyReferenceDatabase 覆盖闸门①：空库 + 有文件时拒绝删除（C1）。
func TestBlobGCRefusesEmptyReferenceDatabase(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	first := putOldBlob(t, dataDir, "orphan one", 48*time.Hour)
	second := putOldBlob(t, dataDir, "orphan two", 48*time.Hour)
	refs := fakeBlobRefs{keys: refSet()}

	scan, err := ScanBlobs(ctx, dataDir, refs, GCOptions{Remove: true})
	if scan == nil {
		t.Fatalf("闸门拒绝时也要返回扫描结果供打印，err=%v", err)
	}
	defer scan.Close()
	if !errors.Is(err, ErrUnsafeDeletion) {
		t.Fatalf("空引用库应拒绝删除，err=%v", err)
	}
	if scan.Gate == nil || scan.Gate.Kind != GateNoReferences {
		t.Fatalf("闸门①结论不对：%+v", scan.Gate)
	}
	if scan.Referenced != 0 || scan.Scanned != 2 || len(scan.Candidates) != 2 {
		t.Fatalf("计数不对：refs=%d scanned=%d candidates=%d", scan.Referenced, scan.Scanned, len(scan.Candidates))
	}
	for _, d := range []Digest{first, second} {
		if !blobExists(t, dataDir, d.Key) {
			t.Fatalf("拒绝删除后 %s 仍应存在", d.Key)
		}
	}
}

// TestBlobGCDryRunOnlyWarns 覆盖预览模式：闸门只提示、不报错（不删文件）。
func TestBlobGCDryRunOnlyWarns(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	blob := putOldBlob(t, dataDir, "orphan", 48*time.Hour)

	scan, err := ScanBlobs(ctx, dataDir, fakeBlobRefs{keys: refSet()}, GCOptions{})
	if err != nil {
		t.Fatalf("预览不应报错：%v", err)
	}
	defer scan.Close()
	if scan.Gate == nil || scan.Gate.Kind != GateNoReferences {
		t.Fatalf("预览也应给出闸门结论：%+v", scan.Gate)
	}
	if !blobExists(t, dataDir, blob.Key) {
		t.Fatalf("预览不能删文件")
	}
}

// TestBlobGCForceDeletes 覆盖 --force：闸门结论仍记录，但放行删除。
func TestBlobGCForceDeletes(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	blob := putOldBlob(t, dataDir, "orphan", 48*time.Hour)

	scan, err := ScanBlobs(ctx, dataDir, fakeBlobRefs{keys: refSet()}, GCOptions{Remove: true, Force: true})
	if err != nil {
		t.Fatalf("--force 应放行：%v", err)
	}
	defer scan.Close()
	if scan.Gate == nil || scan.Gate.Kind != GateNoReferences {
		t.Fatalf("放行后仍应记录闸门结论：%+v", scan.Gate)
	}
	res := scan.Delete(ctx)
	if res.Deleted != 1 || res.Failed != 0 {
		t.Fatalf("delete: %+v", res)
	}
	if blobExists(t, dataDir, blob.Key) {
		t.Fatalf("--force 后 %s 应已删除", blob.Key)
	}
}

// TestBlobGCRefusesUnpairedReferences 覆盖闸门②：抽样的引用 blob 大多不在目录里。
func TestBlobGCRefusesUnpairedReferences(t *testing.T) {
	ctx := context.Background()
	t.Run("mostly missing", func(t *testing.T) {
		dataDir := t.TempDir()
		present := putOldBlob(t, dataDir, "referenced blob", 48*time.Hour)
		putOldBlob(t, dataDir, "orphan blob", 48*time.Hour)
		refs := map[string]struct{}{present.Key: {}}
		for i := 0; i < 9; i++ {
			refs[fmt.Sprintf("%0128x", i+1)] = struct{}{} // 9 个库里引用、目录里没有的 key
		}
		scan, err := ScanBlobs(ctx, dataDir, fakeBlobRefs{keys: refs}, GCOptions{Remove: true})
		if scan == nil || !errors.Is(err, ErrUnsafeDeletion) {
			t.Fatalf("90%% 缺失应拒绝删除：scan=%v err=%v", scan != nil, err)
		}
		defer scan.Close()
		if scan.Gate == nil || scan.Gate.Kind != GateUnpaired || scan.Gate.Sampled != 10 || scan.Gate.SampleMissing != 9 {
			t.Fatalf("闸门②结论不对：%+v", scan.Gate)
		}
	})
	t.Run("invalid reference keys", func(t *testing.T) {
		dataDir := t.TempDir()
		putOldBlob(t, dataDir, "orphan blob", 48*time.Hour)
		scan, err := ScanBlobs(ctx, dataDir, fakeBlobRefs{keys: refSet("not-a-blob-key")}, GCOptions{Remove: true})
		if scan == nil || !errors.Is(err, ErrUnsafeDeletion) {
			t.Fatalf("非法引用 key 应按缺失处理并拒绝：scan=%v err=%v", scan != nil, err)
		}
		defer scan.Close()
		if scan.Gate == nil || scan.Gate.Kind != GateUnpaired || scan.Gate.SampleMissing != 1 {
			t.Fatalf("闸门②结论不对：%+v", scan.Gate)
		}
	})
	t.Run("mostly present", func(t *testing.T) {
		dataDir := t.TempDir()
		first := putOldBlob(t, dataDir, "referenced one", 48*time.Hour)
		second := putOldBlob(t, dataDir, "referenced two", 48*time.Hour)
		putOldBlob(t, dataDir, "orphan blob", 48*time.Hour)
		refs := map[string]struct{}{first.Key: {}, second.Key: {}}
		for i := 0; i < 8; i++ {
			refs[fmt.Sprintf("%0128x", i+100)] = struct{}{}
		}
		scan, err := ScanBlobs(ctx, dataDir, fakeBlobRefs{keys: refs}, GCOptions{Remove: true})
		if err != nil || scan.Gate != nil {
			t.Fatalf("80%% 缺失不该拒绝：gate=%+v err=%v", scan.Gate, err)
		}
		defer scan.Close()
		if len(scan.Candidates) != 1 {
			t.Fatalf("候选数不对：%d", len(scan.Candidates))
		}
	})
}

// TestBlobGCMinAgeSkipsRecentFiles 覆盖 C2 缓解：宽限期内的文件跳过并计入 TooNew。
func TestBlobGCMinAgeSkipsRecentFiles(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	referenced := putOldBlob(t, dataDir, "referenced", 48*time.Hour)
	fresh := putOldBlob(t, dataDir, "fresh upload", 0)
	refs := fakeBlobRefs{keys: refSet(referenced.Key)}

	scan, err := ScanBlobs(ctx, dataDir, refs, GCOptions{MinAge: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Candidates) != 0 || scan.TooNew != 1 {
		t.Fatalf("默认宽限应跳过新文件：candidates=%d tooNew=%d", len(scan.Candidates), scan.TooNew)
	}
	if err := scan.Close(); err != nil {
		t.Fatal(err)
	}

	scan, err = ScanBlobs(ctx, dataDir, refs, GCOptions{MinAge: 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(scan.Candidates) != 1 || scan.Candidates[0].Key != fresh.Key || scan.TooNew != 0 {
		t.Fatalf("MinAge=0 时新文件应是候选：%+v tooNew=%d", scan.Candidates, scan.TooNew)
	}
	if err := scan.Close(); err != nil {
		t.Fatal(err)
	}

	// Now 注入：把「现在」推到 48h 后，刚写入的文件也过了 24h 宽限。
	later := func() time.Time { return time.Now().Add(48 * time.Hour) }
	scan, err = ScanBlobs(ctx, dataDir, refs, GCOptions{MinAge: 24 * time.Hour, Now: later})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Close()
	if len(scan.Candidates) != 1 || scan.Candidates[0].Key != fresh.Key {
		t.Fatalf("Now 注入后新文件应是候选：%+v", scan.Candidates)
	}
}

// TestBlobGCDeleteToleratesFailures 覆盖 C4：逐项失败不中止，ENOENT 视为成功，最后有汇总。
func TestBlobGCDeleteToleratesFailures(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	referenced := putOldBlob(t, dataDir, "referenced", 48*time.Hour)
	gone := putOldBlob(t, dataDir, "vanished before delete", 48*time.Hour)
	blocked := putOldBlob(t, dataDir, "cannot remove", 48*time.Hour)
	deleted := putOldBlob(t, dataDir, "removed fine", 48*time.Hour)

	scan, err := ScanBlobs(ctx, dataDir, fakeBlobRefs{keys: refSet(referenced.Key)}, GCOptions{Remove: true, MinAge: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer scan.Close()
	if len(scan.Candidates) != 3 {
		t.Fatalf("候选数不对：%d", len(scan.Candidates))
	}

	// 扫描之后制造两种逐项结果：一个文件已经不在了（ENOENT 视为成功），
	// 一个路径变成非空目录（root.Remove 会失败）。
	if err := os.Remove(blobPath(t, dataDir, gone.Key)); err != nil {
		t.Fatal(err)
	}
	blockedPath := blobPath(t, dataDir, blocked.Key)
	if err := os.Remove(blockedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blockedPath, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockedPath, "keep"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := scan.Delete(ctx)
	if res.Deleted != 2 || res.Failed != 1 {
		t.Fatalf("删除汇总不对：%+v", res)
	}
	if len(res.Errors) != 1 || res.Errors[0].Key != blocked.Key {
		t.Fatalf("失败项不对：%+v", res.Errors)
	}
	if blobExists(t, dataDir, deleted.Key) {
		t.Fatalf("%s 应已删除", deleted.Key)
	}
	if !blobExists(t, dataDir, referenced.Key) {
		t.Fatalf("被引用的 %s 不应被删", referenced.Key)
	}
	if _, err := os.Stat(blockedPath); err != nil {
		t.Fatalf("失败的路径仍应存在：%v", err)
	}
}

// TestBlobGCReferenceError 覆盖读引用失败：返回错误、不返回扫描结果。
func TestBlobGCReferenceError(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := NewBlobs(dataDir); err != nil {
		t.Fatal(err)
	}
	scan, err := ScanBlobs(context.Background(), dataDir, fakeBlobRefs{err: errors.New("db down")}, GCOptions{})
	if scan != nil || err == nil || !strings.Contains(err.Error(), "读取 blob 引用") {
		t.Fatalf("读引用失败应返回错误：scan=%v err=%v", scan != nil, err)
	}
}

// ---- 测试辅助 ----

// fakeBlobRefs 是不连数据库的 BlobRefSource。
type fakeBlobRefs struct {
	keys map[string]struct{}
	err  error
}

func (f fakeBlobRefs) BlobReferences(context.Context) (map[string]struct{}, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.keys, nil
}

func refSet(keys ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		set[key] = struct{}{}
	}
	return set
}

// putOldBlob 写入一个 blob，并把修改时间改到 age 之前；age 为 0 时保留当前时间。
func putOldBlob(t *testing.T, dataDir, content string, age time.Duration) Digest {
	t.Helper()
	blobs, err := NewBlobs(dataDir)
	if err != nil {
		t.Fatalf("new blobs: %v", err)
	}
	d, err := blobs.Put(strings.NewReader(content), 1<<20)
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	if age > 0 {
		mtime := time.Now().Add(-age)
		if err := os.Chtimes(blobPath(t, dataDir, d.Key), mtime, mtime); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}
	return d
}

func blobPath(t *testing.T, dataDir, key string) string {
	t.Helper()
	return filepath.Join(dataDir, "blobs", key[:2], key[2:4], key)
}

func blobExists(t *testing.T, dataDir, key string) bool {
	t.Helper()
	_, err := os.Stat(blobPath(t, dataDir, key))
	return err == nil
}

func candidateMap(scan *BlobScan) map[string]int64 {
	found := make(map[string]int64)
	for _, c := range scan.Candidates {
		found[c.Key] = c.Size
	}
	return found
}
