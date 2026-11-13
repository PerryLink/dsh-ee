# 扩展协议 v1 概览

> 服务扩展、端侧扩展与 dsh-ee 之间协议的一页速查。规范正文是 [extension-protocol.md](../extension-protocol.md)；设计取舍与完整蓝图见 [plugins.md](../plugins.md)；错误码与排障见 [errors.md](./errors.md)。
>
> 状态标注：**已实现** 表示当前版本（`dsh-ee@20aafbb`）里能用；**设计/未实现** 表示只在设计稿里，不要按它开发。

## 1. 四种扩展形态

| 形态 | `kind` | 跑在哪 | 交付物 | dsh-ee 怎么调它 | 状态 |
|---|---|---|---|---|---|
| 声明式 | `declarative` | dsh-ee 进程内发 HTTP | 清单 `dsh-ee.extension.json` + `openapi.yaml`（+ 附带应用） | 按 OpenAPI 生成工具，dsh-ee 直接请求源系统 | 已实现 |
| 服务 | `service` | 企业自己部署的 HTTP 服务 | 一个实现了清单和约定接口的服务 | 取 `/.well-known/dsh-ee-extension.json`；工具走 `POST /mcp`，其余走 `POST /ee/<名字>` | 已实现 |
| 端侧 | `local` | 员工电脑上的 npm 包（stdio MCP） | npm 包 + 清单 | Agent 直连本机 MCP，不经过 dsh-ee；写操作前问 `/api/local/confirm`，每次调用后报 `/api/local/audit` | 已实现 |
| 内置 | （无清单） | dsh-ee 进程内 Go | 随 dsh-ee 升级 | 与服务扩展同一套内部接口，不走网络（如飞书文档知识源） | 已实现 |

「服务端不运行第三方代码」是这套协议的边界：声明式由 dsh-ee 发请求，服务扩展在企业自己的服务里，端侧在员工电脑上，内置是 dsh-ee 自己的代码。

## 2. 协议面速查

| 面 | 入口 | 说明 |
|---|---|---|
| 清单（服务扩展） | `GET <服务地址>/.well-known/dsh-ee-extension.json` | 安装时由管理员填服务地址，dsh-ee 从这里取；上限 1 MB |
| 清单（声明式 / 端侧） | 目录里的 `dsh-ee.extension.json` | zip 上传或网页选文件 |
| 工具 | `POST <服务地址>/mcp` | 标准 MCP（Streamable HTTP，只回 JSON）：`initialize`、`ping`、`tools/list`、`tools/call` |
| 约定接口 | `POST <服务地址>/ee/<名字>` | `validate`、`options`、`health`、`kb/*`、`oauth/*`、`endpoint`，见 §3 |
| dsh-ee → 扩展身份 | `Authorization: Bearer <ee_jwt>` | RS256、5 分钟、公钥在 `<dsh-ee>/.well-known/jwks.json`；`aud` 是清单 `id` |
| 交给扩展的凭据 | `X-DSH-EE-Credential` / `X-DSH-EE-Shared-Credential` / `X-DSH-EE-Config` | 员工凭据、共享凭据、管理员配置（secret 已解密） |
| 回调地址 | `POST <dsh-ee>/x/<随机串>/<path>` | 只在清单申请了 `endpoint` 权限、且声明了能力时有 |
| 端侧回报 | `POST /api/local/confirm`、`POST /api/local/audit` | 用员工自己的接入令牌认证；确认与审计共用同一张确认表和确认页 |

协议版本写在清单的 `"protocol": "1"`，不写当作 1。

## 3. 约定接口

| 接口 | 用途 | 状态 |
|---|---|---|
| `validate` | 管理员保存配置 / 共享凭据前检查 | 已实现 |
| `options` | 给清单里 `"options": "dynamic"` 的配置项实时给选项 | 已实现 |
| `health` | 「检查」按钮和每小时健康检查；同时发现清单变化 | 已实现 |
| `kb/browse`、`kb/changes`、`kb/document` | 文档库知识源同步（`knowledge.documents`） | 已实现 |
| `kb/search` | 联邦检索（`knowledge.search`，不复制内容，按员工权限实时查） | 已实现 |
| `oauth/authorize-url`、`oauth/exchange`、`oauth/refresh`、`oauth/revoke` | 不标准的 OAuth 流程钩子 | 已实现 |
| `endpoint` | 第三方系统打到 dsh-ee 的回调原样转给扩展 | 已实现 |
| `kb/download` | 网盘类知识源的文件下载（`knowledge.files`） | **设计/未实现**（清单校验认识这个能力，但没有调用方，不要声明） |
| `fields/suggest` | 自动建议扩展字段值 | **设计/未实现**（§6） |
| `audit/deliver` | 审计外发（syslog / Webhook 的内置实现） | **设计/未实现** |
| `authorize/batch` | 权限策略钩子 | **设计/未实现** |
| `org/changes` | 组织同步扩展点（带扩展字段） | **设计/未实现**；当前的组织同步是 LDAP / 飞书目录属性映射（已实现） |

接口字段、cursor 语义和错误格式以 [extension-protocol.md](../extension-protocol.md) §5、§6 为准。

## 4. 凭据与写操作

四种凭据模式（`credential.mode`）与请求头：

| 模式 | 适用 | 带什么 |
|---|---|---|
| `shared` | 只读报表、公共数据、知识同步、回调 | `X-DSH-EE-Shared-Credential` |
| `user_secret` | 员工各有 API token 的系统 | `X-DSH-EE-Credential`（原样） |
| `ee_jwt` | 企业自研系统或愿意加适配层的系统 | `Authorization: Bearer ee_jwt`（不落盘） |
| `oauth` | 飞书、钉钉、Confluence 这类 SaaS | 标准流程 dsh-ee 自己完成；不标准流程实现 `oauth/*` 钩子 |

