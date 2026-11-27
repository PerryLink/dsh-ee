package store

import (
	"context"
	"strings"
)

// blobRefTable 是一张引用 blobs/ 目录里文件的表：它的 blob_key 列存 blob 的键。
// 表名和过滤条件都只能是本包内的常量，不能来自用户输入。
type blobRefTable struct {
	table string
	// filter 是拼在 SELECT 末尾的可选条件；目前只有 kb_docs 用它排除内联正文，
	// 空串表示该表每一行的 blob_key 都是引用。
	filter string
}

// blobRefTables 是 blob-gc（registry.CollectBlobs）认引用时扫的全部表。
//
// ⚠️ 新增任何带 blob_key 列、值指向 blobs/<键> 的表，必须同步加到这里。
// blob-gc 只认这份清单：清单里漏掉一张表，那张表引用的 blob 就会被当成孤儿删掉，
// 而且没有任何报错。server/internal/store/blobs_test.go 会扫 migrations/ 里的
// blob_key 列来盯住这件事，漏加时测试红。
//
// 多扫一张不引用 blob 的表不会误删（只是白查），所以宁可加全；顺序不影响结果
// （UNION 去重），这里按迁移文件顺序写，方便和 migrations/ 对照。
var blobRefTables = []blobRefTable{
	{table: "npm_versions"},                      // 0005_registry.sql：已发布/已下架的 npm 版本
	{table: "kb_docs", filter: "blob_key <> ''"}, // 0012_knowledge.sql：空串表示正文内联，没有原件
	{table: "package_source_files"},              // 0063_package_sources.sql：Conan/Raw 制品文件
}

// BlobReferences 返回数据库里所有仍被引用的 blob 键（blob-gc 据此判断哪些文件是孤儿）。
func (s *Store) BlobReferences(ctx context.Context) (map[string]struct{}, error) {
	selects := make([]string, 0, len(blobRefTables))
	for _, ref := range blobRefTables {
		q := "SELECT blob_key FROM " + ref.table
		if ref.filter != "" {
			q += " WHERE " + ref.filter
		}
		selects = append(selects, q)
	}
	rows, err := s.db.QueryContext(ctx, strings.Join(selects, " UNION "))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make(map[string]struct{})
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
