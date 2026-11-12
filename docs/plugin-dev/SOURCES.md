# 上游来源、版本与许可（SOURCES）

> 状态：draft · 最后同步：2026-11-12 · 维护：dsh-ee 维护者
>
> 本文件是 dsh-ee 文档引用、改编上游材料的追溯来源：仓库、路径、版本、commit、许可、署名、修改说明与同步日期。许可声明见仓库根 [NOTICE](../../NOTICE)。
>
> 本文件只记录可核实的事实，不构成法律意见；许可最终以上游仓库的 `LICENSE` / `NOTICE` 原文为准。所有 pin 在记录时都通过 GitHub 上对应 commit 的文件访问核对过。

## 1. 使用原则

- **以改编为主，保留署名与修改说明**：仓库内对上游材料的使用方式是**改编**（节选、改写、中文化），不是整篇转载，也不是只放链接。所有改编都保留上游署名与「已修改」说明：
  - `dsh-primer` / `dsh-plugin-guide` 的**自有文本**是 Apache-2.0：改编文件在头/文末写明来源、版本 @ commit、许可、`© 2026 dsh-primer contributors` / `© 2026 dsh-plugin-guide contributors` 与修改说明（见 §3）。
  - `dsh-standards` / `dsh-plugin-spec` 的**正文/条款**是 CC BY 4.0：正文改编必须带 TASL + Change Notice + 同视图免责，并随附 CC BY 全文（`docs/plugin-dev/standards/LICENSE-CC-BY`）；代码是 Apache-2.0，但本仓库不 vendor 检查器代码。
  - 其余材料（样例源码、截图、checker results 等）：不改编、不搬运，按各自许可处理，禁止入仓清单见 §5。
- **已改编内容必须登记**：仓库内任何改编自上游的文件都要在 §2（pin）和 §3（逐文件清单）出现，并在文件头/文末标明来源、版本 @ commit、许可、修改与同步日期。
- **pin 写「版本 + commit」，不写分支**：上游分支会变，引用链接一律用 commit 定位。版本列取对应发布物 `package.json` 的 `version`；commit 列取该版本对应的公开 commit（发布 tag 或 npm 发布记录的 `gitHead`）。本地 checkout 的分支 HEAD 不作为 pin。
- **上游改名/拆分先改本文件**：`dsh-plugin-guide` 0.3.x 的知识库与 CLI 已改名为 `dsh-primer`（0.4.x），0.4.0+ 的 `dsh-plugin-guide` 只剩 ENTRY/FLOW/BOOST 指南层。旧材料（`dsh-plugin-guide` 0.3.x 快照）不作为 pin，只用于差异对照。
- **本仓库自有内容**：dsh-ee 自写的文档与代码随仓库按 Apache-2.0 发布；上游改编部分的权利归上游权利人，dsh-ee 的许可不覆盖它们。

## 2. 上游 pin 表

