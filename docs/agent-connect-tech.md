# DSH 深度联动：技术规划与验证方式（v0.3 草案，2026-10-31）

> 配套产品规划：[agent-connect-product.md](agent-connect-product.md)。上游依据为 `deepseek-ai/deepseek-harness` 源码（MIT，commit ce7073d5），本地只读克隆在 `.tools/upstream/deepseek-harness`。

## 0. 结论先行

| 问题 | 方案 |
|---|---|
| 令牌怎么到 DSH | setup 脚本或 `/ee-login` 走设备码流程；令牌存进 `ctx.credentials`（名字 `DSHEE_TOKEN`），配置里只引用名字。S1 已证明通用授权界面不可用，因此不依赖 `ctx.authorization` 展示登录入口 |
| 模型怎么用令牌 | `llm-pi-ai` 提供方配置 `apiKeyEnv: DSHEE_TOKEN`。它是 `CredentialRef`：先读凭据库，再回落到环境变量与 `.env`，每次请求重新解析，换令牌不用重启 |
| 企业连接与 dsh-ee 工具怎么用令牌 | **不再使用 `dsh-mcp-client` 那一行**：它的 HTTP 请求头是配置时写死的（`transport.ts` 里 `requestInit.headers` 静态传入），读不到凭据库。改由接入插件自带一个轻量 MCP 桥：连 dsh-ee 的 `/mcp`，把工具用 `ctx.tools.register` 注册给 DSH，每次调用现解析令牌 |
| 写操作怎么确认 | 插件监听 `tools/pre-execute`，对 dsh-ee 的写工具返回 `{kind:'ask'}`，DSH 自带审批框；危险操作另有服务端两步确认，与客户端无关 |
| 「全部功能」怎么保证 | 路由注册时同时声明 Agent 元数据，工具由这一份声明生成；覆盖率测试要求每个接口要么有工具、要么显式排除 |
| Agent 的权限 | 令牌权限 = 账号 RBAC ∩ 令牌范围；管理工具还要求 `admin_until > now` |

## 1. 前置验证（动手前先做，1～2 天）

每项都在隔离的 `DSH_HOME` 里用真实 DSH 0.2.1-rc.5 + 树外组合包验证，脚本放进 `e2e/dsh/spikes/`。

| 编号 | 要验证的事 | 通过标准 | 不通过时的备选 |
|---|---|---|---|
| S1 | 树外组合包里的插件能 `ctx.authorization.registerFlow`，web profile 的设置页 / 提示能显示我们的 notice（URL + 确认码） | 点登录后浏览器打开 `/device?code=…`，DSH 显示确认码，授权后 flow 报 `authorized` | 插件自己在 UI 用 toast / `ask_user` 提示链接；或保留 `.env` 方式 |
| S2 | `ctx.credentials.set('DSHEE_TOKEN', v)` 后，`llm-pi-ai` 提供方 `apiKeyEnv: DSHEE_TOKEN` 的下一次请求带新值 | fake LLM 日志里 Authorization 随之变化，无需重启 | 提供方行改用 `!!js` + 插件启动时写 `process.env`（热更新失效，需要重启 DSH） |
| S3 | `ctx.tools.register` 在运行中动态注册 / 销毁（cordis 的 effect dispose），下一轮对话的工具列表随之变化 | fake LLM 收到的 `tools` 列表在 `tools/list_changed` 后变化 | 只在会话开始时同步工具；变化在新会话生效 |
| S4 | `ctx.on('tools/pre-execute')` 返回 `ask` 在 web profile 弹审批框；在无头模式（`-p`）下的行为 | web：弹框；无头：按上游策略拒绝或自动通过（需记录，决定 E2E 怎么写） | 只依赖服务端两步确认 |
| S5 | 工具名与参数 schema 约束（长度、字符集、`enum`、嵌套对象）以及工具结果里结构化内容的展示 | 记录约束，写进生成器 | — |
| S6 | 插件内 `fetch` 带令牌访问 `/api/dsh/*` 与 `/mcp`，以及 SSE 长连接在 web profile 热重载时的释放 | 热重载后无残留连接（dsh-ee 侧 SSE 连接数回落） | 改为轮询 `catalog` 的 `revision` |

S1～S4 决定阶段 A、C 的实现路径，必须先做。第二轮 S5～S10（含原 S5、S6）见 [阶段 A 实施规划](agent-connect-phase-a.md) §3。

### 第二轮实测结果（2026-11-01）

| 编号 | 结果 | 对实现的影响 |
|---|---|---|
| S5 | **通过** | `mcp__acme__get_user`、点号、连字符和 81 字符工具名均可注册调用；嵌套 schema 可用，Web 默认折叠 JSON 结果。 |
| S6 | **通过** | `ctx.effect` 返回 abort 清理函数；热重载只保留一个活动连接，停用后归零。 |
| S7 | **通过（调整方案）** | DSH 请求带 `X-DshEE-Client` 时，鉴权、权限和额度错误改为 400 中文文案，避免上游固定英文错误和重试。 |
| S8 | **通过（调整方案）** | `/ee-login`、`/ee-status` 必须在已有对话中执行才有可见命令记录；后台登录继续运行，但不提供完成后的二次通知。 |
| S9 | **通过** | 插件启动时从 pending 文件写入凭据库，首个模型请求即使用新令牌，随后删除文件。 |
| S10 | **本阶段不做** | Web 客户端插件属于阶段 D 可选项，不是统一登录的前置条件。 |

### 实测结果（2026-10-31）

已在 macOS arm64 / Node 24.22.1 上，用真实 DSH 0.2.1-rc.5、树外组合包和 fake LLM 完成 S1～S4。可复现脚本、运行方式和原始输出说明见 [e2e/dsh/spikes](../e2e/dsh/spikes/README.md)。

