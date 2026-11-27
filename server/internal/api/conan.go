package api

import (
	"bufio"
	"io"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/registry"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// conanListLimit 限制单次 Conan 列表请求（search / packages）最多载入的 parent 行数。
// 发布者花两个小文件就能造一条 revision；没有上限时，一次读请求会把该源下所有
// revision 行全部载入内存，成为低成本放大。变量而非常量，测试可以调小上限。
var conanListLimit = 10000

// conanFilesLimit 限制单次 files 请求最多聚合的制品行数，超出返回 413。
var conanFilesLimit = 10000

func (s *Server) handleConanSource(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
	segments, valid := packageRequestPath(r, "conan")
	if !valid || len(segments) < 2 {
		writeErr(w, http.StatusBadRequest, "Invalid Conan path")
		return
	}
	src, ok := s.packageSource(w, r, "conan", segments[0])
	if !ok {
		return
	}
	route, ok := registry.ParseConanPath(segments[1:])
	if !ok {
		writeErr(w, http.StatusNotFound, "Unsupported Conan endpoint")
		return
	}
	switch {
	case route.Operation != "file":
		if !packageMethod(w, r, http.MethodGet) {
			return
		}
	case src.Mode == "proxy":
		if !packageMethod(w, r, http.MethodGet, http.MethodHead) {
			return
		}
	default:
		if !packageMethod(w, r, http.MethodGet, http.MethodHead, http.MethodPut) {
			return
		}
	}
	switch route.Operation {
	case "ping":
		w.Header().Set("X-Conan-Server-Capabilities", "revisions")
		w.WriteHeader(http.StatusOK)
		return
	case "authenticate":
		if s.opts.DemoSite != nil {
			demoSiteRefuse(w, r)
			return
		}
		_, token, _ := r.BasicAuth()
		if s.packagePrincipal(w, r, token, true, true, true) != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, token)
		}
		return
	}
	if s.demoSiteNpmWrite(w, r) {
		return
	}
	upload := r.Method == http.MethodPut
	uploadPreflight := src.Mode == "hosted" && (route.Operation == "credentials" || route.Operation == "revisions")
	p := s.packagePrincipal(w, r, packageBearer(r), !upload, upload || uploadPreflight, false)
	if p == nil {
		return
	}
	if route.Operation == "credentials" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if src.Mode == "proxy" {
		s.proxyPackage(w, r, src, segments[1:], route.Operation == "file")
		return
	}
	switch route.Operation {
	case "file":
		name := route.Revision + "/files/" + route.File
		if r.Method == http.MethodPut {
			s.putPackageFile(w, r, p, src, name, route.Namespace, route, strings.HasPrefix(route.File, "metadata/"))
		} else {
			s.getPackageFile(w, r, src, name)
		}
	case "files":
		s.conanFiles(w, r, src.ID, route.Revision)
	case "latest", "revisions":
		s.conanRevisions(w, r, src.ID, route)
	case "search":
		s.conanSearch(w, r, src.ID)
	case "packages":
		s.conanPackages(w, r, src.ID, route)
	}
}