<!-- docs-check:pin-table:start -->
| 来源 | 仓库 | 路径 | 版本 | commit | 许可 | 本地引用 | 修改说明 | 同步日期 |
|---|---|---|---|---|---|---|---|---|
| dsh-primer | PerryLink/dsh-primer | guide/version-matrix.md、guide/quick-reference.zh-CN.md、guide/plugin-dev-guide.md、guide/release-engineering.zh-CN.md、README-zh.md、MIGRATION.md 等 | 0.4.3 | 8750d31718c16601514e59fe1831de73cfed3d20 | Apache-2.0 | docs/plugin-quality.md；docs/plugin-dev/quick-reference.md†；docs/plugin-dev/cli-matrix.md†；docs/plugin-dev/dev-loop.md†；docs/plugin-dev/errors.md†；docs/plugin-dev/faq.md†；docs/plugin-dev/migration.md†；docs/plugin-dev/release-checklist.md†；docs/plugin-dev/version-matrix.md†；docs/plugin-dev/release-engineering.md† | 改编 + 链接；逐文件改动见 §3 与各文件头/文末 | 2026-11-12 |
| dsh-plugin-guide | PerryLink/dsh-plugin-guide | docs/levels.md、docs/entry.md、docs/flow.md、docs/boost.md、docs/security.md、docs/rules.md、docs/release-checklist.md、SKILL.md、README.md | 0.5.5 | 9f0a8a10b76a872f1a82570734a6b166661fa38b | Apache-2.0 | docs/plugin-quality.md；docs/plugin-dev/quality-layers.md†；docs/plugin-dev/release-checklist.md†；docs/plugin-dev/quick-reference.md†；docs/plugin-dev/cli-matrix.md†；docs/plugin-dev/dev-loop.md†；docs/plugin-dev/errors.md†；docs/plugin-dev/faq.md†；docs/plugin-dev/migration.md†；docs/plugin-dev/version-matrix.md†；docs/plugin-dev/security.md† | 改编 + 链接；逐文件改动见 §3 与各文件头/文末 | 2026-11-12 |
| dsh-standards | PerryLink/dsh-standards | README.md、31-DSH-ENTRY入门规范.md、32-DSH-FLOW顺畅规范.md、33-DSH-BOOST加速实践.md、31-DS-C条款.md、35-三层开发建议与验收清单.md、60-权限与标注规范.md、docs/status-taxonomy.md、40-checker/README.md、LICENSE-CC-BY、LICENSE-APACHE、NOTICE、MODIFICATION-POLICY.md | 1.6.9 | 19c410bd261d942c360386334d4841543a646579 | CC-BY-4.0 文本 / Apache-2.0 代码 | docs/plugin-quality.md；docs/plugin-dev/standards/README.md†；docs/plugin-dev/standards/entry.md†；docs/plugin-dev/standards/flow.md†；docs/plugin-dev/standards/boost.md†；docs/plugin-dev/standards/ds-c-clause-index.md†；docs/plugin-dev/standards/evidence-and-misreadings.md†；docs/plugin-dev/standards/checker-cli.md†；docs/plugin-dev/standards/author-checklist.md†；docs/plugin-dev/standards/capabilities-and-labels.md† | 正文改编（带 Change Notice）+ 链接；代码不 vendor | 2026-11-12 |
| dsh-plugin-spec | PerryLink/dsh-plugin-spec | LICENSE、NOTICE | 0.1.3 | 3387628dc8aee6a2c4dd7458a5a9f9e92199ed68 | CC-BY-4.0 文本 / Apache-2.0 代码 | docs/plugin-quality.md | 仅链接；未复制或改编条款正文 | 2026-11-12 |
| dsh-memento | PerryLink/dsh-memento | LICENSE、README.md | 0.5.30 | 93c65377dc95655f865150690d6ad7ce3e3cbd57 | Apache-2.0 | — | 样例候选；本仓库尚未复制或引用 | 2026-11-12 |
| dsh-library | PerryLink/dsh-library | LICENSE、README.md | 0.3.0 | 40912bf3c4bddd82dd6ca4aa2cba474521c016b7 | Apache-2.0 | — | 样例候选；本仓库尚未复制或引用 | 2026-11-12 |
| dsh-industry-research | PerryLink/dsh-industry-research | LICENSE、README.md | 0.3.25 | 882fd07aad2a372bfd5a25f27f6cbae4cb3335e9 | Apache-2.0 | — | 样例候选；本仓库尚未复制或引用 | 2026-11-12 |
<!-- docs-check:pin-table:end -->

说明：

- 「本地引用」列出引用或改编该来源的 dsh-ee 文件；`†` 表示该文件由 companion PR（#119 / #121）引入、本分支尚未包含，合并后应去掉 `†`（见 §3 的「引入 PR」列）；本文件不伪造当前分支不存在的文件。
- `—` 表示当前只有规划用途、还没有文件引用。
- 本表每行的 commit 都是该版本对应的公开 commit，不是本地 checkout 的分支 HEAD。核对日期见「同步日期」。
- pin 表与 §3 的格式由 [scripts/check-docs.mjs](../../scripts/check-docs.mjs) 校验（列数、版本、commit、许可、日期、本地引用路径、改编清单）。

## 3. 已改编内容清单（逐文件）

