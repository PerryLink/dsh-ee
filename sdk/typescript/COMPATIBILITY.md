# 兼容性说明

本文件说明 `@dsh-ee/harness-client` 与 dsh-ee 服务端、MCP 协议和工具面的兼容边界。
服务端契约变更会先反映在 dsh-ee 仓库的 PR 与本文件，再随 SDK 版本发布。

## 1. SDK 与 dsh-ee API 版本

| 项 | 值 |
|---|---|
| SDK 版本 | `0.1.0` |
| dsh-ee API 主版本 | `api_version = 1` |
| 请求头 | `Dsh-EE-API-Version: 1`（`/v1/*` 与 `/.well-known/dsh-ee`） |
| 响应头 | `Dsh-EE-API-Version`、`X-Request-Id`、`X-RateLimit-*`、`Retry-After` |
| 版本协商 | 服务端只接受主版本 `1`；不支持时返回 `400` + `code: "unsupported_api_version"` + `supported_versions` |

SDK 按主版本 1 实现。服务端新增主版本时，SDK 会以新版本发布，不静默猜测。

## 2. 服务端能力依赖（在途 PR）

本仓库当前基线（`20aafbb`）上，下列能力由在途服务端 PR 提供；**旧部署上对应功能不可用**，
SDK 会按服务端实际响应退化，不影响基础端点：

| 客户端能力 | 服务端来源 | 未合并时的表现 |
|---|---|---|
| `discovery()` / `capabilities()` / `/v1/apps` / `Dsh-EE-API-Version` | #123 | `404`（SDK 抛 `DshApiError`） |
| MCP `tools/list` cursor 分页、写确认 `_meta` | #128 | 一次返回全量、无 `nextCursor`；确认信息只在文本里 |
| `/v1/*`、`/mcp` 的 `X-RateLimit-*` / `Retry-After` / `X-Request-Id` | #129 | 无这些响应头；SDK 不自动重试 |
| 令牌 `client_kind`（`dsh` / `third_party`） | #132 | 旧令牌按 `dsh` 语义；第三方写确认建议重新签发 |

`e2e/tests/35-harness-sdk.mjs` 对这些能力做了 mock/live 分层：mock 层不依赖服务端 PR；
live 层在探测到端点/响应头后才断言，未合并时打印跳过原因。

## 3. 模型面（OpenAI chat completions 子集）

- `GET /v1/models`：`{object:"list", data:[{id, object, owned_by}]}`。
- `POST /v1/chat/completions`：请求体按 OpenAI chat completions 形状转发；非流式返回上游 JSON，
  `stream: true` 时返回 SSE（`data: {...}`、最后 `[DONE]`），服务端强制
  `stream_options.include_usage = true`。
- 错误壳：OpenAI `{"error":{"message","type","code"}}`；HTTP 429 带 `Retry-After` 与
  `X-RateLimit-*`（#129 合并后）。
- **不支持** Anthropic Messages、OpenAI Responses 等其它协议；也不兼容 Claude Code / Codex
  的专有请求格式。Claude Code / Codex 只能作为 MCP 客户端使用 `/mcp` 工具面，见
  [`../examples/`](../examples/)。

## 4. MCP 面

| 项 | 值 |
|---|---|
| 端点 | `POST /mcp`（JSON-RPC 2.0，streamable HTTP） |
| 传输 | `Accept: application/json, text/event-stream`；POST 响应支持 JSON 或 SSE |
| SDK 默认协议版本 | `2025-06-18`（可用 `mcpProtocolVersion` 覆盖） |
| 服务端支持版本 | `2025-11-25`、`2025-06-18`、`2025-03-26`（以发现文档 `mcp.protocol_versions` 为准） |
| 会话 | initialize 响应头 `Mcp-Session-Id`，后续请求回带；`MCP-Protocol-Version` 随协商结果发送 |
| `tools/list` 分页 | 默认 100/页、单页上限 500、`limit: 0` 取全量、`nextCursor` 不透明（#128 合并后） |
| 写确认 | 需要人工确认时结果级 `_meta["dsh-ee/confirmation"] = {id,url,expires_at}`；`content`/`isError` 旧兼容（#128 合并后） |
| 限流 | 429 + JSON-RPC `-32000`，另带 `Retry-After` / `X-RateLimit-*`（#129 合并后） |

SDK 只实现客户端常用方法：`initialize`、`notifications/initialized`、`ping`、`tools/list`、
`tools/call`、`DELETE /mcp`。不实现 MCP 的 GET 事件流（持久 `notifications/tools/list_changed`）、
resources/prompts、sampling/roots。

## 5. 运行环境

| 项 | 要求 |
|---|---|
| Node.js | `>= 18`（全局 `fetch` / `ReadableStream` / `TextDecoder`） |
| 浏览器 | 支持 `fetch` 与 Fetch Streams；需要服务端配置 CORS 白名单（#127，`DSH_EE_CORS_ORIGINS`） |
| 打包格式 | ESM（`type: "module"`，`exports` 指向 `dist/index.js`，类型 `dist/index.d.ts`） |
| 运行时依赖 | 无 |

## 6. 兼容性策略

- SDK 对 dsh-ee 的 API 主版本做显式校验；服务端返回 `400 unsupported_api_version` 时原样暴露。
- SDK 新增能力优先做成可选/可探测（例如 rate limit 头缺失不报错），避免旧部署直接失败。
- 写操作/工具调用**不自动重试**；是否重试由调用方按幂等性决定，429 的 `Retry-After` 由调用方使用。
- 破坏性变更（例如强制新的 API 主版本）会升 SDK major 版本并在 CHANGELOG 标注。
