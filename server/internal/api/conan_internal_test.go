package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// conanTestSource 建一个托管 Conan 源并返回它的 ID。
func conanTestSource(t *testing.T, st *store.Store, name string) int64 {
	t.Helper()
	ctx := context.Background()
	if err := st.CreatePackageSource(ctx, store.PackageSource{Name: name, Protocol: "conan", Mode: "hosted"}); err != nil {
		t.Fatalf("创建 Conan 源: %v", err)
	}
	src, err := st.PackageSource(ctx, "conan", name)
	if err != nil {
		t.Fatalf("读取 Conan 源: %v", err)
	}
	return src.ID
}

// conanTestServer 只需要 store：conanSearch / conanFiles 不碰其它依赖。
func conanTestServer(st *store.Store) *Server {
	return &Server{store: st, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

// insertConanRevisions 直接写 revision 行，跳过真实上传：发布者本来就能用两个小文件
// 造一条 revision，所以这里按发布者的成本造数据。
func insertConanRevisions(t *testing.T, st *store.Store, sourceID int64, parents ...string) {
	t.Helper()
	ctx := context.Background()
	for i, parent := range parents {
		if _, err := st.DB().ExecContext(ctx,
			"INSERT INTO package_source_revisions (source_id,path,parent,published_at) VALUES (?,?,?,?)",
			sourceID, fmt.Sprintf("%s/revisions/r%02d", parent, i), parent, time.Now().UTC()); err != nil {
			t.Fatalf("插入 revision 行: %v", err)
		}
	}
}

// TestConanSearchCapsParents 锁定 search 的上限：超过上限时只返回上限条并带 truncated=true，
// 恰好等于上限时完整返回且 truncated=false。
func TestConanSearchCapsParents(t *testing.T) {
	st := openAuthenticateTestStore(t)
	s := conanTestServer(st)
	old := conanListLimit
	conanListLimit = 3
	t.Cleanup(func() { conanListLimit = old })

	overLimit := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		var got struct {
			Results   []string `json:"results"`
			Truncated *bool    `json:"truncated"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("解析 search 响应: %v: %s", err, rec.Body.String())
		}
		if len(got.Results) != conanListLimit || got.Truncated == nil || !*got.Truncated {
			t.Fatalf("超过上限时应只返回 %d 条 recipe 并标记 truncated=true，实际 results=%d truncated=%v: %s",
				conanListLimit, len(got.Results), got.Truncated, rec.Body.String())
		}
	}

	over := conanTestSource(t, st, "search-over")
	insertConanRevisions(t, st, over, "conans/lib/1.0/_/_", "conans/lib/2.0/_/_", "conans/lib/3.0/_/_", "conans/lib/4.0/_/_")
	rec := httptest.NewRecorder()
	s.conanSearch(rec, httptest.NewRequest(http.MethodGet, "/conan/search-over/v2/conans/search", nil), over)
	if rec.Code != http.StatusOK {
		t.Fatalf("search 状态码 = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	overLimit(rec)

	exact := conanTestSource(t, st, "search-exact")
	insertConanRevisions(t, st, exact, "conans/lib/1.0/_/_", "conans/lib/2.0/_/_", "conans/lib/3.0/_/_")
	rec = httptest.NewRecorder()
	s.conanSearch(rec, httptest.NewRequest(http.MethodGet, "/conan/search-exact/v2/conans/search", nil), exact)
	if rec.Code != http.StatusOK {
		t.Fatalf("search 状态码 = %d，期望 200: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Results   []string `json:"results"`
		Truncated *bool    `json:"truncated"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 search 响应: %v: %s", err, rec.Body.String())
	}
	if len(got.Results) != 3 || got.Truncated == nil || *got.Truncated {
		t.Fatalf("恰好等于上限时应完整返回 3 条并标记 truncated=false，实际 results=%d truncated=%v: %s",
			len(got.Results), got.Truncated, rec.Body.String())
	}
}

// TestConanFilesCapsRevision 锁定 files 的条目上限：超过上限返回 413（files 的响应是
// 「该 revision 的完整文件清单」，截断后的清单会被客户端当成完整集合，故选择显式失败）；
// 恰好等于上限仍完整返回。
func TestConanFilesCapsRevision(t *testing.T) {
	st := openAuthenticateTestStore(t)
	s := conanTestServer(st)
	old := conanFilesLimit
	conanFilesLimit = 3
	t.Cleanup(func() { conanFilesLimit = old })
	sourceID := conanTestSource(t, st, "files")
	ctx := context.Background()

	insertRevision := func(revision string) {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx,
			"INSERT INTO package_source_revisions (source_id,path,parent,published_at) VALUES (?,?,?,?)",
			sourceID, revision, "conans/lib/1.0/_/_", time.Now().UTC()); err != nil {
			t.Fatalf("插入 revision 行: %v", err)
		}
	}
	insertFiles := func(revision string, n int) {
		t.Helper()
		for i := 0; i < n; i++ {
			if _, err := st.DB().ExecContext(ctx,
				"INSERT INTO package_source_files (source_id,path,revision,blob_key,size,uploader_id,updated_at) VALUES (?,?,?,?,?,?,?)",
				sourceID, fmt.Sprintf("%s/files/metadata/m%02d", revision, i), revision, "blob", 1, 1, time.Now().UTC()); err != nil {
				t.Fatalf("插入文件行: %v", err)
			}
		}
	}

	over := "conans/lib/1.0/_/_/revisions/over"
	insertRevision(over)
	insertFiles(over, 4)
	rec := httptest.NewRecorder()
	s.conanFiles(rec, httptest.NewRequest(http.MethodGet, "/conan/files/v2/conans/files", nil), sourceID, over)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("文件数超过上限应返回 413，实际 %d: %s", rec.Code, rec.Body.String())
	}

	exact := "conans/lib/2.0/_/_/revisions/exact"
	insertRevision(exact)
	insertFiles(exact, 3)
	rec = httptest.NewRecorder()
	s.conanFiles(rec, httptest.NewRequest(http.MethodGet, "/conan/files/v2/conans/files", nil), sourceID, exact)
	if rec.Code != http.StatusOK {
		t.Fatalf("恰好等于上限时应返回 200，实际 %d: %s", rec.Code, rec.Body.String())
	}
	var got struct {
		Files map[string]json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("解析 files 响应: %v: %s", err, rec.Body.String())
	}
	if len(got.Files) != 3 {
		t.Fatalf("恰好等于上限时应返回 3 个文件，实际 %d: %s", len(got.Files), rec.Body.String())
	}
}
