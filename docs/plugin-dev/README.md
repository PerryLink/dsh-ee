# DSH 插件开发指南（dsh-ee 作者参考层）

> **来源与许可**
> - 来源：dsh-ee 仓库原创整理（`docs/plugin-dev/README.md`）；本页不改编上游正文，只列索引、术语与企业侧差异。被索引的页面各自在文件头标注上游来源、版本 @ commit、许可与修改说明。
> - 许可：本页随 dsh-ee 仓库以 Apache-2.0 提供（见仓库根 `LICENSE`）。
> - 版权：本页为原创整理，不对上游材料主张版权；引用的事实、链接与节选归其各自作者所有（见各页脚注）。
> - 修改说明：新增。记录写作时核实的 dsh-ee 基线（`20aafbb`）与上游来源版本。
> - 同步日期：2026-11-12（快照；上游更新后需复核，见 §7）。
>
> 本目录是**社区指南的本地参考层**，不是 DeepSeek Harness（DSH）官方文档，也不是 dsh-ee 的认证、评分或安全审计。DSH 插件生态以官方文档和上游仓库为准。

## 0. 这份文档是什么、不是什么

**是**：给在 dsh-ee 里分发 DSH 插件的作者看的一层参考——把上游社区指南（`dsh-primer`、`dsh-plugin-guide`，均为 Apache-2.0）中稳定、可迁移的部分搬到本仓库，补上 dsh-ee 企业侧差异，让作者不用先跳三个仓库才能开始。

**不是**：

- 不是 DSH 官方文档，也不替代官方文档；DSH 的可执行事实（精确签名、服务键、事件 `@mode`、slot 名）以目标宿主版本为准。
- 不是 dsh-ee 扩展（extension）教程。**DSH 插件 ≠ dsh-ee 扩展**，见 §2。
- 不是认证、评分、排名或安全审计。本目录不出现「官方 / 认证 / 推荐 / 通过」一类结论；`static ≠ runtime`、`not-run ≠ pass`。
- 不是全量镜像。官方文档、第三方 MIT 镜像、下载包、社区榜单/徽章不在本仓库分发，只给 canonical 链接。

## 1. 阅读路径

| 你是 | 从这里开始 | 然后 |
|---|---|---|
| 第一次写 DSH 插件 | [quick-reference.md](./quick-reference.md)（骨架、ctx、事件、红线） | [dev-loop.md](./dev-loop.md) → [release-checklist.md](./release-checklist.md) |
| 要把插件发到 dsh-ee | 本页 §3（企业侧差异）→ 现有 [`docs/plugin-authors.md`](../plugin-authors.md) | [release-checklist.md](./release-checklist.md) §6 |
| 宿主升级 / 插件装不上 | [version-matrix.md](./version-matrix.md) | [migration.md](./migration.md) → [errors.md](./errors.md) |
| 排错 | [errors.md](./errors.md) | [faq.md](./faq.md) |
| 发布安全 / 权限自查 | [security.md](./security.md) | [quality-layers.md](./quality-layers.md) §4 |
| 维护多个包 / 持续发布 / 回滚 | [release-engineering.md](./release-engineering.md) | [release-checklist.md](./release-checklist.md) |
| 想知道 ENTRY/FLOW/BOOST 与企业审核的关系 | [quality-layers.md](./quality-layers.md) | 现有 [`docs/plugin-quality.md`](../plugin-quality.md) |
| 只想查命令 | [cli-matrix.md](./cli-matrix.md) | — |

先看一条日历事实：DSH 还在开发者预览期、发版频繁（上游 npm 版本全部是 prerelease）。本目录所有版本数字都是**写作时快照**，用前先跑 `dsh --version`，再回 [version-matrix.md](./version-matrix.md) 对号。

## 2. 术语：四组容易混的东西

