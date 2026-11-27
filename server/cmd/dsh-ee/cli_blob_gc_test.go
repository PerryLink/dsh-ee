package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

func TestCLIBlobGC(t *testing.T) {
	t.Setenv("DSH_EE_DSN", "")
	t.Setenv("DSH_EE_DATA_DIR", "")
	if handled, code, out, stderr := cli("blob-gc", "--help"); !handled || code != 0 || !strings.Contains(out, "blob-gc [--delete] [--force]") || stderr != "" {
		t.Fatalf("help: handled=%v, exit=%d, stdout=%q, stderr=%q", handled, code, out, stderr)
	}
	if handled, code, _, stderr := cli("blob-gc", "--delete"); !handled || code != 2 || !strings.Contains(stderr, "DSH_EE_DATA_DIR") {
		t.Fatalf("missing configuration: handled=%v, exit=%d, stderr=%q", handled, code, stderr)
	}
	// --force 只有配合 --delete 才有意义：别让人以为 --force 本身就会删。
	if handled, code, _, stderr := cli("blob-gc", "--force"); !handled || code != 2 || !strings.Contains(stderr, "--delete") {
		t.Fatalf("force without delete: handled=%v, exit=%d, stderr=%q", handled, code, stderr)
	}
}

// 数据库 schema 比当前二进制新（回滚后旧二进制连上新库的典型形态）时 --delete 必须拒绝：
// 旧二进制的 blob 引用表清单可能不认识新版本加的表，删除会误删新引用指向的文件。
// 只有 --force 能覆盖这个闸门。
func TestCLIBlobGCRefusesDeleteWhenSchemaAhead(t *testing.T) {
	st, dsn := openBlobGCTestStore(t)
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "blobs"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DSH_EE_DSN", dsn)
	t.Setenv("DSH_EE_DATA_DIR", dataDir)

	// 库和二进制一致：正常执行删除（空目录，删 0 个）。
	if handled, code, out, stderr := cli("blob-gc", "--delete"); !handled || code != 0 || !strings.Contains(out, "已删除 0 个 blob") {
		t.Fatalf("schema 一致时应能执行删除：handled=%v exit=%d stdout=%q stderr=%q", handled, code, out, stderr)
	}

	// 放一个数据库里没人引用的 blob 文件：闸门拒绝时它必须原地不动，--force 时才删。
	key := strings.Repeat("a", 128)
	blobFile := filepath.Join(dataDir, "blobs", key[:2], key[2:4], key)
	if err := os.MkdirAll(filepath.Dir(blobFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blobFile, []byte("orphan blob"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 造一条当前二进制不认识的迁移：等价于新版本应用过迁移后回滚到旧二进制。
	const futureVersion = "9999_from_the_future"
	if _, err := st.DB().ExecContext(context.Background(),
		"INSERT INTO schema_migrations (version, applied_at) VALUES (?, UTC_TIMESTAMP(3))", futureVersion); err != nil {
		t.Fatalf("造更新的迁移版本：%v", err)
	}

	_, code, _, stderr := cli("blob-gc", "--delete")
	if code != 1 {
		t.Fatalf("数据库比二进制新时必须拒绝 --delete：exit=%d stderr=%q", code, stderr)
	}
	for _, want := range []string{futureVersion, "同版本", "--force"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("拒绝提示里缺少 %q：%q", want, stderr)
		}
	}
	if _, err := os.Stat(blobFile); err != nil {
		t.Fatalf("拒绝删除时不能动 blobs 目录里的文件：%v", err)
	}

	// --force 是显式覆盖：确认没有新的引用表后仍可执行，那个孤儿文件才被删掉。
	handled, code, out, stderr := cli("blob-gc", "--delete", "--force")
	if !handled || code != 0 || !strings.Contains(out, key) || !strings.Contains(out, "已删除 1 个 blob") {
		t.Fatalf("--force 应能继续执行：handled=%v exit=%d stdout=%q stderr=%q", handled, code, out, stderr)
	}
	if _, err := os.Stat(blobFile); !os.IsNotExist(err) {
		t.Fatalf("--force 应删除未引用的 blob：err=%v", err)
	}
}

// openBlobGCTestStore 建一个随机测试库并跑完迁移，返回库句柄和 DSN；
// 没设 DSH_EE_TEST_DSN 时跳过（和其它包的数据库集成测试一致）。
func openBlobGCTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	base := os.Getenv("DSH_EE_TEST_DSN")
	if base == "" {
		t.Skip("未设置 DSH_EE_TEST_DSN，跳过数据库集成测试")
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	name := "dsh_ee_blobgc_" + hex.EncodeToString(suffix)
	dsn := strings.Replace(base, ")/", ")/"+name, 1)
	if !strings.Contains(dsn, name) {
		// 不回显 DSN：里面可能有密码。
		t.Fatal("DSH_EE_TEST_DSN 应形如 root:@tcp(host:4000)/，库名由测试追加")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("建测试库：%v", err)
	}
	t.Cleanup(func() {
		_, _ = st.DB().ExecContext(context.Background(), "DROP DATABASE `"+name+"`")
		st.Close()
	})
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("跑迁移：%v", err)
	}
	return st, dsn
}
