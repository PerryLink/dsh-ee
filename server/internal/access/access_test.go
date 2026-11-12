package access

import (
	"strings"
	"testing"
)

func testConfig() *Config {
	levels := []string{"普通", "重要", "核心"}
	return &Config{
		Fields: []Field{
			{Object: ObjectDocument, Key: "level", Label: "等级", Type: TypeLevel, Options: levels, Access: true},
			{Object: ObjectUser, Key: "level", Label: "等级", Type: TypeLevel, Options: levels, Access: true},
			{Object: ObjectDocument, Key: "project", Label: "项目", Type: TypeTags, Access: true},
			{Object: ObjectUser, Key: "project", Label: "参与的项目", Type: TypeTags, Access: true},
			{Object: ObjectDocument, Key: "depts", Label: "可见部门", Type: TypeTags, Access: true},
			{Object: ObjectDocument, Key: "source", Label: "来源", Type: TypeSelect, Options: []string{"内部", "外部"}},
		},
		Rules: []Rule{
			{Name: "按等级", Enabled: true, Op: OpLevel, UserField: "level", DocField: "level", Missing: MissingDeny},
			{Name: "按项目", Enabled: true, Op: OpIntersects, UserField: "project", DocField: "project", Missing: MissingOpen, KBases: []string{"projects"}},
			{Name: "停用的", Enabled: false, Op: OpContains, UserField: UserDepartment, DocField: "depts"},
		},
	}
}

func TestEvaluate(t *testing.T) {
	c := testConfig()
	for _, tc := range []struct {
		name      string
		kb        string
		user, doc Values
		want      bool
		reason    string
	}{
		{"等级够", "hr", Values{"level": "重要"}, Values{"level": "重要"}, true, "不低于"},
		{"等级不够", "hr", Values{"level": "普通"}, Values{"level": "核心"}, false, "低于"},
		{"用户没填等级按最低", "hr", Values{}, Values{"level": "重要"}, false, "按最低"},
		{"最低一级谁都能看", "hr", Values{}, Values{"level": "普通"}, true, "最低一级"},
		{"文档没填当作受限", "hr", Values{"level": "核心"}, Values{}, false, "当作受限"},
		{"项目规则只对 projects 库生效", "hr", Values{"level": "普通"}, Values{"level": "普通", "project": []string{"A"}}, true, ""},
		{"项目有交集", "projects", Values{"level": "普通", "project": []string{"A", "B"}}, Values{"level": "普通", "project": []any{"B", "C"}}, true, "都有 B"},
		{"项目没交集", "projects", Values{"level": "普通", "project": []string{"A"}}, Values{"level": "普通", "project": []string{"C"}}, false, "不在文档"},
		{"项目没填不限制", "projects", Values{"level": "普通"}, Values{"level": "普通"}, true, "没填不限制"},
	} {
		got, steps := c.Evaluate(tc.kb, tc.user, tc.doc)
		if got != tc.want {
			t.Errorf("%s：得到 %v，应是 %v；%+v", tc.name, got, tc.want, steps)
		}
		if tc.reason != "" {
			all := ""
			for _, s := range steps {
				all += s.Reason + "\n"
			}
			if !strings.Contains(all, tc.reason) {
				t.Errorf("%s：判断过程里应有「%s」：%s", tc.name, tc.reason, all)
			}
		}
	}
	// 内置属性：部门
	c.Rules[2].Enabled = true
	if ok, _ := c.Evaluate("hr", Values{"level": "普通", UserDepartment: "财务部"}, Values{"level": "普通", "depts": []string{"财务部", "审计部"}}); !ok {
		t.Error("部门在可见部门里应通过")
	}
	if ok, _ := c.Evaluate("hr", Values{"level": "普通", UserDepartment: "研发部"}, Values{"level": "普通", "depts": []string{"财务部"}}); ok {
		t.Error("部门不在可见部门里不该通过")
	}
}

func TestValidate(t *testing.T) {
	c := testConfig()
	for _, tc := range []struct {
		name string
		rule Rule
		want string
	}{
		{"好的", Rule{Name: "x", Op: OpLevel, UserField: "level", DocField: "level"}, ""},
		{"字段没打开访问控制", Rule{Name: "x", Op: OpIntersects, UserField: "project", DocField: "source"}, "用于访问控制"},
		{"等级比较要等级字段", Rule{Name: "x", Op: OpLevel, UserField: "project", DocField: "project"}, "等级字段"},
		{"没有这个字段", Rule{Name: "x", Op: OpIntersects, UserField: "project", DocField: "nope"}, "没有文档字段"},
		{"包含的用户侧不能是多选", Rule{Name: "x", Op: OpContains, UserField: "project", DocField: "project"}, "intersects"},
		{"内置属性", Rule{Name: "x", Op: OpContains, UserField: UserDepartment, DocField: "depts"}, ""},
	} {
		r := tc.rule
		msg := c.ValidateRule(&r)
		if (tc.want == "") != (msg == "") || !strings.Contains(msg, tc.want) {
			t.Errorf("%s：%q，应含 %q", tc.name, msg, tc.want)
		}
	}
	f := Field{Object: ObjectDocument, Key: "level", Label: "等级", Type: TypeLevel, Options: []string{"普通"}}
	if msg := ValidateField(&f); !strings.Contains(msg, "至少两个") {
		t.Errorf("等级字段至少两个等级：%q", msg)
	}
	f = Field{Object: ObjectDocument, Key: "note", Label: "备注", Type: TypeText, Access: true}
	if msg := ValidateField(&f); !strings.Contains(msg, "访问控制") {
		t.Errorf("文本字段不能用于访问控制：%q", msg)
	}
	lv := c.Field(ObjectDocument, "level")
	if _, msg := CleanValue(lv, "绝密"); msg == "" {
		t.Error("不在等级列表里的值应被拒绝")
	}
	if v, msg := CleanValue(c.Field(ObjectDocument, "project"), []any{"B", " A ", "B", ""}); msg != "" || strings.Join(v.([]string), ",") != "A,B" {
		t.Errorf("多选的值应去重排序：%v %q", v, msg)
	}
}
