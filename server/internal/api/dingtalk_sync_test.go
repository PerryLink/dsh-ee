package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/api"
	"github.com/hikariming/dsh-ee/server/internal/feishu"
	"github.com/hikariming/dsh-ee/server/internal/secret"
	"github.com/hikariming/dsh-ee/server/internal/store"
)

// fakeDingTalk 模拟钉钉的登录（OAuth）和通讯录接口。登录用的授权码就是成员的 userId。
type fakeDingTalk struct {
	*httptest.Server
	mux *http.ServeMux
	mu  sync.Mutex
	// 部门 id → 名字 / 上级 / 直属成员（按顺序）
	names   map[int64]string
	parents map[int64]int64
	members map[int64][]string
	users   map[string]map[string]any
	// 单独查一名成员时的错误码：userId → errcode（60121 找不到、50002 不在授权范围）
	single map[string]int
	// 非 0 时通讯录接口整体返回这个错误码
	failCode int
	// 每页给几个成员，用来验证分页
	pageSize int
	lists    int
}

func newFakeDingTalk(t *testing.T) *fakeDingTalk {
	f := &fakeDingTalk{names: map[int64]string{}, parents: map[int64]int64{}, members: map[int64][]string{},
		users: map[string]map[string]any{}, single: map[string]int{}, pageSize: 1}
	fail := func(w http.ResponseWriter, code int, msg string) {
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": code, "errmsg": msg})
	}
	// 通讯录接口的公共检查：应用 token、整体故障
	directory := func(h func(w http.ResponseWriter, in map[string]any)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in map[string]any
			if r.URL.Query().Get("access_token") != "app-token" || json.NewDecoder(r.Body).Decode(&in) != nil {
				fail(w, 40014, "不合法的 access_token")
				return
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.failCode != 0 {
				fail(w, f.failCode, "没有调用该接口的权限")
				return
			}
			h(w, in)
		}
	}
	mux := http.NewServeMux()
	f.mux = mux
	mux.HandleFunc("POST api.dingtalk.com/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["appKey"] != "ding-app" || in["appSecret"] != "ding-app-secret" {
			http.Error(w, "invalid app credentials", 401)
			return
		}
		fmt.Fprint(w, `{"accessToken":"app-token","expireIn":7200}`)
	})
	mux.HandleFunc("POST api.dingtalk.com/v1.0/oauth2/userAccessToken", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		if json.NewDecoder(r.Body).Decode(&in) != nil || in["clientSecret"] != "ding-app-secret" || in["code"] == "" {
			http.Error(w, "unexpected user token request", 400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"accessToken": "ut-" + in["code"], "corpId": "ding-corp"})
	})
	mux.HandleFunc("GET api.dingtalk.com/v1.0/contact/users/me", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.Header.Get("x-acs-dingtalk-access-token"), "ut-")
		_ = json.NewEncoder(w).Encode(map[string]string{"unionId": "union-" + id, "nick": "钉钉昵称 " + id})
	})
	mux.HandleFunc("POST oapi.dingtalk.com/topapi/user/getbyunionid", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "result": map[string]any{
			"userid": strings.TrimPrefix(in["unionid"], "union-"), "contact_type": 0}})
	})
	mux.HandleFunc("POST oapi.dingtalk.com/topapi/v2/department/listsub", directory(func(w http.ResponseWriter, in map[string]any) {
		parent := int64(in["dept_id"].(float64))
		out := []map[string]any{}
		for id, p := range f.parents {
			if p == parent {
				out = append(out, map[string]any{"dept_id": id, "name": f.names[id], "parent_id": p})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "result": out})
	}))
	mux.HandleFunc("POST oapi.dingtalk.com/topapi/v2/user/list", directory(func(w http.ResponseWriter, in map[string]any) {
		f.lists++
		ids := f.members[int64(in["dept_id"].(float64))]
		cursor := int(in["cursor"].(float64))
		if size := int(in["size"].(float64)); size < 1 || size > 100 {
			fail(w, 40035, "不合法的参数")
			return
		}
		end := min(cursor+f.pageSize, len(ids))
		list := []map[string]any{}
		for _, id := range ids[min(cursor, len(ids)):end] {
			list = append(list, f.users[id])
		}
		result := map[string]any{"has_more": end < len(ids), "list": list}
		if end < len(ids) {
			result["next_cursor"] = end
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "result": result})
	}))
	mux.HandleFunc("POST oapi.dingtalk.com/topapi/v2/user/get", directory(func(w http.ResponseWriter, in map[string]any) {
		id, _ := in["userid"].(string)
		if code := f.single[id]; code != 0 {
			fail(w, code, "找不到该用户")
			return
		}
		u, ok := f.users[id]
		if !ok {
			u = map[string]any{"userid": id}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "errmsg": "ok", "result": u})
	}))
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

