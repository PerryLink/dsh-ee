---
title: dsh-ee 扩展开发
status: draft
since: 2026-11-12
lang: zh-CN
---

# dsh-ee 扩展开发

dsh-ee 扩展把企业自己的系统接进来。它和「DSH 插件」是两套东西：DSH 插件是发布到 dsh-ee 的 npm 包，扩展是让 Agent/应用调用企业系统的「企业连接」。两套模型不要混用。

## 1. 四种形态

| 形态 | 跑在哪 | 适合 | 怎么写 |
|---|---|---|---|
| 声明式 | dsh-ee 按清单 + OpenAPI 调源系统 | 有 OpenAPI / REST 接口的系统 | 清单 + 接口文档，不写代码 |
| 服务 | 企业自己部署的 HTTP 服务 | 需要写逻辑、聚合、转换的系统 | Go SDK（`extensions/sdk-go`），实现 MCP + 约定接口 |
| 内置 | dsh-ee 服务端 | 飞书这类官方集成 | 在 `server/` 里实现 |
| 端侧 | 员工电脑上的 npm 包 | 只能用员工登录态、只在内网可达的系统 | 端侧 TS SDK（`extensions/sdk-local`），本机 stdio MCP |

设计取舍与四种形态的完整说明见 [plugins.md](../plugins.md) §4；端侧扩展单独见 [plugins.md](../plugins.md#8-端侧扩展) 与 [extension-local.md](../extension-local.md)。选型时先确认三件事：对方系统有没有接口文档、是否必须用员工本人身份、dsh-ee 服务端能否访问到它。

## 2. SDK、示例与规格

| 资源 | 位置 |
|---|---|
| 服务扩展 Go SDK | [`extensions/sdk-go`](../../extensions/sdk-go) |
| 端侧扩展 TS SDK | [`extensions/sdk-local`](../../extensions/sdk-local) |
| 声明式示例 | [`extensions/examples/demo-declarative`](../../extensions/examples/demo-declarative) |
| 服务扩展示例 | [`extensions/examples/demo-extension`](../../extensions/examples/demo-extension) |
| 端侧老系统模板 | [`extensions/official/legacy-local`](../../extensions/official/legacy-local) |
| 清单 JSON Schema 等规格 | [`extensions/schemas`](../../extensions/schemas) |
| Agent 技能 | [`extensions/skills/dsh-ee-extension-dev`](../../extensions/skills/dsh-ee-extension-dev/SKILL.md) |

两套 SDK 实现同一份协议：清单、`ee_jwt` 验签、MCP、约定接口、错误码。协议是唯一标准，见 [extension-protocol.md](../extension-protocol.md)（错误码见其 §6）；用别的语言自己实现时照协议做，再用 `dsh-ee extension test` 验证。

## 3. 开发流程

```bash
# 1. 从示例复制（会带上 AGENTS.md，Agent 打开目录就知道怎么做）
dsh-ee extension init ./my-ext --id acme/my-ext --kind declarative

# 2. 写清单 / 代码；规格不要凭记忆，查 schema
dsh-ee schema list
dsh-ee schema get manifest

# 3. 静态检查：清单、接口文档、只读标记、凭据写法；不执行代码
dsh-ee extension check ./my-ext --json

# 4. 按协议跑起来测（服务扩展）；全部通过才算合格
dsh-ee extension test ./my-ext --run "go run ."

# 5. 开发时起模拟 dsh-ee（不需要数据库），直接调工具看请求和日志
dsh-ee extension dev
dsh-ee extension call <地址> <工具> --as zhangsan --args '{}'

# 6. 交付材料
dsh-ee extension pack ./my-ext        # 声明式：zip + 校验和
dsh-ee extension report ./my-ext      # 给管理员看的交付说明
```

- `check` 有错误时退出码为 1；每条结果都有 `code`、`severity`、`where`、`message`、`fix`，Agent 可以照着改。
- `test` 用模拟环境按协议逐项调用（清单、验签、MCP、约定接口、错误码、凭据不外泄），再加目录里 `tests/*.yaml` 的场景测试。
- 远程调试（扩展在本机跑、连真实 dsh-ee）需要管理员开调试权限，设计见 [plugins.md](../plugins.md) §9.1、§10。

## 4. 凭据、写操作与确认

- **凭据模式**：`shared`（管理员配共享凭据）、`user_secret`（员工自己填）、`ee_jwt`（dsh-ee 给当前员工现签身份令牌）、`oauth`（标准 OAuth 或 `oauth/*` 钩子）。模式怎么选、凭据交给谁见 [plugins.md](../plugins.md) §7 与 [extension-protocol.md](../extension-protocol.md) §4。
- **写操作**：工具没标 `readOnly` 一律当写操作。服务端确认开关有 `off` / `non_dsh` / `always` 三档；端侧扩展在执行未标 `readOnly` 的工具前，必须先调 dsh-ee 的确认接口，员工在网页上同意后同样的参数再调一次才执行。
- **审计**：端侧扩展每次调用后回报摘要（扩展、版本、工具、耗时、成败），不回报参数和返回内容；这是弱保证，见 [extension-local.md](../extension-local.md)。
- 不要在清单里申请比需要更宽的权限，不要把凭据写进代码、日志或返回结果；`extension test` 会检查凭据外泄。

## 5. 安装与治理

1. 在管理后台「企业连接 → 安装扩展」上传 zip（声明式）或填服务地址（服务扩展），看过安装计划再装成企业连接。
2. 安装时设置可见范围（部门 / 角色 / 人）和凭据模式，并确认请求会发往哪些地址。
3. 服务扩展由企业自己升级；声明式扩展**目前没有自动升级和一键回滚**，升级就是在「企业连接 → 安装扩展」再装一次——旧版 zip / 清单要自己留档（按版本或 tag 保存），需要回退时重新上传旧版。**新版本新增的工具、写操作、权限、凭据模式变化先不启用**，等管理员重新确认。
4. 权限点：写扩展/调试相关的能力由 `extensions.develop` 等权限控制；安装和安装策略由管理员在设置里管。字段、访问规则与可见范围见 [access-control.md](../access-control.md)。

## 6. 与三层规范的关系

ENTRY / FLOW / BOOST 是上游作者/市场工具，**不是 dsh-ee 的准入等级**。dsh-ee 自己的预检（`dsh-ee extension check`、DSH 插件的 `POST /api/me/packages/check`）和三层检查器是三套不同的东西：

- dsh-ee 预检：企业侧安装/上架的静态检查；
- 上游检查器：作者本地自查用的 CLI（见 [standards.md](./standards.md)）；
- dsh-ee 不执行上游 checker，也不把结果映射成等级。

## 7. 下一步

- 查检查项和报错：[错误与 FAQ](./errors-faq.md)
- 协议细节：[extension-protocol.md](../extension-protocol.md)
- 设计稿（供理解取舍，不是使用文档）：[plugins.md](../plugins.md)、[agent-dev.md](../agent-dev.md)
