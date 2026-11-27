package store

import (
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// blob-gc 只从 blobRefTables 清单里的表读「已引用」的 blob 键（blobs.go）：
// 清单漏掉一张带 blob_key 列的表，那张表引用的 blob 就会被当成孤儿删掉，
// 而且删除前没有任何提示。这组测试直接扫 embed 的迁移文件，把清单和 schema 对上：
// 迁移里出现新的 blob_key 表却没同步进清单时，这里会红。
func TestBlobRefTablesCoverMigrations(t *testing.T) {
	migrated, err := blobKeyTablesInMigrations(migrationFS, "migrations/*.sql")
	if err != nil {
		t.Fatalf("扫迁移文件: %v", err)
	}
	if len(migrated) == 0 {
		t.Fatal("迁移文件里一个 blob_key 表都没扫到，扫描逻辑可能已经失效")
	}

	// 正向：schema 里每张有 blob_key 的表都必须在清单里，否则 blob-gc 会误删。
	if missing := blobRefTableGaps(migrated); len(missing) > 0 {
		t.Fatalf("migrations/ 里这些表有 blob_key 列，但不在 blobRefTables 清单里：%v\nblob-gc 会把它们引用的 blob 当孤儿删掉；请把表名加进 server/internal/store/blobs.go 的 blobRefTables。", missing)
	}

	// 反向：清单里的表必须真的在迁移里定义过 blob_key，防手误写错表名、
	// 或表已经不再引用 blob 却留在清单里。
	var stale []string
	for _, ref := range blobRefTables {
		if !migrated[ref.table] {
			stale = append(stale, ref.table)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Fatalf("blobRefTables 里这些表在 migrations/ 里没有 blob_key 列：%v（表名写错，或已不引用 blob，需要同步删掉）", stale)
	}
}

// 用内存文件系统模拟「未来新增一张带 blob_key 的表，但忘了同步清单」：
// 扫描逻辑必须能把它找出来（TestBlobRefTablesCoverMigrations 才会真的变红）。
// 不需要往 migrations/ 里提交假迁移。
func TestBlobRefTableScanDetectsFutureMigration(t *testing.T) {
	fake := fstest.MapFS{
		"migrations/0001_created.sql":   &fstest.MapFile{Data: []byte("CREATE TABLE future_blobs (\n  id BIGINT NOT NULL,\n  blob_key CHAR(128) NOT NULL,\n  PRIMARY KEY (id)\n);\n")},
		"migrations/0002_altered.sql":   &fstest.MapFile{Data: []byte("ALTER TABLE future_archive ADD COLUMN blob_key CHAR(128) NOT NULL;")},
		"migrations/0003_unrelated.sql": &fstest.MapFile{Data: []byte("CREATE TABLE not_a_blob_ref (\n  id BIGINT NOT NULL\n);")},
	}
	migrated, err := blobKeyTablesInMigrations(fake, "migrations/*.sql")
	if err != nil {
		t.Fatalf("扫假迁移: %v", err)
	}
	if got, want := blobRefTableGaps(migrated), []string{"future_archive", "future_blobs"}; !slices.Equal(got, want) {
		t.Fatalf("应把假迁移里漏加清单的表报出来：got %v, want %v", got, want)
	}
}

// blobKeyTablesInMigrations 扫迁移文件，返回出现过 blob_key 列的表名集合。
// 认两种写法：CREATE TABLE 里定义 blob_key 列、ALTER TABLE 加 blob_key 列；
// 迁移文件里 blob_key 只会作为列名出现，所以按语句里有没有这个词判断。
// 切分用和 Migrate 相同的 splitStatements，保证和真正执行的内容一致。
func blobKeyTablesInMigrations(fsys fs.FS, pattern string) (map[string]bool, error) {
	files, err := fs.Glob(fsys, pattern)
	if err != nil {
		return nil, err
	}
	tables := make(map[string]bool)
	for _, f := range files {
		body, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("读 %s: %w", f, err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if !blobKeyWordRe.MatchString(stmt) {
				continue
			}
			if table := statementTableName(stmt); table != "" {
				tables[table] = true
			}
		}
	}
	return tables, nil
}

// blobRefTableGaps 返回迁移里有 blob_key、但不在 blobRefTables 清单里的表（排序后）。
func blobRefTableGaps(migrated map[string]bool) []string {
	listed := make(map[string]bool, len(blobRefTables))
	for _, ref := range blobRefTables {
		listed[ref.table] = true
	}
	var missing []string
	for table := range migrated {
		if !listed[table] {
			missing = append(missing, table)
		}
	}
	sort.Strings(missing)
	return missing
}

// statementTableName 取出 CREATE TABLE / ALTER TABLE 后面的表名；其它语句返回空串。
func statementTableName(stmt string) string {
	fields := strings.Fields(stmt)
	if len(fields) < 3 || !strings.EqualFold(fields[1], "TABLE") {
		return ""
	}
	i := 2
	switch {
	case strings.EqualFold(fields[0], "CREATE"):
		if i+2 < len(fields) && strings.EqualFold(fields[i], "IF") &&
			strings.EqualFold(fields[i+1], "NOT") && strings.EqualFold(fields[i+2], "EXISTS") {
			i += 3
		}
	case strings.EqualFold(fields[0], "ALTER"):
	default:
		return ""
	}
	if i >= len(fields) {
		return ""
	}
	return strings.TrimSuffix(strings.Trim(fields[i], "`"), "(")
}

var blobKeyWordRe = regexp.MustCompile(`(?i)\bblob_key\b`)
