# 令牌生命周期：类型、自省、续期与旧令牌收敛

> 状态：自省（introspection）、`client_kind`、旧令牌 scope 收敛已实现（`#132`）；刷新令牌（OAuth 风格轮换 + 复用检测）已实现，**默认关闭**，见 §3；管理员 Bearer 程序化签发（最小版）已实现，见 §6。
> 相关代码：`server/internal/api/introspect.go`、`server/internal/api/refresh.go`、`server/internal/api/principal.go`、`server/internal/api/tokens.go`、`server/internal/store/tokens.go`、`server/internal/store/tokens_refresh.go`、`server/internal/store/migrations/0035_token_refresh_tokens.sql`。

## 1. 令牌类型

| 类型 | 前缀 | 签发入口 | 有效范围 | `client_kind` |
|---|---|---|---|---|
| 接入令牌 `access` | `ee_at_` | 设备码登录、网页「我的令牌」、管理员签发 | 签发时的 scope，可带额度 / 模型限制 / 到期 | 按令牌属性：`dsh`（设备码）或 `third_party`（手工 / 管理员签发，可显式指定） |
| 旧 npm 令牌 | `ee_npm_` | 网页「npm 令牌」 | 固定 scope（见 §4） | 兼容为 `dsh` |
| 旧 API key | `sk-ee-` | 网页「我的 API key」/ 管理员代发 | 固定 scope（见 §4） | 兼容为 `dsh` |
| 刷新令牌 `refresh` | `ee_rt_` | 开启 `DSH_EE_REFRESH_TOKENS` 后由设备登录 / 手工 / 管理员签发一并下发 | 只用于 `POST /api/auth/refresh` 换新 access + refresh；一次性轮换，见 §3 | 跟随令牌族（`dsh` / `third_party`） |

`dsh` / `third_party` 影响服务端写确认：`dsh` 令牌沿用「DSH 自己有审批卡」的快捷语义，`third_party` 令牌的写操作始终要员工在网页上人工确认。判定只看令牌属性，不看请求头（`X-DshEE-Client` 不再参与）。

## 2. 令牌自省

```
POST /api/auth/introspect
```

两种调用方式：

1. **查自己**：`Authorization: Bearer <要查的令牌>`，请求体留空（或同时把同一枚令牌放进 body 的 `token` 字段）。
2. **管理员查任意令牌**：用网页会话（需要 `keys.manage`），或带 `keys.manage` 有效权限的令牌（例如开了管理模式的令牌），请求体：

```json
{ "token": "ee_at_…" }
```

响应（`application/json`）：

```json
{
  "active": true,
  "kind": "access",
  "token_id": "at:12",
  "legacy": false,
  "client_kind": "third_party",
  "client_name": "Claude Code",
  "prefix": "ee_at_ab12cd",
  "scopes": ["connections", "hub", "models", "registry:read"],
  "expires_at": "2026-12-01T00:00:00Z",
  "user": { "id": 7, "username": "alice", "department": "研发效能部", "account_type": "human" }
}
```

`active:false` 时另外带 `reason`，尽量给出可解释的原因（管理员查询时）：

| `reason` | 含义 |
|---|---|
| `expired` | 已过期（`expires_at` 仍会返回） |
| `revoked` | 已吊销 |
| `user_disabled` | 所属账号已停用 |
| `not_found` | 不认识这个值（不是合法前缀 / 已删除） |
| `inactive` | 其它不可用状态 |

约定与边界：

- **只返回元数据**：令牌哈希、明文、加密后的值一律不返回；`prefix` 是列表页本来就展示的前缀。
- **自省不算使用**：不更新 `last_used_at`、不触发设备令牌滑动续期。
- **权限**：普通用户只能查自己的令牌；查别人的令牌需要 `keys.manage`。网页会话 + 请求体是管理员用法，普通用户会拿到 403。
- **状态码**：调用方没登录 / Bearer 无效 → 401（错误体带 `code`：`token_invalid` / `token_expired` / `token_revoked`）；越权查别人的令牌 → 403；管理员在请求体里查一个已失效的令牌 → 200 + `active:false`（不是 401，方便客户端决定重新登录）；网页会话没带 `token` → 400。
- `kind` 为 `npm` / `key` 时 `legacy:true`；旧 npm 令牌没有可展示的 id，`token_id` 为空。
- scope 是「有效 scope」：管理模式没开或到期时，`admin` 不在列表里。

## 3. 续期（refresh）：已实现，默认关闭

### 3.1 现状与开关

