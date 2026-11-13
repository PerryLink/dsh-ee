package api

import (
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

func accessWith(super bool, perms ...string) *rbac.Access {
	a := &rbac.Access{Super: super, Perms: map[string]bool{}}
	for _, p := range perms {
		a.Perms[p] = true
	}
	return a
}

// 改部门只允许权限严格更大的管理员调整下级；自己、同级、上级都不行。
func TestCanChangeDepartment(t *testing.T) {
	hr := accessWith(false, rbac.UsersManage)
	lead := accessWith(false, rbac.UsersManage, rbac.UsersView, rbac.AuditView)
	other := accessWith(false, rbac.UsersView)
	none := accessWith(false)
	super := accessWith(true)

	cases := []struct {
		name   string
		actor  *rbac.Access
		target *rbac.Access
		want   bool
	}{
		{"超管可以改任何人的部门", super, lead, true},
		{"超管之间也不受限", super, accessWith(true), true},
		{"下级（无权限）", hr, none, true},
		{"下级（权限是自己的真子集）", lead, hr, true},
		{"同级（权限集合相同）", hr, accessWith(false, rbac.UsersManage), false},
		{"上级（权限更多）", hr, lead, false},
		{"目标有自己没有的权限", hr, other, false},
		{"自己（权限集合相同）", hr, hr, false},
		{"目标不是超管但自己也不是超管且目标集更大", hr, super, false},
	}
	for _, c := range cases {
		if got := canChangeDepartment(c.actor, c.target); got != c.want {
			t.Errorf("%s：canChangeDepartment = %v，期望 %v", c.name, got, c.want)
		}
	}
}