| 编号 | 结果 | 对实现的影响 |
|---|---|---|
| S1 | **部分通过** | `registerFlow`、notice 和 `authorized` 均可用；但 Web 聊天页不会自动展示 notice，设置页也没有通用授权入口。接入插件必须自己提供登录入口和 notice 展示，不能依赖现有设置页。 |
| S2 | **通过** | 同一进程的两次模型请求依次为 `Bearer token-one` / `Bearer token-two`，凭据更新无需重启，按原方案使用 `apiKeyEnv`。 |
| S3 | **通过** | 动态工具在三轮请求中的可见性为 `false → true → false`，可以按原方案动态注册 / 销毁。 |
| S4 | **通过** | Web 显示审批卡，`Allow once` 后才执行；无头模式没有审批通道时拒绝且工具体未执行，E2E 应断言 fail closed。 |

发布版 rc.5 的无头 CLI 不接受 `--approval-policy`，无头 S4 验证的是默认策略。阶段 A 仍可采用设备码 + 凭据库，但 S1 的 Web 登录入口需要按备选方案由插件自行实现。

## 2. 总体结构

```
员工电脑                                         dsh-ee（单二进制）
┌──────────────────────────────────────┐        ┌──────────────────────────────────────────┐
│ DSH                                  │        │ 认证：resolveBearer → Principal            │
│  ├ llm-pi-ai 提供方 dsh-ee             │──/v1──▶│   (用户 · RBAC · 令牌范围 · 额度 · 管理模式) │
│  │   apiKeyEnv: DSHEE_TOKEN           │        │ 模型网关 /v1                               │
│  ├ plugin-market（索引）               │─/index▶│ npm 源 /npm                                │
│  └ @<scope>/dsh-connect（接入插件）     │        │ 设备码 /api/device/*  ·  确认页 /device     │
│     ├ 登录方式（authorization flow）    │─device▶│ DSH 接口 /api/dsh/*（目录、技能正文）        │
│     ├ 技能提供方                        │─/api/dsh▶                                         │
│     ├ MCP 桥 → ctx.tools               │──/mcp─▶│ /mcp：企业连接工具 + ee_* 工具（Agent 清单） │
│     ├ 审批钩子 tools/pre-execute        │        │   ├ 工具 → 内部调用原 HTTP 处理函数          │
│     ├ 本机诊断工具 ee_local_doctor       │        │   └ 危险操作两步确认、审计 via=agent        │
│     └ 必装插件自动安装                  │        │ 审计、统计、能力中心（已有）                │
│  ctx.credentials: DSHEE_TOKEN         │        └──────────────────────────────────────────┘
└──────────────────────────────────────┘
```

## 3. 令牌模型（阶段 A）

### 3.1 迁移 `0008_tokens.sql`

```sql
-- 统一接入令牌，只存 sha256；明文只在创建时返回一次。
CREATE TABLE access_tokens (
  id                  BIGINT        NOT NULL AUTO_INCREMENT,
  user_id             BIGINT        NOT NULL,           -- 持有者（个人或机器人账号）
  name                VARCHAR(128)  NOT NULL,
  prefix              VARCHAR(24)   NOT NULL,           -- ee_at_xxxx，用于展示
  token_hash          CHAR(64)      NOT NULL,
  source              VARCHAR(16)   NOT NULL,           -- device | manual | issued
  scopes              VARCHAR(255)  NOT NULL,           -- 逗号分隔，见 3.2
  monthly_token_limit BIGINT        NULL,
  allowed_models      TEXT          NULL,               -- JSON 数组
  expires_at          DATETIME(3)   NULL,
  sliding_days        INT           NULL,               -- 设备令牌：使用时把 expires_at 续到 now+N 天
  admin_until         DATETIME(3)   NULL,               -- 管理模式到期时间
  client_name         VARCHAR(128)  NOT NULL DEFAULT '',-- 设备名
  client_os           VARCHAR(64)   NOT NULL DEFAULT '',
  client_version      VARCHAR(64)   NOT NULL DEFAULT '',
  last_used_at        DATETIME(3)   NULL,
  last_ip             VARCHAR(64)   NOT NULL DEFAULT '',
  revoked_at          DATETIME(3)   NULL,
  created_by          BIGINT        NOT NULL,
  created_at          DATETIME(3)   NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_access_tokens_hash (token_hash),
  KEY idx_access_tokens_user (user_id)
);

-- 设备码登录（RFC 8628 的子集）。device_code 只存 sha256。
CREATE TABLE device_codes (
  id               BIGINT       NOT NULL AUTO_INCREMENT,
  device_hash      CHAR(64)     NOT NULL,
  user_code        CHAR(9)      NOT NULL,              -- XXXX-XXXX，去掉易混字符
  client_name      VARCHAR(128) NOT NULL DEFAULT '',
  client_os        VARCHAR(64)  NOT NULL DEFAULT '',
  client_version   VARCHAR(64)  NOT NULL DEFAULT '',
  scopes           VARCHAR(255) NOT NULL,
  status           VARCHAR(16)  NOT NULL,              -- pending | approved | denied | consumed
  user_id          BIGINT       NULL,
  token_id         BIGINT       NULL,
  token_sealed     VARBINARY(256) NULL,                -- 批准时生成的令牌，主密钥加密，取走即清空
  last_poll_at     DATETIME(3)  NULL,
  expires_at       DATETIME(3)  NOT NULL,
  created_at       DATETIME(3)  NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_device_hash (device_hash),
  UNIQUE KEY uk_device_user_code (user_code)
);

-- 危险操作的一次性确认码。
CREATE TABLE agent_confirms (
  nonce       CHAR(32)     NOT NULL,
  user_id     BIGINT       NOT NULL,
  token_id    BIGINT       NOT NULL,
  tool        VARCHAR(64)  NOT NULL,
  args_hash   CHAR(64)     NOT NULL,
  expires_at  DATETIME(3)  NOT NULL,
  used_at     DATETIME(3)  NULL,
  PRIMARY KEY (nonce)
);

ALTER TABLE users      ADD COLUMN account_type VARCHAR(8) NOT NULL DEFAULT 'human'; -- human | bot
ALTER TABLE users      ADD COLUMN owner_id     BIGINT NULL;                          -- 机器人账号负责人
ALTER TABLE usage_logs ADD COLUMN token_id     BIGINT NULL;
ALTER TABLE usage_logs ADD KEY idx_usage_token_time (token_id, created_at);
ALTER TABLE audit_logs ADD COLUMN via      VARCHAR(8)   NOT NULL DEFAULT 'web';     -- web | agent | system | token
ALTER TABLE audit_logs ADD COLUMN token_id BIGINT       NULL;
ALTER TABLE audit_logs ADD COLUMN client   VARCHAR(128) NOT NULL DEFAULT '';
-- 新权限点与菜单：tokens.dept_manage（本部门令牌管理）；「我的令牌」「令牌管理」替换原 Key 菜单
```

