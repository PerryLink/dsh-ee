# 令牌生命周期：类型、自省、续期与旧令牌收敛

> 状态：自省（introspection）、旧令牌 scope 收敛（`#132`）与 API 客户端注册 / `client_credentials`（§6）已实现；
> 刷新令牌只有设计，未实现，需要维护者先定协议。
> 相关代码：`server/internal/api/introspect.go`、`server/internal/api/principal.go`、`server/internal/api/tokens.go`、
> `server/internal/api/clients.go`、`server/internal/store/clients.go`、
> `server/internal/store/migrations/0032_token_client_kind.sql`、`server/internal/store/migrations/0036_api_clients.sql`。

## 1. 令牌类型

| 类型 | 前缀 | 签发入口 | 有效范围 | `client_kind` |
|---|---|---|---|---|
| 接入令牌 `access` | `ee_at_` | 设备码登录、网页「我的令牌」、管理员签发 | 签发时的 scope，可带额度 / 模型限制 / 到期 | 按令牌属性：`dsh`（设备码）或 `third_party`（手工 / 管理员签发，可显式指定） |
| 旧 npm 令牌 | `ee_npm_` | 网页「npm 令牌」 | 固定 scope（见 §4） | 兼容为 `dsh` |
| 旧 API key | `sk-ee-` | 网页「我的 API key」/ 管理员代发 | 固定 scope（见 §4） | 兼容为 `dsh` |

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

## 3. 续期（refresh）：只做设计，未实现

现状：

- 设备码签发的令牌有**滑动续期**（`sliding_days`）：只要在有效期内使用，到期时间会续到「现在 + N 天」；`DSH_EE_…` 部署参数里可配天数（管理页「连接」设置里的 `device_token_days`）。
- 手工令牌、管理员签发令牌默认长期有效或按 `expires_in_days` 固定到期，没有续期机制。
- 目前已经能做到「会话快过期时重新走一次设备码登录」，接入插件 / SDK 可以用 §2 的自省在请求前判断。

评估过的方案：

| 方案 | 成本 | 结论 |
|---|---|---|
| A. 只保留滑动续期 | 零 | 已实现，覆盖 DSH 设备登录场景 |
| B. 简单续期接口（旧令牌换新令牌） | 中：等于把令牌有效期无限延长，容易变成绕过吊销 | 不做 |
| C. OAuth 2.0 refresh token（`grant_type=refresh_token`） | 大：新表 + 轮换 + 复用检测 + 客户端协议 + 管理页 | **需要维护者先定协议，未实现** |

如果要落 C，最小完整形态大致是：

- 新表 `token_refresh_tokens`：`id`、`token_id`（父 access token）、`secret_hash`（只存哈希）、`expires_at`、`rotated_at`、`used_at`、`revoked_at`、`created_at`；
- `POST /api/auth/refresh`：`{refresh_token}` → 新 access token + 新 refresh token（一次性轮换；旧 refresh token 再次出现时按「复用」吊销整个令牌族）；
- 设备码 / 手工签发时可选返回 refresh token；管理页展示/吊销；审计动作 `token.refresh` / `token.refresh_reuse`；过期记录由后台任务清理；
- 客户端改动：DSH 接入插件、setup 脚本、SDK 都要保存并轮换 refresh token，协议变更需要版本协商。

阻塞点：是否采用 OAuth 刷新语义、refresh token 的有效期与轮换策略、是否允许第三方 Harness 自助续期（涉及审计与吊销语义）、旧客户端兼容窗口。这些需要维护者产品决策，所以本 PR 不做半成品。

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

- API 客户端（§6）暂未做管理页，使用管理 API 创建 / 查看 / 注销。
- 员工「我的令牌」与管理员「令牌管理」的新建表单都有「客户端类型」选择：`DSH 接入`（dsh）/ `第三方 Harness`（third_party），**默认 third_party**；给不用设备码登录的 DSH 手工令牌时显式选 `DSH 接入`。
- 表单下方按选择给出作用说明：`dsh` 只用于 DSH 接入，写操作先在 DSH 审批卡确认；`third_party` 用于其它 Harness/脚本，写操作始终要员工在网页上人工确认。
- 两个列表都显示 `client_kind` 徽标，能直接看出某把令牌是给 DSH 还是给第三方客户端的；旧版 npm/key 令牌由服务端按 `dsh` 兼容显示。
- 后端 `TokenInput.client_kind` 与 `TokenView.client_kind` 已在前几节说明，网页只负责选择与展示，不改签发规则。

## 6. API 客户端注册（client_credentials）

第三方 Harness / CI 这类无人值守的集成方，不应该直接拿一枚管理员令牌。管理员可以按集成方注册一个
API 客户端：拿到 `client_id` / `client_secret`，由客户端自己换短期 access token。

### 6.1 创建与管理（`keys.manage`）