以下清单覆盖 #119（`docs/plugin-dev/standards/`）与 #121（`docs/plugin-dev/`）引入的全部文件：改编类文件写明来源、版本 @ commit、许可、署名与修改说明；`README.md` 为 dsh-ee 原创索引；`LICENSE-CC-BY` 为许可全文。

<!-- docs-check:adapted-table:start -->
| 文件 | 上游来源 | 许可 / 署名 | 引入 PR | 状态 |
|---|---|---|---|---|
| docs/plugin-dev/standards/README.md | dsh-standards README.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/entry.md | dsh-standards 31-DSH-ENTRY入门规范.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/flow.md | dsh-standards 32-DSH-FLOW顺畅规范.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/boost.md | dsh-standards 33-DSH-BOOST加速实践.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/ds-c-clause-index.md | dsh-standards 31-DS-C条款.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/evidence-and-misreadings.md | dsh-standards docs/status-taxonomy.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/checker-cli.md | dsh-standards 40-checker/README.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/author-checklist.md | dsh-standards 35-三层开发建议与验收清单.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/capabilities-and-labels.md | dsh-standards 60-权限与标注规范.md | CC BY 4.0 / © 2026 PerryLink | #119 | 已改编（companion PR 待合并） |
| docs/plugin-dev/standards/LICENSE-CC-BY | CC BY 4.0 官方许可全文 | CC BY 4.0 许可文本 | #119 | 已引入（companion PR 待合并） |
| docs/plugin-dev/README.md | dsh-ee 原创索引（不改编上游正文） | Apache-2.0（本仓库） | #121 | 原创索引 |
| docs/plugin-dev/quick-reference.md | dsh-primer guide/quick-reference.zh-CN.md；dsh-plugin-guide docs/levels.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/cli-matrix.md | dsh-primer README-zh.md、guide/family-toolchain.md；dsh-plugin-guide docs/levels.md、docs/release-checklist.md、SKILL.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/dev-loop.md | dsh-primer guide/plugin-dev-guide.md；dsh-plugin-guide docs/release-checklist.md、docs/levels.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/errors.md | dsh-primer guide/plugin-dev-guide.md、guide/unfixed-issues.md；dsh-plugin-guide docs/rules.md、docs/release-checklist.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/faq.md | dsh-primer README-zh.md、guide/plugin-dev-guide.md、guide/quick-reference.zh-CN.md；dsh-plugin-guide README.md FAQ（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/migration.md | dsh-primer MIGRATION.md、LEGACY-0.3.42.md、guide/migration-0.2.md；dsh-plugin-guide README.md FAQ（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/quality-layers.md | dsh-plugin-guide docs/levels.md、docs/entry.md、docs/flow.md、docs/boost.md、SKILL.md | Apache-2.0 / © 2026 dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/release-checklist.md | dsh-plugin-guide docs/release-checklist.md；dsh-primer guide/plugin-dev-guide.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/version-matrix.md | dsh-primer guide/version-matrix.md；dsh-plugin-guide docs/levels.md、docs/entry.md、docs/release-checklist.md、SKILL.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/release-engineering.md | dsh-primer guide/release-engineering.zh-CN.md；dsh-plugin-guide docs/release-checklist.md（补充） | Apache-2.0 / © 2026 dsh-primer contributors、dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
| docs/plugin-dev/security.md | dsh-plugin-guide docs/security.md、SKILL.md、docs/levels.md；dsh-standards security scope 仅链接（不复述其 CC BY 正文） | Apache-2.0 / © 2026 dsh-plugin-guide contributors | #121 | 已改编（companion PR 待合并） |
<!-- docs-check:adapted-table:end -->

说明：

