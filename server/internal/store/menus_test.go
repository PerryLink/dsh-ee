package store

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

// 0013 之前的默认菜单（「工作台」一组装着员工页面）。
var menusBefore0013 = []rbac.Menu{
	{Kind: "group", Title: "工作台", Children: []rbac.Menu{
		{Kind: "page", Title: "我的工作台", Path: "/workbench", PageKey: "workbench", Permission: rbac.PortalView},
		{Kind: "page", Title: "微应用", Path: "/apps", PageKey: "apps", Permission: rbac.PortalView},
		{Kind: "page", Title: "能力中心", Path: "/hub", PageKey: "market", Permission: rbac.PortalView},
		{Kind: "page", Title: "知识库", Path: "/knowledge", PageKey: "knowledge", Permission: rbac.PortalView},
		{Kind: "page", Title: "下载 DSH", Path: "/dsh", PageKey: "dsh-download", Permission: rbac.PortalView},
		{Kind: "page", Title: "接入指南", Path: "/setup", PageKey: "setup", Permission: rbac.PortalView},
		{Kind: "page", Title: "分享能力", Path: "/publish", PageKey: "publish", Permission: rbac.PortalView},
		{Kind: "page", Title: "我的令牌", Path: "/tokens", PageKey: "my-keys", Permission: rbac.KeysSelf},
	}},
	{Kind: "group", Title: "管理", Children: []rbac.Menu{
		{Kind: "page", Title: "总览", Path: "/admin", PageKey: "admin-dashboard", Permission: rbac.DashboardView},
	}},
}

