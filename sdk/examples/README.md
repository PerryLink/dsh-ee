# 示例：把 dsh-ee 接进第三方 Harness

这些示例只使用本 SDK 覆盖的 **通用 HTTP/MCP 面**。请注意边界：

- **MCP-only 场景**（Claude Code、Codex 等自带模型的客户端）：dsh-ee 只作为远程 MCP 工具提供方。
  客户端仍用自己的模型；dsh-ee 的 `/v1/chat/completions` 不参与。
- **通用 Harness / 自研 Agent**：可以同时用模型网关（OpenAI chat completions 子集）和 MCP 工具。
- SDK **不实现** Anthropic Messages、OpenAI Responses 等协议，也不声明 Claude Code / Codex 的
  专有请求格式兼容；本目录只展示它们作为 MCP 客户端接入 `/mcp` 的配置。
- 未发布 npm / PyPI；示例从本仓库构建产物引入。

| 示例 | 场景 |
|---|---|
| [`mcp-claude-code.md`](mcp-claude-code.md) | Claude Code 以 MCP-only 方式接入 dsh-ee |
| [`mcp-codex.md`](mcp-codex.md) | Codex CLI 以 MCP-only 方式接入 dsh-ee |
| [`generic-harness.mjs`](generic-harness.mjs) | 通用 Harness：发现 → 模型网关 → MCP 工具（可运行） |
| [`browser-cors.md`](browser-cors.md) | 浏览器内 Harness 的 CORS 与鉴权片段 |

公共前提：

```sh
export DSH_EE_BASE='https://dsh-ee.example.com'
export DSH_EE_TOKEN='ee_at_…'   # 管理员签发或设备码登录获得
```

第三方 Harness 建议使用 `client_kind = "third_party"` 的令牌（#132）：写操作始终要求员工在网页
确认。令牌等同密码，不要提交到仓库或打印到共享日志。

构建 SDK（示例依赖 `sdk/typescript/dist`）：

```sh
cd sdk/typescript
npm ci --no-audit --no-fund
npm run build
```
