# 扩展

dsh-ee 的扩展把企业自己的系统接进来（设计见 [docs/plugins.md](../docs/plugins.md)）。SDK、示例、规格和技能都在这个目录下，和服务端一起改、一起测。

| 目录 | 内容 |
|---|---|
| [examples/demo-declarative](./examples/demo-declarative) | 声明式扩展的示例：清单 + OpenAPI 文档，不写代码。复制它就能开始 |
| [examples/demo-extension](./examples/demo-extension) | 服务扩展的示例（Go）：用假数据把所有能力做一遍（工具、动态选项、validate、知识源、联邦检索、回调、错误码、扩展字段） |
| [sdk-go](./sdk-go) | 服务扩展的 Go SDK：MCP 服务端、验 ee_jwt、约定接口、清单自动生成 |
| [sdk-local](./sdk-local) | 端侧扩展的 TypeScript SDK：本机 MCP 服务、写操作确认、审计回报、webSignIn |
| [official/ee-jwt-adapter](./official/ee-jwt-adapter) | 企业系统一侧验 ee_jwt 的适配层样例 |
| [official/legacy-local](./official/legacy-local) | 端侧老系统模板：员工登录一次，带着他的 cookie 取数和提交 |
| [official/claude-code](./official/claude-code) | Claude Code 适配器：一条命令写好 `.mcp.json` 和技能 |
| [schemas](./schemas) | 机器能读的规格：清单的 JSON Schema |
| [skills/dsh-ee-extension-dev](./skills/dsh-ee-extension-dev/SKILL.md) | 给 Agent 的技能：写扩展（声明式、服务、端侧） |
| [skills/dsh-ee-access-config](./skills/dsh-ee-access-config/SKILL.md) | 给 Agent 的技能：配字段、访问规则和管理约束 |

写完用 `dsh-ee extension check <目录>` 检查，`--json` 给程序和 Agent 看。规格用 `dsh-ee schema list` / `dsh-ee schema get <名字>` 查，离线可用。

检查通过后，在管理后台「企业连接 → 安装扩展」里选扩展的 zip 包（或同时选中清单和接口文档），看过安装计划（请求会发往哪些地址、用谁的身份、哪些是写操作、要设置的参数）再装成企业连接。装过的同一个扩展（按 `id`）再装就是升级。

服务扩展装的时候填服务地址（dsh-ee 去取 `/.well-known/dsh-ee-extension.json`），装之前用 `dsh-ee extension test --run "go run ." <目录>` 按协议逐项测。端侧扩展是员工本机上的 npm 包，经企业 npm 源下发。协议见 [docs/extension-protocol.md](../docs/extension-protocol.md)。

改 `skills/` 下的技能时，同步改 `server/internal/extension/skills/` 下的副本（DSH 里作为系统技能下发，测试会核对两边一致）。