旧表 `api_keys`、`npm_tokens` 不动、不迁移，由 3.3 的解析器兼容。

### 3.2 能力范围

| scope | 放行的入口 | 对应 RBAC 权限点（还需账号本身有） |
|---|---|---|
| `models` | `/v1/*` | `llm.use` |
| `registry:read` | `/npm` 读、`/index` | `registry.use` |
| `registry:publish` | `/npm` 写 | `registry.publish` |
| `hub` | `/api/dsh/catalog`、`/api/dsh/skills/*` | `portal.view` |
| `connections` | `/mcp` 里的企业连接工具 | `portal.view` + 连接授权 |
| `agent` | `/mcp` 里员工侧的 `ee_*` 工具 | 各工具声明的权限点 |
| `admin` | `/mcp` 里管理侧的 `ee_*` 工具，且 `admin_until > now` | 各工具声明的权限点 |

设备登录默认申请 `models registry:read hub connections agent`；`admin` 只能在网页上给令牌加（需要账号有任一管理权限点）。

**入口级 scope**：`/mcp` 入口本身按 `connections` 检查（`server/internal/api/dsh.go` 的 `dshPrincipal`），所以只带 `agent`、不带 `connections` 的令牌会被 403；`ee_*` Agent 工具同时要 `connections`（进 `/mcp`）和 `agent`（工具出现并可用）。

### 3.3 统一解析：`internal/api/principal.go`

```go
type Principal struct {
	User      *store.User
	Access    rbac.Access   // 账号 RBAC，已与 scope 求交
	Scopes    ScopeSet
	TokenID   int64         // 新令牌的 id；旧令牌为 0
	Legacy    string        // "", "npm", "key"
	Limit     *int64        // 令牌级每月额度
	Models    []string      // 令牌级可用模型
	AdminLive bool          // 有 admin scope 且 admin_until > now
	Client    string
}

func (s *Server) resolveBearer(ctx context.Context, token string) (*Principal, error)
```

- `ee_at_` → `access_tokens`；`ee_npm_` → 旧 npm 令牌，按 `access` 映射为 `registry:read|publish models hub connections`（保持现在的行为）；`sk-ee-` → `models hub connections`（同现状）。
- 现有的 `dshAuth`、`authKey`、npm 源的令牌校验、`tokenAsKey` 全部改为调用 `resolveBearer` 再检查 scope，删除重复代码。
- `Access` 的求交：每个 RBAC 权限点在 `rbac` 包里登记所属 scope（`agent` 或 `admin`），`Principal.Access` 只保留 scope 允许的那部分。工具过滤、处理函数里的 `currentAccess(r).Has(...)` 自动生效，不需要逐个改处理函数。
- 设备令牌的滑动续期：`last_used_at` 距上次续期超过 1 天时把 `expires_at` 续到 `now + sliding_days`，避免每次请求都写库。
- 吊销、过期返回 401，错误体带 `code: token_revoked | token_expired | token_invalid`，插件据此触发重新登录。

### 3.4 额度

- 网关在现有账号额度检查之后，再检查令牌额度：`SUM(total_tokens) FROM usage_logs WHERE token_id=? AND created_at >= 月初`（走新索引）。
- 机器人账号就是普通账号（`account_type='bot'`），额度、可用模型沿用账号字段，所以「团队额度」不需要新概念。
- `usage_logs` 新写入都带 `token_id`；统计接口加 `group_by=token`。

### 3.5 设备码流程

| 接口 | 认证 | 说明 |
|---|---|---|
| `POST /api/device/code` | 无 | 入参 `{client_name, client_os, client_version, scopes?}`；返回 `{device_code, user_code, verification_uri, verification_uri_complete, interval:5, expires_in:600}`。按 IP 限流（每分钟 30 次） |
| `POST /api/device/token` | 无 | 入参 `{device_code}`；返回 `authorization_pending` / `slow_down`（轮询快于 interval）/ `access_denied` / `expired_token`，或 `{access_token, scopes, user:{username, department}, expires_at}`。令牌只能取一次 |
| `GET /api/device/{user_code}` | 会话 | 确认页读取设备信息与申请的范围 |
| `POST /api/device/{user_code}` | 会话 | `{approve: true/false}`。批准时创建 `access_tokens`（source=device），明文用主密钥加密暂存在 `device_codes.token_sealed`，被取走时清空 |

