package api_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/ldapauth/ldaptest"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// 补 #260 之后剩下的 roles.assign 双人批准 / 互斥角色绕过：改角色的权限点、LDAP / OIDC 组映射。

func mkTestRole(t *testing.T, c *client, code string, perms []string) int64 {
	t.Helper()
	status, body := c.do("POST", "/api/admin/roles", map[string]any{"code": code, "name": code, "permissions": perms}, nil)
	expect(t, "建角色 "+code, status, http.StatusCreated, body)
	return decode[struct {
		ID int64 `json:"id"`
	}](t, body).ID
}

func putTestPolicy(t *testing.T, c *client, exclusive [][]string, dual []string) {
	t.Helper()
	if exclusive == nil {
		exclusive = [][]string{}
	}
	if dual == nil {
		dual = []string{}
	}
	status, body := c.do("PUT", "/api/admin/policy", map[string]any{"super_disabled": false, "no_self_grant": false, "exclusive": exclusive, "dual_approval": dual}, nil)
	expect(t, "改管理约束", status, http.StatusOK, body)
}

func rolePerms(t *testing.T, c *client, id int64) []string {
	t.Helper()
	status, body := c.do("GET", "/api/admin/roles", nil, nil)
	expect(t, "角色列表", status, http.StatusOK, body)
	for _, r := range decode[struct{ Roles []store.Role }](t, body).Roles {
		if r.ID == id {
			return r.Permissions
		}
	}
	t.Fatalf("角色 %d 不在列表里：%s", id, body)
	return nil
}

// #260 的缺口：互斥角色返回 403 + code；双人批准开着时直接带管理角色返回 403 + code；关着时照常 201。
func TestCreateUserRolesPolicyCodes(t *testing.T) {
	f := newPrincipalFixture(t)
	sysAdmin := mkTestRole(t, f.admin, "sys-admin", []string{"users.manage"})
	accessAdmin := mkTestRole(t, f.admin, "access-admin", []string{"fields.manage"})

	create := func(name string, roles []int64) (int, []byte) {
		return f.admin.do("POST", "/api/admin/users", map[string]any{"username": name, "password": name + "-password-1", "role_ids": roles}, nil)
	}

	// 双人批准没开：建账号时直接带管理角色照常 201
	putTestPolicy(t, f.admin, [][]string{{"sys-admin", "access-admin"}}, nil)
	status, body := create("direct-admin", []int64{sysAdmin})
	expect(t, "没开双人批准时建账号带管理角色", status, http.StatusCreated, body)

	status, body = create("both-admin", []int64{sysAdmin, accessAdmin})
	expect(t, "互斥角色一起给", status, http.StatusForbidden, body)
	checkErr(t, "互斥角色一起给", body, "角色 sys-admin 和 access-admin 是互斥的，不能由同一个人同时持有", "user.roles_exclusive", map[string]any{"roles": "sys-admin 和 access-admin"})

	putTestPolicy(t, f.admin, [][]string{{"sys-admin", "access-admin"}}, []string{"roles.assign"})
	status, body = create("shadow-admin", []int64{sysAdmin})
	expect(t, "开了双人批准建账号带管理角色", status, http.StatusForbidden, body)
	checkErr(t, "开了双人批准建账号带管理角色", body, "开了双人批准：建账号时不能直接给管理角色。先建账号，再在用户列表里给他分配角色，由另一位管理员批准后生效", "user.roles_dual_required", nil)
}

