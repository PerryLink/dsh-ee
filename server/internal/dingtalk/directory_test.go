package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type rewrite struct{ target *url.URL }

func (tr rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme, r.URL.Host = tr.target.Scheme, tr.target.Host
	return http.DefaultTransport.RoundTrip(r)
}

func TestDirectoryPagesDepartmentsAndConfirmsLeavers(t *testing.T) {
	tokens := 0
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		tokens++
		fmt.Fprint(w, `{"accessToken":"app-token","expireIn":7200}`)
	})
	body := func(r *http.Request) map[string]any {
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		return in
	}
	mux.HandleFunc("POST /topapi/v2/department/listsub", func(w http.ResponseWriter, r *http.Request) {
		switch body(r)["dept_id"].(float64) {
		case 1:
			fmt.Fprint(w, `{"errcode":0,"result":[{"dept_id":10,"name":"研发部","parent_id":1},{"dept_id":20,"name":"销售部","parent_id":1}]}`)
		case 10:
			// 钉钉返回了已经见过的部门也不能死循环
			fmt.Fprint(w, `{"errcode":0,"result":[{"dept_id":11,"name":"平台组","parent_id":10},{"dept_id":10,"name":"研发部"}]}`)
		default:
			fmt.Fprint(w, `{"errcode":0,"result":[]}`)
		}
	})
	mux.HandleFunc("POST /topapi/v2/user/list", func(w http.ResponseWriter, r *http.Request) {
		in := body(r)
		if r.URL.Query().Get("access_token") != "app-token" || in["size"].(float64) > 100 {
			fmt.Fprint(w, `{"errcode":40014,"errmsg":"bad token"}`)
			return
		}
		switch fmt.Sprint(in["dept_id"], "/", in["cursor"]) {
		case "10/0":
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":true,"next_cursor":1,"list":[{"userid":"u2","name":"吴安","mobile":"138 0000 0002","state_code":"86","org_email":"a@corp.example","title":"工程师","dept_id_list":[10],"extension":"{\"cost_center\":\"CC-1\"}"}]}}`)
		case "10/1":
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":false,"list":[{"userid":"u1","name":"柏博","dept_id_list":[10,11]}]}}`)
		case "11/0":
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":false,"list":[{"userid":"u1","name":"柏博","dept_id_list":[10,11]}]}}`)
		case "20/0":
			fmt.Fprint(w, `{"errcode":60011,"errmsg":"没有调用该接口的权限: app-token"}`)
		default:
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":false,"list":[]}}`)
		}
	})
	mux.HandleFunc("POST /topapi/v2/user/get", func(w http.ResponseWriter, r *http.Request) {
		switch body(r)["userid"] {
		case "left":
			fmt.Fprint(w, `{"errcode":60121,"errmsg":"找不到该用户"}`)
		case "hidden":
			fmt.Fprint(w, `{"errcode":50002,"errmsg":"请求的员工userid不在授权范围内"}`)
		case "broken":
			fmt.Fprint(w, `{"errcode":-1,"errmsg":"系统繁忙"}`)
		default:
			fmt.Fprint(w, `{"errcode":0,"result":{"userid":"u2","mobile":"13800000002","state_code":"86"}}`)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	c := &Client{HTTP: &http.Client{Transport: rewrite{target}}, ID: "app", Secret: "app-secret"}
	ctx := context.Background()

	// 销售部那一页报错：整份目录失败，不能拿半份去更新或禁用
	if _, err := c.Directory(ctx); err == nil || !strings.Contains(err.Error(), "60011") || strings.Contains(err.Error(), "app-token") {
		t.Fatalf("一页出错应整份失败，且不带 token：%v", err)
	}
	var apiErr *APIError
	if _, err := c.Directory(ctx); !errors.As(err, &apiErr) || apiErr.Code != CodeNoPermission {
		t.Fatalf("应保留钉钉的错误码：%v", err)
	}
	for id, want := range map[string]UserState{"left": UserGone, "hidden": UserOutOfScope, "u2": UserActive} {
		if got, err := c.UserState(ctx, id); err != nil || got != want {
			t.Fatalf("%s 的状态应是 %d：%d %v", id, want, got, err)
		}
	}
	// 说不清的错误不能当成离职
	if got, err := c.UserState(ctx, "broken"); err == nil || got == UserGone {
		t.Fatalf("未知错误应原样返回：%d %v", got, err)
	}
	if _, err := c.Lookup(ctx, "wrong-user"); err == nil {
		t.Fatal("成员详情返回另一人的 userId 时不能用来关联身份")
	}
	if tokens != 1 {
		t.Fatalf("应用 token 应缓存复用：取了 %d 次", tokens)
	}
}

func TestDirectoryRejectsNonAdvancingCursor(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"accessToken":"app-token","expireIn":7200}`)
	})
	mux.HandleFunc("POST /topapi/v2/user/list", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errcode":0,"result":{"has_more":true,"next_cursor":0,"list":[{"userid":"u1"}]}}`)
	})
	mux.HandleFunc("POST /topapi/v2/department/listsub", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"errcode":0,"result":[]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	c := &Client{HTTP: &http.Client{Transport: rewrite{target}}, ID: "app", Secret: "app-secret"}
	if dir, err := c.Directory(context.Background()); dir != nil || err == nil || !strings.Contains(err.Error(), "pagination cursor") {
		t.Fatalf("游标不前进时不能返回部分通讯录：%+v %v", dir, err)
	}
}

