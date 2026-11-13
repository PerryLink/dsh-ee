package api

import (
	"testing"

	"github.com/hikariming/dsh-ee/server/internal/rbac"
)

// 建号时显式设置 / 清空额度要有「分配额度」权限；不传额度不受影响。
func TestCanSetQuotaOnCreate(t *testing.T) {
	manage := &rbac.Access{Perms: map[string]bool{rbac.UsersManage: true}}
	quota := &rbac.Access{Perms: map[string]bool{rbac.UsersManage: true, rbac.QuotasManage: true}}
	super := &rbac.Access{Super: true, Perms: map[string]bool{}}
	limit := int64(1000)
	set := nullableInt64{Set: true, Value: &limit}
	explicitNull := nullableInt64{Set: true}
	absent := nullableInt64{}

	cases := []struct {
		name   string
		access *rbac.Access
		limit  nullableInt64
		want   bool
	}{
		{"只有用户管理权限、不传额度", manage, absent, true},
		{"只有用户管理权限、显式设置额度", manage, set, false},
		{"只有用户管理权限、显式清空额度", manage, explicitNull, false},
		{"有分配额度权限、显式设置额度", quota, set, true},
		{"有分配额度权限、显式清空额度", quota, explicitNull, true},
		{"超级管理员、显式清空额度", super, explicitNull, true},
	}
	for _, c := range cases {
		if got := canSetQuotaOnCreate(c.access, c.limit); got != c.want {
			t.Errorf("%s：canSetQuotaOnCreate = %v，期望 %v", c.name, got, c.want)
		}
	}
}
