# 非 DSH 客户端接入（L1）：curl / 自研 Agent 怎么用

> 对应 e2e：`e2e/tests/28-l1-client.mjs`。这条链路不安装 DSH，只用**一个统一令牌**访问模型网关和 `/mcp`，适合 curl、脚本、自研 Agent 或企业集成方。

## 1. 准备令牌

在网页「我的令牌」生成，或由管理员在「令牌管理」下发。令牌以 `ee_at_` 开头，至少需要这些 scope：

| 用途 | scope |
|---|---|
| 读模型 / 调用模型 | `models` |
| 使用 `/mcp` 的 Agent 工具 | `agent` |
| 使用 `/mcp` 的企业连接工具 | `connections` |
| 读能力目录（可选） | `hub` |

不要把管理员令牌写进共享脚本；给每个集成方单独发令牌，按需最小化 scope。

```sh
export BASE='https://dsh-ee.example.com'
export TOKEN='ee_at_…'
```

## 2. 读模型目录

```sh
curl -fsS "$BASE/v1/models" \
  -H "Authorization: Bearer $TOKEN" | jq '.data[].id'
```

返回的是 dsh-ee 的**对外模型名**，不是上游厂商的模型名。

## 3. 调用模型

```sh
curl -fsS "$BASE/v1/chat/completions" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "company-chat",
    "messages": [{"role": "user", "content": "ping"}]
  }'
```

协议是 OpenAI chat completions 子集；额度、审计、上游故障转移都由 dsh-ee 处理。

## 4. MCP 握手与工具列表

`POST /mcp` 是 JSON-RPC；`Accept` 要同时带 JSON 和 SSE：

```sh
curl -fsS "$BASE/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1.0"}}}'
```

拿到 `Mcp-Session-Id` 后列工具：

```sh
curl -fsS "$BASE/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'
```

工具名形如 `crm__lookup`（企业连接）、`kb__search`（知识库）、`ee_me`（Agent 工具）；具体可见范围由令牌 owner 的部门 / 角色 / 连接授权决定。

## 5. 常见错误

| 现象 | 原因 | 处理 |
|---|---|---|
| 401 `token_invalid` / `token_revoked` | 令牌错误、过期或被吊销 | 重新生成，别复用旧值 |
| 403「没有使用工作台的权限」 | 令牌缺少 `/mcp` 需要的 `connections`，或账号没有 portal 权限 | 补 scope / 找管理员开权限 |
| 404 模型不存在 | 用了上游名或未导入的模型 | 先 `GET /v1/models` 看对外名 |
| 429 | 触发租户 / 令牌额度或上游限流 | 退避重试，或找管理员调额度 |

## 6. 与 DSH 的关系

同一套令牌也可以给 DSH 的接入插件用；DSH 只是多了一层本机 profile 和 `/ee-login` 设备确认。非 DSH 客户端不需要装插件，但**没有** DSH 的技能目录、工作台、自动更新等能力，只覆盖 L1 的模型与 MCP。

安全提示：令牌等同密码；示例里的 `$TOKEN` 不要写进仓库、日志或截图。需要按人撤销时，用管理员「令牌管理」按前缀/用户吊销。

## 附录：发现文档字段与入口（契约 v1 速查）

`GET /.well-known/dsh-ee`（匿名）是运行时事实源：下面的地址、scope、模型与应用以它和 `/v1/capabilities` 的返回为准。从 0 接入的步骤（H1/H2、Claude Code / Codex 等配置）见 [harness-integration.md](./harness-integration.md)，本文只列协议字段，不重复接入流程。

### 发现文档字段