// 改角色：开了双人批准（roles.assign）时，给已经有人持有的角色加管理权限点要走配置变更、由另一位管理员批准。
func TestUpdateRoleAdminPermsRequiresDualApproval(t *testing.T) {
	f := newPrincipalFixture(t)
	// 第二位管理员（超级管理员），开双人批准之前建好
	status, body := f.admin.do("GET", "/api/admin/roles", nil, nil)
	expect(t, "角色列表", status, http.StatusOK, body)
	var superID int64
	for _, r := range decode[struct{ Roles []store.Role }](t, body).Roles {
		if r.IsSuper {
			superID = r.ID
		}
	}
	status, body = f.admin.do("POST", "/api/admin/users", map[string]any{"username": "admin2", "password": "admin2-password-1", "role_ids": []int64{superID}}, nil)
	expect(t, "建第二位管理员", status, http.StatusCreated, body)
	admin2 := newClient(t, f.ts.URL)
	status, body = admin2.do("POST", "/api/auth/login", map[string]string{"username": "admin2", "password": "admin2-password-1"}, nil)
	expect(t, "admin2 登录", status, http.StatusOK, body)

	plain := mkTestRole(t, f.admin, "plain", []string{"portal.view"})
	unheld := mkTestRole(t, f.admin, "unheld", []string{"portal.view"})
	putTestPolicy(t, f.admin, nil, []string{"roles.assign"})

	status, body = f.admin.do("POST", "/api/admin/users", map[string]any{"username": "holder", "password": "holder-password-1", "role_ids": []int64{plain}}, nil)
	expect(t, "建账号带非管理角色", status, http.StatusCreated, body)

	status, body = f.admin.do("PATCH", "/api/admin/roles/"+itoa(plain), map[string]any{"permissions": []string{"portal.view", "users.manage"}}, nil)
	expect(t, "给有人持有的角色加管理权限点", status, http.StatusConflict, body)
	if e := decodeErrBody(t, "加管理权限点", body); e.Code != "role.admin_perms_dual_required" || e.Params["role"] != "plain" {
		t.Fatalf("code / params 不对：%s", body)
	}
	if slices.Contains(rolePerms(t, f.admin, plain), "users.manage") {
		t.Fatal("被拒绝的修改不应生效")
	}

	// 不加管理权限点的修改、没人持有的角色，照常直接改
	status, body = f.admin.do("PATCH", "/api/admin/roles/"+itoa(plain), map[string]any{"permissions": []string{"portal.view", "llm.use"}}, nil)
	expect(t, "加员工权限点", status, http.StatusOK, body)
	status, body = f.admin.do("PATCH", "/api/admin/roles/"+itoa(unheld), map[string]any{"permissions": []string{"portal.view", "users.manage"}}, nil)
	expect(t, "没人持有的角色加管理权限点", status, http.StatusOK, body)

	// 走配置变更：提交人不能自己批准，另一位管理员批准后生效
	doc, _ := json.Marshal(map[string]any{
		"version": 1, "policy": map[string]any{"exclusive": []any{}, "dual_approval": []string{"roles.assign"}},
		"roles":  []any{map[string]any{"code": "plain", "name": "plain", "permissions": []string{"portal.view", "llm.use", "users.manage"}}},
		"fields": []any{}, "rules": []any{},
	})
	status, body = testSubmitConfig(t, f.admin, "给 plain 加用户管理", string(doc))
	expect(t, "提交配置变更", status, http.StatusCreated, body)
	id := testChangeID(t, body)
	status, body = testApproveChange(t, f.admin, id)
	expect(t, "自己批准给有人持有的角色加管理权限点", status, http.StatusForbidden, body)
	if slices.Contains(rolePerms(t, f.admin, plain), "users.manage") {
		t.Fatal("自批被拒后不应生效")
	}
	status, body = testApproveChange(t, admin2, id)
	expect(t, "另一位管理员批准", status, http.StatusOK, body)
	if !slices.Contains(rolePerms(t, f.admin, plain), "users.manage") {
		t.Fatal("另一位管理员批准后应生效")
	}

	// 双人批准关掉后，直接改照常
	putTestPolicy(t, f.admin, nil, nil)
	status, body = f.admin.do("PATCH", "/api/admin/roles/"+itoa(plain), map[string]any{"permissions": []string{"portal.view", "users.manage", "fields.manage"}}, nil)
	expect(t, "没开双人批准时加管理权限点", status, http.StatusOK, body)
}