- 同一用户、同一设备名重复登录时，吊销旧的设备令牌（避免越积越多），确认页提示「将替换此设备之前的登录」。
- 审计：`device.approve`、`device.deny`、`token.create`（via=token, source=device）。
- 前端新页面 `web/src/pages/device.tsx`，不走菜单，未登录时跳登录页后回跳。

### 3.6 管理模式

- `POST /api/me/tokens/{id}/elevate`：`{password, hours: 1|8}`。本地账号校验密码哈希，LDAP 账号走一次 bind。成功后设置 `admin_until`，审计 `token.elevate`。
- `DELETE /api/me/tokens/{id}/elevate`：提前结束。
- 到期由服务端计时器触发（进程内维护最近的到期时间；重启时从库里恢复），向该令牌的 MCP 会话推送 `tools/list_changed`。

## 4. 接入插件改造（阶段 A 起）

### 4.1 源码化

现在的 `server/internal/api/connectpkg/index.js` 是 132 行手写 JS。改为：

```
dsh-plugin/                       # 新目录，TypeScript 源码
  package.json                    # devDependencies: @deepseek-ai/cordis 等类型（经 dsh-ee 自己的 npm 源取）
  tsconfig.json
  src/index.ts                    # apply(ctx, config)：组装下面各部分
  src/auth.ts                     # 设备码登录方式 + 令牌失效处理
  src/http.ts                     # 带令牌的 fetch，统一处理 401 / 403 / 网络错误
  src/skills.ts                   # 技能提供方（由现有逻辑搬过来）
  src/bridge.ts                   # MCP 桥：initialize / tools/list / tools/call / GET SSE
  src/approval.ts                 # tools/pre-execute 钩子
  src/doctor.ts                   # 本机诊断工具
  src/plugins.ts                  # 必装插件自动安装（由现有逻辑搬过来）
  test/*.test.ts                  # 用假的 ctx 与假的 dsh-ee 做单元测试
```

- 用 esbuild 打成单文件 `index.js`，输出到 `server/internal/api/connectpkg/index.js` 继续 `go:embed`。产物入库，`make plugin` 重新生成，CI 检查产物与源码一致（构建后 `git diff --exit-code`）。
- 插件版本号仍由服务端按内容哈希生成（`1.0.N`），插件源码变化也会触发重新发布。

### 4.2 `cordis.patch.yml` 的变化

```yaml
- id: dsh-ee-connect
  name: '@<scope>/dsh-connect'
  config:
    base: 'https://dsh-ee.example.com'
    org: '示例公司'
    serverName: example            # 工具名前缀
    tokenRef: DSHEE_TOKEN          # 凭据名
    autoInstall: true
# 删除：dsh-ee-mcp（@deepseek-ai/dsh-mcp-client），由接入插件的 MCP 桥代替
- id: dsh-ee-llm
  name: '@deepseek-ai/dsh-llm-pi-ai'
  config:
    providers:
      - id: dsh-ee
        baseURL: 'https://dsh-ee.example.com/v1'
        api: openai-completions
        apiKeyEnv: DSHEE_TOKEN     # 原来是 !!js process.env…
# plugin-market、agent-default-model 不变
```

`apiKeyEnv` 是 `CredentialRef`，凭据库没有值时回落到环境变量与 `$DSH_HOME/.env`，所以老的 `setup.sh` 写 `.env` 的员工不受影响。

### 4.3 登录（`auth.ts`）

> **已按 S1 实测修正**：DSH 没有展示 notice 的界面，模型又依赖令牌，所以登录不走 `authorization` flow 的界面，而是 setup 脚本里的设备码 + `/ee-login` 命令 + 报错文案引导。下面的流程保留作为插件内部的设备码实现；入口设计与第二轮验证 S5～S10 见 [阶段 A 实施规划](agent-connect-phase-a.md) §2、§3。

- `ctx.authorization.registerFlow`，方法名「登录 <企业名>」：
  1. `POST /api/device/code`，带 `os.hostname()`、平台、DSH 版本。
  2. 发 notice：打开 `verification_uri_complete`，显示 `user_code`。
  3. 按 `interval` 轮询 `/api/device/token`，遵守 `slow_down`。
  4. 拿到令牌后 `ctx.credentials.set('DSHEE_TOKEN', token)`，报 `authorized`。
- 插件启动时 `ctx.credentials.describe(ref)` 未配置 → 提示登录（具体 UI 入口取决于 S1）。
- 任何请求收到 `token_revoked / token_expired` → 清掉凭据库里的值（仅当值与失败的令牌一致，防止竞争），再提示重新登录。

### 4.4 MCP 桥（`bridge.ts`）

- 启动后 `initialize` → `tools/list`，对每个工具 `ctx.tools.register(defineTool({...}))`：
  - 名字：企业连接工具 `<serverName>__<连接>__<工具>`；dsh-ee 工具 `ee_<领域>`。
  - 参数 schema 原样转为 `defineTool` 的参数格式（S5 记录的约束在服务端生成时就满足）。
  - 执行：每次调用 `ctx.credentials.resolve(ref)` 取令牌 → `tools/call` → 结果映射为 ContentBlock；服务端返回的 `isError` 原样透出。
  - 工具的 `_meta` 携带 `{effect: read|write|danger, title_zh, title_en}`，供审批钩子使用。
- GET SSE 收到 `notifications/tools/list_changed` → 重新 `tools/list`，diff 后注册新增、dispose 已移除（S3）。
- 会话：沿用 `Mcp-Session-Id`；dsh-ee 返回 404 时重新 initialize（服务端已有这套逻辑，客户端照 `internal/mcp/client.go` 实现）。

