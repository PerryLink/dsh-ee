---
title: 快速开始
status: draft
since: 2026-11-12
lang: zh-CN
---

# 快速开始

先用 5 分钟判断你属于哪条路径，然后按对应文档做。三条路径互不替代，也不要混用概念：**DSH 插件**是发布到 dsh-ee 的 npm 包，**dsh-ee 扩展**是把企业系统接进来的能力单元，**Harness** 是调用 dsh-ee 的客户端。

## 1. 三条路径

| 你的目标 | 走哪条 | 产物 |
|---|---|---|
| 把内部写的 DSH 插件上架到企业市场 | [DSH 插件发布](./plugin-publish.md) | 企业 npm 源里的插件包 + 审核记录 |
| 把 OA / 工单 / 知识库等企业系统接进来 | [扩展开发](./extensions.md) | 声明式 / 服务 / 端侧扩展，装成「企业连接」 |
| 让自研 Agent、Claude Code、脚本调用 dsh-ee | [Harness 接入](./harness.md) | 接入令牌 + `/v1` 模型、`/mcp` 工具调用 |

## 2. 最短路径：DSH 插件（企业托管）

以下是最小骨架，字段含义、可选项和失败原因见 [plugin-publish.md](./plugin-publish.md)。不要照抄版本号，`engines` 按你实际验证过的 DSH 线填写。

```json
{
  "name": "@company/dsh-my-plugin",
  "version": "0.1.0",
  "main": "./lib/index.js",
  "files": ["lib", "cordis.patch.yml", "README.md", "LICENSE"],
  "license": "MIT",
  "engines": { "dsh": ">=0.2.1-0 <0.3.0", "node": "^22.19.0 || >=24.9.0" },
  "dsh": { "bundle": { "patch": "cordis.patch.yml" } }
}
```

1. 构建产物放进包（`files` 必须包含 `cordis.patch.yml` 和构建结果）。
2. 在网页「分享插件 → 网页上传」选 `.tgz` 先点**预检**，看 `can_submit` / `will_publish` / `requires_review`。
3. `npm publish --registry https://<dsh-ee>/npm/`（或继续网页上传），等待审核；默认白名单模式下审核通过才上架。
4. 版本号发布后不可复用，被驳回也要升版本再发。

## 3. 最短路径：扩展

```bash
# 在 dsh-ee 仓库里，用示例复制一个扩展
dsh-ee extension init ./my-ext --id acme/my-ext --kind declarative

# 写清单和实现，然后本地检查（静态，不执行代码）
dsh-ee extension check ./my-ext --json

# 服务扩展：真的跑起来按协议测；声明式扩展只需要 check + pack
dsh-ee extension test ./my-ext --run "go run ."

# 查规格和错误码（离线可用）
dsh-ee schema list
dsh-ee schema get manifest
```

详细区别（声明式 / 服务 / 端侧）、凭据模式和写确认见 [extensions.md](./extensions.md)。服务扩展协议、端侧扩展协议见 [extension-protocol.md](../extension-protocol.md)。

## 4. 本地起一个 dsh-ee

完整步骤以仓库根 [README](../../README.md#development) 的 Development 一节和 [server/README.md](../../server/README.md#本地开发) 为准。要点：

- 依赖：Go 1.27+、Node 22 + pnpm，以及本地 TiDB 或 OceanBase。
- 首次启动会建表并创建管理员；把 `server/.env.example` 复制成 `.env.local`，填好 `DSH_EE_SECRET_KEY` 和管理员密码。
- 前端 `pnpm dev` 默认连本机 `:8080` 的服务端；数据库用 TiDB/OceanBase 单机即可。
- 演示数据用 `go run ./cmd/seed`（角色、员工、配额、上游、示例微应用）。

## 5. 工具链速查

| 命令 | 用途 |
|---|---|
| `dsh-ee version` | 打印版本 |
| `dsh-ee schema list` / `dsh-ee schema get <名字>` | 离线查清单、错误码等规格 |
| `dsh-ee extension init` | 从示例复制一个扩展，带 `AGENTS.md` |
| `dsh-ee extension check` / `test` | 静态检查 / 协议一致性测试（`check` 有错误时退出码 1） |
| `dsh-ee extension dev` | 起一个不需要数据库的模拟 dsh-ee，开发时用 |
| `node scripts/check-docs.mjs` | 检查文档相对链接、锚点和上游 pin 表 |

## 6. 注意

- 不要用真实令牌填进示例、脚本或截图；本地调试用「我的令牌」里新建的接入令牌，按最小 scope 发放。
- 预检只做静态检查，**不执行插件代码**，也不等于安全审计或任何层级认证。
- 上游 ENTRY / FLOW / BOOST 的使用边界见 [standards.md](./standards.md)，不要把它写进企业准入规则。
