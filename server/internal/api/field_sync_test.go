package api_test

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/ldapauth/ldaptest"
	"github.com/hikariming/dsh-ee/server/internal/secret"
)

// 组织同步带字段（docs/access-control.md §6.1）：LDAP 属性映射到用户字段，登录时写进去；
// 手工改过的值不被覆盖，除非映射选了「以同步为准」。
func TestLDAPFieldSync(t *testing.T) {
	st := openTestStore(t)
	if _, err := api.Bootstrap(context.Background(), st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	dir := ldaptest.Start(t)
	dir.Add(ldapSvc, "svc-secret", map[string][]string{"cn": {"svc"}})
	carol := person("carol", "Carol", "研发部")
	carol["title"] = []string{"重要"}
	carol["projects"] = []string{"A", "B"}
	dir.Add("uid=carol,ou=people,"+ldapBase, "carol-pw", carol)

	key := make([]byte, 32)
	_, _ = rand.Read(key)
	box, _ := secret.NewBox(key)
	srv := api.New(st, box, api.Options{DataDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	code, body = admin.do("PUT", "/api/admin/auth/ldap", map[string]any{
		"enabled": true, "url": dir.URL, "bindDn": ldapSvc, "bindPassword": "svc-secret", "baseDn": ldapBase,
		"userFilter": "(&(objectClass=person)(uid={username}))", "usernameAttr": "uid", "displayNameAttr": "cn",
		"departmentAttr": "ou", "emailAttr": "mail", "groupMode": "none", "autoCreate": true,
	}, nil)
	expect(t, "开 LDAP", code, 200, body)

	for _, f := range []map[string]any{
		{"object": "user", "key": "level", "label": "等级", "type": "level", "options": []string{"普通", "重要", "核心"}, "access": true},
		{"object": "user", "key": "projects", "label": "项目", "type": "tags", "options": []string{"A", "B", "C"}, "access": true},
	} {
		code, body = admin.do("POST", "/api/admin/fields", f, nil)
		expect(t, "加用户字段", code, 201, body)
	}
	code, body = admin.do("PUT", "/api/admin/field-sync", map[string]any{"mappings": []map[string]any{{"source": "ldap", "attr": "title", "field": "nope"}}}, nil)
	expect(t, "映射到不存在的字段", code, 400, body)
	code, body = admin.do("PUT", "/api/admin/field-sync", map[string]any{"mappings": []map[string]any{
		{"source": "ldap", "attr": "title", "field": "level"},
		{"source": "ldap", "attr": "projects", "field": "projects"},
	}}, nil)
	expect(t, "存映射", code, 200, body)

	login := func() int64 {
		t.Helper()
		c := newClient(t, ts.URL)
		code, body := c.do("POST", "/api/auth/login", map[string]string{"username": "carol", "password": "carol-pw"}, nil)
		expect(t, "carol 登录", code, 200, body)
		return decodeAs[struct{ ID int64 }](t, body).ID
	}
	values := func(id int64) map[string]any {
		t.Helper()
		code, body := admin.do("GET", "/api/fields/user/"+itoa(id), nil, nil)
		expect(t, "看字段", code, 200, body)
		out := map[string]any{}
		for _, f := range decodeAs[struct {
			Fields []struct {
				Key   string
				Value any
			}
		}](t, body).Fields {
			out[f.Key] = f.Value
		}
		return out
	}

	id := login()
	v := values(id)
	if v["level"] != "重要" || !strings.Contains(strings.Join(anyStrings(v["projects"]), ","), "A") {
		t.Fatalf("首次登录应带上目录里的等级和项目：%v", v)
	}
	// 目录变了：下次登录跟着变
	dir.Set("uid=carol,ou=people,"+ldapBase, "title", "核心")
	login()
	if values(id)["level"] != "核心" {
		t.Fatalf("目录改了等级，登录后应跟着变：%v", values(id))
	}
	// 管理员手工改过：同步不覆盖
	code, body = admin.do("PUT", "/api/fields/user/"+itoa(id), map[string]any{"values": map[string]any{"level": "普通"}}, nil)
	expect(t, "手工改等级", code, 200, body)
	login()
	if values(id)["level"] != "普通" {
		t.Fatalf("手工改过的值不该被同步覆盖：%v", values(id))
	}
	// 选了「以同步为准」：覆盖
	code, body = admin.do("PUT", "/api/admin/field-sync", map[string]any{"mappings": []map[string]any{{"source": "ldap", "attr": "title", "field": "level", "override": true}}}, nil)
	expect(t, "以同步为准", code, 200, body)
	login()
	if values(id)["level"] != "核心" {
		t.Fatalf("以同步为准时应覆盖手工值：%v", values(id))
	}
}

func anyStrings(v any) []string {
	var out []string
	if xs, ok := v.([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
