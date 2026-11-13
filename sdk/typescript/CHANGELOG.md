# Changelog

本包遵循语义化版本；`0.x` 期间不保证 API 完全稳定，破坏性变更会在 CHANGELOG 标注。
服务端契约版本见 [COMPATIBILITY.md](./COMPATIBILITY.md)。

## [Unreleased]

## [0.1.0] - 2026-11-13

首次加入本仓库（**未发布到 npm**）。

### Added

- `DshHarnessClient`：`baseUrl` / `token` / 可注入 `fetch` / 超时 / 默认头 / `onResponse` 观测钩子。
- 发现与能力：`discovery()`（ETag 缓存 + `If-None-Match` 304）、`capabilities()`、`listApps()`、`getApp()`。
- 模型网关：`listModels()`、`createChatCompletion()`（非流式）、`streamChatCompletion()`（SSE，跨 chunk/CRLF）。
- 设备码：`requestDeviceCode()`、`pollDeviceToken()`、`waitForDeviceToken()`（`slow_down` 自动退避、
  本地等待预算、AbortSignal）、`loginWithDeviceCode()`（自动保存令牌）。
- MCP：`connectMcp()`（initialize + `notifications/initialized`、`Mcp-Session-Id`、
  `MCP-Protocol-Version`）、`ping()`、`listTools()`（`limit`/`cursor`）、`listAllTools()`、
  `callTool()`、`close()`（DELETE `/mcp`）、SSE 响应解析。
- 写确认：`extractConfirmation()` / `isPendingConfirmation()` 解析
  `_meta["dsh-ee/confirmation"]`。
- 错误：`DshApiError` / `DshJsonRpcError` / `DshNetworkError` / `DeviceFlowError`，
  统一携带 `status`、`requestId`、`apiVersion`、`rateLimit`、`retryAfter`、`isRateLimited`。
- 限流头解析：`parseRateLimitHeaders()`、`parseRetryAfter()`；客户端记录
  `lastRequestId` / `lastRateLimit` / `lastApiVersion`。
- 测试：33 个 `node:test` 用例，全部 mock `fetch`；可选 `sdk/examples/generic-harness.mjs` 示例。
