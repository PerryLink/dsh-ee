package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/registry"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestCLIBlobGC(t *testing.T) {
	t.Setenv("DSH_EE_DSN", "")
	t.Setenv("DSH_EE_DATA_DIR", "")
	handled, code, out, stderr := cli("blob-gc", "--help")
	if !handled || code != 0 || stderr != "" {
		t.Fatalf("help: handled=%v, exit=%d, stdout=%q, stderr=%q", handled, code, out, stderr)
	}
	for _, want := range []string{"blob-gc [--delete]", "--force", "--min-age", "退出码"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help 里应有 %q，实际 stdout=%q", want, out)
		}
	}
	if handled, code, _, stderr := cli("blob-gc", "--delete"); !handled || code != 2 || !strings.Contains(stderr, "DSH_EE_DATA_DIR") {
		t.Fatalf("missing configuration: handled=%v, exit=%d, stderr=%q", handled, code, stderr)
	}

	// 相对路径的 DSH_EE_DATA_DIR 在连库之前就拒绝：相对路径可能指向另一套数据目录。
	t.Setenv("DSH_EE_DSN", "root:@tcp(127.0.0.1:4007)/dsh_ee_blobgc_relative")
	t.Setenv("DSH_EE_DATA_DIR", "relative-data-dir")
	if _, code, _, stderr := cli("blob-gc", "--delete"); code != 2 || !strings.Contains(stderr, "绝对路径") {
		t.Fatalf("relative data dir: exit=%d, stderr=%q", code, stderr)
	}

	// 非法的 --min-age 也是用法错误，不连库。
	t.Setenv("DSH_EE_DATA_DIR", t.TempDir())
	if _, code, _, stderr := cli("blob-gc", "--delete", "--min-age=-1h"); code != 2 || !strings.Contains(stderr, "min-age") {
		t.Fatalf("bad min-age: exit=%d, stderr=%q", code, stderr)
	}
}

// TestCLIBlobGCRefusesUnpairedDatabase 复现审计 C1：DSN 指到空库、DATA_DIR 是正确目录时，
// 旧实现会把全部 tarball 判成未引用并删掉且 exit 0。现在必须拒绝删除、保留文件。
func TestCLIBlobGCRefusesUnpairedDatabase(t *testing.T) {
	dsn := openBlobGCTestDSN(t)
	dataDir := t.TempDir()
	keys := []string{
		putTestBlob(t, dataDir, "old tarball one").Key,
		putTestBlob(t, dataDir, "old tarball two").Key,
	}
	for _, key := range keys {
		ageTestBlob(t, dataDir, key, 48*time.Hour)
	}
	t.Setenv("DSH_EE_DSN", dsn)
	t.Setenv("DSH_EE_DATA_DIR", dataDir)

	// 预览：不删文件，但指出库与目录疑似不配对。
	_, code, out, stderr := cli("blob-gc")
	if code != 0 {
		t.Fatalf("dry run: exit=%d, stdout=%q, stderr=%q", code, out, stderr)
	}
	if !strings.Contains(stderr, "不配对") {
		t.Fatalf("dry run 应提示库与目录疑似不配对，stderr=%q", stderr)
	}
	if !strings.Contains(out, filepath.Join(dataDir, "blobs")) {
		t.Fatalf("dry run 应打印绝对目标目录，stdout=%q", out)
	}
	for _, key := range keys {
		if !strings.Contains(out, key) {
			t.Fatalf("dry run 应列出候选 %s，stdout=%q", key, out)
		}
		if !blobFileExists(t, dataDir, key) {
			t.Fatalf("dry run 不应删除 %s", key)
		}
	}

	// --delete：闸门拒绝，退出码 1，文件必须原样还在；提示 --force。
	_, code, out, stderr = cli("blob-gc", "--delete")
	if code != 1 {
		t.Fatalf("delete on empty database: exit=%d（应为 1），stdout=%q, stderr=%q", code, out, stderr)
	}
	if !strings.Contains(stderr, "拒绝删除") || !strings.Contains(stderr, "--force") {
		t.Fatalf("拒绝时应说明原因并提示 --force，stderr=%q", stderr)
	}
	for _, key := range keys {
		if !blobFileExists(t, dataDir, key) {
			t.Fatalf("闸门拒绝后文件仍应存在，%s 被删了", key)
		}
	}
}

// TestCLIBlobGCForceBypassesGates 覆盖 --force 显式越过闸门后仍能清理。
func TestCLIBlobGCForceBypassesGates(t *testing.T) {
	dsn := openBlobGCTestDSN(t)
	dataDir := t.TempDir()
	keys := []string{
		putTestBlob(t, dataDir, "orphan tarball one").Key,
		putTestBlob(t, dataDir, "orphan tarball two").Key,
	}
	for _, key := range keys {
		ageTestBlob(t, dataDir, key, 48*time.Hour)
	}
	t.Setenv("DSH_EE_DSN", dsn)
	t.Setenv("DSH_EE_DATA_DIR", dataDir)

	_, code, out, stderr := cli("blob-gc", "--delete", "--force")
	if code != 0 {
		t.Fatalf("--force delete: exit=%d, stdout=%q, stderr=%q", code, out, stderr)
	}
	if !strings.Contains(stderr, "警告") || !strings.Contains(stderr, "--force") {
		t.Fatalf("--force 应打印越过闸门的警告，stderr=%q", stderr)
	}
	if !strings.Contains(out, "删除完成") || !strings.Contains(out, "成功 2") {
		t.Fatalf("删除后应打印汇总，stdout=%q", out)
	}
	for _, key := range keys {
		if blobFileExists(t, dataDir, key) {
			t.Fatalf("--force 后 %s 应已删除", key)
		}
	}
}

