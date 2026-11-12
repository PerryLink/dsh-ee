package api

import (
	"context"
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 应用依赖的扩展（docs/plugins.md §6.5，照 Dify 的 dependencies_analysis）：
// 应用用到了扩展装出来的连接，导出时记下 "requires": ["acme/oa@^1.2"]；导入时缺扩展、或版本不够，在报告里提示管理员先装。

// appRequires 是一组连接背后的扩展要求：作者/名字@^主版本.次版本。不是扩展装的连接不算。
func (s *Server) appRequires(ctx context.Context, conns []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, slug := range conns {
		a, err := s.store.GetAssetBySlug(ctx, slug)
		if err != nil || a.Kind != store.KindConnection {
			continue
		}
		sp, err := s.latestSpec(ctx, a)
		if err != nil || sp.Extension == nil || sp.Extension.ID == "" || seen[sp.Extension.ID] {
			continue
		}
		seen[sp.Extension.ID] = true
		out = append(out, requireOf(sp.Extension.ID, sp.Extension.Version))
	}
	return out
}

func requireOf(id, version string) string {
	v, err := semver.NewVersion(version)
	if err != nil {
		return id
	}
	return fmt.Sprintf("%s@^%d.%d", id, v.Major(), v.Minor())
}

// splitRequire 把 "acme/oa@^1.2" 拆成 id 和版本范围；没写版本时范围为空。
func splitRequire(req string) (id, constraint string) {
	req = strings.TrimSpace(req)
	// id 里没有 @（作者/名字），第一个 @ 就是分隔点
	id, constraint, _ = strings.Cut(req, "@")
	return strings.TrimSpace(id), strings.TrimSpace(constraint)
}

// checkRequires 核对目标环境装没装这些扩展、版本够不够，返回导入报告的几行。
func (s *Server) checkRequires(ctx context.Context, reqs []string) []importItem {
	var out []importItem
	for _, req := range reqs {
		id, constraint := splitRequire(req)
		if id == "" {
			continue
		}
		it := importItem{Kind: "extension", Slug: id, Title: req}
		a, sp, err := s.findExtension(ctx, id)
		switch {
		case err != nil:
			it.Result, it.Note = "missing", "查不了已装的扩展："+cut(err.Error(), 120)
		case a == nil:
			it.Result, it.Note = "missing", "这个应用要用扩展 "+req+"，目标环境还没装：请管理员先在「企业连接 → 安装扩展」装上"
		default:
			it.Result, it.Note = "reused", "已装 "+sp.Extension.ID+"@"+sp.Extension.Version
			if constraint != "" {
				c, cerr := semver.NewConstraint(constraint)
				v, verr := semver.NewVersion(sp.Extension.Version)
				if cerr == nil && (verr != nil || !c.Check(v)) {
					it.Result = "missing"
					it.Note = fmt.Sprintf("已装的 %s 是 %s，这个应用要 %s：请管理员先升级扩展", id, sp.Extension.Version, constraint)
				}
			}
		}
		out = append(out, it)
	}
	return out
}