| 词 | 指什么 | 谁维护 / 在哪 | 和 dsh-ee 的关系 |
|---|---|---|---|
| **DSH 插件**（plugin） | cordis 运行时上的 npm 包，导出 `name` + `apply(ctx, config)`；通常带 `dsh.bundle.patch` | DeepSeek Harness 生态；本目录大部分内容讲它 | **是 dsh-ee 企业托管/镜像的发布对象** |
| **DSH bundle** | 插件包的一种分发形态：`package.json` 的 `dsh.bundle.patch` 声明组合补丁，装进 profile 的 bundle 层栈 | 同上 | 同上；dsh-ee 预检会检查 `dsh.bundle` / patch 文件 |
| **dsh-ee 扩展**（extension） | 企业连接能力：`declarative`（清单 + OpenAPI）、`service`（企业自部署服务）、`local`（员工机端侧） | dsh-ee 自身；见仓库 [`docs/plugins.md`](../plugins.md) | **不是 DSH 插件**，不要用 `docs/plugin-authors.md` 的 DSH 插件流程去发布 |
| **dsh-ee 企业包** | 发布到 dsh-ee 企业 npm 作用域（默认 `@company/...`）的包，经预检与审核后上架 | dsh-ee 自身；见 [`docs/plugin-authors.md`](../plugin-authors.md) | DSH 插件在企业内的分发形态 |

一句话：**DSH 插件跑在员工机的 DSH 宿主里；dsh-ee 负责准入、npm 源、审核、可见范围、权限与审计。dsh-ee 扩展是 dsh-ee 自己的企业连接模型，和 DSH 插件是两套对象。**

## 3. 写给 dsh-ee 作者：哪些是 DSH 通用，哪些是企业侧

本目录搬运的是 **DSH 通用**内容；发布到 dsh-ee 时有四个额外环节，细节以仓库代码和现有文档为准（本节只做导航与差异提示）。

### 3.1 发布对象与企业 npm

- dsh-ee 有两种插件来源：**上游镜像**（已在公共 npm 上，管理员预载后镜像进企业源，不改包内容）和**企业托管**（内部插件，作者发布到企业作用域）。
- 企业托管用企业 npm 源发布：`npm publish --registry https://<dsh-ee>/npm/`，包名必须在系统设置的企业作用域下；也可以在网页「分享插件 → 网页上传」上传 `.tgz`。
- 包名、版本号、`dsh.bundle` 规则与公共 npm 一致；额外要求见 [`docs/plugin-authors.md`](../plugin-authors.md) §1。

### 3.2 预检（preflight）

- 网页发布前先点**预检**，接口是 `POST /api/me/packages/check`（请求体就是 `.tgz`）；只解包检查，**不写库、不占用版本号**（`server/internal/api/package_check.go` 注释与 `registry.Check`）。
- 结果字段：`can_submit`（无硬冲突，发布接口会接受）、`will_publish`（开放收录模式且无 fail 项时直接上架）、`requires_review`（进审核），另有 `scan`（逐项检查）与 `conflicts`（包名/版本/作用域/同名包/归属/版本占用）。
- 当前检查项（`server/internal/registry/tarball.go:208` `Check`，2026-11-12 基线）：`bundle`、`patch`、`cordis`、`cordis-peer`、`skills`、`install-scripts`、`secrets`、`license`、`engines`、`dependencies`、`size`。结果只有 pass / warn / fail，**不执行插件代码**。
- 预检不是 ENTRY/FLOW/BOOST 判定，也不是安全审计；它只覆盖 dsh-ee 企业准入需要的静态项。

### 3.3 审核、可见范围与版本纪律

- 默认「白名单」模式：发布后是 `pending`，管理员通过后才进 packument、tarball 和插件索引；「开放」模式无 fail 项直接上架，有 fail 项仍进审核。
- 上架后管理员可设可用范围（部门 / 角色 / 人）与下发方式（自选 / 自动安装）。
- **版本号不可复用**：被驳回、下架的版本号都不能重发，修完必须升版本号；且 `npm publish` 后同一版本永远不能重发。
- 企业侧还有一个 DSH 没有的约束：`engines.dsh` 会被 dsh-ee 预检记录并在审核页展示；没有声明会 warn。当前 dsh-ee v0.1 代码注释写明的推荐宿主是 **DSH 0.2.2-rc.1（Cordis 4.1.0）**（`server/internal/registry/cordis_check.go:9-11`），兼容性矩阵本身仍在待跑状态（[`docs/compatibility.md`](../compatibility.md)）。