// LDAP 组映射：同组映射到互斥角色拒绝；开了双人批准不能新加映射到管理角色；被映射的角色加管理权限点也算有人持有；
// 登录同步时互斥的映射角色被跳过并记审计。
func TestLDAPRoleMappingsFollowAdminPolicy(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	dir := ldaptest.Start(t)
	dir.Add(ldapSvc, "svc-secret", map[string][]string{"cn": {"svc"}})
	const opsDN = "cn=ops,ou=groups,dc=acme,dc=local"
	dir.Add("uid=dora,ou=people,"+ldapBase, "dora-dir-pw", person("dora", "Dora", "运维部", devsDN, opsDN))
	dir.Add("uid=erin,ou=people,"+ldapBase, "erin-dir-pw", person("erin", "Erin", "研发部", devsDN))

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	maker := mkTestRole(t, admin, "maker", []string{"portal.view"})
	checker := mkTestRole(t, admin, "checker", []string{"llm.use"})
	opsAdmin := mkTestRole(t, admin, "ops-admin", []string{"users.manage"})
	putTestPolicy(t, admin, [][]string{{"maker", "checker"}}, nil)

	cfg := map[string]any{
		"enabled": true, "url": dir.URL, "bindDn": ldapSvc, "bindPassword": "svc-secret", "baseDn": ldapBase,
		"userFilter": "(&(objectClass=person)(uid={username}))", "usernameAttr": "uid", "displayNameAttr": "cn",
		"departmentAttr": "ou", "emailAttr": "mail", "groupMode": "memberOf", "autoCreate": true,
	}
	put := func(mappings []map[string]any) (int, []byte) {
		cfg["roleMappings"] = mappings
		return admin.do("PUT", "/api/admin/auth/ldap", cfg, nil)
	}

	code, body = put([]map[string]any{{"group": "developers", "roleId": maker}, {"group": "developers", "roleId": checker}})
	expect(t, "同一个组映射到互斥角色", code, http.StatusBadRequest, body)
	if e := decodeErrBody(t, "同组互斥", body); e.Code != "role_mapping.exclusive" || e.Params["roles"] != "maker 和 checker" {
		t.Fatalf("code / params 不对：%s", body)
	}

	// 不同组映射到互斥角色可以保存，登录时兜底
	code, body = put([]map[string]any{{"group": "developers", "roleId": maker}, {"group": "ops", "roleId": checker}})
	expect(t, "不同组映射到互斥角色", code, http.StatusOK, body)

	// dora 同时在两个组：映射来的 maker、checker 都跳过，留下员工角色，并记审计
	dora := newClient(t, ts.URL)
	code, body = dora.do("POST", "/api/auth/login", map[string]string{"username": "dora", "password": "dora-dir-pw"}, nil)
	expect(t, "dora 登录", code, 200, body)
	code, body = dora.do("GET", "/api/auth/me", nil, nil)
	expect(t, "dora me", code, 200, body)
	me := decode[struct {
		Roles []store.RoleRef `json:"roles"`
	}](t, body)
	for _, r := range me.Roles {
		if r.ID == maker || r.ID == checker {
			t.Fatalf("同时在两个组的人不应拿到互斥的映射角色：%s", body)
		}
	}
	code, body = admin.do("GET", "/api/admin/audit?action=user.roles_mapping_dropped", nil, nil)
	expect(t, "审计", code, 200, body)
	if !strings.Contains(string(body), `"target":"dora"`) || !strings.Contains(string(body), "maker") {
		t.Fatalf("跳过互斥角色应记审计：%s", body)
	}
	// erin 只在 developers：照常拿到 maker
	erin := newClient(t, ts.URL)
	code, body = erin.do("POST", "/api/auth/login", map[string]string{"username": "erin", "password": "erin-dir-pw"}, nil)
	expect(t, "erin 登录", code, 200, body)
	code, body = erin.do("GET", "/api/auth/me", nil, nil)
	expect(t, "erin me", code, 200, body)
	if !strings.Contains(string(body), `"code":"maker"`) {
		t.Fatalf("只在一个组的人应拿到映射角色：%s", body)
	}

	// 开了双人批准：新加映射到管理角色拒绝；已有映射保持不动、非管理角色照常
	putTestPolicy(t, admin, [][]string{{"maker", "checker"}}, []string{"roles.assign"})
	code, body = put([]map[string]any{{"group": "developers", "roleId": maker}, {"group": "ops", "roleId": checker}, {"group": "ops", "roleId": opsAdmin}})
	expect(t, "开了双人批准新加管理角色映射", code, http.StatusConflict, body)
	if e := decodeErrBody(t, "管理角色映射", body); e.Code != "role_mapping.dual_required" || e.Params["role"] != "ops-admin" {
		t.Fatalf("code / params 不对：%s", body)
	}
	code, body = put([]map[string]any{{"group": "developers", "roleId": maker}})
	expect(t, "开了双人批准去掉映射", code, http.StatusOK, body)

	// 被映射的角色（这里 maker 也已有人持有）在双人批准下不能直接加管理权限点；只被映射、还没人持有的同样拦
	mapped := mkTestRole(t, admin, "mapped-only", []string{"portal.view"})
	code, body = put([]map[string]any{{"group": "developers", "roleId": maker}, {"group": "nobody", "roleId": mapped}})
	expect(t, "映射到非管理角色", code, http.StatusOK, body)
	code, body = admin.do("PATCH", "/api/admin/roles/"+itoa(mapped), map[string]any{"permissions": []string{"portal.view", "users.manage"}}, nil)
	expect(t, "给被映射的角色加管理权限点", code, http.StatusConflict, body)
	if e := decodeErrBody(t, "被映射角色", body); e.Code != "role.admin_perms_dual_required" {
		t.Fatalf("code 不对：%s", body)
	}
}

