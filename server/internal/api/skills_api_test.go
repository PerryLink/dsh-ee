package api_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 第三方 Harness 的中立技能 / 提示词只读接口：
//
//	GET /v1/skills        当前员工的技能与提示词
//	GET /v1/skills/{name} 单个技能或提示词
//
// 认证与 /api/dsh 相同（Bearer + hub 范围 + 工作台权限），可见范围也相同；
// 只返回中立元数据：不带 DSH 专属字段（brand / required_plugins / 内部 asset id / 系统技能），
// 也不带正文 Markdown。同时回归 /api/dsh 的原有形状不被改动。
func TestSkillsAPI(t *testing.T) {
	f := newPrincipalFixture(t)
	bob := f.user(t, "skills-api-bob", nil)
	f.token(t, bob.ID, "ee_at_skills", []string{rbac.ScopeModels, rbac.ScopeHub}, nil, nil)
	c := newClient(t, f.ts.URL)

	// 未带令牌、令牌无效：401
	code, body := c.do("GET", "/v1/skills", nil, nil)
	expect(t, "列表不带令牌", code, 401, body)
	code, body = c.do("GET", "/v1/skills", nil, bearer("ee_at_bogus"))
	expect(t, "列表无效令牌", code, 401, body)
	code, body = c.do("GET", "/v1/skills/anything", nil, nil)
	expect(t, "详情不带令牌", code, 401, body)

	// 令牌缺 hub 范围：403
	f.token(t, bob.ID, "ee_at_models_only", []string{rbac.ScopeModels}, nil, nil)
	code, body = c.do("GET", "/v1/skills", nil, bearer("ee_at_models_only"))
	expect(t, "列表缺 hub 范围", code, 403, body)
	code, body = c.do("GET", "/v1/skills/anything", nil, bearer("ee_at_models_only"))
	expect(t, "详情缺 hub 范围", code, 403, body)

	// 账号本身没有工作台权限（portal.view）：403
	code, body = f.admin.do("POST", "/api/admin/roles", map[string]any{
		"code": "skills-no-portal", "name": "无工作台权限", "permissions": []string{rbac.KeysSelf},
	}, nil)
	expect(t, "建无工作台角色", code, 201, body)
	noportalRole := decode[store.Role](t, body)
	noportal := f.user(t, "skills-api-noportal", nil)
	code, body = f.admin.do("PUT", "/api/admin/users/"+itoa(noportal.ID)+"/roles", map[string]any{"role_ids": []int64{noportalRole.ID}}, nil)
	expect(t, "换成无工作台角色", code, 200, body)
	f.token(t, noportal.ID, "ee_at_noportal", []string{rbac.ScopeModels, rbac.ScopeHub}, nil, nil)
	code, body = c.do("GET", "/v1/skills", nil, bearer("ee_at_noportal"))
	expect(t, "账号没有工作台权限", code, 403, body)

	marker := "MARKDOWN-BODY-MARKER-4d2f"
	type entry struct {
		Name           string `json:"name"`
		Title          string `json:"title"`
		Description    string `json:"description"`
		Type           string `json:"type"`
		Delivery       string `json:"delivery"`
		ModelInvocable bool   `json:"model_invocable"`
		UserInvocable  bool   `json:"user_invocable"`
		Version        int    `json:"version"`
		UpdatedAt      string `json:"updated_at"`
		BodyAvailable  bool   `json:"body_available"`
	}
	// readList 读 bob 可见的技能列表，返回原始响应与解析后的条目。
	readList := func(who string) ([]byte, []entry) {
		t.Helper()
		code, raw := c.do("GET", "/v1/skills", nil, bearer("ee_at_skills"))
		expect(t, who, code, 200, raw)
		var got struct {
			Skills []entry `json:"skills"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.Skills == nil {
			t.Fatalf("%s：skills 应是数组：%s", who, raw)
		}
		return raw, got.Skills
	}

	// 空列表：没有可见能力时返回空数组，不是 null
	raw, list := readList("空列表")
	if len(list) != 0 {
		t.Fatalf("空目录应没有技能：%s", raw)
	}
	if !strings.Contains(string(raw), `"skills":[]`) {
		t.Fatalf("空列表应编码成 []：%s", raw)
	}

	// 管理员上架几种能力：可见的技能 / 提示词、范围外的技能、未启用的自选技能、微应用。
	// 自动生效（auto）只对管理员开放；可见范围用部门限制。
	publish := func(kind, slug, delivery string, scope map[string]any, spec map[string]any) int64 {
		t.Helper()
		req := map[string]any{"kind": kind, "slug": slug, "delivery": delivery, "spec": spec}
		if scope != nil {
			req["scope"] = scope
		}
		code, body := f.admin.do("POST", "/api/hub", req, nil)
		expect(t, "上架 "+kind+" "+slug, code, 201, body)
		var created struct {
			Asset  store.Asset `json:"asset"`
			Status string      `json:"status"`
		}
		if err := json.Unmarshal(body, &created); err != nil {
			t.Fatal(err)
		}
		if created.Status != store.StatusPublished {
			t.Fatalf("%s 应直接上架：%s", slug, body)
		}
		return created.Asset.ID
	}
	publish("skill", "expense-policy", "auto", nil, map[string]any{
		"title": "报销制度", "description": "差旅报销的标准与流程", "when_to_use": "问报销时", "body": "# 报销制度\n\n" + marker,
	})
	publish("prompt", "weekly-report", "auto", nil, map[string]any{
		"title": "周报模板", "description": "按固定格式写周报", "body": marker,
	})
	publish("skill", "finance-only", "auto", map[string]any{"departments": []string{"财务部"}}, map[string]any{
		"title": "财务专用", "description": "只有财务部能用", "body": marker,
	})
	optionalID := publish("skill", "optional-skill", "optional", nil, map[string]any{
		"title": "自选技能", "description": "需要员工自己启用", "body": marker,
	})
	publish("app", "expense-app", "auto", nil, map[string]any{
		"title": "报销应用", "description": "报销小助手", "persona": "你是报销助手。", "starters": []string{"我要报销"},
	})

	// 列表：只有自动生效、且在可用范围内的技能与提示词；不含自选、范围外、微应用。
	raw, list = readList("列表")
	names := make([]string, 0, len(list))
	for _, it := range list {
		names = append(names, it.Name)
	}
	if !slices.Equal(names, []string{"expense-policy", "weekly-report"}) {
		t.Fatalf("列表应只含可见的技能与提示词，得到 %v：%s", names, raw)
	}
	for _, it := range list {
		if !strings.HasPrefix(it.UpdatedAt, "20") {
			t.Fatalf("%s 的 updated_at 应有时刻：%s", it.Name, raw)
		}
		if _, err := time.Parse(time.RFC3339, it.UpdatedAt); err != nil {
			t.Fatalf("%s 的 updated_at 不是 RFC3339：%v", it.Name, err)
		}
	}
	// 技能与提示词的字段与语义
	byName := func(list []entry, name string) entry {
		t.Helper()
		for _, it := range list {
			if it.Name == name {
				return it
			}
		}
		t.Fatalf("列表里没有 %s：%v", name, list)
		return entry{}
	}
	sk := byName(list, "expense-policy")
	if sk.Title != "报销制度" || sk.Description != "差旅报销的标准与流程" || sk.Type != store.KindSkill ||
		sk.Delivery != store.DeliveryAuto || !sk.ModelInvocable || !sk.UserInvocable || sk.Version != 1 || !sk.BodyAvailable {
		t.Fatalf("技能字段不对：%+v", sk)
	}
	pr := byName(list, "weekly-report")
	if pr.Type != store.KindPrompt || pr.Delivery != store.DeliveryAuto || pr.ModelInvocable || !pr.UserInvocable ||
		pr.Version != 1 || !pr.BodyAvailable {
		t.Fatalf("提示词字段不对：%+v", pr)
	}

	// 不返回正文 Markdown，也不带 DSH 专属字段与内部 id
	for _, forbidden := range []string{
		marker, `"brand"`, `"required_plugins"`, `"asset_id"`, `"when_to_use"`, `"content"`,
		`"persona"`, `"workbench"`, `"icon"`,
		"finance-only", "optional-skill", "expense-app", "dsh-ee-guide",
	} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("列表里不应出现 %q：%s", forbidden, raw)
		}
	}
	// 每条技能只带契约里的字段
	var rawList struct {
		Skills []map[string]any `json:"skills"`
	}
	if err := json.Unmarshal(raw, &rawList); err != nil {
		t.Fatal(err)
	}
	for _, fields := range rawList.Skills {
		want := []string{"name", "title", "description", "type", "delivery", "model_invocable", "user_invocable", "version", "updated_at", "body_available"}
		if len(fields) != len(want) {
			t.Fatalf("技能字段数量不对：%v", fields)
		}
		for _, k := range want {
			if _, ok := fields[k]; !ok {
				t.Fatalf("技能缺少字段 %s：%v", k, fields)
			}
		}
	}

	// 详情：与列表同一份元数据，仍然不返回正文
	code, detail := c.do("GET", "/v1/skills/expense-policy", nil, bearer("ee_at_skills"))
	expect(t, "技能详情", code, 200, detail)
	var one struct {
		Skill *entry `json:"skill"`
	}
	if err := json.Unmarshal(detail, &one); err != nil {
		t.Fatal(err)
	}
	if one.Skill == nil || one.Skill.Name != "expense-policy" || one.Skill.Type != store.KindSkill || !one.Skill.BodyAvailable {
		t.Fatalf("技能详情不对：%s", detail)
	}
	if strings.Contains(string(detail), marker) || strings.Contains(string(detail), `"content"`) {
		t.Fatalf("详情不应返回正文：%s", detail)
	}
	code, detail = c.do("GET", "/v1/skills/weekly-report", nil, bearer("ee_at_skills"))
	expect(t, "提示词详情", code, 200, detail)
	if strings.Contains(string(detail), marker) {
		t.Fatalf("提示词详情不应返回正文：%s", detail)
	}

	// 不存在、不可见、微应用、DSH 系统技能都一样 404
	for _, name := range []string{"no-such-skill", "finance-only", "optional-skill", "expense-app", "workbench"} {
		code, detail = c.do("GET", "/v1/skills/"+name, nil, bearer("ee_at_skills"))
		expect(t, "详情 "+name, code, 404, detail)
	}

	// 员工在网页上启用自选技能后，/v1/skills 同步可见（同一张订阅表，不新建数据）
	bobWeb := newClient(t, f.ts.URL)
	code, body = bobWeb.do("POST", "/api/auth/login", map[string]string{"username": bob.Username, "password": "employee-password-1"}, nil)
	expect(t, "bob 登录网页", code, 200, body)
	code, body = bobWeb.do("POST", "/api/hub/"+itoa(optionalID)+"/subscription", nil, nil)
	expect(t, "启用自选技能", code, 204, body)
	raw, list = readList("启用后的列表")
	opt := byName(list, "optional-skill")
	if opt.Delivery != store.DeliveryOptional || !opt.BodyAvailable {
		t.Fatalf("启用后的自选技能字段不对：%+v", opt)
	}
	code, detail = c.do("GET", "/v1/skills/optional-skill", nil, bearer("ee_at_skills"))
	expect(t, "启用后的自选技能详情", code, 200, detail)

	// 回归：原有 DSH 入口的形状不变（带 brand / required_plugins / 正文，字段仍是 DSH 的）
	dsh := dshClient{t: t, base: f.ts.URL, token: "ee_at_skills"}
	code, raw, _ = dsh.do("GET", "/api/dsh/catalog", nil, nil)
	expect(t, "DSH 目录", code, 200, raw)
	for _, keep := range []string{`"brand"`, `"required_plugins"`, "expense-app", `"kind":"app"`} {
		if !strings.Contains(string(raw), keep) {
			t.Fatalf("DSH 目录形状被改了，缺少 %q：%s", keep, raw)
		}
	}
	code, raw, _ = dsh.do("GET", "/api/dsh/skills/expense-policy", nil, nil)
	expect(t, "DSH 技能正文", code, 200, raw)
	if !strings.Contains(string(raw), marker) || !strings.Contains(string(raw), `"content"`) {
		t.Fatalf("DSH 技能正文应保持原样：%s", raw)
	}
}
