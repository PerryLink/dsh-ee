package api

// 第三方 Harness 的中立技能 / 提示词只读接口：
//
//	GET /v1/skills        当前令牌可见的技能与提示词（只有元数据）
//	GET /v1/skills/{name} 单个技能或提示词的元数据
//
// 数据来源与 /api/dsh/catalog、/api/dsh/skills/{name} 完全相同（userCatalog），
// 可见范围也一致：已上架、在这个员工的可用范围内，并且对他自动生效或已启用；
// 不新建表，也不改 DSH 原生入口的形状。这里只做形状归一：
//   - 不返回 DSH 专属内容：品牌、必装插件、内部 asset id、DSH 系统技能；
//   - 不返回正文 Markdown，正文是否可用用 body_available 表示；
//   - 微应用有独立的只读面，不混进技能列表。

import (
	"net/http"
	"strings"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 这两条路由是给第三方 Harness 的协议入口，不是 Agent 工具：DSH 里的 Agent
// 直接用 skill 工具按需加载，不需要再绕 HTTP 端点。路由表测试要求每条路由都写明原因。
func init() {
	agentExcluded["GET /v1/skills"] = "第三方 Harness 的中立只读协议入口"
	agentExcluded["GET /v1/skills/{name}"] = "第三方 Harness 的中立只读协议入口"
}

// neutralSkill 是一个技能或提示词给第三方 Harness 看的元数据。
type neutralSkill struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	// Type 是 skill 或 prompt；微应用与 DSH 系统技能不在这套只读面里。
	Type           string `json:"type"`
	Delivery       string `json:"delivery"`
	ModelInvocable bool   `json:"model_invocable"`
	UserInvocable  bool   `json:"user_invocable"`
	Version        int    `json:"version"`
	// UpdatedAt 取资产（能力中心里的条目）最近一次变更时间，RFC3339 UTC。
	UpdatedAt string `json:"updated_at"`
	// BodyAvailable 表示 /api/dsh/skills/{name} 有正文；这套接口不返回正文本身。
	BodyAvailable bool `json:"body_available"`
}

// neutralSkills 把 DSH 能力目录里的技能 / 提示词转成中立描述。
// 微应用（/v1/apps）与预置系统技能（dsh-ee-guide 等依赖 DSH 的 /命令 与正文接口）
// 不进入这套契约。
func neutralSkills(c *dshCatalog) []neutralSkill {
	out := make([]neutralSkill, 0, len(c.Skills))
	for _, sk := range c.Skills {
		if _, system := c.system[sk.Name]; system {
			continue
		}
		a := c.assets[sk.Name]
		if a == nil {
			continue
		}
		if a.Kind != store.KindSkill && a.Kind != store.KindPrompt {
			continue
		}
		entry := neutralSkill{
			Name:           sk.Name,
			Title:          sk.Title,
			Description:    sk.Description,
			Type:           a.Kind,
			Delivery:       a.Delivery,
			ModelInvocable: sk.ModelInvocable,
			UserInvocable:  sk.UserInvocable,
			Version:        sk.Version,
			UpdatedAt:      a.UpdatedAt.UTC().Format(time.RFC3339),
		}
		if sp := c.specs[sk.Name]; sp != nil {
			entry.BodyAvailable = strings.TrimSpace(sp.Body) != ""
		}
		out = append(out, entry)
	}
	return out
}

// GET /v1/skills：当前令牌可见的技能与提示词。
// 返回内容与员工权限相关，不进入任何缓存。
func (s *Server) handleNeutralSkills(w http.ResponseWriter, r *http.Request) {
	v, ok := s.dshAuth(w, r)
	if !ok {
		return
	}
	c, err := s.userCatalog(r.Context(), v)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"skills": neutralSkills(c)})
}

// GET /v1/skills/{name}：单个技能或提示词的元数据；不可见与不存在一样返回 404。
func (s *Server) handleNeutralSkill(w http.ResponseWriter, r *http.Request) {
	v, ok := s.dshAuth(w, r)
	if !ok {
		return
	}
	c, err := s.userCatalog(r.Context(), v)
	if err != nil {
		s.internalErr(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	name := r.PathValue("name")
	for _, sk := range neutralSkills(c) {
		if sk.Name == name {
			writeJSON(w, http.StatusOK, map[string]any{"skill": sk})
			return
		}
	}
	writeErr(w, http.StatusNotFound, "没有这个技能，或它不在你的可用范围内")
}
