---
title: 错误与常见问题
status: draft
since: 2026-11-12
lang: zh-CN
---

# 错误与常见问题

按你正在做的事查：发布 DSH 插件 → §1；写/装 dsh-ee 扩展 → §2；接第三方 Harness → §3；文档与工具链 → §4；其余常见问题见 §5。页面里没写到的错误码，用 `dsh-ee schema get errors` 查。

## 1. 发布与审核（DSH 插件）

| 现象 | 常见原因 | 处理 |
|---|---|---|
| 发布 403 | 包名不在企业作用域下 | 改 `name` 或让管理员确认作用域 |
| 发布 409 | 版本号已存在（含被驳回、下架的版本） | 升版本号；版本发布后不可复用 |
| 审核页 `bundle` fail | 没有 `dsh.bundle` / `patch` 字段，或 patch 文件没进 `.tgz` | 检查 `files`，重新 `npm pack` 后确认包内容 |
| `secrets` fail | 包里含 `.env`、`.npmrc`、私钥或疑似 token | 删掉这些文件再发；自动检查会直接判失败 |
| 插件在 DSH 里不生效 | `cordis.patch.yml` 的 `name` 和包名不一致，或配置不符合 Schema | 对齐包名，按插件 Schema 修配置 |
| 安装时报 Node 版本错 | `engines.node` 和员工电脑的 Node 不匹配（Desktop 与 CLI 可能不同） | 放宽或修正 `engines.node`，按实际验证结果填写 |
| 只有界面、没有后端行为 | 浏览器端入口和 host 入口没有同时声明，或 `dsh.client.inject` 不完整 | 对照 [plugin-authors.md](../plugin-authors.md) §1 检查 `dsh.client` |
| 预检结果看不懂 | 不知道 `can_submit` / `will_publish` / `requires_review` 的含义 | 见 [plugin-publish.md](./plugin-publish.md) §2；硬冲突先修，warn 可在审核说明里解释 |

审核是人工流程：默认白名单模式下发布后进待审核，管理员通过才上架。dsh-ee 不做评分/认证，审核状态不代表上游三层的任何结论。

## 2. 扩展开发与安装

