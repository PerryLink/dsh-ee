# AGENTS.md

给在这个仓库里干活的 Agent（Claude Code、Codex 等）。

- 要写 dsh-ee 扩展（把企业系统接进来）：照 [extensions/skills/dsh-ee-extension-dev/SKILL.md](extensions/skills/dsh-ee-extension-dev/SKILL.md) 做。
- 要配权限（字段、访问规则、管理约束）：照 [extensions/skills/dsh-ee-access-config/SKILL.md](extensions/skills/dsh-ee-access-config/SKILL.md) 做。
- 设计文档在 `docs/`：扩展是 [plugins.md](docs/plugins.md)，协议是 [extension-protocol.md](docs/extension-protocol.md)，权限是 [access-control.md](docs/access-control.md)，让 Agent 做开发是 [agent-dev.md](docs/agent-dev.md)。
