// Package access 是按扩展字段控制访问的判断（docs/access-control.md §4～§5）。
//
// 这里只有纯计算：给定字段定义、规则、用户和文档各自的字段值，判断这个人能不能看这篇文档，并说清楚
// 判断经过了哪几条规则、卡在哪一条。存取在 store 包，拦截在 api 包的每个出口上。
//
// 规则是库级「谁能查」之上的附加条件，只会收紧，不会放宽。
package access

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// 字段加在哪类对象上。
const (
	ObjectDocument = "document"
	ObjectKBase    = "kbase"
	ObjectUser     = "user"
)

// 字段类型。
const (
	TypeText   = "text"
	TypeNumber = "number"
	TypeDate   = "date"
	TypeSelect = "select"
	TypeTags   = "tags"
	TypeLevel  = "level"
)

var (
	Objects = []string{ObjectDocument, ObjectKBase, ObjectUser}
	Types   = []string{TypeText, TypeNumber, TypeDate, TypeSelect, TypeTags, TypeLevel}
)

// Field 是一个扩展字段的定义。
type Field struct {
	ID     int64  `json:"id,omitempty" yaml:"-"`
	Object string `json:"object" yaml:"object"`
	// 字段的键，同一类对象里唯一；扩展带进来的是「扩展 id.键」
	Key   string `json:"key" yaml:"key"`
	Label string `json:"label" yaml:"label"`
	Type  string `json:"type" yaml:"type"`
	// select / tags 的选项；level 从低到高的等级
	Options []string `json:"options" yaml:"options,omitempty"`
	// 用于访问控制：只有有「管理字段和规则」权限的人能改它的值
	Access bool `json:"access" yaml:"access,omitempty"`
	// 哪个扩展带进来的；手工建的是空
	Source string `json:"source,omitempty" yaml:"source,omitempty"`
}

// 比较方式。
const (
	// 等级比较：用户的等级 ≥ 文档的等级
	OpLevel = "level_gte"
	// 有交集：文档的值和用户的值至少有一个相同
	OpIntersects = "intersects"
	// 包含：用户的值（一个）在文档的值（多个）里
	OpContains = "contains"
)

var Ops = []string{OpLevel, OpIntersects, OpContains}

// 文档没填这个字段时怎么算。
const (
	// 当作受限：没填的文档，只有维护者看得到（默认，免得漏填就等于公开）
	MissingDeny = "deny"
	// 不限制：没填的文档不受这条规则约束
	MissingOpen = "open"
)

// 用户这一侧可以直接用的内置属性（不用建字段）。
const (
	UserDepartment = "@department"
	UserUsername   = "@username"
	UserRoles      = "@roles"
)

var UserBuiltins = []string{UserDepartment, UserUsername, UserRoles}