设备码签发的令牌仍有**滑动续期**（`sliding_days`）：有效期内使用会把到期时间续到「现在 + N 天」；`DSH_EE…` 部署参数里可配天数（管理页「连接」设置里的 `device_token_days`）。手工 / 管理员签发的令牌按 `expires_in_days` 固定到期或长期有效。

在此之外新增 OAuth 风格的 access + refresh 双令牌，**默认关闭**：

```
DSH_EE_REFRESH_TOKENS=1
```

- **关闭时（默认）**：`POST /api/auth/refresh` 返回 404；设备登录、手工 / 管理员签发与之前完全一样，响应里不会多出字段。旧客户端零行为变化。
- **开启后**：设备登录批准、员工手工创建令牌、管理员签发令牌都会额外返回一枚 `ee_rt_` refresh token；客户端可以调用刷新端点做一次性轮换。滑动续期保持原样（两种机制不冲突，但不建议同一客户端混用）。
- TTL 与宽限期都可配（见 §3.4）；存储新增表 `token_refresh_tokens`，只存 sha256；`access_tokens.family_id` 记录令牌族。

### 3.2 端点

```
POST /api/auth/refresh
Content-Type: application/json

{ "refresh_token": "ee_rt_…", "client_kind": "third_party" }
```

- `client_kind` 可选；填了必须与令牌族一致（`dsh` / `third_party`），否则 400 `invalid_request`。
- 成功（200）：

```json
{
  "access_token": "ee_at_…",
  "token_type": "Bearer",
  "expires_in": 3600,
  "refresh_token": "ee_rt_…",
  "refresh_expires_in": 2592000,
  "scopes": ["connections", "hub", "models", "registry:read"],
  "client_kind": "third_party"
}
```

- 失败统一 **401 `invalid_grant`**（不存在 / 过期 / 已吊销 / 已轮换 / 账号停用都不区分，避免泄漏令牌是否存在）；`refresh_token` 缺省或格式不对是 400 `invalid_request`。
- 响应只含明文令牌本身，**永远不返回哈希**；服务端日志、审计里也不写 refresh 明文 / 哈希。
- 刷新换出来的 access token 继承原令牌的 scope、额度、模型限制、`client_name`、来源，但**不继承管理模式**：`admin` 范围与 `admin_until` 要本人重新开启。
- 新 access token 的有效期由 `DSH_EE_REFRESH_ACCESS_TTL` 决定（默认 1h，不再滑动续期）；新 refresh 重新计算空闲 TTL，且不能超过令牌族的绝对上限。

### 3.3 轮换与复用检测

- 每次刷新都是一次性轮换：旧 refresh 行写 `rotated_at`，同事务签出新 access + 新 refresh，两者共用 `family_id`。
- **旧 refresh 再次出现 = 复用**：吊销整个令牌族（族内所有 access token 与 refresh token），记审计 `token.refresh_reuse`（含族 id、被复用的 refresh id、吊销数量、IP），返回 401。这是「令牌泄漏被重放」的信号。
- 并发轮换只有一次成功：轮换在数据库事务里先 `SELECT … FOR UPDATE` 锁旧行，再用 `rotated_at IS NULL` 的条件更新（CAS）；晚到的请求按复用处理（严格模式）。
- **轮换宽限** `DSH_EE_REFRESH_GRACE`（默认 `0` 严格）：>0 时，旧 refresh 在轮换后这段时间内重试会**原样返回上次的响应**（响应由主密钥加密后存在旧行上，宽限期过后由后台任务清空），适合「客户端没收到响应时幂等重试」。宽限期外仍出现旧令牌 → 按复用吊销整族。
- 关联吊销：吊销 / 登出 access token 会连带吊销整族 refresh；按账号 / 设备 / 部门批量吊销、同一设备重新走设备登录替换旧令牌，也都会作废旧 refresh。因此「吊销 access 后还能用 refresh 续期」不成立。
- 过期与清理：单枚 refresh 过期 → 401；族到达绝对上限 → 401；后台每小时清理宽限已过的重放密文，并删除过期 30 天以上且已轮换 / 吊销的记录。

### 3.4 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `DSH_EE_REFRESH_TOKENS` | 关闭 | 总开关；关闭时端点 404，签发响应不带 refresh token |
| `DSH_EE_REFRESH_ACCESS_TTL` | `1h` | 刷新换出的 access token 有效期；允许 `1m`–`24h` |
| `DSH_EE_REFRESH_TOKEN_TTL` | `720h`（30 天） | 单枚 refresh token 的空闲有效期（轮换重新计算）；允许 `1h`–`8760h` |
| `DSH_EE_REFRESH_FAMILY_TTL` | `4320h`（180 天） | 令牌族绝对上限，轮换继承、不会延长；不能小于单枚 refresh TTL |
| `DSH_EE_REFRESH_GRACE` | `0`（严格） | 轮换宽限，允许 `0`–`5m`；见 §3.3 |

