package api

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/hikariming/dsh-ee/server/internal/access"
)

// 组织同步带字段（docs/access-control.md §6.1）：LDAP / 飞书目录里的属性映射到用户字段。
// 每次登录（LDAP）和每次组织同步时写进去。手工改过的值不会被同步覆盖，除非这条映射选了「以同步为准」。

const fieldSyncSetting = "field_sync"

type fieldSyncMapping struct {
	// ldap / feishu
	Source string `json:"source"`
	// LDAP 属性名（title、employeeType…）；飞书是 job_title / city / employee_no / employee_type 或自定义字段的 id（C-…）
	Attr string `json:"attr"`
	// 用户字段的键
	Field string `json:"field"`
	// 以同步为准：覆盖手工改过的值
	Override bool `json:"override"`
}

type fieldSyncSettings struct {
	Mappings []fieldSyncMapping `json:"mappings"`
}

var fieldSyncAttrRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_;.-]{0,63}$`)

func (s *Server) fieldSync(ctx context.Context) fieldSyncSettings {
	var set fieldSyncSettings
	_, _ = s.store.GetSettingJSON(ctx, fieldSyncSetting, &set)
	if set.Mappings == nil {
		set.Mappings = []fieldSyncMapping{}
	}
	return set
}

// syncAttrs 是某个来源要额外读的属性。
func (s *Server) syncAttrs(ctx context.Context, source string) []string {
	var out []string
	for _, m := range s.fieldSync(ctx).Mappings {
		if m.Source == source && !slices.Contains(out, m.Attr) {
			out = append(out, m.Attr)
		}
	}
	return out
}

// applyDirectoryFields 把目录里的属性按映射写成这个用户的字段值。出错只记日志，不影响登录和同步。
func (s *Server) applyDirectoryFields(ctx context.Context, source string, userID int64, attrs map[string][]string) {
	set := s.fieldSync(ctx)
	if len(set.Mappings) == 0 {
		return
	}
	c, err := s.accessConfig(ctx)
	if err != nil {
		s.log.Warn("读字段定义失败，跳过同步字段", "err", err)
		return
	}
	for _, m := range set.Mappings {
		if m.Source != source {
			continue
		}
		f := c.Field(access.ObjectUser, m.Field)
		if f == nil {
			continue
		}
		vals := attrs[m.Attr]
		if vals == nil {
			// LDAP 属性名不分大小写
			for k, v := range attrs {
				if strings.EqualFold(k, m.Attr) {
					vals = v
				}
			}
		}
		var raw any
		switch {
		case len(vals) == 0:
			// 目录里没有这个值：不清空，免得一次读不到就把访问权限收掉或放开
			continue
		case f.Type == access.TypeTags:
			list := make([]any, len(vals))
			for i, v := range vals {
				list[i] = v
			}
			raw = list
		default:
			raw = vals[0]
		}
		val, msg := access.CleanValue(f, raw)
		if msg != "" {
			s.log.Info("目录里的值不符合字段定义，跳过", "user", userID, "field", f.Key, "reason", msg)
			continue
		}
		if _, err := s.store.SetSyncedFieldValue(ctx, access.ObjectUser, userID, f.ID, val, m.Override); err != nil {
			s.log.Warn("写同步的字段值失败", "user", userID, "field", f.Key, "err", err)
		}
	}
}

// GET /api/admin/field-sync
func (s *Server) handleGetFieldSync(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.fieldSync(r.Context()))
}

// PUT /api/admin/field-sync {mappings}
func (s *Server) handlePutFieldSync(w http.ResponseWriter, r *http.Request) {
	if !s.guardDual(w, r) {
		return
	}
	var req fieldSyncSettings
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Mappings) > 50 {
		writeErr(w, http.StatusBadRequest, "最多 50 条映射")
		return
	}
	c, err := s.accessConfig(r.Context())
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	seen := map[string]bool{}
	for i := range req.Mappings {
		m := &req.Mappings[i]
		m.Attr, m.Field = strings.TrimSpace(m.Attr), strings.TrimSpace(m.Field)
		if m.Source != "ldap" && m.Source != "feishu" {
			writeErr(w, http.StatusBadRequest, "来源只能是 ldap 或 feishu")
			return
		}
		if !fieldSyncAttrRe.MatchString(m.Attr) {
			writeErr(w, http.StatusBadRequest, "属性名 "+m.Attr+" 不对")
			return
		}
		if c.Field(access.ObjectUser, m.Field) == nil {
			writeErr(w, http.StatusBadRequest, "没有用户字段 "+m.Field+"：先在「字段」里加上")
			return
		}
		key := m.Source + "\n" + m.Field
		if seen[key] {
			writeErr(w, http.StatusBadRequest, "同一个来源里，字段 "+m.Field+" 只能映射一次")
			return
		}
		seen[key] = true
	}
	if req.Mappings == nil {
		req.Mappings = []fieldSyncMapping{}
	}
	if err := s.store.PutSettingJSON(r.Context(), fieldSyncSetting, req, currentUser(r).ID); err != nil {
		s.internalErr(w, r, err)
		return
	}
	s.audit(r, "settings.update", fieldSyncSetting, map[string]any{"mappings": req.Mappings})
	writeJSON(w, http.StatusOK, req)
}
