package dingtalk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// 组织同步用的通讯录接口（企业内部应用，服务端 API）：
//   - POST /topapi/v2/department/listsub  取一个部门的直接子部门，根部门 dept_id=1
//   - POST /topapi/v2/user/list           分页取一个部门直属成员的详情（cursor / size ≤ 100）
//   - POST /topapi/v2/user/get            查一名成员
// 都用应用的 accessToken 放在 access_token 查询参数里，响应是 {errcode, errmsg, result}。

// RootDepartment 是钉钉通讯录的根部门。
const RootDepartment int64 = 1

// 查单个成员（topapi/v2/user/get）时用得上的错误码。
const (
	// CodeUserNotFound：找不到该用户（离职、被移出企业，或 userid 本来就不存在）
	CodeUserNotFound = 60121
	// CodeUserOutOfScope：请求的员工 userid 不在应用的通讯录授权范围内
	CodeUserOutOfScope = 50002
	// CodeDeptOutOfScope：请求的部门 id 不在应用的通讯录授权范围内
	CodeDeptOutOfScope = 50004
	// CodeNoPermission：应用没有调用这个接口的权限
	CodeNoPermission = 60011
)

// APIError 是钉钉 oapi 接口返回的业务错误（HTTP 200，errcode 非 0）。
type APIError struct {
	Step string
	Code int64
	Msg  string
}

func (e *APIError) Error() string { return fmt.Sprintf("%s: errcode %d %s", e.Step, e.Code, e.Msg) }

// User 是通讯录里的一名成员。
type User struct {
	UserID string `json:"userid"`
	Name   string `json:"name"`
	// 手机号是钉钉账号本身的登录号码（钉钉验证过），state_code 是国际区号
	Mobile    string `json:"mobile"`
	StateCode string `json:"state_code"`
	// email 是员工自己在资料里填的，没人验证过；org_email 是企业分配的邮箱
	Email     string  `json:"email"`
	OrgEmail  string  `json:"org_email"`
	JobNumber string  `json:"job_number"`
	Title     string  `json:"title"`
	WorkPlace string  `json:"work_place"`
	DeptIDs   []int64 `json:"dept_id_list"`
	// 企业自定义的扩展字段：一段 JSON 文本，键是字段名
	Extension string `json:"extension"`
}

// Attrs 是同步字段时能用的属性：title、job_number、work_place，以及扩展字段的名字。
func (u User) Attrs() map[string][]string {
	out := map[string][]string{}
	add := func(k, v string) {
		if k, v = strings.TrimSpace(k), strings.TrimSpace(v); k != "" && v != "" {
			out[k] = append(out[k], v)
		}
	}
	add("title", u.Title)
	add("job_number", u.JobNumber)
	add("work_place", u.WorkPlace)
	var ext map[string]any
	if u.Extension != "" && json.Unmarshal([]byte(u.Extension), &ext) == nil {
		for k, v := range ext {
			if s, ok := v.(string); ok {
				add(k, s)
			}
		}
	}
	return out
}

// VerifiedMobile 返回带国际区号的手机号（+8613800000000）；缺区号或号码时返回空，不去猜。
func (u User) VerifiedMobile() string {
	code, number := digits(u.StateCode), digits(u.Mobile)
	if code == "" || number == "" {
		return ""
	}
	return "+" + code + number
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}

// Directory 是一次完整拉到的组织：部门 id → 名字，以及全部成员。
type Directory struct {
	Departments map[int64]string
	Users       []User
}