### 3.5 审计与运维

- `token.refresh`：`via=token`，target `at:<新 access id>`；detail 含 `family_id`、`refresh_token_id`、`access_token_id`、`client_kind`、`expires_in`（不含任何明文 / 哈希）。
- `token.refresh_reuse`：高危事件，detail 含 `family_id`、`reused_refresh_token_id`、`revoked_access_tokens`、`revoked_refresh_tokens`；管理员审计页可按动作筛。
- 管理员仍可用 `POST /api/auth/introspect` 查 access token 状态；refresh token 不参与自省，也不当 Bearer 使用（会 401）。
- 当前未实现：`/api/auth/revoke` 端点、管理页展示 / 吊销 refresh 族、客户端（DSH 接入插件 / setup 脚本 / SDK）保存与轮换 refresh。接入方需要自己实现「保存 refresh、到期前刷新、刷新失败重新登录」。

## 4. 旧令牌 scope 收敛

现状（默认，未收敛）：

| 令牌 | 默认 scope |
|---|---|
| `ee_npm_`（read） | `models`、`registry:read`、`hub`、`connections` |
| `ee_npm_`（publish） | 上面再加 `registry:publish` |
| `sk-ee-` | `models`、`hub`、`connections` |

也就是说，旧令牌「顺便」能调模型网关、读工作台目录、调 MCP 工具；审计里已知这属于历史遗留，第三方不应依赖。

收敛开关：

```
DSH_EE_RESTRICT_LEGACY_TOKEN_SCOPES=1
```

- **默认关闭**，不改变现有客户端行为。
- 打开后：`ee_npm_` 只保留 `registry:read`（publish 令牌再加 `registry:publish`），`sk-ee_` 只保留 `models`；它们仍能完成原始用途（npm 源、模型网关），但不能再读工作台目录 / 调 MCP / 用 DSH 接口。
- 自省会返回收窄后的 scope；`GET /api/dsh/whoami` 增加 `legacy_scopes_restricted` 字段，方便客户端诊断。
- 迁移建议：需要工作台 / MCP / Agent 能力的客户端改用 `ee_at_` 令牌，按需勾选 scope；设备登录与第三方 Harness 本来就应走 `ee_at_`。

没有默认收窄的原因：旧 npm 令牌 / API key 仍在不少现有部署里被 DSH 与脚本使用，默认收窄会让这些客户端直接 403/401；因此选「可配置的兼容收敛」，由管理员确认现有客户端都迁走后再打开。

## 5. 网页端

- 员工「我的令牌」与管理员「令牌管理」的新建表单都有「客户端类型」选择：`DSH 接入`（dsh）/ `第三方 Harness`（third_party），**默认 third_party**；给不用设备码登录的 DSH 手工令牌时显式选 `DSH 接入`。
- 表单下方按选择给出作用说明：`dsh` 只用于 DSH 接入，写操作先在 DSH 审批卡确认；`third_party` 用于其它 Harness/脚本，写操作始终要员工在网页上人工确认。
- 两个列表都显示 `client_kind` 徽标，能直接看出某把令牌是给 DSH 还是给第三方客户端的；旧版 npm/key 令牌由服务端按 `dsh` 兼容显示。
- 后端 `TokenInput.client_kind` 与 `TokenView.client_kind` 已在前几节说明，网页只负责选择与展示，不改签发规则。

## 6. 程序化签发（最小版）

机器人 / CI 在无人值守场景不能走网页会话。本版不引入 `client_id` / `client_secret` 注册，
先让管理员用**带 `keys.manage` 的 Bearer 令牌**调用现有签发接口，给指定员工或机器人续发 `ee_at_`。

### 6.1 用法

入口不变（additive：同一接口同时接受网页会话与 Bearer）：

```
POST /api/admin/tokens
Authorization: Bearer ee_at_…            # 管理员令牌，需 keys.manage
{ "user_id": 42, "name": "CI bot token", "scopes": ["models", "hub"],
  "client_kind": "third_party", "client_name": "ci-runner", "expires_in_days": 7 }
```