// dept 加一个部门；parent 为 0 表示不是任何部门的子部门（根部门 1 本身不用加）。
func (f *fakeDingTalk) dept(id, parent int64, name string) {
	f.names[id], f.parents[id] = name, parent
}

// put 把成员放进这些部门（替换他原来的部门）。
func (f *fakeDingTalk) put(user map[string]any, depts ...int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := user["userid"].(string)
	f.removeLocked(id)
	user["dept_id_list"] = depts
	f.users[id] = user
	for _, d := range depts {
		f.members[d] = append(f.members[d], id)
	}
}

func (f *fakeDingTalk) removeLocked(id string) {
	delete(f.users, id)
	for d, ids := range f.members {
		f.members[d] = slices.DeleteFunc(slices.Clone(ids), func(x string) bool { return x == id })
	}
}

func (f *fakeDingTalk) remove(id string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeLocked(id)
	if code != 0 {
		f.single[id] = code
	}
}

// dingTalkRoute 把发往钉钉域名的请求转到假钉钉，其它请求（假飞书 / 假 IdP）原样放行。
type dingTalkRoute struct{ target *url.URL }

func (tr dingTalkRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host != "api.dingtalk.com" && req.URL.Host != "oapi.dingtalk.com" {
		return http.DefaultTransport.RoundTrip(req)
	}
	r := req.Clone(req.Context())
	r.URL.Scheme, r.URL.Host = tr.target.Scheme, tr.target.Host
	r.Host = req.URL.Host
	return http.DefaultTransport.RoundTrip(r)
}

func (f *fakeDingTalk) client(t *testing.T) *http.Client {
	target, err := url.Parse(f.URL)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Transport: dingTalkRoute{target}}
}