// TestCLIBlobGCMinAge 覆盖 C2 缓解：默认 --min-age=24h 跳过刚写入的文件，--min-age=0 才删。
func TestCLIBlobGCMinAge(t *testing.T) {
	dsn := openBlobGCTestDSN(t)
	ctx := context.Background()
	st, err := store.OpenExisting(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	dataDir := t.TempDir()
	referenced := putTestBlob(t, dataDir, "referenced tarball")
	ageTestBlob(t, dataDir, referenced.Key, 48*time.Hour)
	fresh := putTestBlob(t, dataDir, "just uploaded tarball") // mtime 是现在
	if err := st.CreatePackageSource(ctx, store.PackageSource{Name: "gc-cli", Protocol: "raw", Mode: "hosted"}); err != nil {
		t.Fatalf("create source: %v", err)
	}
	raw, err := st.PackageSource(ctx, "raw", "gc-cli")
	if err != nil {
		t.Fatalf("package source: %v", err)
	}
	const file = "releases/artifact.bin"
	f := store.PackageSourceFile{Path: file, BlobKey: referenced.Key, Size: referenced.Size, UploaderID: 1}
	if err := st.SavePackageSourceFile(ctx, raw.ID, file, "", "", f, true, "", nil, nil); err != nil {
		t.Fatalf("save file: %v", err)
	}

	t.Setenv("DSH_EE_DSN", dsn)
	t.Setenv("DSH_EE_DATA_DIR", dataDir)

	// 默认 min-age 24h：新文件被跳过，不删；引用文件保留。
	_, code, out, stderr := cli("blob-gc", "--delete")
	if code != 0 {
		t.Fatalf("default min-age: exit=%d, stdout=%q, stderr=%q", code, out, stderr)
	}
	if !blobFileExists(t, dataDir, fresh.Key) {
		t.Fatalf("默认 --min-age=24h 时刚写入的 %s 不应被删", fresh.Key)
	}
	if !strings.Contains(out, "跳过 1") {
		t.Fatalf("汇总里应说明跳过了 1 个太新的文件，stdout=%q", out)
	}
	if !blobFileExists(t, dataDir, referenced.Key) {
		t.Fatalf("被引用的 %s 不应被删", referenced.Key)
	}

	// --min-age=0 关闭宽限：新文件被清理。
	_, code, out, stderr = cli("blob-gc", "--delete", "--min-age=0")
	if code != 0 {
		t.Fatalf("min-age=0: exit=%d, stdout=%q, stderr=%q", code, out, stderr)
	}
	if blobFileExists(t, dataDir, fresh.Key) {
		t.Fatalf("--min-age=0 时 %s 应已删除", fresh.Key)
	}
	if !blobFileExists(t, dataDir, referenced.Key) {
		t.Fatalf("被引用的 %s 不应被删", referenced.Key)
	}
}

// ---- 测试辅助 ----

// openBlobGCTestDSN 建一个已迁移的一次性数据库，返回它的 DSN；未设置 DSH_EE_TEST_DSN 时跳过。
func openBlobGCTestDSN(t *testing.T) string {
	t.Helper()
	base := os.Getenv("DSH_EE_TEST_DSN")
	if base == "" {
		t.Skip("未设置 DSH_EE_TEST_DSN，跳过数据库集成测试")
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("rand: %v", err)
	}
	dbName := "dsh_ee_cli_blobgc_test_" + hex.EncodeToString(suffix)
	dsn := strings.Replace(base, ")/", ")/"+dbName, 1)
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.DB().ExecContext(context.Background(), "DROP DATABASE `"+dbName+"`")
		st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return dsn
}

func putTestBlob(t *testing.T, dataDir, content string) registry.Digest {
	t.Helper()
	blobs, err := registry.NewBlobs(dataDir)
	if err != nil {
		t.Fatalf("new blobs: %v", err)
	}
	d, err := blobs.Put(strings.NewReader(content), 1<<20)
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	return d
}

func blobFilePath(dataDir, key string) string {
	return filepath.Join(dataDir, "blobs", key[:2], key[2:4], key)
}

// ageTestBlob 把 blob 文件的修改时间改成 age 之前，模拟已落盘一段时间的 tarball。
func ageTestBlob(t *testing.T, dataDir, key string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(blobFilePath(dataDir, key), old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
}

func blobFileExists(t *testing.T, dataDir, key string) bool {
	t.Helper()
	_, err := os.Stat(blobFilePath(dataDir, key))
	return err == nil
}
