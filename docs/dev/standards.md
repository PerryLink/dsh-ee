---
title: 规范与检查（ENTRY / FLOW / BOOST）
status: draft
since: 2026-11-12
lang: zh-CN
---

# 规范与检查：ENTRY / FLOW / BOOST 是什么、不是什么

上游社区维护三条采用深度轨道（ENTRY / FLOW / BOOST），并配套检查器与条款集。dsh-ee 做**企业侧映射与按许可的改编参考**（`docs/plugin-dev/standards/`，逐篇带 Change Notice），不做认证、评分、排名或准入门槛。规范正文的版权归上游，许可与版本见 [SOURCES.md](../plugin-dev/SOURCES.md)。

## 1. 三条轨道分别回答什么

| 轨道 | 回答的问题 | dsh-ee 侧的对应 |
|---|---|---|
| **ENTRY** | 插件能不能被识别、安装、启动 | 发布前预检覆盖包结构、清单、依赖、密钥等静态项；**不执行插件** |
| **FLOW** | 装好之后用户旅程顺不顺（安装、配置、使用、升级、卸载） | 企业侧的审核、可见范围、下发、升级策略；作者自己的体验证据由作者提供 |
| **BOOST** | 有没有用好 Cordis 的原生能力 | dsh-ee 不做运行时基准，也不把 BOOST 当准入条件 |

三层各自的判定、检查项 ID 和证据状态（`recognized`、`static`、`runtime`、`not-run` 等）以上游为准。这里的表只用于说明 dsh-ee 侧落在哪，**不是规范正文**。

## 2. 不是什么

- 不是三个等级，不能换算成分数、评级或证书；也没有「通过三层」这种总判定。
- 不是 dsh-ee 的准入规则：dsh-ee 的安装/上架只看自己的预检与审核状态。
- 不是安全审计：上游的 `security` scope 和 dsh-ee 的密钥/安装脚本扫描都只是静态检查。
- 层名和徽章不随规范文本的 CC BY 许可授权，使用受上游 `governance/mark-policy.md` 约束：不做排名、不做门禁、同视图带免责声明。

dsh-ee 与三层的完整映射（包括为什么不在 dsh-ee 展示层级）见 [plugin-quality.md](../plugin-quality.md)。

## 3. 作者怎么本地自查

上游检查器是 Node 工具（需要 Node 24.9+），**只建议作者在自己的机器上跑**；dsh-ee 服务端不执行它。命令以固定版本的检查器文档为准，下面是常用形态：

```bash
# 固定版本安装，避免 npx 同名包
npm i -D dsh-standards@1.6.9

# 默认静态检查，输出 Markdown
npx dsh-standards check .

# 机器可读结果，便于 CI / 证据
npx dsh-standards check . --json

# 单文件 HTML 报告，零外部资源
npx dsh-standards check . --html dsh-check.html
```

读结果时注意：

- 退出码 `2` 表示结果 **incomplete**，不是检查失败；`not-run` / `NA` / `static` 都不等于 `pass`。
- `runtime` 检查会真的启动插件，在 Windows 上可能降级为 `degraded` / `not-run`。
- `npx` 里有同名但不同项目的包，不要按记忆敲命令，用本仓库安装的依赖或按检查器 README 的说明执行。

上游的命令、标志位和输出格式可能随版本变化；本文不复制其正文，使用前以 [SOURCES.md](../plugin-dev/SOURCES.md) 里 pin 的版本为准。

## 4. dsh-ee 的边界

- **正文改编按 CC BY 4.0 的要求**：上游规范正文是 CC BY 4.0，改编必须带 TASL + 显著位置的 Change Notice，并随附许可全文。仓库内 `docs/plugin-dev/standards/` 的参考文档就是按这一要求改编的（逐篇 Change Notice，附 `LICENSE-CC-BY`）；本文只做概览与链接，不复制条款、表格、图片。
- **不 vendor 检查器**：检查器代码是 Apache-2.0，但 dsh-ee 服务端是单文件 Go 二进制，不能内置 Node 运行时；也不在服务端或浏览器里执行上游脚本。
- **不做证据字段（当前）**：dsh-ee 没有存储作者层级/证据的字段；审核只按企业自己的规则和预检结果进行。

## 5. 相关链接

- dsh-ee 企业侧映射：[plugin-quality.md](../plugin-quality.md)
- 作者发布流程：[plugin-publish.md](./plugin-publish.md)
- 上游来源与许可：[SOURCES.md](../plugin-dev/SOURCES.md)、仓库根 [NOTICE](../../NOTICE)
- 检查器常见错误：[错误与 FAQ](./errors-faq.md)