### 3.4 DSH 宿主侧与企业侧的分工

| 问题 | 谁回答 | 看哪 |
|---|---|---|
| 插件能不能被识别、安装、启动 | DSH 宿主 + 上游检查器 | [quality-layers.md](./quality-layers.md) ENTRY 段、`dsh-standards` |
| 包能不能进企业源、要不要审核 | dsh-ee 预检 + 审核 | 本页 §3.2–3.3、[`docs/plugin-authors.md`](../plugin-authors.md) |
| 员工能不能用、可见范围 | dsh-ee 管理员配置 | dsh-ee 页面与 [`docs/access-control.md`](../access-control.md) |
| 企业系统怎么接进来（不是 DSH 插件） | dsh-ee 扩展协议 | [`docs/plugins.md`](../plugins.md) |

## 4. 文档索引与上游来源

每篇文件头都有完整的来源/版本 @ commit/许可/版权/修改说明/同步日期，文末有一行脚注。表里只列主来源（补充来源见各页文件头）。

| 文件 | 内容 | 主来源（版本 @ commit，Apache-2.0） |
|---|---|---|
| [quick-reference.md](./quick-reference.md) | 一页速查：骨架、ctx、事件、cordis.yml、红线 | `PerryLink/dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `guide/quick-reference.zh-CN.md` |
| [version-matrix.md](./version-matrix.md) | 版本口径、peer 区间、破坏性断层、会话格式、dsh-ee 矩阵 | `PerryLink/dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `guide/version-matrix.md` |
| [cli-matrix.md](./cli-matrix.md) | 宿主 / 作者工具 / dsh-ee CLI 命令矩阵 | `PerryLink/dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `README-zh.md`、`guide/family-toolchain.md`；`dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） `docs/levels.md` |
| [dev-loop.md](./dev-loop.md) | 从零到发布的开发循环与本地验证 | `dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `guide/plugin-dev-guide.md`；`dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） `docs/release-checklist.md` |
| [migration.md](./migration.md) | 旧包名 0.3→`dsh-primer` 改名；0.2 世代迁移清单 | `dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `MIGRATION.md`、`guide/migration-0.2.md` |
| [faq.md](./faq.md) | 常见问题（DSH 侧 + dsh-ee 侧） | `dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`）；`dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） |
| [errors.md](./errors.md) | 症状 → 原因 → 处理；怎么读检查报告 | `dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `guide/plugin-dev-guide.md` §7.3；`dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） `docs/rules.md` |
| [quality-layers.md](./quality-layers.md) | ENTRY / FLOW / BOOST 指南层概览与证据纪律 | `PerryLink/dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） `docs/levels.md`、`entry.md`、`flow.md`、`boost.md` |
| [release-checklist.md](./release-checklist.md) | 发布前自查清单（DSH 侧 + dsh-ee 侧） | `dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） `docs/release-checklist.md` |
| [security.md](./security.md) | 信任边界、最小权限、凭据、egress、文件/子进程/动态执行、提示注入、依赖与发布安全 | `PerryLink/dsh-plugin-guide` 0.5.5（tag `v0.5.5` = `9f0a8a1`） `docs/security.md` |
| [release-engineering.md](./release-engineering.md) | 多包契约、版本线、发布流水线、README parity、registry 行为、回滚与 tag 门禁 | `PerryLink/dsh-primer` 0.4.3（tag `v0.4.3` = `8750d31`） `guide/release-engineering.zh-CN.md` |

## 5. 旧包名改名：`dsh-plugin-guide` 0.3.x → `dsh-primer`

这件事必须先说清，否则会把旧链接当当前指南：