| 现象 | 常见原因 | 处理 |
|---|---|---|
| 安装扩展时提示「端侧扩展不在这里安装」 | 端侧扩展是 npm 包，不是 zip/服务地址 | 发布到企业 npm 源、按插件下发；见 [extension-local.md](../extension-local.md) |
| `extension check` 退出码 1 | 清单或接口文档有错误 | 加 `--json` 看每条 `code` / `where` / `fix` |
| 服务扩展 `test` 不通过 | 清单和 MCP 工具不一致、验签失败、约定接口缺实现、凭据外泄 | 先修 `check`，再用 `dsh-ee extension test` 逐项定位；协议见 [extension-protocol.md](../extension-protocol.md#6-错误码) |
| 端侧扩展写操作没有确认就执行了 | 工具误标了 `readOnly`，或本机进程绕过了 SDK | `check` 会提示可疑的 `readOnly`；弱保证的边界见 [extension-local.md](../extension-local.md) |
| 员工调用返回 `authorize` | 员工还没授权，或授权过期 | 告诉员工去连接页面重新授权（错误码定义见 [extension-protocol.md](../extension-protocol.md#6-错误码)） |
| 员工调用返回 `setup` | 管理员没配好共享凭据或配置 | 找管理员补配置，不要让员工反复重试 |
| 联邦检索时某个来源被跳过 | 该扩展返回 `unavailable`，或超过超时 | 查扩展健康状态；dsh-ee 按协议熔断，不重试（见 [extension-protocol.md](../extension-protocol.md#6-错误码)） |
| 写操作一直不生效 | 新版本新增了写操作工具，尚未重新确认；或确认被拒 | 管理员重新确认安装；员工被拒的确认不会重放 |

扩展检查项的完整清单可离线查：`dsh-ee schema list`、`dsh-ee schema get errors`。

## 3. 接入与令牌（L1 / Harness）

| 现象 | 常见原因 | 处理 |
|---|---|---|
| 401 `token_invalid` / `token_revoked` / `token_expired` | 令牌错误、过期或被吊销 | 重新生成令牌，不要复用旧值 |
| 403 `permission_denied` / 「没有使用工作台的权限」 | 令牌缺 `models` 或 `/mcp` 入口需要的 `connections`，或账号没有对应权限点 | 补 scope 或找管理员开权限 |
| 403 `model_not_allowed` / 404 `model_not_found` | 模型不在可用列表，或对外模型没有可用上游 | 先 `GET /v1/models` 看对外名；仍失败找管理员检查模型接入 |
| 429 `rate_limit_exceeded`（`/v1`）/ JSON-RPC `-32000`（`/mcp`） | 该令牌的请求频率超过服务端限流（#129 合并后生效） | 按 `Retry-After` 秒数退避；用 `X-RateLimit-*` 提前降速 |
| 429 `monthly_quota_exceeded` 等 | 账号或令牌的月度额度用完，或触发上游限流 | 退避重试；持续出现找管理员调额度 |
| `/mcp` 返回 403 / 工具列表里没有某个工具 | 令牌缺 `connections`，或当前账号看不到该工具（可见范围由连接授权决定） | 补 scope；重新 `tools/list`；确认连接授权 |
| `/mcp` JSON-RPC `-32602` / `-32601` / `-32700` | 工具名不可见、方法不支持、请求体不是合法 JSON-RPC | 只用已实现的方法；重新 `tools/list`；检查请求体 |
| 写操作 `tools/call` 返回 `isError: true` + 确认链接（`_meta["dsh-ee/confirmation"]`） | 非 DSH 客户端调用了没标 `readOnly` 的工具，需要员工在网页确认 | 员工打开 `/confirm/<id>` 确认后，用**完全相同**的参数再调一次（10 分钟内有效，一次同意只能执行一次） |
| 端侧扩展调 `confirm` / `audit` 报错 | 扩展 id、工具名、版本或 code 格式不合法 | 按 [extension-protocol.md](../extension-protocol.md) §9 的字段核对 |

MCP 的无状态行为、`tools/list` 分页、`/mcp` 的 scope 要求与完整错误表见 [l1-client.md](../l1-client.md)；扩展侧错误码见 [extension-protocol.md](../extension-protocol.md#6-错误码)。

## 4. 文档与工具链

| 现象 | 原因 | 处理 |
|---|---|---|
| CI 的 `docs` job 失败 | 相对链接/锚点断了，或 SOURCES pin 表格式不对 | 本地跑 `node scripts/check-docs.mjs` 看具体文件和原因 |
| 新增文档没被检查到 | 文件不在 `docs/` 下，或不是 `.md` | 检查脚本只扫 `docs/**/*.md`；开发文档放 `docs/dev/` |
| `docs/dev/*.md` front matter 报错 | 缺 `title` / `status` / `since` / `lang`，或取值不合法 | 按 [docs/dev/README.md](./README.md) 的维护规则补 |
| 上游链接失效 | 上游改名/拆分（已发生过 `dsh-plugin-guide` 0.3 → `dsh-primer`） | 更新 [SOURCES.md](../plugin-dev/SOURCES.md) 的 pin 和引用页，不要先合再修 |

## 5. FAQ

**Q：错误码有站内索引页吗？**
当前没有独立的站内错误码页。面向机器和 Agent 的索引是 `dsh-ee schema get errors`（离线可用）；面向人的就是本页 + 各协议文档的错误码表。`/docs/errors` 是设计稿里规划的路由，产品页面尚未提供，链接先不要写进集成文档。

**Q：`/llms.txt` 在哪里？**
还没有实现。给 Agent 的机器可读信息目前用 `dsh-ee schema` 系列命令和 [extensions/skills](../../extensions/skills) 下的技能文件。

**Q：三层规范报错要去哪查？**
上游检查器的错误码与修复说明以上游文档为准；仓库内 `docs/plugin-dev/standards/checker-cli.md` 是按 CC BY 4.0 改编的摘要（带 Change Notice），细节按检查器自己的输出处理。边界见 [standards.md](./standards.md)。

**Q：文档里为什么没有完整的 ENTRY/FLOW/BOOST 条款？**
条款正文是 CC BY 4.0，仓库内在 `docs/plugin-dev/standards/` 按该许可要求做了改编摘要（逐篇 Change Notice，随附 `LICENSE-CC-BY`），不整篇搬运；需要原文请到上游仓库对应版本查看，链接在 [SOURCES.md](../plugin-dev/SOURCES.md)。

**Q：文档的版本和代码版本怎么对应？**
每篇文档 front matter 有 `since`；上游材料按「版本 + commit」pin。dsh-ee 自身版本用 `dsh-ee version` 或 `GET /healthz` 查看。