// dingTalkSignIn 走一遍钉钉登录：start → 钉钉授权页（这里直接带着授权码回来）→ callback；返回最后跳到的地址。
func dingTalkSignIn(t *testing.T, c *client, startQuery, userID string) string {
	t.Helper()
	browser := *c.hc
	browser.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := browser.Get(c.base + "/api/auth/oidc/start?" + startQuery)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	u, err := res.Location()
	if err != nil || res.StatusCode != 302 {
		t.Fatalf("start 应该跳转：%d %v", res.StatusCode, err)
	}
	if u.Host != "login.dingtalk.com" {
		// 没有跳去钉钉（没登录、入口没开）：把跳转地址交给调用方判断
		return res.Header.Get("Location")
	}
	res, err = browser.Get(c.base + "/api/auth/oidc/callback?" + url.Values{"authCode": {userID}, "state": {u.Query().Get("state")}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 {
		t.Fatalf("callback 应该跳转：%d", res.StatusCode)
	}
	return res.Header.Get("Location")
}

type dingTalkSyncBody struct {
	OK         bool     `json:"ok"`
	Error      string   `json:"error"`
	Remote     int      `json:"remote"`
	Created    int      `json:"created"`
	Reconciled int      `json:"reconciled"`
	Linked     int      `json:"linked"`
	Disabled   []string `json:"disabled"`
	Conflicts  []string `json:"conflicts"`
	Skipped    []string `json:"skipped"`
	Kept       []string `json:"kept"`
	Unlinked   []string `json:"unlinked"`
}

func whoAmI(t *testing.T, c *client) string {
	t.Helper()
	code, body := c.do("GET", "/api/auth/me", nil, nil)
	if code != 200 {
		return ""
	}
	return decodeAs[struct{ Username string }](t, body).Username
}

func TestDingTalkOrganizationSync(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	ding := newFakeDingTalk(t)
	// 根部门 1 ─ 研发部 10 ─ 平台组 11；销售部 20
	ding.dept(10, 1, "研发部")
	ding.dept(11, 10, "平台组")
	ding.dept(20, 1, "销售部")
	ding.put(map[string]any{"userid": "boss", "name": "老板", "org_email": "boss@corp.example"}, 1)
	ding.put(map[string]any{"userid": "staff-2", "name": "吴安", "mobile": "13800000002", "state_code": "86", "email": "self@example.com", "org_email": "wuan@corp.example", "title": "工程师"}, 10)
	ding.put(map[string]any{"userid": "staff-3", "name": "柏博", "email": "bob@example.com"}, 10)
	// 同一个人在两个部门里只算一次；userId 不能直接当用户名的用摘要
	ding.put(map[string]any{"userid": "Staff 4/平台", "name": "柯若"}, 10, 11)
	box, _ := secret.NewBox(make([]byte, 32))
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: ding.client(t)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	role := func(codeName string) int64 {
		code, body := admin.do("POST", "/api/admin/roles", map[string]any{"code": codeName, "name": codeName, "permissions": []string{"dashboard.view"}}, nil)
		expect(t, "创建映射角色", code, 201, body)
		return decodeAs[struct{ ID int64 }](t, body).ID
	}
	engineer, platform := role("engineer"), role("platform")
	settings := map[string]any{
		"enabled": true, "preset": "dingtalk", "name": "钉钉", "autoCreate": true,
		"clientId": "ding-app", "clientSecret": "ding-app-secret", "dingTalkCorpId": "ding-corp",
		// 部门名和部门 id 都能当映射的组
		"roleMappings": []map[string]any{{"group": "研发部", "roleId": engineer}, {"group": "11", "roleId": platform}},
	}
	code, body = admin.do("PUT", "/api/admin/auth/oidc", settings, nil)
	expect(t, "保存钉钉登录", code, 200, body)
	sync := func(what string) dingTalkSyncBody {
		t.Helper()
		code, body := admin.do("POST", "/api/admin/auth/dingtalk/sync", nil, nil)
		expect(t, what, code, 200, body)
		if strings.Contains(string(body), "ding-app-secret") || strings.Contains(string(body), "app-token") {
			t.Fatalf("同步结果不能带凭据：%s", body)
		}
		return decodeAs[dingTalkSyncBody](t, body)
	}
	roles := func(u *store.User) []int64 {
		t.Helper()
		a, err := st.UserAccess(ctx, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		return a.RoleIDs
	}

	res := sync("首次同步")
	if !res.OK || res.Remote != 4 || res.Created != 4 || res.Reconciled != 0 || len(res.Conflicts) != 0 {
		t.Fatalf("首次组织同步结果不对：%+v", res)
	}
	if ding.lists < 6 {
		t.Fatalf("研发部三个人每页一个，应该翻页去取：只调了 %d 次", ding.lists)
	}
	wuan, err := st.GetUserByExternalDN(ctx, store.AuthOIDC, "dingtalk:ding-corp:staff-2")
	if err != nil || wuan.Username != "staff-2" || wuan.DisplayName != "吴安" || wuan.Department != "研发部" || wuan.Email != "wuan@corp.example" {
		t.Fatalf("同步后的成员资料不对：%+v err=%v", wuan, err)
	}
	if got := roles(wuan); len(got) != 2 || !slices.Contains(got, engineer) {
		t.Fatalf("研发部应映射到 engineer 角色（外加默认员工角色）：%v", got)
	}
	keruo, err := st.GetUserByExternalDN(ctx, store.AuthOIDC, "dingtalk:ding-corp:Staff 4/平台")
	if err != nil || !strings.HasPrefix(keruo.Username, "ding-") || keruo.Department != "研发部 / 平台组" {
		t.Fatalf("多部门成员不对：%+v err=%v", keruo, err)
	}
	if got := roles(keruo); !slices.Contains(got, engineer) || !slices.Contains(got, platform) {
		t.Fatalf("按部门名和部门 id 的映射都应生效：%v", got)
	}
	if boss, err := st.GetUserByUsername(ctx, "boss"); err != nil || boss.Department != "" || len(roles(boss)) != 1 {
		t.Fatalf("根部门成员不该有映射角色：%+v err=%v", boss, err)
	}

	// 同一个 userId 用钉钉登录，进的是同步建好的账号，映射的角色不被登录抹掉。
	emp := newClient(t, ts.URL)
	if loc := dingTalkSignIn(t, emp, "next=/build", "staff-2"); loc != "/build" {
		t.Fatalf("钉钉登录失败：%s", loc)
	}
	if again, err := st.GetUserByExternalDN(ctx, store.AuthOIDC, "dingtalk:ding-corp:staff-2"); err != nil || again.ID != wuan.ID || len(roles(again)) != 2 {
		t.Fatalf("登录应复用同步账号并保留角色：%+v err=%v", again, err)
	}

	// 调部门：资料和映射角色跟着变，不新增账号。
	ding.put(map[string]any{"userid": "staff-2", "name": "吴安", "org_email": "wuan@corp.example"}, 20)
	res = sync("部门变化同步")
	if !res.OK || res.Created != 0 || res.Reconciled != 4 {
		t.Fatalf("部门变化不该新建账号：%+v", res)
	}
	if wuan, err = st.GetUser(ctx, wuan.ID); err != nil || wuan.Department != "销售部" || slices.Contains(roles(wuan), engineer) {
		t.Fatalf("调到销售部后应收回 engineer 角色：%+v err=%v", wuan, err)
	}

	// staff-3 离职（钉钉说找不到）、柯若只是不在应用的授权范围里。没开同步禁用时谁都不动。
	ding.remove("staff-3", 60121)
	ding.remove("Staff 4/平台", 50002)
	if res = sync("目录缺席但没开禁用"); len(res.Disabled) != 0 {
		t.Fatalf("没开同步禁用不该禁用账号：%+v", res)
	}
	saved := decodeAs[map[string]any](t, mustGet(t, admin, "/api/admin/auth/oidc"))
	saved["dingTalkSync"], saved["dingTalkSyncDisable"] = true, true
	code, body = admin.do("PUT", "/api/admin/auth/oidc", saved, nil)
	expect(t, "打开每小时同步与离职禁用", code, 200, body)
	gone := newClient(t, ts.URL)
	bob, _ := st.GetUserByExternalDN(ctx, store.AuthOIDC, "dingtalk:ding-corp:staff-3")
	token := secret.Token()
	if err := st.CreateSession(ctx, secret.Hash(token), bob.ID, 3600_000_000_000); err != nil {
		t.Fatal(err)
	}
	cookie := map[string]string{"Cookie": "dsh_ee_session=" + token}
	if code, body = gone.do("GET", "/api/auth/me", nil, cookie); code != 200 {
		t.Fatalf("离职前会话应可用：%d %s", code, body)
	}
	res = sync("离职禁用")
	if !res.OK || len(res.Disabled) != 1 || res.Disabled[0] != "staff-3" || len(res.Kept) != 1 || !strings.HasPrefix(res.Kept[0], keruo.Username) {
		t.Fatalf("只有钉钉确认找不到的成员才禁用，看不到的留着：%+v", res)
	}
	if bob, err = st.GetUser(ctx, bob.ID); err != nil || !bob.Disabled {
		t.Fatalf("离职员工应被禁用：%+v err=%v", bob, err)
	}
	if code, _ = gone.do("GET", "/api/auth/me", nil, cookie); code != 401 {
		t.Fatalf("禁用后会话应失效：%d", code)
	}
	if keruo, err = st.GetUser(ctx, keruo.ID); err != nil || keruo.Disabled {
		t.Fatalf("不在授权范围里的成员不该禁用：%+v err=%v", keruo, err)
	}
	// 已禁用的不再重复禁用；离职的人回到目录也不自动恢复
	ding.put(map[string]any{"userid": "staff-3", "name": "柏博"}, 10)
	if res = sync("已禁用不自动恢复"); len(res.Disabled) != 0 || !slices.ContainsFunc(res.Skipped, func(s string) bool { return strings.HasPrefix(s, "staff-3") }) {
		t.Fatalf("本地已禁用的账号不自动恢复：%+v", res)
	}

	// 通讯录接口报错、目录为空：整轮不生效，绝不禁用。
	ding.failCode = 60011
	if res = sync("目录错误"); res.OK || !strings.Contains(res.Error, "60011") {
		t.Fatalf("目录错误应明确报告：%+v", res)
	}
	ding.failCode = 0
	for _, id := range []string{"boss", "staff-2", "staff-3"} {
		ding.remove(id, 60121)
	}
	if res = sync("目录为空"); res.OK || len(res.Disabled) != 0 {
		t.Fatalf("目录为空不该生效：%+v", res)
	}
	if wuan, err = st.GetUser(ctx, wuan.ID); err != nil || wuan.Disabled {
		t.Fatalf("目录失败不应禁用员工：%+v err=%v", wuan, err)
	}
	last := decodeAs[struct{ Last *dingTalkSyncBody }](t, mustGet(t, admin, "/api/admin/auth/dingtalk/sync"))
	if last.Last == nil || last.Last.OK || last.Last.Error == "" {
		t.Fatalf("同步状态应记下最近一次的结果：%+v", last.Last)
	}
}

// 飞书是主登录方式、钉钉是第二登录入口：同一名员工用哪边登录都进同一个账号。
func TestFeishuDingTalkIdentityBinding(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := api.Bootstrap(ctx, st, "admin", "admin-password-123"); err != nil {
		t.Fatal(err)
	}
	idp := newFakeIdP(t)
	ding := newFakeDingTalk(t)
	contacts := &fakeFeishuContacts{base: idp.Config.Handler, department: feishu.Department{ID: "dep-rd", Name: "研发部"}, single: map[string]string{}, users: []feishu.User{
		{OpenID: "ou-1", Name: "吴安", Email: "alice@example.com", Mobile: "+8613800000001", EnterpriseEmail: "alice@corp.example", DepartmentIDs: []string{"dep-rd"}},
		{OpenID: "ou-2", Name: "柏博", Email: "bob@example.com", Mobile: "+8613800000002", DepartmentIDs: []string{"dep-rd"}},
		{OpenID: "ou-3", Name: "柯若", Email: "carol@example.com", DepartmentIDs: []string{"dep-rd"}},
	}}
	idp.Config.Handler = contacts
	ding.put(map[string]any{"userid": "staff-a", "name": "吴安", "mobile": "13800000001", "state_code": "86", "org_email": "Alice@corp.example"}, 1)
	ding.put(map[string]any{"userid": "staff-b", "name": "柏博", "mobile": "138-0000-0002", "state_code": "86"}, 1)
	// 只有员工自填的邮箱和飞书那边一样：没人验证过，不能据此关联
	ding.put(map[string]any{"userid": "staff-c", "name": "柯若", "email": "carol@example.com"}, 1)
	box, _ := secret.NewBox(make([]byte, 32))
	srv := api.New(st, box, api.Options{DataDir: t.TempDir(), OIDCClient: ding.client(t)}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	admin := newClient(t, ts.URL)
	code, body := admin.do("POST", "/api/auth/login", map[string]string{"username": "admin", "password": "admin-password-123"}, nil)
	expect(t, "管理员登录", code, 200, body)
	second := map[string]any{
		"enabled": true, "preset": "dingtalk", "name": "钉钉", "autoCreate": false,
		"clientId": "ding-app", "clientSecret": "ding-app-secret", "dingTalkCorpId": "ding-corp", "dingTalkBot": true,
	}
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", second, nil)
	expect(t, "主登录方式没开时不能开第二入口", code, 400, body)
	code, body = admin.do("PUT", "/api/admin/auth/oidc", map[string]any{
		"enabled": true, "preset": "feishu", "name": "飞书", "issuer": idp.URL, "clientId": "dsh-ee", "clientSecret": "idp-secret",
		"scopes": "profile email", "usernameClaim": "preferred_username", "nameClaim": "name", "emailClaim": "email",
		"departmentClaim": "department", "groupsClaim": "groups", "autoCreate": true, "feishuApiBase": idp.URL,
		"feishuSyncDisable": true,
	}, nil)
	expect(t, "保存飞书主登录方式", code, 200, body)
	bad := map[string]any{"enabled": true, "preset": "feishu", "name": "飞书二号", "issuer": idp.URL, "clientId": "dsh-ee", "clientSecret": "idp-secret"}
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", bad, nil)
	expect(t, "两个入口不能是同一个来源", code, 400, body)
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", second, nil)
	expect(t, "保存钉钉第二入口", code, 200, body)
	if strings.Contains(string(body), "ding-app-secret") || !strings.Contains(string(body), `"dingTalkBot":true`) {
		t.Fatalf("第二入口不回显密钥，但应保留机器人开关：%s", body)
	}
	if primary := mustGet(t, admin, "/api/admin/auth/oidc"); !strings.Contains(string(primary), `"preset":"feishu"`) {
		t.Fatalf("保存第二入口不能动主登录方式：%s", primary)
	}
	methods := string(mustGet(t, admin, "/api/auth/methods"))
	if !strings.Contains(methods, `"oidc":{"name":"飞书"}`) || !strings.Contains(methods, `"oidc_secondary":{"name":"钉钉","provider":"dingtalk"}`) {
		t.Fatalf("登录页应同时给出两个入口：%s", methods)
	}

	code, body = admin.do("POST", "/api/admin/auth/feishu/sync", nil, nil)
	expect(t, "飞书组织同步", code, 200, body)
	alice, err := st.GetUserByUsername(ctx, "alice")
	if err != nil || alice.ExternalDN != "feishu:ou-1" {
		t.Fatalf("飞书同步应建出 alice：%+v err=%v", alice, err)
	}
	feishuLogin := func(c *client, openID, email string) string {
		return idp.login(t, c, map[string]any{"sub": openID, "preferred_username": email, "email": email}, nil)
	}
	dingSync := func(what string) dingTalkSyncBody {
		t.Helper()
		code, body := admin.do("POST", "/api/admin/auth/dingtalk/sync", nil, nil)
		expect(t, what, code, 200, body)
		return decodeAs[dingTalkSyncBody](t, body)
	}
	users := func() int {
		n, err := st.CountUsers(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := users()

	// 没绑定、也没开自动关联：手机号对得上也不合并。钉钉身份进不了 alice 的账号。
	stranger := newClient(t, ts.URL)
	if loc := dingTalkSignIn(t, stranger, "provider=dingtalk&next=/build", "staff-a"); !strings.HasPrefix(loc, "/login?oidc_error=") || whoAmI(t, stranger) != "" {
		t.Fatalf("没有绑定时钉钉身份不该登录进任何账号：%s", loc)
	}
	if res := dingSync("没开自动关联的钉钉同步"); res.Linked != 0 || res.Created != 0 || users() != before {
		t.Fatalf("没开自动关联时同步不该关联或建号：%+v", res)
	}

	// 没登录不能发起绑定。
	if loc := dingTalkSignIn(t, newClient(t, ts.URL), "provider=dingtalk&bind=1", "staff-a"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("没登录不该能绑定：%s", loc)
	}

	// 显式绑定：alice 先用飞书登录，再在个人账号页绑定自己的钉钉身份。
	aliceWeb := newClient(t, ts.URL)
	if loc := feishuLogin(aliceWeb, "ou-1", "alice@example.com"); loc != "/build" {
		t.Fatalf("飞书登录失败：%s", loc)
	}
	type identities struct {
		Identities []struct {
			Provider, Via             string
			Bound, CanBind, CanUnlink bool
		}
	}
	mine := decodeAs[identities](t, mustGet(t, aliceWeb, "/api/me/identities"))
	if len(mine.Identities) != 2 || mine.Identities[0].Provider != "feishu" || mine.Identities[0].Via != "home" || mine.Identities[0].CanUnlink ||
		mine.Identities[1].Provider != "dingtalk" || mine.Identities[1].Bound || !mine.Identities[1].CanBind {
		t.Fatalf("绑定前的身份列表不对：%+v", mine)
	}
	if loc := dingTalkSignIn(t, aliceWeb, "provider=dingtalk&bind=1", "staff-a"); loc != "/account?identity_bound=dingtalk" {
		t.Fatalf("绑定钉钉身份失败：%s", loc)
	}
	if whoAmI(t, aliceWeb) != "alice" || users() != before {
		t.Fatalf("绑定不该换会话或建号")
	}
	mine = decodeAs[identities](t, mustGet(t, aliceWeb, "/api/me/identities"))
	if !mine.Identities[1].Bound || mine.Identities[1].Via != "explicit" || !mine.Identities[1].CanUnlink {
		t.Fatalf("绑定后的身份列表不对：%+v", mine)
	}
	// 两条登录路径落到同一个账号
	viaDing, viaFeishu := newClient(t, ts.URL), newClient(t, ts.URL)
	if loc := dingTalkSignIn(t, viaDing, "provider=dingtalk&next=/build", "staff-a"); loc != "/build" {
		t.Fatalf("绑定后用钉钉登录失败：%s", loc)
	}
	if loc := feishuLogin(viaFeishu, "ou-1", "alice@example.com"); loc != "/build" {
		t.Fatalf("飞书登录失败：%s", loc)
	}
	if a, b := whoAmI(t, viaDing), whoAmI(t, viaFeishu); a != "alice" || b != "alice" || users() != before {
		t.Fatalf("钉钉和飞书登录应是同一个账号：%q %q", a, b)
	}
	// 用钉钉登录不改主身份、资料
	if again, err := st.GetUser(ctx, alice.ID); err != nil || again.ExternalDN != "feishu:ou-1" || again.DisplayName != "吴安" || again.Email != "alice@example.com" {
		t.Fatalf("关联身份登录不该改账号资料：%+v err=%v", again, err)
	}
	code, body = admin.do("GET", "/api/admin/audit?action=oidc.identity_link", nil, nil)
	expect(t, "身份关联审计", code, 200, body)
	if !strings.Contains(string(body), "explicit") || !strings.Contains(string(body), "alice") {
		t.Fatalf("显式绑定应记审计：%s", body)
	}

	// 已经属于 alice 的钉钉身份，bob 不能再绑；alice 也不能再绑第二个钉钉身份。
	bobWeb := newClient(t, ts.URL)
	if loc := feishuLogin(bobWeb, "ou-2", "bob@example.com"); loc != "/build" {
		t.Fatalf("飞书登录失败：%s", loc)
	}
	if loc := dingTalkSignIn(t, bobWeb, "provider=dingtalk&bind=1", "staff-a"); !strings.HasPrefix(loc, "/account?identity_error=") || !strings.Contains(loc, "identity_error_code=identity.linked_elsewhere") {
		t.Fatalf("别人的钉钉身份不该能绑：%s", loc)
	}
	if loc := dingTalkSignIn(t, aliceWeb, "provider=dingtalk&bind=1", "staff-b"); !strings.HasPrefix(loc, "/account?identity_error=") || !strings.Contains(loc, "identity_error_code=identity.already_linked") {
		t.Fatalf("一个账号只能绑一个钉钉身份：%s", loc)
	}
	// 本地账号（管理员）不能绑定
	if loc := dingTalkSignIn(t, admin, "provider=dingtalk&bind=1", "staff-b"); !strings.HasPrefix(loc, "/account?identity_error=") || !strings.Contains(loc, "identity_error_code=identity.account_ineligible") {
		t.Fatalf("本地账号不该能绑定钉钉身份：%s", loc)
	}

	// 开自动关联：钉钉同步按核实过的手机号把 staff-b 关联到 bob；staff-c 只有自填邮箱一样，不关联。
	second["linkByContact"] = true
	delete(second, "clientSecret")
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", second, nil)
	expect(t, "打开自动关联", code, 200, body)
	res := dingSync("自动关联的钉钉同步")
	if !res.OK || res.Linked != 2 || res.Created != 0 || users() != before || !slices.ContainsFunc(res.Skipped, func(s string) bool { return strings.HasPrefix(s, "staff-c") }) {
		t.Fatalf("应关联 staff-a（已绑定）和 staff-b（手机号），staff-c 不关联也不建号：%+v", res)
	}
	bobDing := newClient(t, ts.URL)
	if loc := dingTalkSignIn(t, bobDing, "provider=dingtalk&next=/build", "staff-b"); loc != "/build" || whoAmI(t, bobDing) != "bob" {
		t.Fatalf("自动关联后 bob 用钉钉登录应进自己的账号：%s", loc)
	}
	if loc := dingTalkSignIn(t, newClient(t, ts.URL), "provider=dingtalk&next=/build", "staff-c"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("自填邮箱一样不该关联到 carol：%s", loc)
	}
	// 另一个钉钉成员登记了和 alice 一样的手机号：alice 已有钉钉身份，不会被顶掉，也不合并
	ding.put(map[string]any{"userid": "staff-x", "name": "冒名", "mobile": "13800000001", "state_code": "86"}, 1)
	if loc := dingTalkSignIn(t, newClient(t, ts.URL), "provider=dingtalk&next=/build", "staff-x"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("同手机号的第二个钉钉成员不该进 alice 的账号：%s", loc)
	}
	ding.remove("staff-x", 0)

	code, body = aliceWeb.do("DELETE", "/api/me/identities/dingtalk", nil, nil)
	expect(t, "解除 alice 的钉钉身份", code, 200, body)
	mine = decodeAs[identities](t, mustGet(t, aliceWeb, "/api/me/identities"))
	if mine.Identities[1].Bound || !mine.Identities[1].CanBind {
		t.Fatalf("解除后应显示未绑定并允许重绑：%+v", mine)
	}
	if res = dingSync("同步不能恢复主动解除的身份"); res.Linked != 1 || !slices.Contains(res.Skipped, "staff-a") {
		t.Fatalf("同步应跳过主动解除的身份：%+v", res)
	}
	second["autoCreate"] = true
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", second, nil)
	expect(t, "打开钉钉自动建号", code, 200, body)
	fresh := newClient(t, ts.URL)
	if loc := dingTalkSignIn(t, fresh, "provider=dingtalk&next=/build", "staff-a"); !strings.HasPrefix(loc, "/login?oidc_error=") || whoAmI(t, fresh) != "" || users() != before {
		t.Fatalf("主动解除的身份不能自动恢复或建号：%s", loc)
	}
	second["autoCreate"] = false
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", second, nil)
	expect(t, "还原钉钉自动建号设置", code, 200, body)
	ding.put(map[string]any{"userid": "staff-y", "name": "吴安"}, 1)
	if loc := dingTalkSignIn(t, aliceWeb, "provider=dingtalk&bind=1", "staff-y"); loc != "/account?identity_bound=dingtalk" {
		t.Fatalf("主动解除的身份不该阻止绑定另一个账号：%s", loc)
	}
	code, body = aliceWeb.do("DELETE", "/api/me/identities/dingtalk", nil, nil)
	expect(t, "解除临时绑定", code, 200, body)
	ding.remove("staff-y", 0)
	if loc := dingTalkSignIn(t, aliceWeb, "provider=dingtalk&bind=1", "staff-a"); loc != "/account?identity_bound=dingtalk" {
		t.Fatalf("alice 应能自己重新绑定：%s", loc)
	}
	if loc := dingTalkSignIn(t, fresh, "provider=dingtalk&next=/build", "staff-a"); loc != "/build" || whoAmI(t, fresh) != "alice" {
		t.Fatalf("显式重新绑定后应恢复登录：%s", loc)
	}

	// bob 从钉钉离职：只解除钉钉关联，账号归飞书管，不禁用。
	second["dingTalkSyncDisable"] = true
	code, body = admin.do("PUT", "/api/admin/auth/oidc?slot=secondary", second, nil)
	expect(t, "打开钉钉离职处理", code, 200, body)
	ding.remove("staff-b", 60121)
	res = dingSync("钉钉离职")
	if !res.OK || len(res.Unlinked) != 1 || res.Unlinked[0] != "bob" || len(res.Disabled) != 0 {
		t.Fatalf("钉钉离职应只解除关联：%+v", res)
	}
	if bob, err := st.GetUserByUsername(ctx, "bob"); err != nil || bob.Disabled {
		t.Fatalf("飞书那边还在职，账号不该禁用：%+v err=%v", bob, err)
	}

	// alice 从飞书离职（主身份）：账号禁用，钉钉这条路也进不来。
	contacts.users = contacts.users[1:]
	contacts.single["ou-1"] = "resigned"
	code, body = admin.do("POST", "/api/admin/auth/feishu/sync", nil, nil)
	expect(t, "飞书离职同步", code, 200, body)
	if again, err := st.GetUser(ctx, alice.ID); err != nil || !again.Disabled {
		t.Fatalf("主身份离职应禁用账号：%s %+v err=%v", body, again, err)
	}
	if whoAmI(t, viaDing) != "" {
		t.Fatalf("禁用后钉钉登录建立的会话也应失效")
	}
	if loc := dingTalkSignIn(t, newClient(t, ts.URL), "provider=dingtalk&next=/build", "staff-a"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("账号禁用后不能再用钉钉登录：%s", loc)
	}

	// 员工自己解除关联后，钉钉身份不再进这个账号。
	carolWeb := newClient(t, ts.URL)
	if loc := feishuLogin(carolWeb, "ou-3", "carol@example.com"); loc != "/build" {
		t.Fatalf("飞书登录失败：%s", loc)
	}
	if loc := dingTalkSignIn(t, carolWeb, "provider=dingtalk&bind=1", "staff-c"); loc != "/account?identity_bound=dingtalk" {
		t.Fatalf("carol 绑定钉钉失败：%s", loc)
	}
	code, body = carolWeb.do("DELETE", "/api/me/identities/feishu", nil, nil)
	expect(t, "主身份不能自己解除", code, 404, body)
	code, body = carolWeb.do("DELETE", "/api/me/identities/dingtalk", nil, nil)
	expect(t, "解除钉钉关联", code, 200, body)
	if loc := dingTalkSignIn(t, newClient(t, ts.URL), "provider=dingtalk&next=/build", "staff-c"); !strings.HasPrefix(loc, "/login?oidc_error=") {
		t.Fatalf("解除后钉钉身份不该还能登录：%s", loc)
	}
}