// Rule 是一条访问规则。
type Rule struct {
	ID      int64  `json:"id,omitempty" yaml:"-"`
	Name    string `json:"name" yaml:"name"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
	Op      string `json:"op" yaml:"op"`
	// 用户这一侧：用户字段的键，或内置属性（@department / @username / @roles）
	UserField string `json:"user_field" yaml:"user_field"`
	// 文档这一侧：文档字段的键
	DocField string `json:"doc_field" yaml:"doc_field"`
	// 文档没填时：deny（默认）/ open
	Missing string `json:"missing" yaml:"missing,omitempty"`
	// 只对这些知识库生效（标识）；空表示全部
	KBases []string `json:"kbases" yaml:"kbases,omitempty"`
}

// Values 是一个对象的字段值：键 → 值。select / level / text 是字符串，tags 是字符串数组。
type Values map[string]any

// Strings 把一个值看成一组字符串：单个值是一个元素，没填是空。
func (v Values) Strings(key string) []string {
	switch x := v[key].(type) {
	case string:
		if x != "" {
			return []string{x}
		}
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, it := range x {
			if s, ok := it.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case float64:
		return []string{strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", x), "0"), ".")}
	}
	return nil
}

// Config 是一整套字段和规则。plan / test / explain 可以拿一份还没应用的 Config 来算。
type Config struct {
	Fields []Field `json:"fields" yaml:"fields"`
	Rules  []Rule  `json:"rules" yaml:"rules"`
}

// Field 按对象和键找字段。
func (c *Config) Field(object, key string) *Field {
	for i := range c.Fields {
		if c.Fields[i].Object == object && c.Fields[i].Key == key {
			return &c.Fields[i]
		}
	}
	return nil
}

// Step 是判断过程里的一步。
type Step struct {
	Rule string `json:"rule"`
	Op   string `json:"op,omitempty"`
	// pass / deny / skip
	Result string `json:"result"`
	Reason string `json:"reason"`
	// 这一步比较的两个值
	UserValue []string `json:"user_value,omitempty"`
	DocValue  []string `json:"doc_value,omitempty"`
}

// Applies 报告规则对这个知识库是否生效。
func (r *Rule) Applies(kbSlug string) bool {
	return r.Enabled && (len(r.KBases) == 0 || slices.Contains(r.KBases, kbSlug))
}

// Check 用一条规则判断一个人和一篇文档。
func (c *Config) Check(r *Rule, user, doc Values) Step {
	st := Step{Rule: r.Name, Op: r.Op, UserValue: user.Strings(r.UserField), DocValue: doc.Strings(r.DocField)}
	df := c.Field(ObjectDocument, r.DocField)
	if df == nil {
		// 字段被删了（正常情况下删不掉被规则用到的字段）：按最严的算
		st.Result, st.Reason = "deny", "规则用到的文档字段「"+r.DocField+"」不存在"
		return st
	}
	label := df.Label
	if len(st.DocValue) == 0 {
		if r.Missing == MissingOpen {
			st.Result, st.Reason = "pass", "文档没填「"+label+"」，这条规则设的是没填不限制"
		} else {
			st.Result, st.Reason = "deny", "文档没填「"+label+"」，这条规则设的是没填当作受限（只有维护者能看）"
		}
		return st
	}
	userLabel := r.UserField
	if uf := c.Field(ObjectUser, r.UserField); uf != nil {
		userLabel = uf.Label
	} else if name := map[string]string{UserDepartment: "部门", UserUsername: "用户名", UserRoles: "角色"}[r.UserField]; name != "" {
		userLabel = name
	}
	switch r.Op {
	case OpLevel:
		rank := func(v string) int { return slices.Index(df.Options, v) }
		need := rank(st.DocValue[0])
		have := -1
		if len(st.UserValue) > 0 {
			have = rank(st.UserValue[0])
		}
		switch {
		case need < 0:
			st.Result, st.Reason = "deny", fmt.Sprintf("文档的「%s」是 %s，不在等级列表里", label, st.DocValue[0])
		case have >= need:
			st.Result, st.Reason = "pass", fmt.Sprintf("用户的「%s」（%s）不低于文档的「%s」（%s）", userLabel, st.UserValue[0], label, st.DocValue[0])
		case len(st.UserValue) == 0:
			st.Result, st.Reason = "deny", fmt.Sprintf("用户没有填「%s」，按最低算；文档的「%s」是 %s", userLabel, label, st.DocValue[0])
			if need == 0 {
				st.Result, st.Reason = "pass", fmt.Sprintf("文档的「%s」是最低一级（%s），谁都能看", label, st.DocValue[0])
			}
		default:
			st.Result, st.Reason = "deny", fmt.Sprintf("用户的「%s」（%s）低于文档的「%s」（%s）", userLabel, st.UserValue[0], label, st.DocValue[0])
		}
	case OpIntersects, OpContains:
		var common []string
		for _, v := range st.UserValue {
			if slices.Contains(st.DocValue, v) {
				common = append(common, v)
			}
		}
		switch {
		case len(common) > 0:
			st.Result, st.Reason = "pass", fmt.Sprintf("用户的「%s」和文档的「%s」都有 %s", userLabel, label, strings.Join(common, "、"))
		case len(st.UserValue) == 0:
			st.Result, st.Reason = "deny", fmt.Sprintf("用户没有「%s」；文档的「%s」是 %s", userLabel, label, strings.Join(st.DocValue, "、"))
		default:
			st.Result, st.Reason = "deny", fmt.Sprintf("用户的「%s」（%s）不在文档的「%s」（%s）里", userLabel, strings.Join(st.UserValue, "、"), label, strings.Join(st.DocValue, "、"))
		}
	default:
		st.Result, st.Reason = "deny", "不认识的比较方式 "+r.Op
	}
	return st
}

// Evaluate 判断一个人能不能看 kbSlug 这个库里的一篇文档：生效的规则都要通过。
// 返回结果和每条规则的判断过程。没有生效的规则就是允许。
func (c *Config) Evaluate(kbSlug string, user, doc Values) (bool, []Step) {
	allowed := true
	steps := []Step{}
	for i := range c.Rules {
		r := &c.Rules[i]
		if !r.Enabled {
			continue
		}
		if !r.Applies(kbSlug) {
			steps = append(steps, Step{Rule: r.Name, Result: "skip", Reason: "这条规则不对这个知识库生效"})
			continue
		}
		st := c.Check(r, user, doc)
		if st.Result == "deny" {
			allowed = false
		}
		steps = append(steps, st)
	}
	return allowed, steps
}

// Active 报告有没有对这个知识库生效的规则。
func (c *Config) Active(kbSlug string) bool {
	for i := range c.Rules {
		if c.Rules[i].Applies(kbSlug) {
			return true
		}
	}
	return false
}

var keyOK = func(s string) bool {
	if s == "" || len(s) > 96 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '_' && r != '-' && r != '.' && r != '/' {
			return false
		}
	}
	return s[0] >= 'a' && s[0] <= 'z'
}

// ValidateField 检查一个字段定义，返回给人看的问题。
func ValidateField(f *Field) string {
	f.Key, f.Label = strings.TrimSpace(f.Key), strings.TrimSpace(f.Label)
	switch {
	case !slices.Contains(Objects, f.Object):
		return "字段要加在 document / kbase / user 上"
	case !keyOK(f.Key):
		return "字段的键只能用小写字母、数字、下划线，以字母开头，例如 level"
	case f.Label == "" || len([]rune(f.Label)) > 32:
		return "字段要有名字，最多 32 个字"
	case !slices.Contains(Types, f.Type):
		return "字段类型只能是 " + strings.Join(Types, " / ")
	}
	var opts []string
	for _, o := range f.Options {
		if o = strings.TrimSpace(o); o != "" {
			if slices.Contains(opts, o) {
				return "选项「" + o + "」重复了"
			}
			if len([]rune(o)) > 64 {
				return "选项最多 64 个字"
			}
			opts = append(opts, o)
		}
	}
	f.Options = opts
	needs := f.Type == TypeSelect || f.Type == TypeTags || f.Type == TypeLevel
	switch {
	case f.Type == TypeLevel && len(opts) < 2:
		return "等级字段要从低到高列出至少两个等级"
	case !needs && len(opts) > 0:
		return "只有 select / tags / level 字段有选项"
	case len(opts) > 200:
		return "选项最多 200 个"
	case f.Access && !needs:
		return "用于访问控制的字段只能是 select / tags / level"
	}
	if f.Options == nil {
		f.Options = []string{}
	}
	return ""
}

// CleanValue 核对并规整一个字段的值。返回规整后的值（nil 表示清空）和问题。
func CleanValue(f *Field, v any) (any, string) {
	if v == nil {
		return nil, ""
	}
	one := func(x any) (string, bool) {
		s, ok := x.(string)
		return strings.TrimSpace(s), ok
	}
	switch f.Type {
	case TypeTags:
		var list []string
		switch x := v.(type) {
		case []string:
			list = x
		case []any:
			for _, it := range x {
				s, ok := one(it)
				if !ok {
					return nil, "「" + f.Label + "」要填一组文字"
				}
				list = append(list, s)
			}
		case string:
			if strings.TrimSpace(x) != "" {
				list = []string{strings.TrimSpace(x)}
			}
		default:
			return nil, "「" + f.Label + "」要填一组文字"
		}
		out := []string{}
		for _, s := range list {
			if s = strings.TrimSpace(s); s == "" || slices.Contains(out, s) {
				continue
			}
			if len(f.Options) > 0 && !slices.Contains(f.Options, s) {
				return nil, "「" + f.Label + "」没有选项 " + s
			}
			if len([]rune(s)) > 64 {
				return nil, "「" + f.Label + "」的每一项最多 64 个字"
			}
			out = append(out, s)
		}
		if len(out) == 0 {
			return nil, ""
		}
		if len(out) > 100 {
			return nil, "「" + f.Label + "」最多 100 项"
		}
		sort.Strings(out)
		return out, ""
	case TypeNumber:
		n, ok := v.(float64)
		if !ok {
			return nil, "「" + f.Label + "」要填数字"
		}
		return n, ""
	default:
		s, ok := one(v)
		if !ok {
			return nil, "「" + f.Label + "」要填文字"
		}
		if s == "" {
			return nil, ""
		}
		if (f.Type == TypeSelect || f.Type == TypeLevel) && !slices.Contains(f.Options, s) {
			return nil, "「" + f.Label + "」没有选项 " + s
		}
		if len([]rune(s)) > 500 {
			return nil, "「" + f.Label + "」最多 500 个字"
		}
		return s, ""
	}
}

// ValidateRule 对着字段定义检查一条规则。
func (c *Config) ValidateRule(r *Rule) string {
	r.Name = strings.TrimSpace(r.Name)
	if r.Missing == "" {
		r.Missing = MissingDeny
	}
	switch {
	case r.Name == "" || len([]rune(r.Name)) > 64:
		return "规则要有名字，最多 64 个字"
	case !slices.Contains(Ops, r.Op):
		return "比较方式只能是 " + strings.Join(Ops, " / ")
	case r.Missing != MissingDeny && r.Missing != MissingOpen:
		return "文档没填时只能是 deny（当作受限）或 open（不限制）"
	}
	df := c.Field(ObjectDocument, r.DocField)
	if df == nil {
		return "没有文档字段 " + r.DocField
	}
	if !df.Access {
		return "文档字段「" + df.Label + "」没有打开「用于访问控制」：谁都能改它的值，不能拿来控制访问"
	}
	uf := c.Field(ObjectUser, r.UserField)
	builtin := slices.Contains(UserBuiltins, r.UserField)
	if uf == nil && !builtin {
		return "没有用户字段 " + r.UserField + "；也可以用内置属性 " + strings.Join(UserBuiltins, " / ")
	}
	if uf != nil && !uf.Access {
		return "用户字段「" + uf.Label + "」没有打开「用于访问控制」"
	}
	switch r.Op {
	case OpLevel:
		if df.Type != TypeLevel || uf == nil || uf.Type != TypeLevel {
			return "等级比较要两边都是等级字段"
		}
		if !slices.Equal(df.Options, uf.Options) {
			return "等级比较要两边的等级列表一样（" + strings.Join(df.Options, " < ") + " 和 " + strings.Join(uf.Options, " < ") + "）"
		}
	case OpIntersects, OpContains:
		if df.Type != TypeTags && df.Type != TypeSelect {
			return "「" + df.Label + "」要是 select 或 tags 字段"
		}
		if uf != nil && uf.Type != TypeTags && uf.Type != TypeSelect {
			return "「" + uf.Label + "」要是 select 或 tags 字段"
		}
		if r.Op == OpContains && uf != nil && uf.Type == TypeTags {
			return "包含是「用户的一个值在文档的一组值里」，用户这一侧要是单选字段或内置属性；两边都是多选请用 intersects"
		}
	}
	var kbs []string
	for _, k := range r.KBases {
		if k = strings.TrimSpace(k); k != "" && !slices.Contains(kbs, k) {
			kbs = append(kbs, k)
		}
	}
	r.KBases = kbs
	if r.KBases == nil {
		r.KBases = []string{}
	}
	return ""
}