```
POST /api/admin/clients
{ "name": "Claude Code CI", "owner_user_id": 42, "scopes": ["models", "hub"] }

→ 201
{
  "client": {
    "id": 1, "client_id": "dshc_…", "name": "Claude Code CI", "owner_user_id": 42,
    "owner_username": "ci-bot", "owner_department": "研发效能部",
    "scopes": ["models", "hub"], "client_kind": "third_party",
    "created_by": 1, "disabled_at": null, "last_used_at": null, "created_at": "2026-11-13T00:00:00Z"
  },
  "client_secret": "dshs_…"
}
```

- **权限**：网页会话 + `keys.manage`（与「令牌管理」页同一权限点）。
- **owner**：令牌签给哪个账号，员工或机器人（bot）都可以；owner 被停用后立即停止签发。
- **scope**：受 owner 当前权限限制（和手工令牌同一套映射，`admin` 永远不能通过签发接口下发）；
  不填默认 `models, registry:read, hub, connections`。创建时校验一次，之后每次换 token 再和
  owner 当前权限求交集，管理员收回权限对已注册客户端立即生效。
- **secret**：只存 sha256，**只在创建响应里出现一次**；列表 / 详情永远不返回明文或哈希。
  本版没有 secret 轮换：需要换 secret 时注销后重建。

`GET /api/admin/clients` 列出全部客户端（含已禁用），带 owner 用户名 / 部门与最近使用时间。
`DELETE /api/admin/clients/{client_id}` 注销（幂等）：之后不能再换 token；已经签出的 access token
按自身有效期自然过期。

### 6.2 换 token（公开端点）

```
POST /api/auth/client-token
{ "client_id": "dshc_…", "client_secret": "dshs_…" }

→ 200
{
  "access_token": "ee_at_…", "token_type": "Bearer", "expires_in": 3600,
  "scopes": ["models", "hub"], "client_id": "dshc_…", "client_name": "Claude Code CI",
  "client_kind": "third_party", "token_id": "at:123"
}
```

- 不需要网页会话 / Bearer，凭据只在请求体里。
- 签出的 access token **1 小时**有效，`source=issued`、`client_kind=third_party`、`client_name=客户端名称`；
  到期后客户端用同一对凭据再换一枚即可，不需要人工介入。
- `scope` = 客户端注册的 scopes ∩ owner 当前权限；交集为空时返回 403 `insufficient_scope`，不签发空令牌。
- **凭据失败统一 401 `{"error":"invalid_client"}`**：client_id 不存在、secret 不对、客户端已禁用、
  owner 已停用都回同一个响应，不区分原因，避免被用来探测注册信息。
- **IP 限流**：每 IP 每分钟最多 120 次换 token，`DSH_EE_CLIENT_TOKEN_PER_MINUTE` 可调，`0` / `off` 关闭；
  超限返回 429 `rate_limited` + `Retry-After: 60`。只防用错误 secret 撞库 / 刷库，正常集成远低于这个量。
- **审计**：`token.issue`，`via=client_credentials`、`client=客户端名称`，actor 是 owner 账号；
  detail 含 `client_id`、scope、`client_kind`、到期时间，不含 secret / 令牌明文。管理动作另记
  `client.create` / `client.revoke`。审计来源字段已加宽到能放下 `client_credentials`。

### 6.3 与刷新令牌的关系

本版换 token 只返回 access token（1 小时），不返回 refresh token：刷新令牌仍只有设计（§3），
未实现。等刷新令牌按 §3 落地、`DSH_EE_REFRESH_TOKENS` 开启后，在 client_credentials 的响应里补
`refresh_token` 即可，客户端协议不需要改。

### 6.4 未实现

- 管理页前端（当前用管理 API / curl 管理客户端）；
- secret 轮换 / 多 secret 过渡；
- OAuth authorization code 流程、按 client 的独立额度与限流维度。

## 7. 相关测试

- `server/internal/api/introspect_test.go`：无凭据/无效令牌 401、越权 403、self 与管理员查询、过期/吊销/不存在、管理员 Bearer、旧 npm/key 自省、scope 收敛开关。
- `server/internal/api/tokens_test.go`、`device_test.go`：签发默认 `client_kind` 与 whoami。
- `server/internal/store/tokens_test.go`、`migrations_test.go`：`client_kind` 迁移与默认值、令牌归属字段。
- `server/internal/api/clients_test.go`（需数据库）：创建 / secret 只回一次 / 错误 secret 与未知 client_id
  不可区分 / 禁用客户端 / owner 停用 / scope 超权与权限收回后的交集 / 换 token / 固定 1h TTL / IP 限流 /
  `client.create`、`token.issue via=client_credentials` 审计。
- `server/internal/store/clients_test.go`：库层创建 / 按 client_id 取 / 列表 owner 信息 / 最近使用 / 禁用幂等。
- 网页端：`pnpm build`（tsc）覆盖 i18n 与类型；e2e `03-employee-keys` 断言新建默认 third_party、可显式选 dsh、列表显示客户端类型，`10-tokens` 断言管理员签发默认 third_party。