### 4.5 审批（`approval.ts`）

```ts
ctx.on('tools/pre-execute', async (exec, next) => {
  const meta = bridge.meta(exec.name)
  if (!meta || meta.effect === 'read') return next()
  return {
    kind: 'ask',
    reason: `dsh-ee ${meta.effect}: ${exec.name}`,
    displayReason: { en: meta.title_en, zh: meta.title_zh },
  }
})
```

审批只是第一道。危险操作的第二道在服务端（5.5），即使换一个不认这个钩子的客户端，也不会误执行。

### 4.6 本机诊断（`doctor.ts`）

插件本地注册 `ee_local_doctor`（只读），检查：

| 检查 | 方法 |
|---|---|
| 凭据 | `ctx.credentials.describe('DSHEE_TOKEN')`：已配置 / 来源（凭据库或 `.env`） |
| 令牌有效 | `GET /api/dsh/whoami`（新增）：用户、部门、范围、过期时间、管理模式 |
| npm 源 | 读 `~/.npmrc`：是否有 `@<scope>:registry=` 与对应 `_authToken`，令牌前缀是否匹配 |
| 接入插件版本 | 当前版本 vs `GET /api/connect` 的最新版本 |
| 必装插件 | `pluginManager` 已装列表 vs 目录里的 `required_plugins` |
| 模型 | `GET /v1/models` 能否返回、默认模型是否在列表里 |
| 企业连接 | 桥的会话状态、上次 `tools/list` 时间与工具数 |

输出结构化结果 + 修复建议。修复本身由模型调 DSH 自带的文件 / shell 工具完成（这些工具有 DSH 自己的审批），插件不直接改文件。

## 5. Agent 工具框架（阶段 B、C）

### 5.1 路由与工具同源

把 `server.go` 里的 `mux.Handle(...)` 改为一个路由表：

```go
type route struct {
	pattern string          // "POST /api/admin/users"
	h       http.HandlerFunc
	perms   []string        // 任一即可；空 = 只需登录
	agent   *agentSpec      // nil 时必须出现在 agentExcluded 里
}

type agentSpec struct {
	tool    string          // "ee_users"
	action  string          // "create"
	summary string          // 给模型看的一句话（中文为主，附英文）
	effect  effect          // read | write | danger
	scope   string          // agent | admin
	params  []param         // 名称、类型、必填、说明、枚举；生成 JSON Schema
	path    map[string]string // 路径参数取自哪个入参：{"id": "user_id"}
	query   []string        // 哪些入参放进 query
	preview func(ctx context.Context, p *Principal, args map[string]any) (string, error) // danger 必填
	shape   func([]byte) any  // 可选：裁剪响应，减少回给模型的字段
}

var agentExcluded = map[string]string{
	"POST /api/auth/login":           "登录由人完成",
	"POST /api/auth/password":        "凭据只在网页上由本人操作",
	"GET /api/admin/upstream/export": "离线包导出必须在网页上做",
	"POST /api/admin/upstream/import":"离线包导入必须在网页上做",
	"POST /api/admin/menus/reset":    "全局破坏性操作",
	"POST /api/device/{user_code}":   "授权必须由人在浏览器完成",
	// … 以及 /npm/、/v1/、/mcp、/setup.sh 等协议入口
}
```

`server.go` 遍历路由表注册 mux（会话认证 + `requirePerm` 保持不变），同时生成 Agent 工具清单。

### 5.2 调用方式：内部转发到原处理函数

工具调用不重写业务逻辑：

1. 校验 Principal 的 scope（`agent` / `admin` 且 `AdminLive`）与工具声明的权限点。
2. 按 `agentSpec` 把入参拼成 `*http.Request`（方法、路径参数、query、JSON body）。
3. context 里放入 `withUser(user, principal.Access)` 和 `withVia(agent, tokenID, client)`。
4. 直接调用路由表里的 `perms` 包装后的处理函数（跳过会话 cookie 那一层），用 `httptest.ResponseRecorder` 收结果。
5. 2xx → 工具成功，body 经 `shape` 裁剪后作为文本 / 结构化内容；4xx → `isError`，消息就是网页上会显示的那句中文错误；5xx → `isError` + 通用错误，详情只进服务端日志。

这样校验、审计、hub 通知、额度等所有副作用与网页完全一致。`s.audit` 从 context 读 `via / token_id / client` 写进新列。

### 5.3 工具分组

按领域合并成「一个工具 + `action` 枚举」，参数是各 action 参数的并集，description 里逐个 action 列出必填项。预估：

| 工具 | 主要 action | scope |
|---|---|---|
| `ee_me` | whoami, overview（额度用量）, usage | agent |
| `ee_tokens` | list, create, revoke, request_admin（返回网页链接） | agent |
| `ee_hub` | search, detail, enable, disable, create, update, new_version | agent |
| `ee_plugins` | catalog, detail, my_packages, upload（读本机 .tgz 由插件上传）, import_request | agent |
| `ee_connections` | mine, request_access | agent |
| `ee_records` | my_calls, my_usage | agent |
| `ee_users` | list, create, update, set_roles, set_quota, create_bot | admin |
| `ee_roles` | list, create, update, delete, permissions | admin |
| `ee_menus` | list, create, update, reorder, delete | admin |
| `ee_models` | providers.*, models.*, check | admin |
| `ee_admin_tokens` | list, issue, revoke, revoke_by（人 / 设备 / 部门） | admin |
| `ee_review` | queue, inspect（包文件、脚本、技能正文）, decide | admin |
| `ee_hub_admin` | patch（范围、下发）, plugin_policy, delete | admin |
| `ee_connections_admin` | test, create, sync, tool（开关 / 授权）, calls | admin |
| `ee_registry` | overview, check, upstream_feed, sync, jobs, check_updates | admin |
| `ee_settings` | get, put（字段白名单）, connect_publish, ldap_get, ldap_put（不含密码）, ldap_test, ldap_sync | admin |
| `ee_audit` | query | admin |
| `ee_stats` | query | admin |
| `ee_doctor` | 服务端诊断（见 4.6 的服务端部分） | agent |

