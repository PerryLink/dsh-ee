# Claude Code 适配器

在一个项目目录里一条命令接好 dsh-ee：

```bash
node extensions/official/claude-code/setup.mjs --server https://ee.acme.com --dir . [--local @acme/legacy-oa-local]
export DSH_EE_TOKEN=<在 dsh-ee「我的令牌」里建的令牌>
claude
```

它做三件事：

- 把 dsh-ee 的 `/mcp` 写进 `.mcp.json`（Streamable HTTP）。令牌写成 `${DSH_EE_TOKEN}`，从环境变量取，不进文件；已有的别的 MCP 服务器不动。
- `--local` 列出的端侧扩展作为本机 MCP 服务写进去，带上 `DSH_EE_URL`、`DSH_EE_TOKEN`、`DSH_EE_HARNESS=claude-code`。
- 把 `extensions/skills/` 下的技能复制到 `.claude/skills/`。

**不会冒充 DSH**（不带 `X-DshEE-Client`）：Claude Code 可以被设成自动同意，所以写操作走 dsh-ee 的服务端确认，员工要在网页上同意（[docs/plugins.md](../../../docs/plugins.md) §7.5）。

`--dry-run` 只打印要写哪些文件。测试：`node --test test/*.test.mjs`。