func TestDirectoryWalksTheWholeOrganization(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"accessToken":"app-token","expireIn":7200}`)
	})
	mux.HandleFunc("POST /topapi/v2/department/listsub", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]float64
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in["dept_id"] == 1 {
			fmt.Fprint(w, `{"errcode":0,"result":[{"dept_id":10,"name":"研发部","parent_id":1}]}`)
			return
		}
		fmt.Fprint(w, `{"errcode":0,"result":[]}`)
	})
	mux.HandleFunc("POST /topapi/v2/user/list", func(w http.ResponseWriter, r *http.Request) {
		var in map[string]float64
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch {
		case in["dept_id"] == 1:
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":false,"list":[{"userid":"boss","name":"老板","dept_id_list":[1]}]}}`)
		case in["cursor"] == 0:
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":true,"next_cursor":7,"list":[{"userid":"u2","name":"吴安","mobile":"138 0000 0002","state_code":"86","org_email":"a@corp.example","title":"工程师","dept_id_list":[10],"extension":"{\"cost_center\":\"CC-1\"}"}]}}`)
		case in["cursor"] == 7:
			fmt.Fprint(w, `{"errcode":0,"result":{"has_more":false,"list":[{"userid":"boss","name":"老板"},{"userid":"u1","name":"柏博","mobile":"13800000001","dept_id_list":[10]}]}}`)
		default:
			fmt.Fprint(w, `{"errcode":40035,"errmsg":"bad cursor"}`)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	target, _ := url.Parse(srv.URL)
	c := &Client{HTTP: &http.Client{Transport: rewrite{target}}, ID: "app", Secret: "app-secret"}
	dir, err := c.Directory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(dir.Users) != 3 || dir.Users[0].UserID != "boss" || dir.Users[1].UserID != "u1" || dir.Users[2].UserID != "u2" {
		t.Fatalf("应翻页取全、去重并按 userId 排序：%+v", dir.Users)
	}
	if dir.Departments[10] != "研发部" || dir.Departments[RootDepartment] != "" {
		t.Fatalf("部门不对：%v", dir.Departments)
	}
	u2 := dir.Users[2]
	if u2.VerifiedMobile() != "+8613800000002" {
		t.Fatalf("手机号应带区号归一：%q", u2.VerifiedMobile())
	}
	// 没有区号的手机号不算核实过的
	if got := dir.Users[1].VerifiedMobile(); got != "" {
		t.Fatalf("缺区号不该猜：%q", got)
	}
	attrs := u2.Attrs()
	if attrs["title"][0] != "工程师" || attrs["cost_center"][0] != "CC-1" {
		t.Fatalf("同步字段的属性不对：%v", attrs)
	}
}
