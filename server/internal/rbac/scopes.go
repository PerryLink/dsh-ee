package rbac

const (
	ScopeModels          = "models"
	ScopeRegistryRead    = "registry:read"
	ScopeRegistryPublish = "registry:publish"
	ScopeHub             = "hub"
	ScopeConnections     = "connections"
	ScopeAgent           = "agent"
	ScopeAdmin           = "admin"
	// 配置令牌（docs/agent-dev.md §7）：导出权限配置、plan / test / explain / simulate、提交变更请求；批准不行
	ScopeConfig = "config"
)

var scopePermissions = map[string][]string{
	ScopeModels:          {LLMUse},
	ScopeRegistryRead:    {RegistryUse},
	ScopeRegistryPublish: {RegistryPublish},
	ScopeHub:             {PortalView},
	ScopeConnections:     {PortalView},
	ScopeAgent:           {PortalView, LLMUse, KeysSelf, RegistryUse, RegistryPublish},
	ScopeConfig:          {FieldsManage, RolesManage, UsersView},
}

func scopeAllows(scope, permission string) bool {
	if scope == ScopeAdmin {
		for _, employee := range []string{PortalView, LLMUse, KeysSelf, RegistryUse, RegistryPublish} {
			if permission == employee {
				return false
			}
		}
		return true
	}
	for _, code := range scopePermissions[scope] {
		if code == permission {
			return true
		}
	}
	return false
}

// ScopeFilter 在账号现有权限上再按令牌 scope 求交，令牌永远不能超过账号权限。
func ScopeFilter(access *Access, scopes []string) *Access {
	if access == nil {
		return &Access{Perms: map[string]bool{}}
	}
	out := &Access{RoleIDs: append([]int64(nil), access.RoleIDs...), RoleCodes: append([]string(nil), access.RoleCodes...), Perms: map[string]bool{}}
	for _, scope := range scopes {
		if scope == ScopeAdmin && access.Super {
			out.Super = true
		}
		for _, p := range Catalog {
			if scopeAllows(scope, p.Code) && access.Has(p.Code) {
				out.Perms[p.Code] = true
			}
		}
	}
	return out
}

// HasAdminPerms 判断账号是否有任何管理类权限点（超级管理员也算）。
// 只有这样的账号才能给令牌开启管理模式；管理模式开启时令牌才带 admin 范围。
func HasAdminPerms(access *Access) bool {
	if access == nil {
		return false
	}
	if access.Super {
		return true
	}
	for code, on := range access.Perms {
		if on && scopeAllows(ScopeAdmin, code) {
			return true
		}
	}
	return false
}

// IsAdminPerm 报告一个权限点是不是管理类的（员工自己用的那几个不算）。
func IsAdminPerm(code string) bool { return scopeAllows(ScopeAdmin, code) }