func (c *Client) oapiCall(ctx context.Context, step, path string, in, result any) error {
	token, err := c.AppToken(ctx)
	if err != nil {
		return err
	}
	var env struct {
		Code   json.Number     `json:"errcode"`
		Msg    string          `json:"errmsg"`
		Result json.RawMessage `json:"result"`
	}
	endpoint := oapi + path + "?" + url.Values{"access_token": {token}}.Encode()
	if err := c.call(ctx, step, http.MethodPost, endpoint, "", in, &env); err != nil {
		return err
	}
	if code, err := env.Code.Int64(); err != nil || code != 0 {
		if err != nil {
			return fmt.Errorf("%s: invalid response", step)
		}
		return &APIError{Step: step, Code: code, Msg: clip(strings.ReplaceAll(env.Msg, token, "[redacted]"), 200)}
	}
	if err := json.Unmarshal(env.Result, result); err != nil {
		return fmt.Errorf("%s: invalid response", step)
	}
	return nil
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

type department struct {
	ID   int64  `json:"dept_id"`
	Name string `json:"name"`
}

// Directory 从根部门开始完整拉取当前企业。任何一页出错都直接失败，调用方不会拿半份目录去更新或禁用员工。
func (c *Client) Directory(ctx context.Context) (*Directory, error) {
	out := &Directory{Departments: map[int64]string{RootDepartment: ""}, Users: []User{}}
	queue := []int64{RootDepartment}
	seenUsers := map[string]bool{}
	for next := 0; next < len(queue); next++ {
		dept := queue[next]
		cursor := int64(0)
		for {
			var page struct {
				HasMore bool   `json:"has_more"`
				Next    int64  `json:"next_cursor"`
				List    []User `json:"list"`
			}
			if err := c.oapiCall(ctx, "DingTalk department members", "/topapi/v2/user/list",
				map[string]any{"dept_id": dept, "cursor": cursor, "size": 100}, &page); err != nil {
				return nil, err
			}
			for _, u := range page.List {
				if u.UserID == "" || seenUsers[u.UserID] {
					continue
				}
				seenUsers[u.UserID] = true
				out.Users = append(out.Users, u)
			}
			if !page.HasMore {
				break
			}
			if page.Next <= cursor {
				return nil, fmt.Errorf("DingTalk department members: invalid pagination cursor for department %d", dept)
			}
			cursor = page.Next
		}
		var children []department
		if err := c.oapiCall(ctx, "DingTalk departments", "/topapi/v2/department/listsub",
			map[string]any{"dept_id": dept}, &children); err != nil {
			return nil, err
		}
		for _, child := range children {
			if _, seen := out.Departments[child.ID]; child.ID <= 0 || seen {
				continue
			}
			out.Departments[child.ID] = child.Name
			queue = append(queue, child.ID)
		}
	}
	slices.SortStableFunc(out.Users, func(a, b User) int { return strings.Compare(a.UserID, b.UserID) })
	return out, nil
}

// Lookup 单独查一名成员。
func (c *Client) Lookup(ctx context.Context, userID string) (*User, error) {
	var u User
	if err := c.oapiCall(ctx, "DingTalk member lookup", "/topapi/v2/user/get", map[string]string{"userid": userID}, &u); err != nil {
		return nil, err
	}
	if u.UserID != userID || userID == "" {
		return nil, errors.New("DingTalk member lookup: response did not match requested staff ID")
	}
	return &u, nil
}

// UserState 是单独向钉钉确认一名成员时得到的结论。
type UserState int

const (
	// UserActive：还在企业里，只是这次没出现在拉到的目录里
	UserActive UserState = iota
	// UserGone：钉钉明确说找不到这个人（离职或被移出企业）
	UserGone
	// UserOutOfScope：应用的通讯录授权范围里没有这个人，看不到状态
	UserOutOfScope
)

// UserState 单独查一名成员。目录里没看到不等于离职：也可能是应用的通讯录授权范围只覆盖了
// 一部分员工。按目录禁用账号之前用它确认；其它错误原样返回，让整轮同步失败而不是误禁用。
func (c *Client) UserState(ctx context.Context, userID string) (UserState, error) {
	_, err := c.Lookup(ctx, userID)
	var apiErr *APIError
	switch {
	case err == nil:
		return UserActive, nil
	case errors.As(err, &apiErr) && apiErr.Code == CodeUserNotFound:
		return UserGone, nil
	case errors.As(err, &apiErr) && (apiErr.Code == CodeUserOutOfScope || apiErr.Code == CodeDeptOutOfScope || apiErr.Code == CodeNoPermission):
		return UserOutOfScope, nil
	}
	return UserActive, err
}