- 工具列表按 Principal 过滤：没有任何 action 可用的工具不出现；`action` 枚举也只列可用的。
- 体积预算：员工侧 `tools/list` ≤ 6k token，管理员（管理模式）≤ 15k token。测试里用近似计数（字符数 / 2.5）卡住。
- 「上传本地插件包」需要读本机文件：服务端工具只接收 base64 不合适（经模型上下文），所以 `ee_plugins.upload` 由插件本地实现：读文件 → 直接 `POST /api/me/packages`，只把结果回给模型。这类「插件本地实现、服务端声明」的 action 在 spec 里标 `local: true`。

### 5.4 危险操作的两步确认

1. 第一次调用不带 `confirm`：服务端执行 `preview`，生成 nonce，写 `agent_confirms(user, token, tool, args_hash, expires_at=now+5min)`，返回：
   ```json
   {"confirm_required": true, "confirm": "<nonce>", "preview": "将吊销 zhangsan 的 3 个令牌：…", "expires_in": 300}
   ```
2. 第二次调用带 `confirm`：校验 nonce 未用、未过期、user/token/tool 一致、`args_hash`（去掉 `confirm` 后规范化 JSON 的 sha256）一致 → 标记已用 → 执行。
3. tool description 写明：「先调用拿到 preview，把 preview 原样展示给用户并得到同意后，再带 confirm 调用」。DSH 审批框在第二次调用时也会弹，`displayReason` 带上 preview。

被定为 danger 的：所有 DELETE、吊销令牌、设角色、改权限点、审核通过 / 驳回、系统设置与 LDAP 写入、插件策略、连接工具开关、上游同步。

### 5.5 工具清单的实时更新

在已有 `hubBroker` 上增加触发点，向相关会话推 `tools/list_changed`：

- 用户角色 / 权限点变化（受影响用户）。
- 令牌范围变化、管理模式开启 / 结束 / 到期（该令牌）。
- 连接与授权变化（已有）。

## 6. 向导与说明（阶段 B、D）

以「系统技能」下发，复用现有技能目录：服务端生成，不入 `assets` 表、不经审核，`GET /api/dsh/catalog` 按 Principal 拼进 `skills`。

| 技能 | 对谁 | 调用方式 | 内容来源 |
|---|---|---|---|
| `dsh-ee-guide` | 有 `agent` 范围的人 | 模型可调用 | 企业名、可用模型与额度规则、能力中心用法、`ee_*` 工具的使用约定（何时查询、何时要确认） |
| `dsh-ee-doctor` | 同上 | `/dsh-ee-doctor` | 诊断流程：先 `ee_local_doctor` 与 `ee_doctor`，按结果逐项修复 |
| `dsh-ee-share` | 有发布权限的人 | 模型可调用 | 把对话成果整理成技能 / 提示词 / 微应用的规范与范例 |
| `dsh-ee-admin` | 管理模式中 | 模型可调用 | 管理工具约定、危险操作两步确认的话术、审核检查清单 |
| `dsh-ee-setup` | 管理模式中 | `/dsh-ee-setup` | 首次部署清单，配合新增 `GET /api/admin/setup-status`（返回每一步是否已完成：访问地址、供应商、默认模型、额度、LDAP、角色、连接、接入插件） |

技能正文由模板 + 当前设置渲染，设置变化时 `revision` 变化，DSH 自动刷新。

## 7. 网页改动

| 文件 | 改动 |
|---|---|
| `pages/device.tsx`（新） | 设备授权确认页 |
| `pages/my-keys.tsx` → `pages/tokens.tsx` | 「我的令牌」：新旧令牌合并列表、生成、吊销、管理模式（输入密码） |
| `pages/admin/keys.tsx` → `pages/admin/tokens.tsx` | 「令牌管理」：筛选、下发、批量吊销 |
| `pages/admin/users.tsx` | 机器人账号：类型、负责人；不能设密码 |
| `pages/setup.tsx` | 第一步运行命令，第二步在 DSH 里登录；手动令牌折叠为「无浏览器环境」 |
| `pages/admin/audit.tsx` | 来源列与令牌 / 设备筛选 |
| `pages/admin/dashboard.tsx` | 用量按令牌、机器人账号拆分 |
| `pages/admin/connect-settings.tsx` | 登录方式开关、设备令牌有效期、Agent 总开关、是否允许手动令牌 |
| `i18n/{zh,en}/tokens.ts`（新） | 文案 |

## 8. 安全设计

| 威胁 | 防护 |
|---|---|
| 令牌泄露 | 存本机凭据库（同一 OS 用户可读）；默认无 admin；按设备吊销；滑动过期 |
| 越权：令牌权限超出账号 | 每次请求现查 RBAC 再与 scope 求交；不存储「令牌权限快照」 |
| 提示注入诱导写操作 | 写操作 DSH 审批；危险操作服务端两步确认；Agent 无法开启管理模式、无法批准设备码 |
| 确认码重放 / 换参数 | nonce 一次性、5 分钟、绑定用户 + 令牌 + 工具 + 参数哈希 |
| 设备码钓鱼（诱导别人批准你的设备码） | 确认页显著展示设备名、系统、IP 与申请的范围；码 10 分钟过期；批准后在「我的令牌」里出现并可吊销 |
| 设备码暴力猜测 | user_code 8 位、31 字符表（约 8.5×10¹¹）；批准接口需要会话；`/token` 轮询限流 |
| 机密进入模型上下文 | LDAP 密码、供应商 API key、连接凭据的字段不进工具入参也不进返回（`shape` 白名单；写入类 action 不暴露这些字段） |
| 机器人账号被当人用 | 不能网页登录、不能设备登录；只能管理员下发令牌 |

