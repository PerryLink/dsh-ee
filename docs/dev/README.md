---
title: 开发文档索引
status: draft
since: 2026-11-12
lang: zh-CN
---

# dsh-ee 开发文档

本目录是 dsh-ee 面向使用者与开发者的文档正文来源（单一事实源）。`docs/` 下的其它文件大多是设计稿、评审记录和交付报告，不是这里的使用文档；`docs/plugin-dev/` 是按许可改编自社区指南/规范的上游参考层（来源与署名见 [SOURCES](../plugin-dev/SOURCES.md)），两者不要混读。

文档状态：`draft` 待评审 · `review` 评审中 · `stable` 已随版本核实。当前各篇均为 `draft`，内容按仓库当前代码整理，接口在 v0.x 仍可能变化。

## 按角色读

| 你要做的事 | 从这里开始 | 接着读 |
|---|---|---|
| 把内部 DSH 插件发布到 dsh-ee | [快速开始](./quickstart.md) | [DSH 插件发布](./plugin-publish.md) |
| 给企业系统写扩展（声明式 / 服务 / 端侧） | [扩展开发](./extensions.md) | [extension-protocol.md](../extension-protocol.md)、[plugins.md](../plugins.md) |
| 让第三方 Harness / 自研客户端接入 dsh-ee | [Harness 接入](./harness.md) | [l1-client.md](../l1-client.md) |
| 搞清 ENTRY / FLOW / BOOST 是什么、不是什么 | [规范与检查](./standards.md) | [plugin-quality.md](../plugin-quality.md) |
| 查错误码和常见失败 | [错误与 FAQ](./errors-faq.md) | — |

## 文档清单

| 文件 | 内容 | 状态 |
|---|---|---|
| [quickstart.md](./quickstart.md) | 5 分钟判断该走哪条路径；本地起 dsh-ee 与常用命令 | draft |
| [extensions.md](./extensions.md) | dsh-ee 扩展的四种形态、SDK、开发与安装流程 | draft |
| [harness.md](./harness.md) | 非 DSH 客户端（L1）的令牌、模型与 MCP 接入；DSH 插件接入 | draft |
| [plugin-publish.md](./plugin-publish.md) | DSH 插件发布到 dsh-ee：清单、预检、发布、审核 | draft |
| [standards.md](./standards.md) | 上游三层标准的边界；dsh-ee 只做映射与链接 | draft |
| [errors-faq.md](./errors-faq.md) | 发布、扩展、接入常见错误与 FAQ | draft |

## 术语表

| 术语 | 一句话定义 |
|---|---|
| DSH 插件 | cordis 插件（npm 包），dsh-ee 的发布对象；清单里用 `dsh.bundle` / `dsh.client` 声明形态 |
| 企业包 | 发布到企业 npm 作用域（如 `@company/...`）、经 dsh-ee 审核后上架的 DSH 插件 |
| dsh-ee 扩展 | 把企业系统接进来的能力单元，分声明式、服务、内置（`server/` 内）与端侧四种；见 [plugins.md](../plugins.md) §4、§8 |
| 端侧扩展 | 跑在员工电脑上的扩展（npm 包 + 本机 MCP 服务），见 [extension-local.md](../extension-local.md) |
| Harness | 调用 dsh-ee 的客户端运行时：DSH、Claude Code、Codex、自研 Agent 等 |
| ENTRY / FLOW / BOOST | 上游三条采用深度轨道，不是 dsh-ee 的等级、评分或认证；见 [plugin-quality.md](../plugin-quality.md) |
| 预检 | dsh-ee 对 `.tgz` 的静态检查（`POST /api/me/packages/check`），不执行插件代码，不等于任何层级判定 |

## 来源与许可

- 本目录文档为 dsh-ee 原创，随仓库按 Apache-2.0 发布；引用上游材料只给固定版本的链接，不复制正文、图片或条款。
- 上游来源、版本、commit、许可与修改说明：[plugin-dev/SOURCES.md](../plugin-dev/SOURCES.md)；仓库根许可声明：[NOTICE](../../NOTICE)。
- 文档里的外部链接按「版本 + commit」固定，不写 `main`；上游发版或改名时先更新 SOURCES。

## 维护规则

- 每篇必须有 front matter：`title`、`status`、`since`、`lang`；新增文档要同时在上面两个表登记。
- 相对链接必须能解析、锚点必须存在；pin 表行必须完整。本地运行 `node scripts/check-docs.mjs` 检查，CI 的 `docs` job 也会跑。
- 本目录只写 dsh-ee 视角的摘要与外链，不复制上游正文；仓库内按许可改编的上游参考统一放 `docs/plugin-dev/`，来源与署名登记在 SOURCES。