// OIDC 组映射：和 LDAP 同一套检查；登录时互斥的映射角色被跳过。
func TestOIDCRoleMappingsFollowAdminPolicy(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t)
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: idp.Client()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)

	maker := mkTestRole(t, admin, "maker", []string{"portal.view"})
	checker := mkTestRole(t, admin, "checker", []string{"llm.use"})
	opsAdmin := mkTestRole(t, admin, "ops-admin", []string{"users.manage"})
	putTestPolicy(t, admin, [][]string{{"maker", "checker"}}, nil)

	cfg := map[string]any{
		"enabled": true, "preset": "keycloak", "name": "SSO", "issuer": idp.URL, "clientId": "dsh-ee", "clientSecret": "idp-secret",
		"scopes": "profile email", "usernameClaim": "preferred_username", "nameClaim": "name", "emailClaim": "email",
		"groupsClaim": "groups", "autoCreate": true,
	}
	put := func(mappings []map[string]any) (int, []byte) {
		cfg["roleMappings"] = mappings
		code, body := admin.do("PUT", "/api/admin/auth/oidc", cfg, nil)
		delete(cfg, "clientSecret") // 之后沿用已存的
		return code, body
	}
	code, body = put([]map[string]any{{"group": "g1", "roleId": maker}, {"group": "g2", "roleId": checker}})
	expect(t, "不同组映射到互斥角色", code, http.StatusOK, body)
	code, body = put([]map[string]any{{"group": "g1", "roleId": maker}, {"group": "G1", "roleId": checker}})
	expect(t, "同一个组映射到互斥角色", code, http.StatusBadRequest, body)
	if e := decodeErrBody(t, "同组互斥", body); e.Code != "role_mapping.exclusive" {
		t.Fatalf("code 不对：%s", body)
	}

	emp := newClient(t, ts.URL)
	if loc := idp.login(t, emp, map[string]any{"sub": "u-1", "preferred_username": "fay", "groups": []string{"g1", "g2"}}, nil); loc != "/build" {
		t.Fatalf("登录应成功：%s", loc)
	}
	code, body = emp.do("GET", "/api/auth/me", nil, nil)
	expect(t, "fay me", code, 200, body)
	if strings.Contains(string(body), `"code":"maker"`) || strings.Contains(string(body), `"code":"checker"`) {
		t.Fatalf("同时在两个组的人不应拿到互斥的映射角色：%s", body)
	}
	// 再登录一次，只剩 g1：照常拿到 maker
	again := newClient(t, ts.URL)
	idp.login(t, again, map[string]any{"sub": "u-1", "preferred_username": "fay", "groups": []string{"g1"}}, nil)
	code, body = again.do("GET", "/api/auth/me", nil, nil)
	expect(t, "fay me", code, 200, body)
	if !strings.Contains(string(body), `"code":"maker"`) {
		t.Fatalf("只在一个组时应拿到映射角色：%s", body)
	}

	putTestPolicy(t, admin, [][]string{{"maker", "checker"}}, []string{"roles.assign"})
	code, body = put([]map[string]any{{"group": "g1", "roleId": maker}, {"group": "g2", "roleId": checker}, {"group": "admins", "roleId": opsAdmin}})
	expect(t, "开了双人批准新加管理角色映射", code, http.StatusConflict, body)
	if e := decodeErrBody(t, "管理角色映射", body); e.Code != "role_mapping.dual_required" {
		t.Fatalf("code 不对：%s", body)
	}
}