- #119 的 `docs/plugin-dev/standards/`：9 篇正文改编自 dsh-standards（CC BY 4.0）。每篇文件头都有 `Source` / `License` / `© 2026 PerryLink` / `Changes` / `Date` / `Modified version` 与「非官方 · 非认证 · 非安全审计」同视图声明，形状按上游 `MODIFICATION-POLICY.md` 的要求；`LICENSE-CC-BY` 是 CC BY 4.0 全文，不是上游作品。
- #121 的 11 篇改编自 dsh-primer / dsh-plugin-guide 的自有文本（Apache-2.0）。每篇文末有「改编自 …」脚注，写明来源文件、许可、版权归上游 contributors、有删改与同步日期；`security.md` 只摘要指南层，dsh-standards 的 security scope 只链接、不复述其 CC BY 正文。
- `docs/plugin-dev/README.md`（#121）是原创索引，在清单中单列为「原创索引」，不计入改编；`docs/plugin-dev/SOURCES.md`（本文件）也是 dsh-ee 原创记录。
- 本表与 §2 的 commit 以**公开可访问**为准。若某个文件头的脚注 commit 与本表不一致（例如引用了只在本地 checkout 存在、未推送到公开仓库的 commit），以本表为准并修正该脚注，保证链接可访问。
- companion PR 合并后，把 §2「本地引用」里对应文件的 `†` 去掉，交给 CI 做存在性校验；合并前本分支不包含这些文件。

## 4. 逐来源核对记录（2026-11-12）

### 4.1 dsh-primer 0.4.3

- 仓库 `PerryLink/dsh-primer`，tag `v0.4.3` 对应 commit `8750d31718c16601514e59fe1831de73cfed3d20`；该 commit 的 `package.json` 为 `"version": "0.4.3"`、`"license": "Apache-2.0"`。
- 许可证据：`LICENSE` 是 Apache License 2.0 全文；`README-zh.md` / `README.md` 末尾写明自有文本（`SKILL.md`、`guide/`、`README` 等）按 Apache-2.0，`© 2026 dsh-primer contributors`。
- 注意：该版本 `NOTICE.md` 开头仍写「本仓库（dsh-plugin-guide）」，是改名前的历史文案（上游待修）。本仓库改编其自有文本，不引用、不搬运其归档的第三方内容（`references/official-docs/`、`downloads/`，见 §5）。
- dsh-ee 用法：`docs/plugin-dev/` 下 9 篇改编自其 `guide/`、`README-zh.md`、`MIGRATION.md` 等（#121，Apache-2.0，见 §3）；[docs/plugin-quality.md](../plugin-quality.md) 只链接其版本矩阵。

### 4.2 dsh-plugin-guide 0.5.5