| 字段 | 说明 |
|---|---|
| `name` / `version` / `api_version` | 服务名（固定 `dsh-ee`）、构建版本、契约主版本（当前 `1`） |
| `auth` | `type:"bearer"`、`header:"Authorization: Bearer <token>"`、`token_types:["ee_at_"]`、`token_flows:["device_code","web_manual"]` |
| `device_authorization_endpoint` / `token_endpoint` | 设备码流程：`/api/device/code`、`/api/device/token`（公开） |
| `scopes_supported` | 设备码可申请的 scope（`admin` 只能网页限时开启，不在其中） |
| `models.protocol` / `models.streaming` / `models.endpoint` / `models.chat_completions_endpoint` | OpenAI chat completions 子集；模型列表与对话补全的实际地址 |
| `mcp.transport` / `mcp.endpoint` / `mcp.protocol_versions` | MCP Streamable HTTP 端点与支持的协议版本 |
| `apps.web` / `apps.data_tools` / `apps.endpoint` / `apps.web_url` | 微应用网页版（`/a/<slug>`）、数据工具、只读描述列表地址 |
| `capabilities_endpoint` | 当前令牌能力摘要 `/v1/capabilities` |
| `openapi_url` | 机器可读契约 `/v1/openapi.json`（OpenAPI 3.1，匿名只读） |
| `limits.max_request_bytes` / `limits.max_response_bytes` / `limits.max_mcp_request_bytes` | `/v1/chat/completions` 与 `/mcp` 的请求/响应体上限 |
| `errors.format` / `errors.documentation_url` / `errors.codes` | 中立统一错误壳形状、错误文档地址、当前稳定错误码 |

### 入口一览

| 入口 | 认证 | 用途 |
|---|---|---|
| `GET /.well-known/dsh-ee` | 匿名 | 发现文档（`ETag` + `private, max-age=300`） |
| `GET /v1/openapi.json` | 匿名 | 静态 OpenAPI 3.1 契约（`ETag` + `public, max-age=300`） |
| `GET /v1/capabilities` | Bearer 可选 | 当前令牌可用模型 / 工具计数 / 微应用 / 技能摘要 / revision |
| `GET /v1/apps`、`GET /v1/apps/{name}` | Bearer（`hub`） | 只读微应用描述 |
| `GET /v1/models` | Bearer（`models`） | 对外模型名 |
| `POST /v1/chat/completions` | Bearer（`models`） | 对话补全 |
| `/mcp`（POST / GET / DELETE） | Bearer（`connections`） | MCP JSON-RPC：初始化、工具列表与调用、事件流 |
| `POST /api/device/code`、`POST /api/device/token` | 匿名 | 设备码流程获取令牌 |

### 令牌获取方式

- `device_code`：`POST /api/device/code` 申请设备码，员工在浏览器打开返回的 `verification_uri_complete`（`/device?code=…`）确认，客户端再 `POST /api/device/token` 轮询领取；错误码 `authorization_pending` / `slow_down` / `access_denied` / `expired_token`，令牌只能取一次。scope 用发现文档的 `scopes_supported`。
- `web_manual`：员工在网页「我的令牌」生成，或管理员在「令牌管理」下发（需要网页会话，没有程序化签发/续期 API）。第三方长期集成建议按集成方单独发 `ee_at_` 令牌、最小化 scope。

### 版本、缓存与弃用

- 响应头 `Dsh-EE-API-Version: 1` 覆盖发现文档与所有 `/v1/*`；新增中立端点还接受请求头 `Dsh-EE-API-Version` 协商（缺省/`1`/`1.x` 接受，不支持的主版本返回 400 `unsupported_api_version` + `supported_versions`）。
- 发现文档带按请求推断的端点地址，只允许私有缓存（`private, max-age=300`）；`/v1/openapi.json` 是静态内容，可共享缓存（`public, max-age=300`）；`/v1/capabilities`、`/v1/apps*` 与令牌/权限相关，`Cache-Control: no-store`。
- 弃用策略：破坏性变更升 `api_version` 主版本、旧主版本保留，届时再通过 `Deprecation` / `Sunset` 响应头与变更日志通知。当前 v1 **没有**弃用计划，服务端不发送这些头，发现文档也不再声明该字段，避免虚假承诺。
- 每令牌限流与 `X-RateLimit-*` / `Retry-After` 尚未实现，发现文档/OpenAPI 不声明这些响应头；实现后会在同一契约里补充。