## 9. 分阶段任务

| 阶段 | 服务端 | 插件 | 网页 | 测试 |
|---|---|---|---|---|
| **A** | 0008 迁移；`principal.go` 统一解析并替换 4 处鉴权；令牌 CRUD 与下发接口；设备码接口；机器人账号；额度按令牌；接入包 patch 改为 `apiKeyEnv` + 去掉 mcp-client 行 | `dsh-plugin/` 源码化与构建；`auth.ts`；`http.ts`；技能提供方搬迁；MCP 桥只接企业连接 | device、tokens、admin/tokens、users、setup | 4 个 spike；10.1～10.3 中 A 的部分 |
| **B**（已完成，见 [阶段 B 完成报告](agent-connect-phase-b-report.md)） | 路由表重构；agentSpec 框架与内部转发；员工侧工具；`/api/dsh/whoami`；`ee_doctor`；系统技能 guide / doctor / share；审计 via；两步确认框架（从 C 提前） | 桥接入 `ee_*`；审批钩子；`ee_local_doctor`；本地 upload | audit 来源列 | 覆盖率测试；员工工具集成测试；真实 DSH 场景 B |
| **C**（已完成，见 [阶段 C 完成报告](agent-connect-phase-c-report.md)） | 管理模式（elevate、计时器、list_changed）；管理侧工具；两步确认；`shape` 白名单 | 审批 displayReason 带 preview | tokens 页管理模式 | 安全矩阵测试；真实 DSH 场景 C |
| **D**（已完成，见 [阶段 D 完成报告](agent-connect-phase-d-report.md)） | `setup-status`；系统技能 admin / setup；审核 inspect | — | — | 真实 DSH 场景 D |

## 10. 验证方式

### 10.1 自动化测试分层

| 层 | 位置 | 跑在哪 | 覆盖 |
|---|---|---|---|
| Go 单元 | `internal/api/*_test.go`（纯函数部分）、`internal/rbac` | CI | scope 求交、args 规范化与哈希、user_code 生成与解析、参数 → 请求拼装、JSON Schema 生成 |
| Go 集成 | `internal/api/tokens_test.go`、`device_test.go`、`agent_test.go` | CI（TiDB / OceanBase 两种库，沿用现有测试库） | 见 10.2、10.3 |
| 插件单元 | `dsh-plugin/test/*.test.ts` | CI | 用假 ctx（credentials / authorization / tools / on）与假 dsh-ee：登录轮询（pending → slow_down → 成功）、401 清凭据、list_changed 后注册 / 销毁、审批钩子对 read / write / danger 的判断 |
| 浏览器 E2E | `e2e/tests/10-tokens.mjs`、`11-agent.mjs` | CI | 见 10.4 |
| 真实 DSH | `e2e/dsh/`（把 `.tools/spike` 的脚本收编进仓库） | 手动 / 每晚（需要从公网装 DSH） | 见 10.5 |
| 回归 | 现有 Go 测试 + E2E 01～09 | CI | 改鉴权后旧令牌、旧 key、npm 源、网关、能力中心全部照常 |

### 10.2 关键集成测试用例（Go）

**令牌与设备码**

- 设备码全流程：code → token 返回 pending → 网页批准 → token 返回令牌 → 再取一次返回 `expired_token`（只能取一次）。
- 轮询过快返回 `slow_down`；过期返回 `expired_token`；拒绝返回 `access_denied`。
- 同设备重复登录吊销旧令牌。
- 滑动续期：模拟 `last_used_at` 超过 1 天后 `expires_at` 前移；过期后 401 带 `token_expired`。
- 旧令牌兼容：`ee_npm_`（read / publish）、`sk-ee-` 在 `/v1`、`/npm`、`/api/dsh`、`/mcp` 上的行为与改造前逐条一致（用表驱动测试把现状固化下来，**改造前先写**）。
- 额度：令牌额度先到 → 429；账号额度先到 → 429；机器人账号用量不计入任何个人。

**权限矩阵（表驱动）**

行 = {员工, 部门管理员, 管理员} × {默认设备令牌, 带 admin 未开管理模式, 管理模式中, 管理模式已过期, 旧 npm 令牌}；列 = 每个 `ee_*` 工具的每个 action。断言：`tools/list` 中是否出现、`tools/call` 是否允许，与「RBAC ∩ scope ∩ 管理模式」的期望表一致。期望表由 agentSpec 自动推导，另有一小组手写的关键断言防止推导本身写错（例如「员工永远看不到 ee_users」）。

**Agent 工具**

- 覆盖率：遍历路由表，每条路由要么有 `agent`，要么在 `agentExcluded`（带原因）；`agentExcluded` 里不能有不存在的路由。
- 工具与网页一致：对每个 read action，同一用户用会话调接口与用令牌调工具，结果（经 `shape` 前）一致。
- 两步确认：不带 confirm 只返回 preview 且无副作用；带正确 confirm 执行；重放、过期、换参数、换用户、换令牌全部拒绝。
- 审计：工具执行产生的审计行 `via='agent'`、`token_id`、`client` 正确；网页操作仍是 `via='web'`。
- list_changed：改角色、开启 / 结束管理模式后，已连接的 SSE 会话收到通知（沿用 hub_test 里的 SSE 读取工具）。
- 机密不外泄：对 settings / ldap / providers / connections 的所有 read action，断言返回里不含密码、API key、连接凭据（扫描已知测试机密字符串）。
- 体积预算：员工、管理员两种 Principal 的 `tools/list` 序列化大小不超过预算。