- 仓库 `PerryLink/dsh-plugin-guide`，tag `v0.5.5` 对应 commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`；该 commit 的 `package.json` 为 `"version": "0.5.5"`、`"license": "Apache-2.0"`。
- 许可证据：`LICENSE` 是 Apache License 2.0 全文；`README.md` 末尾为 `© 2026 dsh-plugin-guide contributors`；`NOTICE.md` 声明自有文本按 Apache-2.0、不是 DeepSeek Harness 的官方标准/认证/安全审计/背书/排名，且不随包分发上游文档镜像。
- dsh-ee 用法：`docs/plugin-dev/` 下 10 篇改编自其 `docs/levels.md`、`entry.md`、`flow.md`、`boost.md`、`security.md`、`rules.md`、`release-checklist.md` 与 `SKILL.md` 等（#121，Apache-2.0，见 §3）；[docs/plugin-quality.md](../plugin-quality.md) 只链接指南页。

### 4.3 dsh-standards 1.6.9

- 仓库 `PerryLink/dsh-standards`，tag `v1.6.9` 对应 commit `19c410bd261d942c360386334d4841543a646579`；该 commit 的 `package.json` 为 `"version": "1.6.9"`。
- 许可：该仓库按材料类型双许可（`LICENSE`）——
  - 文本、规范、清单、文档、翻译：CC BY 4.0（全文 `LICENSE-CC-BY`，SPDX `CC-BY-4.0`）；修改必须带显著位置的 Change Notice（`MODIFICATION-POLICY.md`）。
  - 代码、脚本、schema、检查器：Apache License 2.0（`LICENSE-APACHE`，SPDX `Apache-2.0`）。
  - 层名 `DSH-ENTRY` / `DSH-FLOW` / `DSH-BOOST` 与徽章不由上述两个许可授权，其使用受 `governance/mark-policy.md` 约束。`NOTICE` 的版权行为 `Copyright 2026 PerryLink`。
- dsh-ee 用法：`docs/plugin-dev/standards/` 下 9 篇正文按 CC BY 4.0 改编并逐篇带 Change Notice（含 `author-checklist.md`、`capabilities-and-labels.md`），`LICENSE-CC-BY` 随附（#119，见 §3）；代码/检查器不 vendor、不在服务端执行；[docs/plugin-quality.md](../plugin-quality.md) 只链接其许可与条款仓。

### 4.4 dsh-plugin-spec 0.1.3

- 仓库 `PerryLink/dsh-plugin-spec`，npm 发布记录 `0.1.3` 的 `gitHead` 为 `3387628dc8aee6a2c4dd7458a5a9f9e92199ed68`；该 commit 的 `package.json` 为 `"version": "0.1.3"`、`"license": "Apache-2.0"`。
- 许可：与 `dsh-standards` 相同的双许可结构（正文 CC BY 4.0 / 代码 Apache-2.0），`NOTICE` 版权行为 `Copyright 2026 PerryLink`。
- dsh-ee 用法：只链接为规则原文仓，未改编、未复制（仓库内尚无对应改编文件）。

### 4.5 样例来源（候选）

- `dsh-memento` 0.5.30，commit `93c65377dc95655f865150690d6ad7ce3e3cbd57`（tag `v0.5.30`）。
- `dsh-library` 0.3.0，commit `40912bf3c4bddd82dd6ca4aa2cba474521c016b7`（npm 发布记录 `gitHead`）。
- `dsh-industry-research` 0.3.25，commit `882fd07aad2a372bfd5a25f27f6cbae4cb3335e9`（tag `v0.3.25`）。
- 三个仓库的 `package.json` 均声明 `"license": "Apache-2.0"`，`LICENSE` 为 Apache License 2.0 全文；`dsh-memento`、`dsh-library` 的 README 末尾有 `© 2026 dsh-<repo> contributors`。
- 当前状态：仅作为后续样例库/DX 内容的候选来源，本仓库尚未复制其源码、README、截图或资源。若将来镜像或再分发，必须随包保留原 `LICENSE` / `THIRD_PARTY_NOTICES`，并在此表登记修改说明。

## 5. 禁止入仓的上游材料

以下材料不在本仓库任何位置复制或再分发（依据 pin 的上游仓库核对）：

- `dsh-primer` 的 `references/official-docs/`（DeepSeek Harness 官方文档的 MIT 镜像）与 `downloads/`（可再生的第三方快照，部分带内部使用约束，且上游自身不入 git）。
- `dsh-standards` 的 `40-checker/results/`（含第三方包元数据等，未被仓库双许可单独授权）、`governance/`（贡献者与 COI 信息）、`40-checker/vendor/`（vendored 第三方代码）。
- 各上游仓库的 `node_modules/`、`coverage/`、构建产物、`*.tgz`；样例仓库的 `upstream/` 目录。
- 上游 README / 站点截图与营销内容（star、家族/市场徽章等）；需要展示时应重制并署名。

## 6. 同步与复核

- **触发**：上游发 npm 新版本；上游改名或拆分仓库；dsh-ee 调整支持的 DSH 版本线（`engines.dsh`）；companion PR（#119 / #121）新增或调整改编文件。
- **步骤**：读上游 `CHANGELOG` / Releases / `LICENSE` / `NOTICE` → 更新 §2 pin 表、§3 改编清单与 §4 核对记录 → 更新引用/改编文件及其署名 → 运行 `node scripts/check-docs.mjs` → 提 PR。
- **复核周期**：每季度人工复核一次（下次建议 2027-01-15）。上游 404 或改名时，先修 pin 再合并，不采用「先合再修」。
- **失败策略**：CI 的本地链接检查覆盖不到外链，外链有效性依赖本文件的复核流程；不要把分支名或 `main` 写进引用链接。

## 7. 相关文件

- 仓库根 [NOTICE](../../NOTICE)
- 三层映射：[docs/plugin-quality.md](../plugin-quality.md)
- 开发文档索引：[docs/dev/README.md](../dev/README.md)
- 文档检查脚本：[scripts/check-docs.mjs](../../scripts/check-docs.mjs)