| 时间线 | 包 / 仓库 | 是什么 |
|---|---|---|
| 0.3.x（至 0.3.42） | `dsh-plugin-guide`（旧 npm 包，已 deprecated） | 旧的知识库 + `dsh-plugin-dev` CLI 技能；最后一个 npm 版本 0.3.42，包名不再更新 |
| 0.4.0+ | `dsh-plugin-guide`（新仓库/新 npm 包） | **另一个产品**：ENTRY/FLOW/BOOST 指南层与 guide 技能；不是 0.3.x 的兼容升级 |
| 0.4.x 起 | `dsh-primer` | 原 0.3.x 知识库 + CLI 的继任者；CLI 命令 `dsh-primer`，旧命令 `dsh-plugin-dev` 保留为 legacy alias |

- 上游 `MIGRATION.md` 明确写：`dsh-plugin-guide` 0.4.0 **不是**旧 agent skill 的兼容更新；旧代码留在历史里，旧 npm 版本仍可安装。
- 本目录的来源 pin 只用**公开 tag/commit**：`dsh-primer` 0.4.3 = tag `v0.4.3` / commit `8750d31718c16601514e59fe1831de73cfed3d20`（npm 发布记录 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`），`dsh-plugin-guide` 0.5.5 = tag `v0.5.5` / commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`；与仓库 `docs/plugin-dev/SOURCES.md` 的 pin 表使用同一组公开 commit。旧的 `dsh-plugin-guide@0.3.39` 只用于对照，**不 pin、不搬**。
- 升级 profile 时注意：锁在 `^0.3` 的 profile 不会自动升到 `dsh-primer`；换包名后要显式 `dsh plugin add dsh-primer` 并移除旧包。

## 6. 本目录**不**搬的内容

为避免把企业产品写成社区推广页、也避免再分发未获授权的材料，以下内容一律不进入本目录，只保留 canonical 链接：

- 营销/榜单/star CTA、插件家族计数与互链、下载量、市场徽章、追踪像素。
- 官方文档的 MIT 镜像（上游 `references/official-docs/` 是全文副本，本仓库不复制）；只链接官方文档站与上游仓库。
- 上游 `downloads/`、`references/` 快照、社区仓库源码归档、内部限定内容（上游 NOTICE 已声明部分内容禁止再分发）。
- 第三方徽章/CI 徽章、社区排行榜、认证等级。
- `dsh-standards` / `dsh-plugin-spec` 的条款正文（正文许可为 **CC BY 4.0**，与指南自有文本的 Apache-2.0 不同）；本目录只做映射摘要并链接。

如需完整规范与检查器，请直接使用上游 `dsh-standards`（代码 Apache-2.0、正文 CC BY 4.0）与官方文档。

## 7. 版本同步与维护

- 每个文件头记录 **上游仓库/路径 + 版本 @ commit + 许可 + 版权 + 修改说明 + 同步日期**；pin 只用公开 tag/commit（不引用本地未推送分支的 commit），文末脚注给出公开的固定 commit 链接。核对方式：在 GitHub 上打开对应 commit 的 blob，或 `git show <commit>:<path>`。
- 触发复核的事件（任一发生就应更新本目录，通常单独提 PR）：
  1. `dsh-primer` / `dsh-plugin-guide` 发 npm 新版本；
  2. dsh-ee 调整推荐/支持的 DSH 线（当前参考 `server/internal/registry/cordis_check.go` 与 [`docs/compatibility.md`](../compatibility.md)）；
  3. 上游改名/挪仓（已经发生过一次：0.3 → 0.4 拆分 + 改名 `dsh-primer`）。
- 本目录是**快照**，不是「永远最新」的承诺；任何与上游冲突之处，以目标宿主实际行为与上游固定版本原文为准，并在 PR 说明里标注分歧。
- 版权口径：只对可核实的上游材料按 Apache-2.0 保留署名（`© 2026 <上游> contributors`，并注明上游 LICENSE 未填写版权主体）；**不把上游材料写成 PerryLink 全资版权**，也不替 dsh-ee 声明未经确认的版权主体。

---

> 本页为 dsh-ee 原创索引，随本仓库以 Apache-2.0 提供；不含上游改编正文，引用的事实与链接归其各自所有者。同步日期 2026-11-12。
