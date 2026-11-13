# Codex CLI（MCP-only）接入 dsh-ee

Codex 自带模型与 Agent 循环；这里只把 dsh-ee 当作**远程 MCP 工具服务器**（streamable HTTP），
不涉及 dsh-ee 的模型网关，也不使用 OpenAI Responses API 之外的任何 dsh-ee 能力声明。

## 1. 准备令牌

同 [`mcp-claude-code.md`](mcp-claude-code.md)：至少 `connections`（可选 `agent` / `hub`），
建议由管理员签发 `client_kind = "third_party"` 的令牌（#132），按密码保管。

## 2. 配置 MCP 服务器

Codex 的 MCP 配置在 `~/.codex/config.toml`（项目级 `.codex/config.toml` 仅对受信任项目生效）。
远程 streamable HTTP 服务器用 `url` + `bearer_token_env_var`（变量名，不写令牌本身）。
以 Codex 当前文档为准：<https://github.com/openai/codex>。

```toml
[mcp_servers.dsh-ee]
url = "https://dsh-ee.example.com/mcp"
bearer_token_env_var = "DSH_EE_TOKEN"
```

启动 Codex 前导出令牌：

```sh
export DSH_EE_TOKEN='ee_at_…'
codex
```

也可以用 CLI 添加（不同 Codex 版本参数可能不同，以 `codex mcp add --help` 为准）：

```sh
codex mcp add dsh-ee --url "$DSH_EE_BASE/mcp" --bearer-token-env-var DSH_EE_TOKEN
```

检查：

```sh
codex mcp list
```

## 3. 工具面与注意事项

- Codex 会把 MCP 工具暴露给模型；工具名形如 `crm__lookup`、`kb__search`、`ee_me`。
  可见范围由令牌 owner 的部门/角色/连接授权决定。
- 工具多时由 dsh-ee 的 `tools/list` 分页返回（#128 合并后）；旧部署一次返回全量。
  本 SDK 不参与 Codex 的 MCP 客户端实现，分页/重连行为由 Codex 侧处理。
- 写操作需要员工在 dsh-ee 网页确认：dsh-ee 返回确认链接（结果级
  `_meta["dsh-ee/confirmation"]`），确认后同参数重试才执行。Codex 侧会把它当作工具结果展示。
- 429 限流会带 `Retry-After` / `X-RateLimit-*`（#129 合并后），由 Codex 的重试策略处理。
- 不要在 Codex 里把 dsh-ee 配成 **OpenAI Responses** 供应商：本 SDK/服务端都未实现该协议；
  dsh-ee 的模型面是 OpenAI chat completions 子集，且与 Codex 的模型配置无关。

## 4. 排查

| 现象 | 处理 |
|---|---|
| 连接失败 / 401 | 确认 `DSH_EE_TOKEN` 已导出、URL 是 `…/mcp`、令牌未过期 |
| 403 权限不足 | 令牌缺 `connections`，或账号没有 portal 权限 |
| 看不到工具 | 检查连接可见范围/部门角色；确认 dsh-ee 未被防火墙挡住 |
| 写操作反复确认 | 参数变化会重新确认；确认后 10 分钟内、同参数只执行一次 |

> 说明：dsh-ee 未声明对 Codex 的 OpenAI Responses 协议或任何专有 API 的兼容；
> 此示例仅覆盖 MCP 工具面。