func (s *Server) conanFiles(w http.ResponseWriter, r *http.Request, sourceID int64, revision string) {
	files := map[string]map[string]string{}
	after := ""
	for {
		items, err := s.store.PackageArtifacts(r.Context(), sourceID, &revision, after, 200)
		if err != nil {
			s.packageSourceErr(w, r, err)
			return
		}
		for _, f := range items {
			after = f.Path
			files[strings.TrimPrefix(f.Path, revision+"/files/")] = map[string]string{}
		}
		// files 的响应体是「该 revision 的完整文件清单」，Conan 客户端把它当成完整集合
		// 来决定下载/校验哪些文件；截断后的清单会让客户端静默漏文件，且它无法补救，
		// 所以超出上限时显式失败（413），而不是返回一份看起来完整的残缺清单。
		if len(files) > conanFilesLimit {
			writeErr(w, http.StatusRequestEntityTooLarge, "Conan revision has too many files")
			return
		}
		if len(items) < 200 {
			break
		}
	}
	if len(files) == 0 {
		s.packageSourceErr(w, r, store.ErrNotFound)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

type conanRevisionInfo struct {
	Revision string    `json:"revision"`
	Time     time.Time `json:"time"`
}

func (s *Server) conanRevisions(w http.ResponseWriter, r *http.Request, sourceID int64, route registry.ConanPath) {
	limit := 10000
	if route.Operation == "latest" {
		limit = 1
	}
	revisions, err := s.store.PackageRevisions(r.Context(), sourceID, route.Parent, limit)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	if len(revisions) == 0 {
		s.packageSourceErr(w, r, store.ErrNotFound)
		return
	}
	items := make([]conanRevisionInfo, 0, len(revisions))
	for _, rev := range revisions {
		items = append(items, conanRevisionInfo{Revision: path.Base(rev.Path), Time: rev.Time})
	}
	if route.Operation == "latest" {
		writeJSON(w, http.StatusOK, items[0])
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revisions": items})
}

func (s *Server) conanSearch(w http.ResponseWriter, r *http.Request, sourceID int64) {
	// 多取一行用来精确判断是否被上限截断，避免「恰好等于上限」被误报成截断。
	parents, err := s.store.PackageRevisionParents(r.Context(), sourceID, "conans/", "conans0", conanListLimit+1)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	truncated := len(parents) > conanListLimit
	if truncated {
		parents = parents[:conanListLimit]
	}
	pattern := r.URL.Query().Get("q")
	ignoreCase := !strings.EqualFold(r.URL.Query().Get("ignorecase"), "false")
	items := []string{}
	for _, parent := range parents {
		if ref := registry.ConanReference(parent); ref != "" && registry.MatchConanReference(pattern, ref, ignoreCase) {
			items = append(items, ref)
		}
	}
	// truncated 是新增的顶层布尔字段：Conan 客户端只读 results，忽略未知字段，
	// 老客户端行为不变；新客户端至少能知道还有结果没返回。
	writeJSON(w, http.StatusOK, map[string]any{"results": items, "truncated": truncated})
}

func (s *Server) conanPackages(w http.ResponseWriter, r *http.Request, sourceID int64, route registry.ConanPath) {
	revision := route.Revision
	if revision == "" {
		revisions, err := s.store.PackageRevisions(r.Context(), sourceID, route.Parent, 1)
		if err != nil {
			s.packageSourceErr(w, r, err)
			return
		}
		if len(revisions) == 0 {
			s.packageSourceErr(w, r, store.ErrNotFound)
			return
		}
		revision = revisions[0].Path
	}
	// 包列表同样是「列出全部」语义，也必须设上限。它的响应是 package ID → info 的
	// map，没有加 truncated 的位置，所以超过上限的部分直接不返回：单个 revision 有
	// 上万个二进制包本身就不正常，与其为这种数据再引入一种错误形态，不如保持响应
	// 形状不变、只锁住内存/CPU 上界。
	parents, err := s.store.PackageRevisionParents(r.Context(), sourceID, revision+"/packages/", revision+"/packages0", conanListLimit)
	if err != nil {
		s.packageSourceErr(w, r, err)
		return
	}
	items := map[string]map[string]map[string]string{}
	for _, parent := range parents {
		info := map[string]map[string]string{}
		if !strings.EqualFold(r.URL.Query().Get("list_only"), "true") {
			info, err = s.conanPackageInfo(r, sourceID, parent)
			if err != nil {
				s.packageSourceErr(w, r, err)
				return
			}
		}
		items[path.Base(parent)] = info
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) conanPackageInfo(r *http.Request, sourceID int64, parent string) (map[string]map[string]string, error) {
	info := map[string]map[string]string{}
	revisions, err := s.store.PackageRevisions(r.Context(), sourceID, parent, 1)
	if err != nil || len(revisions) == 0 {
		return info, err
	}
	f, err := s.store.PackageArtifact(r.Context(), sourceID, revisions[0].Path+"/files/conaninfo.txt")
	if err != nil {
		return nil, err
	}
	if s.registry == nil {
		return nil, store.ErrNotFound
	}
	blob, err := s.registry.Blobs().Open(f.BlobKey)
	if err != nil {
		return nil, err
	}
	defer blob.Close()
	lines := bufio.NewScanner(io.LimitReader(blob, 128<<10))
	var fields map[string]string
	for lines.Scan() {
		line := strings.TrimSpace(lines.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			fields = nil
			key := line[1 : len(line)-1]
			if key == "settings" || key == "options" {
				fields = map[string]string{}
				info[key] = fields
			}
		} else if key, value, ok := strings.Cut(line, "="); ok && fields != nil {
			fields[key] = value
		}
	}
	return info, lines.Err()
}