- **Bearer 必须带 `keys.manage`**：等价于「账号有该权限 + 该令牌处于管理模式（`admin` 范围且
  `admin_until` 未过期）」。普通员工令牌、管理模式已过期的令牌返回 403；无效 / 过期令牌 401。
  管理员先在网页「我的令牌」上给这枚令牌开启管理模式（1 小时 / 8 小时），CI 用它调用；
  管理模式剩余时间就是程序化签发的窗口，超时后重新开启即可。
- 带 `tokens.dept_manage` 的部门令牌管理员（同样需要管理模式）也能调用，但只能给本部门机器人签发，
  与网页上的部门限制一致。
- 请求体与网页签发一致：可指定 `scopes`、`client_kind`（dsh / third_party）、`client_name`、
  `expires_in_days`、`monthly_token_limit`、`allowed_models`。scope 仍受**目标账号**权限限制
  （令牌不能超过账号权限）；机器人的写操作走 `third_party` 的人工确认语义。
- 建议显式给 `expires_in_days`：无人值守令牌不应长期有效；轮换 / 续发由 CI 自己按计划重新调用。
- **审计**：`token.issue`，`via=token`、`token_id=调用方令牌 id`、actor=管理员；detail 含目标
  `user_id`、scopes、`client_kind`、`client_name` 等，不含明文 / 哈希。
- **refresh 兼容**：开启 `DSH_EE_REFRESH_TOKENS` 时，程序化签发的响应同样带 `ee_rt_` refresh token，
  客户端可走 §3 的轮换。

### 6.2 安全边界

- 管理模式下的令牌权限等于**账号权限**（管理模式不按 scope 收窄），因此能调用本接口的令牌
  等价于能铸造任意可签发目标的权限令牌。只应交给受信任的 CI / 自动化，不要发给普通员工或第三方。
- 调用方令牌被吊销、账号被停用、管理模式到期，都会让后续签发立即失败；签发出来的令牌与网页
  签发完全同源（`source=issued`、同样的吊销 / 审计 / refresh 联动）。
- 签发不提供「按 client 维度」的独立限流或独立吊销；需要按集成方计量时先看 §6.3。

### 6.3 未实现：`client_id` / `client_secret` 与 client_credentials（设计）

若要「按集成方而不是员工」计量 / 吊销，或不能把一枚管理员令牌交给 CI，后续再做：

- 新表 `api_clients`：`client_id`、`client_secret`（只存哈希，可轮换）、owner（bot 账号）、
  scopes、状态（启用 / 禁用）；
- `POST /api/admin/clients` 创建 / 轮换 / 禁用，secret 只在创建或轮换时显示一次；
- `POST /api/auth/token` 的 `grant_type=client_credentials`，scope = client.scopes ∩ owner RBAC；
- 审计 `client.*` 与 `token.issue via=client`；
- 可选 mTLS（由反向代理终止，dsh-ee 只读可信头）。

本 PR 不做，避免与 refresh 的令牌族 / 复用检测语义混在一起。迁移号（未来 `0036+`）与接口命名
需要在维护者确认后定。

## 7. 相关测试

- `server/internal/api/token_programmatic_test.go`：管理员 Bearer 签发（含 refresh 兼容）、scope / client_kind / client_name / 到期属性、`token.issue` 审计归属（via=token、token_id、目标 user_id）、普通令牌与过期管理模式 403、无效 Bearer 401、目标账号不存在 400。需要数据库。
- `server/internal/api/refresh_test.go`：开关关闭 404、签发默认不带 refresh、轮换、旧 refresh 复用吊销整族、并发只成功一次、宽限期重放、过期、client_kind 不一致、吊销/停用账号后失效、跨用户隔离、设备登录下发 refresh、审计归属与不含明文/哈希。需要数据库（`DSH_EE_TEST_DSN`）。
- `server/internal/store/tokens_refresh_test.go`：库层轮换 / 复用族吊销、批量吊销联动 refresh、过期清理与重放密文清理。
- `server/internal/api/introspect_test.go`：无凭据/无效令牌 401、越权 403、self 与管理员查询、过期/吊销/不存在、管理员 Bearer、旧 npm/key 自省、scope 收敛开关。
- `server/internal/api/tokens_test.go`、`device_test.go`：签发默认 `client_kind` 与 whoami。
- `server/internal/store/tokens_test.go`、`migrations_test.go`：`client_kind` 迁移与默认值、令牌归属字段。
- 网页端：`pnpm build`（tsc）覆盖 i18n 与类型；e2e `03-employee-keys` 断言新建默认 third_party、可显式选 dsh、列表显示客户端类型，`10-tokens` 断言管理员签发默认 third_party。
