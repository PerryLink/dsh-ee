# dsh-standards / checker：dsh-ee 作者参考（索引）

> 本目录是 dsh-ee 文档站里的**改编摘要**，供给 dsh-ee 开发 DSH 插件的作者查规范与检查器。
> 上游是社区项目 [PerryLink/dsh-standards](https://github.com/PerryLink/dsh-standards)（文本 CC BY 4.0 / 代码 Apache-2.0）。
> 这里不是上游原文，也不是认证机构页面；dsh-ee 不执行 checker，不展示等级、评分或排名。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/README.md@19c410bd261d942c360386334d4841543a646579
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 改写为 dsh-ee 文档目录的索引页：保留上游的“是什么/不是什么”与三轨定位，删除徽章、star/下载量、仓库经营数据、上游内部研究文件清单与营销段落
- 新增「dsh-ee 作者怎么用」「dsh-ee 与 checker 的边界」「许可处理」三节（dsh-ee 侧新增文字，非上游）
- 命令与版本固定为 dsh-standards@1.6.9；不复制上游 results/ 统计（统计会过期且不构成结论）
- 上游快照 commit 固定，下面所有文档的 Source 行都指向同一 commit；上游升级后应重新核对而不是假定兼容
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 这是什么

`dsh-standards` 是 PerryLink 维护的社区规范/检查器仓库，给 DSH 插件提供一套**可复跑的检查**：

- 三套采用深度（ENTRY / FLOW / BOOST）：每套回答一个问题，不合并成一个分数；
- 一套基线条款集 DS-C（C-1…C-22），是 checker 默认 scope 与 `DSC-xx` 检查 ID 的由来；
- 一个零依赖 Node 检查器（`dsh-standards` / `dsh-levels` 两个命令），由作者在**本机**运行。

本目录把作者真正会用到的部分整理成 8 篇：

| 文档 | 内容 |
|---|---|
| [ENTRY 入门规范](./entry.md) | 能不能被识别、安装、启动；`recognized` / `runtime` 等结论状态 |
| [FLOW 顺畅规范](./flow.md) | 用户旅程能不能装、配、用、升级、卸载；J1–J8 |
| [BOOST 加速实践](./boost.md) | 有没有用好 Cordis 原语；LEV-1–8 与证据卡 |
| [DS-C 条款索引](./ds-c-clause-index.md) | C-1…C-22 / `DSC-01…22`、package 与 listing 两个 profile、`security` scope |
| [capabilities / commitments / labels](./capabilities-and-labels.md) | 作者声明面：15 个能力 ID、9 条承诺、5 类标签与 `DSC-11/12/13` 规则 |
| [checker 命令与报告字段](./checker-cli.md) | 固定 1.6.9 的安装/命令/退出码，`dsh-conformance/1` 报告 JSON 字段 |
| [作者自检清单](./author-checklist.md) | 三层最短路径、ENTRY/FLOW/BOOST 验收清单、评审一分钟清单 |
| [证据边界与常见误读](./evidence-and-misreadings.md) | `not-run` / `NA` / `static` 为什么不是 PASS，exit 2 为什么不是失败 |

## 三套采用深度（不是三个等级）

| 轨道 | 规范/实践 | 回答什么 | 机器入口 | 证据状态（例子） |
|---|---|---|---|---|
| ENTRY | DSH-ENTRY 入门规范 | 现有插件几乎不改，能否被识别、安装、启动 | `--scope entry`、`DSC-14` | `recognized` / `static` / `runtime` / `blocked` / `failed` |
| FLOW | DSH-FLOW 顺畅规范 | 装得上之后，用户旅程顺不顺 | J1–J8、`--flow-evidence` | `runtime` / `manual` / `not-run` / `failed` |
| BOOST | DSH-BOOST 加速实践 | 有没有用好 Cordis 原生能力 | LEV-1–8、`--boost-evidence` | `measured` / `not-measured` / `failed` |
| DS-C（历史命名） | 基线条款集 C-1…C-22 | 能否装、能跑、不破坏宿主 | 默认 `--scope ds-c`（`DSC-01…14`） | PASS / WARN / FAIL / NA / ERROR |

四条不是四层：DS-C 是 v1 的条款命名，checker 为兼容仍用 `ds-c` scope 与 `DSC-xx` ID；ENTRY/FLOW/BOOST 是 v2 的三条采用深度。DS-C **不是第四层**，也不是证书。

## dsh-ee 作者怎么用

1. **本地自检（可选，建议）**：发布前在本机跑 checker，命令和版本见 [checker-cli.md](./checker-cli.md)。默认是静态检查，不执行你的插件代码。
2. **dsh-ee 发布预检是另一套**：`POST /api/me/packages/check` 检查 tarball 结构、`dsh.bundle.patch`、`engines.dsh`、安装脚本、密钥、License、体积等，结果只决定 `can_submit` / `will_publish` / `requires_review`。它**不是** dsh-standards，不产出 ENTRY/FLOW/BOOST 结论。
3. **两者的证据不要混写**：dsh-ee 预检通过 ≠ checker 结论；checker 的 `not-run` / `NA` / `static` ≠ 通过。
4. **dsh-ee 不做认证/评分/排名/准入门槛**：不在发布页、插件详情页或市场卡片展示层级、分数、排行榜或 badge；后续即使展示证据链接，也只标 `self-declared` + 日期 + 免责声明，且不进入准入或排序。
5. **服务端不执行 checker**：dsh-ee 服务端是 Go 单文件服务，不带 Node 运行时；checker 只在作者本机运行。本目录的命令块只供复制，不接服务端。

## dsh-ee 与 checker 的边界

| 被检对象 | 用什么 | 不要用 |
|---|---|---|
| DSH 插件（cordis npm 包） | `dsh-standards check` + dsh-ee 发布预检 | dsh-ee `extension check`（对象不同） |
| dsh-ee 扩展 / 连接 | `dsh-ee extension check` / `extension test` | dsh-standards（对象不同） |
| 第三方 Harness | 契约测试 + 端到端测试 | 层级判定 / 评分 / 认证 |

## 许可处理（本目录的实际情况）

上游按材料类型分许可（见上游 `LICENSE` / `NOTICE` / `MODIFICATION-POLICY.md`）：

| 材料 | 上游许可 | 本目录处理 |
|---|---|---|
| 规范、清单、README、docs 等文本 | **CC BY 4.0** | 改编后保留 TASL + Change Notice + 同视图免责；每页文首有完整字段；见 [LICENSE-CC-BY](./LICENSE-CC-BY) |
| 检查器代码、脚本、schema | **Apache-2.0** | **没有复制**。本目录不 vendor 检查器，不含可执行脚本、schema 或 `vendor/`；字段说明是对外行为的事实描述 |
| `results/`、`governance/`、第三方元数据 | 未随上述许可授权 / 各自许可 | **没有复制**。本文不引用统计数字、不复制 governance 正文、不列第三方元数据 |
| 层名/徽标 `DSH-ENTRY/FLOW/BOOST` | 两个许可都不含，另有 mark policy | 只作说明与引用；每页同视图带“非官方 · 非认证 · 非安全审计”，不作等级/认证/排名使用 |

补充：

- 上游来源：`https://github.com/PerryLink/dsh-standards`，本目录快照的 commit 是 `19c410bd261d942c360386334d4841543a646579`（对应 `dsh-standards@1.6.9`，tag `v1.6.9`）；命令固定到 `1.6.9`，避免 `npx` 同名包冲突。
- **没有复制 Apache-2.0 材料，所以本目录不含 `LICENSE-APACHE` / `NOTICE`**；如果以后要复制 checker 的代码、脚本或 schema，必须同时附上上游 `LICENSE-APACHE` + `NOTICE`（`Copyright 2026 PerryLink`）并标注修改，不能只沿用 CC BY 声明，也不能把代码片段当成 CC BY 文本。
- 每页的 `Changes` 是逐页改动清单；`Date` 是本次改编日期；`Modified version: dsh-ee docs` 表示这是 dsh-ee 侧的改编版，**不是上游发布**。
- PerryLink 未审阅、未背书本改编版；本目录不暗示上游认可 dsh-ee。
- dsh-ee 侧新增文字按本仓库根 `LICENSE`（Apache-2.0）提供。
- 本目录不是法律意见；上游 `LICENSE` 也声明其不是法律意见。对外措辞请维护者复核。
- 许可细节以上游为准，链接：
  [LICENSE](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/LICENSE)、
  [LICENSE-CC-BY](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/LICENSE-CC-BY)、
  [LICENSE-APACHE](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/LICENSE-APACHE)、
  [NOTICE](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/NOTICE)、
  [MODIFICATION-POLICY.md](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/MODIFICATION-POLICY.md)。

## 一句话边界

这些规范与检查器是**作者工具**：帮你在本地发现问题、留下证据，不是认证、不是评分、不是准入门槛，也不代表安全审计结论。

**非官方 · 非认证 · 非安全审计。**