// 已有部署升级到 0013：还是默认值的菜单按「用 / 造 / 接入」重排，管理员改过的不动。
func TestMigrateMenusToBuild(t *testing.T) {
	ctx := context.Background()
	for _, renamed := range []bool{false, true} {
		st := openTokenTestStore(t)
		if err := st.ResetMenus(ctx, menusBefore0013); err != nil {
			t.Fatal(err)
		}
		if renamed {
			if _, err := st.DB().ExecContext(ctx, "UPDATE menus SET title = '应用广场' WHERE page_key = 'apps'"); err != nil {
				t.Fatal(err)
			}
		}
		// 退回到 0013 之前，再迁移一次
		for _, q := range []string{
			"ALTER TABLE assets DROP COLUMN pending_scope",
			"ALTER TABLE assets DROP COLUMN scope_requested_at",
			"ALTER TABLE assets DROP COLUMN scope_note",
			"DELETE FROM schema_migrations WHERE version = '0013_build'",
		} {
			if _, err := st.DB().ExecContext(ctx, q); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		ms, err := st.ListMenus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[int64]*Menu{}
		byKey := map[string]*Menu{}
		var groups []string
		for _, m := range ms {
			byID[m.ID] = m
			if m.Kind == "group" {
				groups = append(groups, m.Title)
			} else {
				byKey[m.PageKey] = m
			}
		}
		if !slices.Equal(groups, []string{"用", "造", "接入", "管理"}) {
			t.Fatalf("分组：%v", groups)
		}
		parent := func(key string) string {
			m := byKey[key]
			if m == nil || m.ParentID == nil {
				return ""
			}
			return byID[*m.ParentID].Title
		}
		if byKey["market"] != nil {
			t.Fatal("默认的「能力中心」应去掉")
		}
		for key, want := range map[string]string{"apps": "用", "knowledge": "用", "workbench": "用", "app-new": "造", "build": "造", "templates": "造", "materials": "造", "publish": "造", "dsh-download": "接入", "setup": "接入", "my-keys": "接入"} {
			if got := parent(key); got != want {
				t.Fatalf("%s 应在「%s」里，实际「%s」", key, want, got)
			}
		}
		if byKey["publish"].Visible || byKey["publish"].Title != "分享插件" {
			t.Fatalf("上传插件的页面应隐藏：%+v", byKey["publish"])
		}
		if renamed {
			if byKey["apps"].Title != "应用广场" {
				t.Fatalf("管理员改过的标题不应覆盖：%+v", byKey["apps"])
			}
		} else if byKey["apps"].Title != "企业应用" || byKey["knowledge"].Title != "知识问答" {
			t.Fatalf("默认标题应更新：%+v %+v", byKey["apps"], byKey["knowledge"])
		}
		if byKey["apps"].Sort >= byKey["workbench"].Sort {
			t.Fatal("企业应用应排在我的工作台前面（默认首页）")
		}
	}
}

// 0020 之前的默认管理菜单：「账号与模型」「管理」两组。
var menusBefore0020 = []rbac.Menu{
	{Kind: "group", Title: "用", Children: []rbac.Menu{
		{Kind: "page", Title: "企业应用", Path: "/apps", PageKey: "apps", Permission: rbac.PortalView},
	}},
	{Kind: "group", Title: "账号与模型", Children: []rbac.Menu{
		{Kind: "page", Title: "用户", Path: "/admin/users", PageKey: "admin-users", Permission: rbac.UsersView},
		{Kind: "page", Title: "角色与权限", Path: "/admin/roles", PageKey: "admin-roles", Permission: rbac.RolesManage},
		{Kind: "page", Title: "菜单与页面", Path: "/admin/menus", PageKey: "admin-menus", Permission: rbac.MenusManage},
		{Kind: "page", Title: "模型接入", Path: "/admin/providers", PageKey: "admin-providers", Permission: rbac.ProvidersManage},
		{Kind: "page", Title: "额度分配", Path: "/admin/quotas", PageKey: "admin-quotas", Permission: rbac.QuotasManage},
		{Kind: "page", Title: "令牌管理", Path: "/admin/tokens", PageKey: "admin-keys", Permission: rbac.KeysManage},
	}},
	{Kind: "group", Title: "管理", Children: []rbac.Menu{
		{Kind: "page", Title: "总览", Path: "/admin", PageKey: "admin-dashboard", Permission: rbac.DashboardView},
		{Kind: "page", Title: "运营数据", Path: "/admin/stats", PageKey: "admin-stats", Permission: rbac.DashboardView},
		{Kind: "page", Title: "审计日志", Path: "/admin/audit", PageKey: "admin-audit", Permission: rbac.AuditView},
		{Kind: "page", Title: "能力管理", Path: "/admin/plugins", PageKey: "admin-plugins", Permission: rbac.PluginsManage},
		{Kind: "page", Title: "企业连接", Path: "/admin/connections", PageKey: "admin-connections", Permission: rbac.ConnectionsManage},
		{Kind: "page", Title: "待审核", Path: "/admin/review", PageKey: "admin-review", Permission: rbac.PluginsManage},
		{Kind: "page", Title: "npm 源", Path: "/admin/registry", PageKey: "admin-registry", Permission: rbac.RegistryManage},
		{Kind: "page", Title: "上游预载", Path: "/admin/upstream", PageKey: "admin-upstream", Permission: rbac.UpstreamManage},
		{Kind: "page", Title: "DSH 分发", Path: "/admin/distribution", PageKey: "admin-distribution", Permission: rbac.DistributionManage},
		{Kind: "page", Title: "系统设置", Path: "/admin/settings", PageKey: "admin-settings", Permission: rbac.SettingsManage},
	}},
}

// 已有部署升级到 0020：管理菜单拆成六组，新增「品牌与外观」「登录方式」；管理员改过的项不动。
func TestMigrateMenusAdminNav(t *testing.T) {
	ctx := context.Background()
	for _, renamed := range []bool{false, true} {
		st := openTokenTestStore(t)
		if err := st.ResetMenus(ctx, menusBefore0020); err != nil {
			t.Fatal(err)
		}
		if renamed {
			if _, err := st.DB().ExecContext(ctx, "UPDATE menus SET title = '模型供应商' WHERE page_key = 'admin-providers'"); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0020_admin_nav'"); err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		ms, err := st.ListMenus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[int64]*Menu{}
		byKey := map[string]*Menu{}
		var groups []*Menu
		for _, m := range ms {
			byID[m.ID] = m
			if m.Kind == "group" {
				groups = append(groups, m)
			} else {
				byKey[m.PageKey] = m
			}
		}
		slices.SortFunc(groups, func(a, b *Menu) int { return a.Sort - b.Sort })
		var titles []string
		for _, g := range groups {
			titles = append(titles, g.Title)
		}
		if !slices.Equal(titles, []string{"用", "运营", "账号与权限", "模型与额度", "能力与连接", "分发与源", "系统"}) {
			t.Fatalf("分组：%v", titles)
		}
		parent := func(key string) string {
			m := byKey[key]
			if m == nil || m.ParentID == nil {
				return ""
			}
			return byID[*m.ParentID].Title
		}
		want := map[string]string{
			"admin-dashboard": "运营", "admin-stats": "运营", "admin-audit": "运营",
			"admin-users": "账号与权限", "admin-roles": "账号与权限", "admin-menus": "账号与权限",
			"admin-providers": "模型与额度", "admin-quotas": "模型与额度", "admin-keys": "模型与额度",
			"admin-plugins": "能力与连接", "admin-review": "能力与连接", "admin-connections": "能力与连接",
			"admin-distribution": "分发与源", "admin-registry": "分发与源", "admin-upstream": "分发与源",
			"admin-settings": "系统", "admin-brand": "系统", "admin-auth": "系统",
		}
		if renamed {
			want["admin-providers"] = "账号与权限"
		}
		for key, g := range want {
			if got := parent(key); got != g {
				t.Fatalf("%s 应在「%s」里，实际「%s」", key, g, got)
			}
		}
		if b := byKey["admin-brand"]; b.Path != "/admin/brand" || b.Permission != rbac.SettingsManage || b.Sort >= byKey["admin-settings"].Sort {
			t.Fatalf("品牌与外观：%+v", b)
		}
		// 再跑一次不会重复插入
		if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0020_admin_nav'"); err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		again, _ := st.ListMenus(ctx)
		if len(again) != len(ms) {
			t.Fatalf("重复迁移后菜单数 %d → %d", len(ms), len(again))
		}
	}
}

// 全新部署的默认菜单与迁移后的结构一致：管理页面都在管理分组里，用户页面都不在。
func TestDefaultMenusAdminGroups(t *testing.T) {
	for _, g := range rbac.DefaultMenus {
		admin := 0
		for _, c := range g.Children {
			if strings.HasPrefix(c.Path, "/admin") {
				admin++
			}
		}
		if admin != 0 && admin != len(g.Children) {
			t.Fatalf("分组「%s」混了管理页面和员工页面", g.Title)
		}
	}
}

// 0032：已有部署补「开发」组（开发文档 / 开发样例两项），重复迁移不重复插入。
func TestMigrateMenusDev(t *testing.T) {
	ctx := context.Background()
	// 0032 之前的默认菜单：去掉「开发」组
	before0032 := make([]rbac.Menu, 0, len(rbac.DefaultMenus))
	for _, g := range rbac.DefaultMenus {
		if g.Title != "开发" {
			before0032 = append(before0032, g)
		}
	}
	st := openTokenTestStore(t)
	if err := st.ResetMenus(ctx, before0032); err != nil {
		t.Fatal(err)
	}
	// 模拟 0013 之后的旧库排序（造 13、接入 16），验证「开发」插在 19、排在接入之后
	for _, q := range []string{
		"UPDATE menus SET sort = 13 WHERE kind = 'group' AND title = '造'",
		"UPDATE menus SET sort = 16 WHERE kind = 'group' AND title = '接入'",
	} {
		if _, err := st.DB().ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0032_dev_menus'"); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	ms, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byKey := map[string]*Menu{}
	var dev, jieru *Menu
	for _, m := range ms {
		if m.Kind == "group" {
			switch m.Title {
			case "开发":
				dev = m
			case "接入":
				jieru = m
			}
			continue
		}
		byKey[m.PageKey] = m
	}
	if dev == nil || jieru == nil {
		t.Fatalf("缺少分组：开发=%v 接入=%v", dev, jieru)
	}
	if dev.Sort <= jieru.Sort {
		t.Fatalf("「开发」应排在「接入」之后：%d <= %d", dev.Sort, jieru.Sort)
	}
	for key, want := range map[string]struct {
		title string
		path  string
		icon  string
		sort  int
	}{
		"dev-docs":     {"开发文档", "/docs", "BookMarked", 10},
		"dev-examples": {"开发样例", "/examples", "FlaskConical", 20},
	} {
		m := byKey[key]
		if m == nil {
			t.Fatalf("缺少页面 %s", key)
		}
		if m.ParentID == nil || *m.ParentID != dev.ID {
			t.Fatalf("%s 应挂在「开发」下：%+v", key, m)
		}
		if m.Kind != "page" || m.Title != want.title || m.Path != want.path || m.Icon != want.icon || m.Sort != want.sort {
			t.Fatalf("%s 字段不对：%+v", key, m)
		}
		if m.Permission != rbac.PortalView || !m.Visible {
			t.Fatalf("%s 权限/可见性不对：%+v", key, m)
		}
	}
	// 再跑一次不会重复插入
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0032_dev_menus'"); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	again, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(ms) {
		t.Fatalf("重复迁移后菜单数 %d → %d", len(ms), len(again))
	}
}

// 全新部署的默认菜单：「开发」组紧跟在「接入」之后，两项都是前端固定路由。
func TestDefaultMenusIncludesDev(t *testing.T) {
	var titles []string
	var dev *rbac.Menu
	for i := range rbac.DefaultMenus {
		g := &rbac.DefaultMenus[i]
		titles = append(titles, g.Title)
		if g.Title == "开发" {
			dev = g
		}
	}
	idx := func(title string) int { return slices.Index(titles, title) }
	if idx("开发") != idx("接入")+1 {
		t.Fatalf("「开发」应紧跟「接入」：%v", titles)
	}
	if dev == nil || len(dev.Children) != 2 {
		t.Fatalf("「开发」组内容不对：%+v", dev)
	}
	want := map[string]string{"dev-docs": "/docs", "dev-examples": "/examples"}
	for _, c := range dev.Children {
		if want[c.PageKey] != c.Path || c.Permission != rbac.PortalView || c.Kind != "page" {
			t.Fatalf("%+v", c)
		}
	}
}

// 0034：已有部署升级后，「扩展签名」页挂在「企业连接」旁边（管理员挪过企业连接就跟着它）；
// 只补一次，重复迁移不会重复插入。
func TestMigrateMenusSigningKeys(t *testing.T) {
	ctx := context.Background()
	st := openTokenTestStore(t)
	if err := st.ResetMenus(ctx, rbac.DefaultMenus); err != nil {
		t.Fatal(err)
	}
	// 模拟 0033 之前的库：页不存在、迁移记录也没有
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM menus WHERE page_key = 'admin-signing-keys'"); err != nil {
		t.Fatal(err)
	}
	replay := func() {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0034_admin_signing_keys_menu'"); err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	replay()
	ms, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]*Menu{}
	for _, m := range ms {
		byID[m.ID] = m
	}
	var page, connections *Menu
	for _, m := range ms {
		switch m.PageKey {
		case "admin-signing-keys":
			page = m
		case "admin-connections":
			connections = m
		}
	}
	if page == nil {
		t.Fatal("迁移后没有扩展签名页面")
	}
	if connections == nil || connections.ParentID == nil || page.ParentID == nil || *page.ParentID != *connections.ParentID {
		t.Fatalf("扩展签名应和企业连接同组：page=%+v connections=%+v", page, connections)
	}
	if g := byID[*page.ParentID]; g == nil || g.Title != "能力与连接" {
		t.Fatalf("扩展签名应在「能力与连接」里：%+v", g)
	}
	if page.Path != "/admin/signing-keys" || page.Permission != rbac.ConnectionsManage || page.Sort <= connections.Sort {
		t.Fatalf("扩展签名菜单字段：%+v", page)
	}
	// 管理员把企业连接挪到别的分组：新页面跟着它，而不是硬塞回「能力与连接」
	other := &Menu{ParentID: nil, Kind: "group", Title: "自定义组", Sort: 999, Visible: true}
	if err := st.CreateMenu(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "UPDATE menus SET parent_id = ? WHERE page_key = 'admin-connections'", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM menus WHERE page_key = 'admin-signing-keys'"); err != nil {
		t.Fatal(err)
	}
	replay()
	again, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var moved *Menu
	for _, m := range again {
		if m.PageKey == "admin-signing-keys" {
			moved = m
		}
	}
	if moved == nil || moved.ParentID == nil || *moved.ParentID != other.ID {
		t.Fatalf("企业连接挪组后扩展签名应跟过去：%+v", moved)
	}
	// 再跑一次迁移不会重复插入
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0034_admin_signing_keys_menu'"); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	third, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != len(again) {
		t.Fatalf("重复迁移后菜单数 %d → %d", len(again), len(third))
	}
}

