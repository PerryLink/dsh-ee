package api

// 老 /api/* 错误响应的稳定机器码（P4 五语化基础设施）。
//
// 错误信封见 server.go 的 apiError：
//
//	{"error": "<中文默认文案>", "code": "<domain>.<reason>", "params": {...}}
//
// 约定：
//   - error 永远是给人看的中文默认文案，与加 code 之前逐字一致；老客户端只读它，行为不变。
//   - code 是稳定的机器码，命名 <domain>.<reason>，只用小写字母、数字和下划线；
//     已有 code 的含义不再修改，改语义就换新 code。
//   - params 是给前端多语言文案插值的参数（如 {"minutes": 15}），键名与译文里的
//     {{占位符}} 一致；没有参数时整个字段不出现。
//   - 只给「用户能看到、语义稳定」的拒绝加 code；内部 500、审计失败等保持只写 error。
//   - 前端译文登记在 web/src/i18n/{zh,en}/api-errors.ts（命名空间 apiErrors，键路径就是
//     code 原文，点号即层级）。查不到译文时前端回退服务端 error 中文，所以语言包可以逐步补：
//     es/pt/hi 等由语言包 PR 合并后按同一份清单补齐。
//
// 当前已迁移的领域：登录与认证、登录限流、设备码登录、用户与账号管理、角色、权限点、
// 菜单、系统设置与管理约束、额度与 API key、访问申请审批、令牌管理、管理员模式、
// 初始化与审核（admin.go / rbac.go / quota.go / settings.go / admin_policy.go / access.go /
// tokens.go / admin_mode.go / admin_setup.go 里的用户可见拒绝）。
//
// 新增一个 code 的流程：
//  1. 这里加常量 + 注释（语义、状态码、params）；
//  2. 把对应调用点的 writeErr 换成 writeErrCode（见 server.go），error 文案原样保留；
//  3. 在 web 的 zh/en 两份 api-errors.ts 里加同一个键路径。
const (
	codeReleaseInvalid          = "release.invalid"
	codeReleaseBusy             = "release.busy"
	codeReleaseChecksum         = "release.checksum"
	codeReleaseChecksumFormat   = "release.checksum_format"
	codeReleaseChecksumMismatch = "release.checksum_mismatch"
	codeReleaseBody             = "release.body"
	codeReleaseImported         = "release.imported"
	// codeUpstreamAuthFailed：上游 npm 仓库返回 401/403，502。params 无。
	codeUpstreamAuthFailed        = "upstream.auth_failed"
	codeIdentityEntranceDisabled  = "identity.entrance_disabled"
	codeIdentitySessionChanged    = "identity.session_changed"
	codeIdentityProviderInvalid   = "identity.provider_invalid"
	codeIdentityAccountIneligible = "identity.account_ineligible"
	codeIdentityHomeProvider      = "identity.home_provider"
	codeIdentityHomeElsewhere     = "identity.home_elsewhere"
	codeIdentityLinkedElsewhere   = "identity.linked_elsewhere"
	codeIdentityAlreadyLinked     = "identity.already_linked"
	codeIdentityUnlinkMissing     = "identity.unlink_missing"

	codeFeishuChatDisabled   = "feishu_chat.disabled"
	codeFeishuChatEmpty      = "feishu_chat.empty"
	codeFeishuChatMissing    = "feishu_chat.missing"
	codeFeishuChatSend       = "feishu_chat.send"
	codeDingTalkChatDisabled = "dingtalk_chat.disabled"
	codeDingTalkChatUnbound  = "dingtalk_chat.unbound"
	codeDingTalkChatEmpty    = "dingtalk_chat.empty"
	codeDingTalkChatMissing  = "dingtalk_chat.missing"
	codeDingTalkChatSend     = "dingtalk_chat.send"

	// ---- 员工网页对话：server/internal/api/runtime_chat.go ----

	// codeRuntimeChatBotUnavailable：取或建线程、读机器人资料时机器人不存在、不属于本人或未开启网页对话，404。params 无。
	codeRuntimeChatBotUnavailable = "runtime_chat.bot_unavailable"
	// codeRuntimeChatBotDisabled：发消息时对话绑定的机器人或当前账号已不满足对话条件，409；历史仍可读。params 无。
	codeRuntimeChatBotDisabled = "runtime_chat.bot_disabled"
	// codeRuntimeChatNotFound：对话不存在或不属于本人，404。params 无。
	codeRuntimeChatNotFound = "runtime_chat.not_found"
	// codeRuntimeChatInvalidMessage：消息为空、超长或含非法字符，400。params：max（最大字节数）。
	codeRuntimeChatInvalidMessage = "runtime_chat.invalid_message"
	// codeRuntimeChatInvalidKey：幂等键缺失、超长或含非可见 ASCII 字符，400。params：max（最大字符数）。
	codeRuntimeChatInvalidKey = "runtime_chat.invalid_key"
	// codeRuntimeChatBusy：这段对话已有一条消息在排队或运行，409。params 无。
	codeRuntimeChatBusy = "runtime_chat.busy"
	// codeRuntimeChatIdempotencyConflict：同一个幂等键重放了不同的消息内容，409。params 无。
	codeRuntimeChatIdempotencyConflict = "runtime_chat.idempotency_conflict"
	// codeRuntimeChatQueueFull：员工排队或运行中的云任务已达上限，429。params 无。
	codeRuntimeChatQueueFull = "runtime_chat.queue_full"

	// ---- 常驻云电脑（员工电脑 / 团队电脑）：server/internal/api/runtime_computer.go ----

	// codeRuntimeComputerBusy：电脑正在跑任务或正在被另一次操作改动，手动休眠 / 重置 / 回收被拒，409。params 无。
	codeRuntimeComputerBusy = "runtime_computer.busy"
	// codeRuntimeComputerUnsupported：当前运行时不支持常驻电脑（provider 不支持休眠 / 唤醒或已关闭常驻），每轮都是新电脑，409。params 无。
	codeRuntimeComputerUnsupported = "runtime_computer.unsupported"
	// codeRuntimeComputerProviderFailed：沙箱服务执行休眠 / 删除失败，502。params 无。
	codeRuntimeComputerProviderFailed = "runtime_computer.provider_failed"
	// codeRuntimeComputerNotFound：管理员按电脑 ID 休眠 / 回收时没有这台电脑，404。params 无。
	codeRuntimeComputerNotFound = "runtime_computer.not_found"

	// ---- 登录与认证：server/internal/api/auth.go ----

	// codeAuthLoginRequired：没有网页会话或嵌入令牌，401。params 无。
	codeAuthLoginRequired = "auth.login_required"
	// codeAuthSessionExpired：会话不存在或已过期，401。params 无。
	codeAuthSessionExpired = "auth.session_expired"
	// codeAuthBadCredentials：用户名或密码错误，401。params 无。
	// 账号被禁用、账号类型不是人、用户不存在都归到这里，不向客户端区分（避免枚举账号）。
	codeAuthBadCredentials = "auth.bad_credentials"
	// codeAuthNotProvisioned：企业目录账号还没有开通 dsh-ee（或已被目录禁用），403。params 无。
	codeAuthNotProvisioned = "auth.not_provisioned"
	// codeAuthDirectoryUnavailable：企业目录（LDAP / OIDC）暂时无法验证，502。params 无。
	codeAuthDirectoryUnavailable = "auth.directory_unavailable"
	// codeAuthForbidden：当前角色没有该操作的权限点，403。params 无。
	codeAuthForbidden = "auth.forbidden"
	// codeAuthBadOldPassword：修改密码时原密码不正确，400。params 无。
	codeAuthBadOldPassword = "auth.bad_old_password"
	// codeAuthPasswordManagedExternally：密码由企业目录或统一登录管理，不能在本站修改，400。params 无。
	codeAuthPasswordManagedExternally = "auth.password_managed_externally"
	// codeAuthWeakPassword：新密码短于最小长度，400。params：min（最小字符数）。
	codeAuthWeakPassword = "auth.weak_password"
	// codeAuthPasswordTooLong：新密码超过 bcrypt 能处理的最大字节数，400。params：max（最大字节数）。
	codeAuthPasswordTooLong = "auth.password_too_long"
	// codeAuthInitDone：首次初始化已完成（或没开放网页初始化），404。params 无。
	codeAuthInitDone = "auth.init_done"
	// codeAuthInitLocked：初始化码错误次数过多已作废，429。params 无。
	codeAuthInitLocked = "auth.init_locked"
	// codeAuthInitBadCode：初始化码不对，403。params 无。
	codeAuthInitBadCode = "auth.init_bad_code"
	// codeAuthBadUsername：用户名格式不对，400。params 无。
	codeAuthBadUsername = "auth.bad_username"

	// ---- 模型上游：server/internal/api/llm.go（首次配置向导会用到） ----

	// codeLLMBaseURLInvalid：base_url 不是 http(s) 地址，400。params 无。
	codeLLMBaseURLInvalid = "llm.base_url_invalid"
	// codeLLMNameKeyRequired：name 为空或过长、api_key 为空，400。params 无。
	codeLLMNameKeyRequired = "llm.name_key_required"
	// codeLLMVendorInvalid：vendor 含非法字符，400。params 无。
	codeLLMVendorInvalid = "llm.vendor_invalid"
	// codeLLMModelInvalid：模型名不合法，400。params：model。
	codeLLMModelInvalid = "llm.model_invalid"
	// codeLLMNameExists：上游名称已存在，409。params 无。
	codeLLMNameExists = "llm.name_exists"

	// ---- 登录限流：server/internal/api/auth.go、login_ratelimit.go ----

	// codeAuthTooManyAttempts：同一用户名连续失败达到上限后被锁定，429。params：minutes（锁定分钟数）。
	codeAuthTooManyAttempts = "auth.too_many_attempts"
	// codeAuthIPRateLimited：同一出口 IP 的登录失败次数达到窗口上限，429。
	// 响应同时带 Retry-After 头；params：retry_after（距窗口重置的秒数，与 Retry-After 一致）。
	codeAuthIPRateLimited = "auth.ip_rate_limited"

	// ---- 设备码登录：server/internal/api/device.go ----

	// codeDeviceScopeUnsupported：申请的 scope 不在允许列表里，400。params 无。
	codeDeviceScopeUnsupported = "device.scope_unsupported"
	// codeDeviceSetupCodeExpired：发起设备登录时带的接入码不存在或已过期，400。params 无。
	codeDeviceSetupCodeExpired = "device.setup_code_expired"
	// codeDeviceCodeNotFound：确认码（user_code）不存在或已过期，404。params 无。
	codeDeviceCodeNotFound = "device.code_not_found"
	// codeDeviceCodeNotPending：批准时确认码不存在、已被处理或已过期，400。params 无。
	codeDeviceCodeNotPending = "device.code_not_pending"
	// codeDeviceTooManyAttempts：发起设备码或轮询令牌太频繁被按 IP 限流，429。params 无。
	// 该响应的 error 仍是 RFC 8628 的 rate_limited（见 writeDeviceErr）。
	codeDeviceTooManyAttempts = "device.too_many_attempts"
	// codeDeviceCodeExpired：设备码过期或已被领取过，400。error 仍是 expired_token。
	codeDeviceCodeExpired = "device.code_expired"
	// codeDeviceCodeDenied：用户在网页上拒绝了这次设备登录，400。error 仍是 access_denied。
	codeDeviceCodeDenied = "device.code_denied"
	// codeDevicePending：设备码还没被确认，400。error 仍是 authorization_pending。
	codeDevicePending = "device.pending"
	// codeDeviceSlowDown：轮询间隔小于服务端要求，400。error 仍是 slow_down。
	codeDeviceSlowDown = "device.slow_down"

	// ---- 用户与账号管理：server/internal/api/admin.go、rbac.go、quota.go ----

	// codeUserInvalidUsername：新建账号的用户名不符合命名规则，400。params 无。
	codeUserInvalidUsername = "user.invalid_username"
	// codeUserNotFound：要操作的用户不存在，404。params 无。
	codeUserNotFound = "user.not_found"
	// codeUserExists：新建账号时用户名已被占用，409。params 无。
	codeUserExists = "user.exists"
	// codeUserInvalidAccountType：account_type 不是 human / bot，400。params 无。
	codeUserInvalidAccountType = "user.invalid_account_type"
	// codeUserBotCredentialsForbidden：机器人账号不能带密码或 LDAP / OIDC 登录方式，400。params 无。
	codeUserBotCredentialsForbidden = "user.bot_credentials_forbidden"
	// codeUserInvalidAuthSource：auth_source 不是 local / ldap / oidc，400。params 无。
	codeUserInvalidAuthSource = "user.invalid_auth_source"
	// codeUserExternalPasswordForbidden：建 LDAP / OIDC 账号时不能同时设本地密码，400。params 无。
	codeUserExternalPasswordForbidden = "user.external_password_forbidden"
	// codeUserPasswordManagedExternally：LDAP / OIDC 账号的密码由目录管理，不能在这里重置，400。params 无。
	codeUserPasswordManagedExternally = "user.password_managed_externally"
	// codeUserInvalidQuota：新建账号时 monthly_token_limit 为负数，400。params 无。
	codeUserInvalidQuota = "user.invalid_quota"
	// codeUserCannotDisableSelf：管理员不能停用自己，400。params 无。
	codeUserCannotDisableSelf = "user.cannot_disable_self"
	// codeUserCannotUnbindSelf：管理员不能解除自己的身份绑定，400。params 无。
	codeUserCannotUnbindSelf = "user.cannot_unbind_self"
	// codeUserOutrankedTarget：不能停用 / 重置密码 / 解绑权限比自己大的用户，403。params 无。
	codeUserOutrankedTarget = "user.outranked_target"
	// codeUserOutrankedRoles：不能改权限比自己大的用户的角色，403。params 无。
	codeUserOutrankedRoles = "user.outranked_roles"
	// codeUserDepartmentChangeForbidden：不能改自己、同级或权限比自己大的用户的部门，403。params 无。
	codeUserDepartmentChangeForbidden = "user.department_change_forbidden"
	// codeUserLastSuperAdminDisable：这是最后一个启用中的超级管理员，不能停用，400。params 无。
	codeUserLastSuperAdminDisable = "user.last_super_admin_disable"
	// codeUserLastSuperAdminUnassign：这是最后一个启用中的超级管理员，不能取消，400。params 无。
	codeUserLastSuperAdminUnassign = "user.last_super_admin_unassign"
	// codeUserNoIdentityBinding：只有 OIDC 账号有身份绑定可以解除，400。params 无。
	codeUserNoIdentityBinding = "user.no_identity_binding"
	// codeUserRolesAssignForbidden：只有「管理用户」权限时不能显式分配角色，403。params 无。
	codeUserRolesAssignForbidden = "user.roles_assign_forbidden"
	// codeUserQuotaAssignForbidden：只有「管理用户」权限时不能在建号时设置额度，403。params 无。
	codeUserQuotaAssignForbidden = "user.quota_assign_forbidden"
	// codeUserRolesSelfGrant：开了「不能给自己授权」，管理员不能改自己的角色，403。params 无。
	codeUserRolesSelfGrant = "user.roles_self_grant"
	// codeUserRolesExclusive：要给的角色里有一组互斥角色，403。params：roles（「a 和 b」）。
	codeUserRolesExclusive = "user.roles_exclusive"
	// codeUserRolesDualRequired：开了双人批准（roles.assign），建账号时不能直接带管理角色，403。params 无。
	codeUserRolesDualRequired = "user.roles_dual_required"

	// ---- 角色：server/internal/api/rbac.go ----

	// codeRoleInvalidCode：角色代码不符合命名规则，400。params 无。
	codeRoleInvalidCode = "role.invalid_code"
	// codeRoleInvalidName：角色名称为空或太长，400。params 无。
	codeRoleInvalidName = "role.invalid_name"
	// codeRoleExists：新建角色时角色代码已存在，409。params 无。
	codeRoleExists = "role.exists"
	// codeRoleNotFound：要操作的角色不存在，404。params 无。
	codeRoleNotFound = "role.not_found"
	// codeRoleUnknown：要授予的角色不存在，403。params：id（角色 id）。
	codeRoleUnknown = "role.unknown"
	// codeRoleSuperModifyForbidden：只有超级管理员能修改超级管理员角色，403。params 无。
	codeRoleSuperModifyForbidden = "role.super_modify_forbidden"
	// codeRoleOutrankedUpdate：这个角色带有当前管理员没有的权限，不能修改，403。params 无。
	codeRoleOutrankedUpdate = "role.outranked_update"
	// codeRoleOutrankedDelete：这个角色带有当前管理员没有的权限，不能删除，403。params 无。
	codeRoleOutrankedDelete = "role.outranked_delete"
	// codeRoleSuperPermissionsImplicit：超级管理员自动拥有全部权限，不需要单独配置，400。params 无。
	codeRoleSuperPermissionsImplicit = "role.super_permissions_implicit"
	// codeRoleInUse：角色还在被使用（内置角色等），不能删除，409。params 无。
	codeRoleInUse = "role.in_use"
	// codeRoleGrantSuperForbidden：只有超级管理员能授予超级管理员角色，403。params：role（角色名）。
	codeRoleGrantSuperForbidden = "role.grant_super_forbidden"
	// codeRoleGrantOverreach：不能授予超出自己权限的角色，403。params：role（角色名）。
	codeRoleGrantOverreach = "role.grant_overreach"
	// codeRoleAdminPermsDualRequired：开了双人批准（roles.assign），给已有人持有（或被 LDAP / OIDC 映射）的角色
	// 加管理权限点要通过配置变更提交，409。params：role（角色名）。
	codeRoleAdminPermsDualRequired = "role.admin_perms_dual_required"

	// ---- 目录组 → 角色映射：server/internal/api/ldap.go、oidc.go ----

	// codeRoleMappingDualRequired：开了双人批准（roles.assign），不能新加映射到管理角色的组映射，409。params：role（角色名）。
	codeRoleMappingDualRequired = "role_mapping.dual_required"
	// codeRoleMappingExclusive：同一个组映射到了一组互斥角色，400。params：group（组）、roles（「a 和 b」）。
	codeRoleMappingExclusive = "role_mapping.exclusive"

	// ---- 权限点：server/internal/api/rbac.go ----

	// codePermInvalidCode：权限点代码不符合命名规则，400。params 无。
	codePermInvalidCode = "perm.invalid_code"
	// codePermInvalidName：权限点名称为空或太长，400。params 无。
	codePermInvalidName = "perm.invalid_name"
	// codePermExists：新建权限点时代码已存在，409。params 无。
	codePermExists = "perm.exists"
	// codePermNotFound：要操作的权限点不存在，404。params 无。
	codePermNotFound = "perm.not_found"
	// codePermUnknown：引用的权限点不存在，400。params：code（权限点代码）。
	codePermUnknown = "perm.unknown"
	// codePermInUse：权限点还在被菜单或内置逻辑使用，不能删除，409。params 无。
	codePermInUse = "perm.in_use"
	// codePermGrantOverreach：不能把当前管理员没有的权限授给角色，403。params 无。
	codePermGrantOverreach = "perm.grant_overreach"
	// codePermDenied：当前管理员没有这项管理权限，403。params 无。
	codePermDenied = "perm.denied"

	// ---- 菜单：server/internal/api/rbac.go ----

	// codeMenuNotFound：要操作的菜单不存在，404。params 无。
	codeMenuNotFound = "menu.not_found"
	// codeMenuUnknown：排序时引用的菜单不存在，400。params：id（菜单 id）。
	codeMenuUnknown = "menu.unknown"
	// codeMenuInvalidTitle：菜单标题为空或太长，400。params 无。
	codeMenuInvalidTitle = "menu.invalid_title"
	// codeMenuInvalidIcon：图标名不合法，400。params 无。
	codeMenuInvalidIcon = "menu.invalid_icon"
	// codeMenuInvalidKind：kind 不是 group / page / link，400。params 无。
	codeMenuInvalidKind = "menu.invalid_kind"
	// codeMenuGroupNotTop：分组只能放在顶层，400。params 无。
	codeMenuGroupNotTop = "menu.group_not_top"
	// codeMenuGroupHasChildren：有子项的菜单不能改成分组，400。params 无。
	codeMenuGroupHasChildren = "menu.group_has_children"
	// codeMenuInvalidPath：页面路由不合法，400。params 无。
	codeMenuInvalidPath = "menu.invalid_path"
	// codeMenuReservedPath：用了系统保留的路由，400。params：path（被占用的路由）。
	codeMenuReservedPath = "menu.reserved_path"
	// codeMenuMissingPageKey：没有选择页面组件（page_key），400。params 无。
	codeMenuMissingPageKey = "menu.missing_page_key"
	// codeMenuPathInUse：路由已被另一个菜单使用，400。params：path（路由）、title（占用它的菜单标题）。
	codeMenuPathInUse = "menu.path_in_use"
	// codeMenuInvalidLink：外部链接不是 http(s) 地址，400。params 无。
	codeMenuInvalidLink = "menu.invalid_link"
	// codeMenuInvalidParent：上级菜单不存在或不是分组，400。params 无。
	codeMenuInvalidParent = "menu.invalid_parent"
	// codeMenuInvalidGroupChild：只能把页面或链接放进分组，400。params 无。
	codeMenuInvalidGroupChild = "menu.invalid_group_child"
	// codeMenuInUse：分组下还有菜单，不能删除，409。params 无。
	codeMenuInUse = "menu.in_use"

	// ---- 系统设置与管理约束：server/internal/api/settings.go、admin_policy.go ----

	// codeSettingsInvalidValue：保存的系统设置有一项不合法，400。params 无
	//（具体哪一项仍在 error 的中文文案里）。
	codeSettingsInvalidValue = "settings.invalid_value"
	// codeSettingsForbidden：当前管理员没有改动这一段设置的权限，403。params：field（设置段的 json 名）。
	codeSettingsForbidden = "settings.forbidden"
	// codePolicyDualRequired：开了双人批准（config），管理约束要通过配置变更提交，409。params 无。
	codePolicyDualRequired = "policy.dual_required"
	// codePolicyPresetDualRequired：开了双人批准（config），预设要通过配置变更提交，409。params 无。
	codePolicyPresetDualRequired = "policy.preset_dual_required"
	// codePolicyInvalidPreset：没有这个管理约束预设，400。params 无。
	codePolicyInvalidPreset = "policy.invalid_preset"
	// codePolicyPresetOverreach：建预设角色要求当前管理员自己有它的全部权限，403。params：role（角色名）。
	codePolicyPresetOverreach = "policy.preset_overreach"

	// ---- 额度与 API key：server/internal/api/quota.go、admin.go ----

	// codeQuotaNothingToUpdate：分配额度时一个字段都没传，400。params 无。
	codeQuotaNothingToUpdate = "quota.nothing_to_update"
	// codeQuotaInvalidLimit：monthly_token_limit 为负数，400。params 无。
	codeQuotaInvalidLimit = "quota.invalid_limit"
	// codeQuotaInvalidExpiry：quota_expires_at 不是将来的时间，400。params 无。
	codeQuotaInvalidExpiry = "quota.invalid_expiry"
	// codeKeyInvalidName：key 名称为空，400。params 无。
	codeKeyInvalidName = "key.invalid_name"
	// codeKeyNameTooLong：key 名称超过 128 个字符，400。params 无。
	codeKeyNameTooLong = "key.name_too_long"
	// codeKeyInvalidExpiry：expires_at 不是将来的时间，400。params 无。
	codeKeyInvalidExpiry = "key.invalid_expiry"
	// codeKeyLimitReached：一个人保留的有效 key 数达到上限，400。params：max（上限个数）。
	codeKeyLimitReached = "key.limit_reached"
	// codeKeyNotFound：key 不存在，404。params 无。
	codeKeyNotFound = "key.not_found"
	// codeKeyRevoked：key 已经吊销，404。params 无。
	codeKeyRevoked = "key.revoked"
	// codeKeyNotFoundOrRevoked：代发 key 的吊销接口：key 不存在或已吊销，404。params 无。
	codeKeyNotFoundOrRevoked = "key.not_found_or_revoked"
	// codeKeyUserNotFound：代发 key 时指定的用户不存在，400。params 无。
	codeKeyUserNotFound = "key.user_not_found"
	// codeKeyUserDisabled：代发 key 时指定的用户已被停用，400。params 无。
	codeKeyUserDisabled = "key.user_disabled"

	// ---- 访问申请审批：server/internal/api/access.go ----

	// codeRequestPurposeRequired：申请访问时没写用途（或超过 500 字符），400。params 无。
	codeRequestPurposeRequired = "request.purpose_required"
	// codeRequestAlreadyGranted：申请人已经能用了，不需要申请，400。params：title（对象标题）。
	codeRequestAlreadyGranted = "request.already_granted"
	// codeRequestInvalidDecision：decision 不是 approve / reject，400。params 无。
	codeRequestInvalidDecision = "request.invalid_decision"
	// codeRequestNotFound：申请不存在或已经处理过，404。params 无。
	codeRequestNotFound = "request.not_found"
	// codeRequestNotApprover：当前用户不是这条申请的审批人，403。params 无。
	codeRequestNotApprover = "request.not_approver"
	// codeRequestNotPending：这条申请刚被别人处理了，409。params 无。
	codeRequestNotPending = "request.not_pending"

	// ---- 令牌管理：server/internal/api/tokens.go、admin_mode.go ----

	// codeTokenInvalidName：令牌名称为空或超过 128 个字符，400。params 无。
	codeTokenInvalidName = "token.invalid_name"
	// codeTokenInvalidClientKind：client_kind 不是 dsh / third_party，400。params 无。
	codeTokenInvalidClientKind = "token.invalid_client_kind"
	// codeTokenInvalidLimit：monthly_token_limit 为负数，400。params 无。
	codeTokenInvalidLimit = "token.invalid_limit"
	// codeTokenInvalidExpiry：expires_in_days 不在 0–max 之间，400。params：max（最大天数）。
	codeTokenInvalidExpiry = "token.invalid_expiry"
	// codeTokenInvalidScope：申请的 scope 超出账号权限，或包含暂不支持的 admin，400。params 无。
	codeTokenInvalidScope = "token.invalid_scope"
	// codeTokenManualDisabled：管理员已关闭员工手动生成令牌，403。params 无。
	codeTokenManualDisabled = "token.manual_disabled"
	// codeTokenInvalidID：令牌 id 格式不正确（不是 at:<id> / npm:<id> / key:<id>），400。params 无。
	codeTokenInvalidID = "token.invalid_id"
	// codeTokenNotFound：要操作的令牌不存在；不是自己的一律按不存在处理，404。params 无。
	codeTokenNotFound = "token.not_found"
	// codeTokenUnknown：批量吊销时引用的令牌不存在，400。params：id（请求里的原始 id，如 at:12）。
	codeTokenUnknown = "token.unknown"
	// codeTokenInvalidKind：令牌类型不是 at / npm / key，400。params 无。
	codeTokenInvalidKind = "token.invalid_kind"
	// codeTokenManageForbidden：当前管理员没有令牌管理权限，403。params 无。
	codeTokenManageForbidden = "token.manage_forbidden"
	// codeTokenUserNotFound：签发令牌时指定的账号不存在，400。params 无。
	codeTokenUserNotFound = "token.user_not_found"
	// codeTokenDepartmentScopeForbidden：只有本部门令牌管理权限，不能给别的部门账号签发令牌，403。params 无。
	codeTokenDepartmentScopeForbidden = "token.department_scope_forbidden"
	// codeTokenBulkIDInvalid：批量吊销只支持 at:<id> 形式，400。params 无。
	codeTokenBulkIDInvalid = "token.invalid_bulk_id"
	// codeTokenRevokeFilterRequired：批量吊销没给任何条件（令牌、账号、设备、部门），400。params 无。
	codeTokenRevokeFilterRequired = "token.revoke_filter_required"
	// codeTokenDepartmentRevokeForbidden：只有本部门令牌管理权限，不能吊销别的部门账号的令牌，403。params 无。
	codeTokenDepartmentRevokeForbidden = "token.department_revoke_forbidden"

	// ---- 管理员模式：server/internal/api/admin_mode.go ----

	// codeAdminModeWebOnly：开启管理模式只能由本人在网页上输入密码，403。params 无。
	codeAdminModeWebOnly = "admin_mode.web_only"
	// codeAdminModeInvalidDuration：管理模式只能开 1 小时或 8 小时，400。params 无。
	codeAdminModeInvalidDuration = "admin_mode.invalid_duration"
	// codeAdminModePermissionRequired：账号没有管理权限，不能开启管理模式，403。params 无。
	codeAdminModePermissionRequired = "admin_mode.permission_required"
	// codeAdminModeTooManyAttempts：开启管理模式时连续输错密码被锁定，429。params：minutes（锁定分钟数）。
	// 与登录共用失败限流，文案里的 15 分钟与 lockFor 一致。
	codeAdminModeTooManyAttempts = "admin_mode.too_many_attempts"
	// codeAdminModeBadPassword：开启管理模式时密码不对，403。params 无。
	// 不用 401：网页把任何 401 都当成会话过期而退出登录。
	codeAdminModeBadPassword = "admin_mode.bad_password"
	// codeAdminModeForbidden：账号没有管理权限（申请管理模式链接），403。params 无。
	codeAdminModeForbidden = "admin_mode.forbidden"

	// ---- 初始化与审核：server/internal/api/admin_setup.go ----

	// codeAdminSetupInvalidPackageName：要查看的插件包名不合法，400。params 无。
	codeAdminSetupInvalidPackageName = "admin_setup.invalid_package_name"
	// codeAdminSetupVersionNotFound：要查看的插件版本不存在，404。params 无。
	codeAdminSetupVersionNotFound = "admin_setup.version_not_found"
	// codeAdminSetupAssetNotFound：要查看的能力不存在，404。params 无。
	codeAdminSetupAssetNotFound = "admin_setup.asset_not_found"
	// codeAdminSetupReviewForbidden：当前管理员没有审核这类能力的权限，403。params 无。
	codeAdminSetupReviewForbidden = "admin_setup.review_forbidden"
)

// deviceErrCode 把设备码流程（RFC 8628）对外 error 字段里的协议码映射成前端用的
// <domain>.<reason>。error 字段本身保持不变，DSH 客户端仍按它分支；没有映射的协议码
// 只写 error，不加 code。
var deviceErrCode = map[string]string{
	"rate_limited":          codeDeviceTooManyAttempts,
	"expired_token":         codeDeviceCodeExpired,
	"access_denied":         codeDeviceCodeDenied,
	"authorization_pending": codeDevicePending,
	"slow_down":             codeDeviceSlowDown,
}
