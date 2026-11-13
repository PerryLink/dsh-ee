# Claude Code（MCP-only）接入 dsh-ee

Claude Code 自带模型与 Agent 循环；这里只把 dsh-ee 当作**远程 MCP 工具服务器**，
让 Claude Code 调用企业连接/知识库/`ee_*` 工具。不涉及 dsh-ee 的模型网关，也不使用
Anthropic Messages 协议以外的任何 dsh-ee 能力声明。

## 1. 准备令牌

在 dsh-ee 网页「我的令牌」创建，或由管理员签发，至少需要：

| scope | 用途 |
|---|---|
| `connections` | 企业连接工具、`/mcp` |
| `agent`（可选） | `ee_*` 工作台工具 |
| `hub`（可选） | 读能力目录 |

第三方 Harness 建议由管理员签发 `client_kind = "third_party"` 的令牌（#132）。
令牌以 `ee_at_` 开头，按密码保管。

## 2. 增加 MCP 服务器

当前 Claude Code 支持 streamable HTTP MCP + 自定义 `Authorization` 头。以官方文档为准：
<https://code.claude.com/docs/en/mcp>。

命令行（推荐，令牌只在 shell 环境里出现一次）：

```sh
claude mcp add --transport http dsh-ee "$DSH_EE_BASE/mcp" \
  --header "Authorization: Bearer $DSH_EE_TOKEN"
```

项目内共享配置（`.mcp.json`，不要提交真实令牌；环境变量展开方式以 Claude Code 版本文档为准）：

```json
{
  "mcpServers": {
    "dsh-ee": {
      "type": "http",
      "url": "https://dsh-ee.example.com/mcp",
      "headers": {
        "Authorization": "Bearer ${DSH_EE_TOKEN}"
      }
    }
  }
}
```

检查连接：

```sh
claude mcp list
# 或在会话里 /mcp 查看 dsh-ee 的连接状态与工具
```

## 3. 工具面说明

- 工具名形如 `crm__lookup`（企业连接）、`kb__search`（知识库）、`ee_me`（工作台 Agent 工具）。
  可见范围由令牌 owner 的部门/角色/连接授权决定。
- 工具多时 Claude Code 由服务端 `tools/list` 分页返回（#128 合并后）；旧部署一次返回全量。
- 写操作需要员工在 dsh-ee 网页确认：工具结果会带确认链接（文本 + 结果级
  `_meta["dsh-ee/confirmation"]`）。确认后需用完全相同的参数再次调用才会执行。
- 429 限流由 dsh-ee 按令牌计数，会带 `Retry-After`；Claude Code 的 MCP 客户端会自行处理重试策略。
- Claude Code 侧不要配置 dsh-ee 的 `/v1` 为模型供应商：它是 OpenAI chat completions 子集，
  不是 Anthropic API。

## 4. 排查

| 现象 | 处理 |
|---|---|
| `401` / 登录失效 | 重新签发 `ee_at_` 令牌；确认 `Authorization: Bearer` 头已带上 |
| `403`「没有使用工作台的权限」 | 令牌缺 `connections`，或账号没有 portal 权限 |
| 看不到某个连接的工具 | 检查该连接的可见范围与令牌 owner 的部门/角色 |
| 写操作反复要求确认 | 每次参数变化都会重新发起确认；确认后 10 分钟内、同参数只执行一次 |
| `429` | 按 `Retry-After` 退避，或找管理员调整令牌额度/限流 |

> 说明：dsh-ee 未声明对 Claude Code 的 Anthropic Messages 协议或任何专有 API 的兼容；
> 此示例仅覆盖 MCP 工具面。
