# Changelog

本包遵循语义化版本；`0.x` 期间不保证 API 完全稳定，破坏性变更会在 CHANGELOG 标注。
服务端契约版本见 [COMPATIBILITY.md](./COMPATIBILITY.md)。

## [Unreleased]

## [0.1.0] - 2026-11-13

首次加入本仓库（**未发布到 PyPI**）。

### Added

- `DshClient`：`base_url` / `token` / 可注入 `transport` / 默认头 / 超时；记录
  `last_request_id` / `last_rate_limit` / `last_api_version`。
- 发现与能力：`discovery(refresh=False)`（ETag 缓存 + `If-None-Match` 304）、`capabilities()`。
- 模型网关：`list_models()`、`chat_completion(model, messages, **params)`（非流式）。
- 设备码：`request_device_code()`、`poll_device_token()`、`wait_for_device_token()`
  （`slow_down` 自动退避、本地等待预算）、`login_with_device_code()`（自动保存令牌）。
- MCP：`connect_mcp()`（initialize、`Mcp-Session-Id`、`MCP-Protocol-Version`）、`ping()`、
  `list_tools()`（`limit`/`cursor`）、`list_all_tools()`、`call_tool()`、`close()`（DELETE `/mcp`）。
- 写确认：`extract_confirmation()` / `is_pending_confirmation()` 解析
  `_meta["dsh-ee/confirmation"]`。
- 错误：`DshClientError` / `DshJsonRpcError` / `DshNetworkError` / `DeviceFlowError`，
  统一携带 `status`、`request_id`、`api_version`、`rate_limit`、`retry_after`、`is_rate_limited`。
- 限流头解析：`parse_rate_limit_headers()`、`parse_retry_after()`。
- 测试：19 个 `unittest` 用例，全部使用假 transport。