// 0035：已有部署升级后，「企业插件目录」页挂在「企业连接」所在分组（和扩展签名同一组）；
// 只补一次，重复迁移不会重复插入。
func TestMigrateMenusDirectory(t *testing.T) {
	ctx := context.Background()
	st := openTokenTestStore(t)
	if err := st.ResetMenus(ctx, rbac.DefaultMenus); err != nil {
		t.Fatal(err)
	}
	// 模拟 0034 之前的库：页不存在、迁移记录也没有
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM menus WHERE page_key = 'admin-directory'"); err != nil {
		t.Fatal(err)
	}
	replay := func() {
		t.Helper()
		if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0035_admin_directory_menu'"); err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	replay()
	ms, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]*Menu{}
	var page, connections *Menu
	for _, m := range ms {
		byID[m.ID] = m
		switch m.PageKey {
		case "admin-directory":
			page = m
		case "admin-connections":
			connections = m
		}
	}
	if page == nil {
		t.Fatal("迁移后没有企业插件目录页面")
	}
	if connections == nil || connections.ParentID == nil || page.ParentID == nil || *page.ParentID != *connections.ParentID {
		t.Fatalf("企业插件目录应和企业连接同组：page=%+v connections=%+v", page, connections)
	}
	if g := byID[*page.ParentID]; g == nil || g.Title != "能力与连接" {
		t.Fatalf("企业插件目录应在「能力与连接」里：%+v", g)
	}
	if page.Path != "/admin/directory" || page.Permission != rbac.ConnectionsManage || page.Icon != "FolderOpen" {
		t.Fatalf("企业插件目录菜单字段：%+v", page)
	}
	// 管理员把企业连接挪到别的分组：新页面跟着它，而不是硬塞回「能力与连接」
	other := &Menu{ParentID: nil, Kind: "group", Title: "自定义组", Sort: 999, Visible: true}
	if err := st.CreateMenu(ctx, other); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "UPDATE menus SET parent_id = ? WHERE page_key = 'admin-connections'", other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM menus WHERE page_key = 'admin-directory'"); err != nil {
		t.Fatal(err)
	}
	replay()
	again, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var moved *Menu
	for _, m := range again {
		if m.PageKey == "admin-directory" {
			moved = m
		}
	}
	if moved == nil || moved.ParentID == nil || *moved.ParentID != other.ID {
		t.Fatalf("企业连接挪组后企业插件目录应跟过去：%+v", moved)
	}
	// 再跑一次迁移不会重复插入
	if _, err := st.DB().ExecContext(ctx, "DELETE FROM schema_migrations WHERE version = '0035_admin_directory_menu'"); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	third, err := st.ListMenus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(third) != len(again) {
		t.Fatalf("重复迁移后菜单数 %d → %d", len(again), len(third))
	}
}