### 10.3 安全专项

- 用 A 用户的令牌 + B 用户的 confirm nonce → 拒绝。
- 机器人账号走网页登录 / 设备批准 → 拒绝。
- 给没有任何管理权限的账号的令牌加 `admin` scope → 接口拒绝。
- 吊销令牌后，已建立的 `/mcp` 会话下一次调用即 401（不依赖会话缓存）。
- 设备码 `/token` 在未批准时不泄露任何用户信息；`GET /api/device/{code}` 需要会话。

### 10.4 浏览器 E2E

`10-tokens.mjs`

1. 员工登录网页；用 `fetch` 模拟 DSH 调 `/api/device/code`；打开 `verification_uri_complete`，确认页显示设备名与范围，截图；点「授权」。
2. 模拟 DSH 轮询拿到令牌，用它调 `/v1/models`、`/api/dsh/catalog` 成功。
3. 「我的令牌」里出现这台设备，吊销后再调返回 401 `token_revoked`。
4. 管理员建机器人账号、下发令牌、设额度；运营统计里机器人账号单独成行。
5. 管理员给自己的令牌开启管理模式（输入密码），列表显示到期时间。

`11-agent.mjs`（直接对 `/mcp` 发 JSON-RPC，模拟 Agent）

1. 员工令牌 `tools/list`：有 `ee_me`、`ee_hub`，没有 `ee_users`。
2. `ee_hub.enable` 一个技能 → 能力中心页面上变成「已在 DSH 生效」。
3. `ee_hub.create` 一个提示词 → 审核页出现，来源显示「经 Agent」。
4. 管理员令牌未开管理模式：看不到 `ee_users`；开启后看到，`ee_users.create` 建用户；`ee_admin_tokens.revoke` 不带 confirm 只返回 preview，带 confirm 后生效。
5. 审计日志页按来源「Agent」筛选，看到以上操作。
6. i18n 测试（06）覆盖新页面。

### 10.5 真实 DSH 端到端

在仓库里新建 `e2e/dsh/`：`run.sh`（隔离 `DSH_HOME`、起测试用 dsh-ee、假 LLM、假 MCP）、`fake-llm.mjs`（按剧本返回工具调用，已有）、`scenarios/*.json`（剧本）。每个场景断言 dsh-ee 侧的数据库记录与假 LLM 收到的请求。

| 场景 | 剧本 | 断言 |
|---|---|---|
| A1 登录 | 运行 `setup.sh`（不带令牌）→ 启动 DSH web → 触发登录 flow → 脚本代替人调用批准接口 | 凭据库里有 `DSHEE_TOKEN`；`.env` 里没有；模型请求带的是新令牌 |
| A2 换令牌 | 网页吊销 → DSH 发一次请求 | DSH 提示重新登录；重新登录后不重启就能继续对话（S2） |
| A3 兼容 | 老方式：`.env` 写 `ee_npm_` 令牌 | 一切照旧 |
| B1 员工自助 | 剧本：`ee_me.overview` → `ee_hub.search` → `ee_hub.enable` | 订阅记录存在；第二轮对话的系统技能 / 工具列表包含新技能 |
| B2 分享 | 剧本：`ee_hub.create`（prompt） | 审批钩子触发（web profile 下用自动批准的测试配置，或 S4 确定的方式）；审核队列有一条 via=agent |
| B3 诊断 | 故意删掉 `.npmrc` 里的令牌 → `/dsh-ee-doctor` 剧本：`ee_local_doctor` | 结果里 npm 一项为 ❌ 且给出修复建议 |
| C1 管理 | 开管理模式 → 剧本：`ee_users.create` → `ee_admin_tokens.revoke`（两次调用） | 用户已建；第一次调用无副作用；第二次带 confirm 后令牌已吊销 |
| C2 到期 | 管理模式 1 分钟（测试配置）后再调 `ee_users.list` | 工具已从列表消失；直接调用被拒 |
| D1 部署向导 | 空库 + 管理模式 → `/dsh-ee-setup` 剧本按清单调用 | `setup-status` 全部完成；员工按 A1 能接入 |

### 10.6 人工验收清单（每阶段结束时）

- 在 web profile 的真实界面里各走一遍：登录 flow 的提示是否清楚、审批框文案（中 / 英）是否说清楚要做什么、危险操作的 preview 是否可读。
- 用一个真实模型（DeepSeek）而非假 LLM 跑一遍员工向导与管理向导，检查模型是否按约定先预览后确认、是否会乱调工具；把发现的问题回写到系统技能里。
- 截图归档到 `e2e/shots/`（不入库）。

### 10.7 上线与回滚

- 迁移只加表加列，旧令牌全部兼容，可以直接升级。
- 接入插件新版本发布后，员工的 DSH 下次检查更新时升级；老版本插件仍能用（`ee_npm_` + `.env`），只是没有登录与 Agent 工具。
- Agent 功能有总开关（系统设置），关闭时 `/mcp` 不返回 `ee_*` 工具，系统技能不下发。
- 回滚 dsh-ee 二进制时，新列与新表被忽略；已发出的 `ee_at_` 令牌在旧版本上不可用，需要员工改回 `.env` 方式——发布说明里写明。
