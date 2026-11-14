package registry

import (
	"archive/tar"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 离线包：能上网的 dsh-ee（DMZ）导出已镜像的上游包，摆渡到内网实例导入。
// 格式是一个 tar：第一项 dsh-ee-bundle.json 是清单，之后每个 tarball 一项 blobs/<sha512>。
// 导入时逐个重算 sha512，与文件名、清单里的 integrity 都对得上才入库。

const bundleManifestName = "dsh-ee-bundle.json"

type BundleManifest struct {
	Format    int             `json:"format"`
	CreatedAt time.Time       `json:"created_at"`
	Source    string          `json:"source"`
	Since     *time.Time      `json:"since,omitempty"`
	Packages  []BundlePackage `json:"packages"`
}

type BundlePackage struct {
	Name     string            `json:"name"`
	IsPlugin bool              `json:"is_plugin"`
	DistTags map[string]string `json:"dist_tags"`
	Catalog  struct {
		Title         string `json:"title"`
		Description   string `json:"description"`
		Author        string `json:"author"`
		Category      string `json:"category"`
		Icon          string `json:"icon"`
		Homepage      string `json:"homepage"`
		Repository    string `json:"repository"`
		Featured      bool   `json:"featured"`
		UpstreamScore *int   `json:"upstream_score,omitempty"`
		UpstreamStars *int   `json:"upstream_stars,omitempty"`
	} `json:"catalog"`
	Versions []BundleVersion `json:"versions"`
}

type BundleVersion struct {
	Version          string          `json:"version"`
	Manifest         json.RawMessage `json:"manifest"`
	Integrity        string          `json:"integrity"`
	Shasum           string          `json:"shasum"`
	Size             int64           `json:"size"`
	Blob             string          `json:"blob"`
	IsPlugin         bool            `json:"is_plugin"`
	HasInstallScript bool            `json:"has_install_script"`
	Deprecated       *string         `json:"deprecated,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

// ExportBundle 把已上架的上游镜像写成离线包。since 非空时只导出这之后新增的版本（增量）。
func (s *Service) ExportBundle(ctx context.Context, w io.Writer, since *time.Time) (int, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return 0, err
	}
	pkgs, err := s.store.ListPackages(ctx, store.PackageFilter{})
	if err != nil {
		return 0, err
	}
	man := BundleManifest{Format: 1, CreatedAt: time.Now().UTC(), Source: set.OrgName, Since: since}
	var blobs []string
	seenBlob := map[string]bool{}
	for _, p := range pkgs {
		if p.Origin != store.OriginUpstream {
			continue
		}
		vs, err := s.store.ListVersions(ctx, p.Name, store.StatusPublished)
		if err != nil {
			return 0, err
		}
		bp := BundlePackage{Name: p.Name, IsPlugin: p.IsPlugin, DistTags: p.DistTags}
		bp.Catalog.Title, bp.Catalog.Description, bp.Catalog.Author = p.Title, p.Description, p.Author
		bp.Catalog.Category, bp.Catalog.Icon, bp.Catalog.Homepage, bp.Catalog.Repository = p.Category, p.Icon, p.Homepage, p.Repository
		bp.Catalog.Featured, bp.Catalog.UpstreamScore, bp.Catalog.UpstreamStars = p.Featured, p.UpstreamScore, p.UpstreamStars
		for _, v := range vs {
			if since != nil && v.CreatedAt.Before(*since) {
				continue
			}
			bp.Versions = append(bp.Versions, BundleVersion{
				Version: v.Version, Manifest: v.Manifest, Integrity: v.Integrity, Shasum: v.Shasum, Size: v.Size, Blob: v.BlobKey,
				IsPlugin: v.IsPlugin, HasInstallScript: v.HasInstallScript, Deprecated: v.Deprecated, CreatedAt: v.CreatedAt,
			})
			if !seenBlob[v.BlobKey] {
				seenBlob[v.BlobKey] = true
				blobs = append(blobs, v.BlobKey)
			}
		}
		if len(bp.Versions) > 0 {
			man.Packages = append(man.Packages, bp)
		}
	}

	tw := tar.NewWriter(w)
	body, err := json.Marshal(man)
	if err != nil {
		return 0, err
	}
	if err := tw.WriteHeader(&tar.Header{Name: bundleManifestName, Mode: 0o644, Size: int64(len(body)), ModTime: man.CreatedAt}); err != nil {
		return 0, err
	}
	if _, err := tw.Write(body); err != nil {
		return 0, err
	}
	for _, key := range blobs {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		f, err := s.blobs.Open(key)
		if err != nil {
			return 0, fmt.Errorf("tarball %s 丢失：%w", key, err)
		}
		st, err := f.Stat()
		if err == nil {
			err = tw.WriteHeader(&tar.Header{Name: "blobs/" + key, Mode: 0o644, Size: st.Size(), ModTime: man.CreatedAt})
		}
		if err == nil {
			_, err = io.Copy(tw, f)
		}
		f.Close()
		if err != nil {
			return 0, err
		}
	}
	return len(man.Packages), tw.Close()
}

// ImportResult 是离线导入的结果。
type ImportResult struct {
	Packages int      `json:"packages"`
	Added    int      `json:"added"`
	Existing int      `json:"existing"`
	Pending  int      `json:"pending"`
	Skipped  []string `json:"skipped"`
}

// ImportBundle 从 r 流式读取离线包并入库。插件版本在白名单模式下进入待审核。
func (s *Service) ImportBundle(ctx context.Context, r io.Reader, by int64) (*ImportResult, *store.SyncJob, error) {
	set, err := s.settings(ctx)
	if err != nil {
		return nil, nil, err
	}
	// 导入和在线预载共用同一份任务名额：解包、逐版本扫描、写 blob 同样吃 CPU 和磁盘，
	// 不限并发时管理员误操作或脚本刷接口就能同时跑很多个。
	release, err := s.gate.reserve(by, nil, s.MaxSyncJobs, s.MaxSyncJobsPerUser)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	tr := tar.NewReader(r)
	h, err := tr.Next()
	if err != nil {
		return nil, nil, errf(400, "不是 dsh-ee 离线包（tar）：%v", err)
	}
	if h.Name != bundleManifestName {
		return nil, nil, errf(400, "离线包的第一项应该是 %s，得到 %q", bundleManifestName, h.Name)
	}
	var man BundleManifest
	if err := json.NewDecoder(io.LimitReader(tr, 1<<30)).Decode(&man); err != nil {
		return nil, nil, errf(400, "离线包清单格式错误：%v", err)
	}
	if man.Format != 1 {
		return nil, nil, errf(400, "不支持的离线包格式 %d", man.Format)
	}
	total := 0
	for _, p := range man.Packages {
		total += len(p.Versions)
	}
	params, _ := json.Marshal(map[string]any{"source": man.Source, "created_at": man.CreatedAt, "packages": len(man.Packages)})
	j := &store.SyncJob{Kind: "import", Params: params, Total: total, Owner: s.owner}
	if by != 0 {
		j.CreatedBy = &by
	}
	if err := s.store.CreateJob(ctx, j); err != nil {
		return nil, nil, err
	}
	jb := &job{st: s.store, j: j}
	jb.logf("离线包来自「%s」，生成于 %s，%d 个包、%d 个版本", man.Source, man.CreatedAt.Format(time.DateTime), len(man.Packages), total)

	res, err := s.importBundle(ctx, tr, &man, set, jb, by)
	jb.mu.Lock()
	if err != nil {
		jb.j.Status = "failed"
	} else if jb.j.Failed > 0 {
		jb.j.Status = "partial"
	} else {
		jb.j.Status = "succeeded"
	}
	jb.mu.Unlock()
	if err != nil {
		jb.logf("✘ 导入中断：%v", err)
	} else {
		jb.logf("完成：新增 %d 个版本（%d 个待审核），已存在 %d 个，跳过 %d 个", res.Added, res.Pending, res.Existing, len(res.Skipped))
	}
	jb.flush(true)
	return res, j, err
}

func (s *Service) importBundle(ctx context.Context, tr *tar.Reader, man *BundleManifest, set *store.AppSettings, jb *job, by int64) (*ImportResult, error) {
	// 1. 先把 tarball 都落盘（按内容寻址，重复导入不占额外空间）
	have := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("读取离线包：%w", err)
		}
		dir, key := path.Split(h.Name)
		if dir != "blobs/" || !blobKey.MatchString(key) {
			continue
		}
		d, err := s.blobs.Put(tr, s.MaxTarball)
		if err != nil {
			return nil, fmt.Errorf("写入 %s：%w", key, err)
		}
		if d.Key != key {
			return nil, fmt.Errorf("离线包里的 %s 内容与文件名不符（实际 sha512 %s），可能已被篡改", key, d.Key)
		}
		have[key] = true
		jb.add(0, 0, d.Size)
	}

	// 2. 再按清单入库
	res := &ImportResult{Packages: len(man.Packages), Skipped: []string{}}
	scopes := set.CompanyScopes()
	var publisher *int64
	if by != 0 {
		publisher = &by
	}
	for _, bp := range man.Packages {
		if !ValidName(bp.Name) || InScopes(bp.Name, scopes) {
			res.Skipped = append(res.Skipped, bp.Name)
			jb.logf("跳过 %s：属于企业作用域或包名不合法", bp.Name)
			jb.add(len(bp.Versions), 0, 0)
			continue
		}
		if p, err := s.store.GetPackage(ctx, bp.Name); err == nil && p.Origin == store.OriginHosted {
			res.Skipped = append(res.Skipped, bp.Name)
			jb.logf("跳过 %s：本地有同名的企业托管包", bp.Name)
			jb.add(len(bp.Versions), 0, 0)
			continue
		}
		pkg := &store.NpmPackage{Name: bp.Name, IsPlugin: bp.IsPlugin, DistTags: bp.DistTags}
		if err := s.store.EnsureUpstreamPackage(ctx, pkg); err != nil {
			return nil, err
		}
		c := bp.Catalog
		_ = s.store.UpdatePackageCatalog(ctx, bp.Name, (&FeedPlugin{Title: c.Title, Description: c.Description, Author: c.Author, Icon: c.Icon,
			Homepage: c.Homepage, Repository: c.Repository, Category: c.Category, Featured: c.Featured, Score: c.UpstreamScore, Stars: c.UpstreamStars}).patch())
		if bp.IsPlugin {
			yes := true
			_ = s.store.UpdatePackageCatalog(ctx, bp.Name, store.CatalogPatch{IsPlugin: &yes})
		}
		if len(bp.DistTags) > 0 {
			_ = s.store.SetPackageDistTags(ctx, bp.Name, bp.DistTags)
		}
		for _, bv := range bp.Versions {
			if _, err := s.store.GetVersion(ctx, bp.Name, bv.Version); err == nil {
				res.Existing++
				jb.add(1, 0, 0)
				continue
			}
			if !have[bv.Blob] && !s.blobs.Has(bv.Blob) {
				jb.logf("✘ %s@%s：离线包里缺少它的 tarball", bp.Name, bv.Version)
				jb.add(1, 1, 0)
				continue
			}
			if err := CheckIntegrity(Digest{Key: bv.Blob, Integrity: sha512Integrity(bv.Blob), Shasum: bv.Shasum}, bv.Integrity, ""); err != nil {
				jb.logf("✘ %s@%s：%v", bp.Name, bv.Version, err)
				jb.add(1, 1, 0)
				continue
			}
			var m map[string]any
			if json.Unmarshal(bv.Manifest, &m) != nil || str(m["name"]) != bp.Name || str(m["version"]) != bv.Version {
				jb.logf("✘ %s@%s：清单与包名 / 版本号不一致", bp.Name, bv.Version)
				jb.add(1, 1, 0)
				continue
			}
			status := store.StatusPublished
			if bv.IsPlugin && pluginNeedsReview(set) {
				status = store.StatusPending
			}
			nv := &store.NpmVersion{Name: bp.Name, Version: bv.Version, Manifest: bv.Manifest, Integrity: bv.Integrity, Shasum: bv.Shasum,
				Size: bv.Size, BlobKey: bv.Blob, IsPlugin: bv.IsPlugin, HasInstallScript: bv.HasInstallScript, Status: status,
				PublisherID: publisher, Deprecated: bv.Deprecated, CreatedAt: bv.CreatedAt}
			if status == store.StatusPublished {
				t := time.Now().UTC().Truncate(time.Millisecond)
				nv.PublishedAt = &t
			}
			if bv.IsPlugin {
				nv.Scan = s.scanBlob(ctx, bp.Name, bv.Version, bv.Blob, bv.Size, scopes)
			}
			if err := s.store.InsertMirroredVersion(ctx, nv); err != nil {
				if errors.Is(err, store.ErrExists) {
					res.Existing++
					jb.add(1, 0, 0)
					continue
				}
				return nil, err
			}
			res.Added++
			if status == store.StatusPending {
				res.Pending++
			}
			jb.add(1, 0, 0)
		}
	}
	return res, nil
}

// scanBlob 对已落盘的 tarball 做一次自动检查（失败时返回 nil，不挡导入）。
func (s *Service) scanBlob(ctx context.Context, name, version, key string, size int64, scopes []string) json.RawMessage {
	f, err := s.blobs.Open(key)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := InspectTarball(f)
	if err != nil {
		return nil
	}
	m, err := ParseManifest(info.PackageJSON)
	if err != nil {
		return nil
	}
	items := Check(m, info, size, scopes)
	if m.Name != name || m.Version != version {
		items = append([]ScanItem{{Key: "manifest-mismatch", Level: LevelFail, Params: map[string]any{"inner": m.Name + "@" + m.Version}}}, items...)
	}
	b, _ := json.Marshal(items)
	return b
}

// sha512Integrity 由 blob key（sha512 hex）还原 npm integrity 字符串。
func sha512Integrity(key string) string {
	raw, _ := hex.DecodeString(key)
	return "sha512-" + base64.StdEncoding.EncodeToString(raw)
}