- 服务扩展每次调用都带 `ee_jwt`；源系统的凭据按清单模式另放请求头。
- 写操作：工具没标 `readOnly` 就当写操作。调用方不是 DSH（或管理员把写操作确认设成 `always`）时，dsh-ee 不直接执行，先返回确认链接；员工在网页确认后同样参数再调一次才执行，一次同意只能执行一次。规则在 `server/internal/api/write_confirm.go`，确认方式可在「系统设置」改。
- 审计记扩展、版本、摘要、工具、凭据模式、耗时、成败，不记返回内容。

## 5. 限制与超时

| 项 | 值 | 证据 |
|---|---|---|
| 清单大小 | 1 MB | `extMaxManifest` |
| 约定接口响应 | 8 MB | `extMaxResponse` |
| 工具结果 | 64 KB（超出截断） | `connResultLimit` |
| 单次调用超时 | 默认 30 秒（沿用连接超时） | `defaultConnTimeout` |
| 连续失败熔断 | 5 次失败后熔断 30 秒，只影响这个扩展 | `extBreakerFails` / `extBreakerOpen` |
| 健康检查 | 每小时一次；清单摘要变化会记下，等管理员重新安装确认 | `runExtHealth` |
| 知识源同步 | 一轮最多 200 页、5000 篇；每个联邦来源 3 秒上限 | `kbSourceMaxPages` / `kbFederatedTimeout` |
| 回调 | 请求体上限 1 MB，每个地址每分钟 120 次；转发时去掉 Cookie / Authorization | `extEndpointBody` / `extEndpointPerMinute` |
| 写确认 | 待确认 15 分钟、同意后 10 分钟内有效、只执行一次 | `confirmPendingTTL` / `confirmApprovedTTL` |

## 6. 清单校验与协议测试

- 清单规格：`dsh-ee schema get manifest`（schema 编译进二进制，离线可查；仓库副本在 [`extensions/schemas`](../../extensions/schemas/README.md)）。
- 静态检查 `dsh-ee extension check <目录> [--json]`：62 条检查项，49 error / 10 warning / 3 info，其中 2 条可以在清单里用 `checkIgnore` 带理由忽略（`tool.readonly_suspect`、`tool.readonly_unmarked`）。退出码 0 通过 / 1 有问题 / 2 用法错误。
- 协议测试 `dsh-ee extension test <目录|地址> [--run "启动命令"] [--json]`：19 项一致性测试（清单、验签、MCP 工具一致性、每个声明了的能力、错误码、凭据不外泄、并发稳定性），再跑目录里 `tests/*.yaml` 的场景测试。退出码 0 全部通过 / 1 有失败 / 2 用法错误。
- 本机开发：`dsh-ee extension dev` 起一个只有 JWKS 和预置员工的模拟 dsh-ee；`dsh-ee extension call` 以某位员工身份调工具。都不需要数据库。
- 安装前检查与安装计划：`POST /api/admin/extensions/check` 只读检查；敏感变更（服务地址、反向权限、同版本内容变化、凭据模式、允许地址）必须 `confirm:true` 才会安装。

## 7. 实现状态

**已实现**：声明式 / 服务 / 端侧 / 内置四种形态；安装计划与敏感变更确认；`ee_jwt` / `oauth` / `user_secret` / `shared` 四种凭据；写操作服务端确认；回调地址；知识源同步与联邦检索；扩展附带字段；健康检查与熔断；`extension` 的 `init` / `check` / `test` / `dev` / `call` / `pack` / `report` / `schema` 命令。

**设计/未实现**（不要对外承诺）：

- 扩展签名与安装时验签（`extension sign` / `extension verify` 命令不存在；现在安装不校验签名）；
- 企业目录、自动升级、声明式扩展的版本保留与一键回滚（升级只能再装一次）；
- `extension init --kind local` 脚手架（端侧扩展请复制 `extensions/official/legacy-local`）；
- 网盘知识源的 `kb/download` 与 `knowledge.files`；
- 组织同步扩展 `org/changes` 带字段、权限策略 `authorize/batch`、字段填充 `fields/suggest`、审计投递 `audit/deliver`、事件 `events/*`、Agent 适配器 `agent/render` / `agent/config`。

## 8. 从哪里开始

| 你要做的事 | 看哪里 |
|---|---|
| 写服务扩展 / 端侧扩展 | [extension-protocol.md](../extension-protocol.md)（规范）、[`extensions/sdk-go`](../../extensions/sdk-go/sdk.go)、[`extensions/sdk-local`](../../extensions/sdk-local/README.md) |
| 让 Agent 完成扩展开发 | [`extensions/skills/dsh-ee-extension-dev/SKILL.md`](../../extensions/skills/dsh-ee-extension-dev/SKILL.md)、[agent-dev.md](../agent-dev.md) |
| 让第三方 Harness / 自研客户端接入 dsh-ee（模型、MCP、发现与能力摘要） | [l1-client.md](../l1-client.md) |
| 声明式扩展示例 / 服务扩展示例 | [`extensions/examples/demo-declarative`](../../extensions/examples/demo-declarative)、[`extensions/examples/demo-extension`](../../extensions/examples/demo-extension) |
| 查错误码、排查装不上 / 调不通 | [errors.md](./errors.md)（仓库级错误面索引见 [../errors.md](../errors.md)） |
| 看设计取舍与未来扩展点 | [plugins.md](../plugins.md) |
| 企业侧怎么发布、审核、授权与审计（管理员视角） | [enterprise-publishing.md](../enterprise-publishing.md) |
