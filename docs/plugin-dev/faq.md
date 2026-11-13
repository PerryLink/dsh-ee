# 常见问题（FAQ）

> **来源与许可**
> - 主来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`README-zh.md`、`guide/plugin-dev-guide.md` §7.3、`guide/quick-reference.zh-CN.md`
> - 上游文件（固定 commit，公开链接）：[README-zh.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/README-zh.md)、[guide/plugin-dev-guide.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/plugin-dev-guide.md)、[guide/quick-reference.zh-CN.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/quick-reference.zh-CN.md)、[README.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/README.md)、[SKILL.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/SKILL.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`README.md` FAQ、`SKILL.md`、`docs/levels.md`（Apache-2.0）。dsh-ee 侧问答来自本仓库基线 `20aafbb` 的 `docs/plugin-authors.md`、`docs/plugin-quality.md`、`server/internal/registry/*.go`（自己的文档与代码事实）。
> - 修改说明：中文改写为问答体；合并上游分散的 FAQ/踩坑内容；删除营销、家族、社区数字与本地镜像路径；新增 dsh-ee 企业侧问答；不复述 `dsh-standards` 的 CC BY 条款。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档，也不是认证、评分或安全审计。涉及宿主行为的问题以目标版本官方文档与实际运行为准。

## A. 定位与许可

### A1. 这些文档的定位是什么？

不是。本目录是 dsh-ee 仓库内的**社区指南参考层**，正文主要改编自两个社区项目（`dsh-primer`、`dsh-plugin-guide`，均 Apache-2.0），文件头保留了来源、版本 @ commit、许可与修改说明。它不代表 DeepSeek 官方，也不代表 dsh-ee 的认证或背书。DSH 的权威事实以官方文档、目标版本代码与会话内 inspection 为准。

### A2. 为什么不直接整包搬上游内容？

三类原因：

1. **许可不同**：`dsh-primer`/`dsh-plugin-guide` 自有文本是 Apache-2.0，可以改编（保留署名与修改说明）；但 `dsh-standards`/`dsh-plugin-spec` 的**正文**是 **CC BY 4.0**，需要 TASL + Changes + 同视图免责，本目录只链接不复述。
2. **不搬未授权内容**：官方文档的 MIT 镜像、`downloads/` 快照、内部限定内容、社区仓库源码归档不在本仓库再分发。
3. **避免过期复制**：上游发版频繁，复制契约/API 必腐。本目录只搬「稳定、可迁移」的部分，其余给 canonical 链接。

### A3. dsh-ee 的「扩展」和这里说的「DSH 插件」是一回事吗？

**不是**：

- **DSH 插件** = cordis npm 包（`name` + `apply(ctx, config)`，通常带 `dsh.bundle.patch`），跑在员工机的 DSH 宿主里，发布到企业 npm 源。
- **dsh-ee 扩展** = 企业连接能力（`declarative` / `service` / `local`），用 `dsh-ee.extension.json` 清单和 `dsh-ee extension ...` 命令治理。

两者模型、生命周期、检查工具都不同。不要用 `docs/plugin-authors.md` 的 DSH 插件流程去发布 dsh-ee 扩展，也不要用 `dsh-ee extension check` 检查 DSH 插件。详见 [`docs/plugins.md`](../plugins.md) 与 [`docs/plugin-quality.md`](../plugin-quality.md)。

### A4. 那 dsh-ee 会不会给插件打分/认证？

不会。三层（ENTRY / FLOW / BOOST）是作者/市场工具，不是 dsh-ee 的评分、等级或认证；dsh-ee 的预检和审核只有 pass / warn / fail / 待审 / 已上架这些状态，不显示三层等级、不据此排名。`security` 是 `dsh-standards` 的一个 scope，不是第四层；预检的密钥/安装脚本检查也不是安全审计。详见 [`docs/plugin-quality.md`](../plugin-quality.md)。

## B. 安装与版本

### B1. 为什么 `dsh plugin add` 拒绝安装我的插件？

最常见原因是**兼容范围不匹配**：`dsh plugin add` 会拿运行时版本比对插件声明的 `@deepseek-ai/dsh-*` peer 范围（以及 `engines.dsh`，按较新口径该字段存在时优先），不匹配即拒绝安装，并打印声明的范围与豁免命令。

自救路径是按 `<包@精确版本> × <宿主版本>` 授予精确豁免（不是范围，宿主升级后要重新授予）：

```sh
dsh plugin --profile <p> version-exemptions
dsh plugin --profile <p> allow-version <pkg@ver> --dsh-version <runtime> --accept-risk
```

根本修法是把范围补全（每条发布线一个 clause），并注意范围只表达「能不能装」，表达不了某个 API 在不在——仍要运行时探测。详见 [version-matrix.md](./version-matrix.md) §5 与 [errors.md](./errors.md) §3。

### B2. 为什么 `pnpm add @deepseek-ai/dsh-tools` 装到很旧的版本？

因为**子包的 npm `latest` 标签可能是冻住的**（长期停在早期版本），`latest` 不代表当前版本。写死版本或显式钉 `next`/`alpha`，并在安装前查当期值。判断宿主代次只认 `dsh --version`，不要看子包版本号。见 [version-matrix.md](./version-matrix.md) §2.2。

### B3. 插件装上了，但行为没生效 / 日志里只有一行 `patch: entry <id> not found`？

说明你的 patch 覆盖打空了：上游删行/改名后，按 `id` 覆盖的层不再命中任何行，且**不报错**。修法：`dsh --profile <name> --dump-config` 对照实际行 id，改成 `insert` 或新 id；升级宿主后必跑一次。见 [dev-loop.md](./dev-loop.md) §3、[migration.md](./migration.md) §B.0。

### B4. 升级 DSH 后插件坏了，先做什么？

1. `dsh --version` 确认新线；
2. `dsh --profile <name> --dump-config` 查 `patch: entry not found`；
3. 对照 [migration.md](./migration.md) 的破坏性变更清单；
4. 更新 `engines.dsh` / peer 范围并升版本号；
5. 重跑静态检查与干净 profile 冒烟。

### B5. DSH 有稳定版吗？我该锁哪个版本？

上游 2026-10 的快照里，npm 全部版本都是 prerelease、GitHub Release 全部 `prerelease: true`；没有「稳定版」可等。作者策略是**钉版本 + 声明区间 + 运行时探测**。当前 dsh-ee v0.1 代码注释的推荐宿主是 DSH `0.2.2-rc.1`（Cordis 4.1.0），兼容矩阵仍在待跑状态。见 [version-matrix.md](./version-matrix.md) §1。

### B6. Node 版本要求是什么？

DSH 0.2.2 线要求 Node `>=24.9.0`；更早的 0.2.x 线可用 `^22.19.0`。dsh-ee 作者示例写 `^22.19.0 || >=24.9.0`。Node 版本不匹配时，安装可能失败或运行时静默异常；发布前在目标 Node 上跑一次干净 profile 冒烟。

## C. 开发

### C1. 最小可用的插件长什么样？

导出 `name` + `apply(ctx, config)`（可选 `inject`）；配置用 Schemastery `Schema<Config>`；所有注册走 `ctx.effect()`/`ctx.on()`/服务 `register()`；工具用 `defineTool`。完整骨架见 [quick-reference.md](./quick-reference.md) §1。

### C2. 我的包该做成 bundle 还是纯 cordis 插件？

- 要**分发**（npm/git/企业源）→ 做成 bundle：`package.json` 声明 `dsh.bundle.patch`，`dsh plugin add` 进 profile 的 bundle 层栈，重启生效。
- 只是**插一行组合**（无 `dsh.bundle`）→ `dsh plugin add` 装依赖并往 profile `cordis.patch.yml` 加 insert 行，配置 HMR 实时生效。

两条通道不要混写。见 [dev-loop.md](./dev-loop.md) §1。

### C3. 我可以给会话事件加一个新的 `type` 吗？

**不可以**。`SessionEventMap` 成员默认 required-on-read：不认识该事件类型的 build 会拒绝整份日志，除非事件带信封的 `ignorable: true`，而运行时 `Session.append()` 无法写这个标记——写了会让**整个会话打不开**。插件状态从既有事件推导，或放进 inspection 找到的 storage 服务。见 [quick-reference.md](./quick-reference.md) §11 红线 10。

### C4. UI 插件装上后整个 slot 白屏 / 日志只有 `web boot: 1 entry did not activate`？

常见原因：

- 组件里 `require` 了 Harness Client 包（抛错组件会把整个 slot 搞白）；
- `dsh.client.inject` 写成了函数或漏了依赖（必须是**静态数组**，且覆盖 `apply()` 触及的每个服务）；
- 往 `ctx` 上写了未声明的属性；
- 只在 `apply()` 里做异步初始化，但注册顺序/所有权不对。

见 [migration.md](./migration.md) §B.7、[quick-reference.md](./quick-reference.md) §11 红线 13。

### C5. 配置项改了要重启吗？为什么设置页没有我的字段？

只有 `Volatile<T>` 字段会进配置表单并可热更新（`.get()` 读取）；普通字段不进表单。表单命名空间 = **profile 条目 id**，不是插件名。旧 `settings.yaml` 会一次性导入后改名。见 [migration.md](./migration.md) §B.3。

### C6. 工具 `parameters` 可以直接写 JSON Schema 吗？

不行。它是 **DSH 自己的方言**（`{ type, required, ... }` 形态），不是完整 JSON Schema；`execute` 只返回 `output.schema` 声明的规范 JSON 值，人类可读内容放 `output.render`。见 [quick-reference.md](./quick-reference.md) §1、§11。

## D. dsh-ee 企业侧

### D1. dsh-ee 预检在哪、会不会占版本号？

在网页「分享插件 → 网页上传」选完 `.tgz` 后点**预检**，接口是 `POST /api/me/packages/check`（请求体就是 `.tgz`）。它只解包检查，**不写库、不占用版本号**；返回 `can_submit` / `will_publish` / `requires_review` 与 `scan` / `conflicts`。见 [README.md](./README.md) §3.2。

### D2. 预检 fail / warn 分别怎么办？

- `fail`：硬问题（如没有 `dsh.bundle` / patch 文件缺失 / 检出密钥 / 包名或版本冲突），提交前先修。
- `warn`：判断项（如缺 `license`、缺 `engines.dsh`、有安装脚本、包偏大、技能文件多），可以在审核说明里解释，也可以修掉。
- 预检不执行插件代码；通过预检不等于 ENTRY/FLOW/BOOST 通过，更不是安全审计。

见 [errors.md](./errors.md) §6。

### D3. 发布返回 403 / 409 是什么原因？

- **403**：包名不在企业作用域下（系统设置的企业作用域，默认 `@company`）。
- **409**：版本号已存在（含被驳回、下架的版本）——版本号不可复用，升版本号重发。
- 其他失败项对照预检 `scan` 的 key（bundle / patch / secrets / license / engines 等）。

见 [`docs/plugin-authors.md`](../plugin-authors.md) §8。

### D4. 包里的 `SKILL.md` 为什么不出现在能力中心？

DSH 插件包内技能由插件运行时注入 DSH，不会出现在 dsh-ee 能力中心的技能 Tab，也不能在 dsh-ee 里按技能单独授权、下架或审核。预检会统计并提示这类文件（`skills` warn）。企业要按技能治理，应把技能单独发布为能力，或等后续 skill 包设计。见 [`docs/plugin-authors.md`](../plugin-authors.md) §3。

### D5. dsh-ee 会替我跑 ENTRY/FLOW/BOOST 吗？

不会。dsh-ee 只做企业准入相关的静态预检 + 人工审核；ENTRY 的静态检查与三层审计是作者自己的工具（`dsh-standards` / `dsh-levels`）。dsh-ee 页面不展示三层等级，也不应该用审核状态暗示三层结论。见 [`docs/plugin-quality.md`](../plugin-quality.md)。

### D6. 审核通过、上架之后，我还要做什么？

维护版本：改代码后升版本号再发布；关注 DSH 宿主升级导致的兼容变化；下架/回滚与可见范围/下发方式由管理员配置；调用会进审计。版本号不可复用是硬规则。见 [dev-loop.md](./dev-loop.md) §7–§8。

## E. 维护

### E1. 这些文档过期了怎么办？

每个文件头都有来源版本 @ commit 与同步日期；核对方式 `git show <commit>:<path>`。触发更新的典型事件：上游发 npm 新版本、dsh-ee 调整推荐/支持的 DSH 线、上游改名/挪仓。更新时改「修改说明 + 同步日期」，必要时新开单独 PR（本目录是快照，不是自动同步）。

### E2. 为什么有的内容只给链接、不搬正文？

许可（CC BY 正文不能无声明复制）、体积（官方文档全文镜像很大）和漂移（契约/API 复制必腐）三方面原因。链接可保证读者拿到最新原文；本目录只对稳定、可迁移的部分做改编并保留署名。

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `README-zh.md`、`guide/plugin-dev-guide.md` §7.3、`guide/quick-reference.zh-CN.md`（原文：[README-zh.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/README-zh.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `README.md` FAQ（Apache-2.0）；dsh-ee 侧问答取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
