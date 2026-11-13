package store

import (
	"context"
	"testing"
)

// 企业目录登记表（migrations/0034_directory_entries.sql）：同一 directory_id 覆盖登记，
// 再 apply 同一份目录是幂等的；不同目录互不影响。
func TestApplyDirectoryEntries(t *testing.T) {
	st := openTokenTestStore(t)
	ctx := context.Background()
	uid := int64(9)
	entry := DirectoryEntry{
		Kind: "extension", Name: "acme/keyring", Version: "1.0.0", Channel: "stable",
		Digest:     "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		SourceType: "tarball", SourceURL: "https://cdn.example.com/keyring-1.0.0.zip",
		Notes: "审核过", SignatureKeyID: "0123456789abcdef", SignatureKeyState: "active",
		SignatureJSON: `{"version":1}`, AppliedBy: &uid,
	}
	plugin := DirectoryEntry{
		Kind: "dsh-plugin", Name: "@acme/oa", Version: "2.0.0", Channel: "beta",
		Digest: "sha512-" + rep("A", 86) + "==", SourceType: "npm", SourceRegistry: "https://npm.acme.internal",
	}
	res, err := st.ApplyDirectoryEntries(ctx, "acme-prod", rep("a", 64), uid, []DirectoryEntry{entry, plugin})
	if err != nil || res.Created != 2 || res.Updated != 0 || res.Removed != 0 || res.Unchanged != 0 {
		t.Fatalf("首次登记：%+v %v", res, err)
	}
	rows, err := st.ListDirectoryEntriesOf(ctx, "acme-prod")
	if err != nil || len(rows) != 2 {
		t.Fatalf("列表：%+v %v", rows, err)
	}
	if rows[0].DirectoryID != "acme-prod" || rows[0].DocumentDigest != rep("a", 64) || rows[0].AppliedBy == nil || *rows[0].AppliedBy != uid || rows[0].CreatedAt.IsZero() {
		t.Fatalf("落库的行不对：%+v", rows[0])
	}
	// 再 apply 同一份：幂等，行 id 不变
	ids := map[int64]bool{}
	for _, r := range rows {
		ids[r.ID] = true
	}
	res, err = st.ApplyDirectoryEntries(ctx, "acme-prod", rep("a", 64), uid, []DirectoryEntry{entry, plugin})
	if err != nil || res.Created != 0 || res.Updated != 0 || res.Unchanged != 2 || res.Removed != 0 {
		t.Fatalf("重复 apply 应幂等：%+v %v", res, err)
	}
	rows, _ = st.ListDirectoryEntriesOf(ctx, "acme-prod")
	if len(rows) != 2 {
		t.Fatalf("重复 apply 不应产生新行：%+v", rows)
	}
	for _, r := range rows {
		if !ids[r.ID] {
			t.Fatalf("重复 apply 不应换行 id：%+v", r)
		}
	}

	// 换内容 + 移除一条 + 换目录文档摘要
	changed := entry
	changed.Digest = rep("b", 64)
	changed.Notes = "换了内容"
	res, err = st.ApplyDirectoryEntries(ctx, "acme-prod", rep("b", 64), uid, []DirectoryEntry{changed})
	if err != nil || res.Created != 0 || res.Updated != 1 || res.Removed != 1 || res.Unchanged != 0 {
		t.Fatalf("覆盖登记：%+v %v", res, err)
	}
	rows, _ = st.ListDirectoryEntriesOf(ctx, "acme-prod")
	if len(rows) != 1 || rows[0].Digest != rep("b", 64) || rows[0].Notes != "换了内容" || rows[0].DocumentDigest != rep("b", 64) {
		t.Fatalf("覆盖后的行：%+v", rows)
	}

	// 不同目录互不影响；列表按目录汇总
	if _, err := st.ApplyDirectoryEntries(ctx, "acme-test", rep("c", 64), uid, []DirectoryEntry{plugin}); err != nil {
		t.Fatal(err)
	}
	all, err := st.ListDirectoryEntries(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("全部条目：%+v %v", all, err)
	}
	sums, err := st.ListDirectorySummaries(ctx)
	if err != nil || len(sums) != 2 {
		t.Fatalf("目录汇总：%+v %v", sums, err)
	}
	byID := map[string]DirectorySummary{}
	for _, s := range sums {
		byID[s.DirectoryID] = s
	}
	if byID["acme-prod"].EntriesCount != 1 || byID["acme-test"].EntriesCount != 1 || byID["acme-prod"].AppliedBy == nil {
		t.Fatalf("汇总内容不对：%+v", byID)
	}
}

func rep(s string, n int) string {
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}
