# ENTRY / FLOW / BOOST：三层质量指南概览

> **来源与许可**
> - 来源：`PerryLink/dsh-plugin-guide`（[GitHub](https://github.com/PerryLink/dsh-plugin-guide)）`docs/levels.md`、`docs/entry.md`、`docs/flow.md`、`docs/boost.md`、`SKILL.md`
> - 上游文件（固定 commit，公开链接）：[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)、[docs/entry.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/entry.md)、[docs/flow.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/flow.md)、[docs/boost.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/boost.md)、[SKILL.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/SKILL.md)
> - 版本：`dsh-plugin-guide@0.5.5`；公开 pin：tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`（2026-11-08）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布；上游 `package.json` `"license": "Apache-2.0"`）
> - 版权：© 2026 dsh-plugin-guide contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 修改说明：中文节选并改写；保留三层的问题划分、两轴证据语义与「不是等级/不是认证」的边界；压缩具体检查项与报告字段（检查器与条款由 `dsh-standards` / `dsh-plugin-spec` 维护，正文许可为 **CC BY 4.0**，本页**不复述条款**，只链接）；新增 dsh-ee 企业侧映射提示。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方标准，不是认证、评分或排名。三层的判定与证据由 `dsh-standards` 负责；本页只说「该问什么、证据怎么算」。

## 0. 三层不是三个等级

| 层 | 回答的问题 | 是什么 | 能给出判定吗 |
|---|---|---|---|
| **ENTRY** | 插件能被识别、安装、启动吗？ | 兼容底线 | 能：`recognized` / `runtime` / `failed` / `incomplete` / `unavailable` |
| **FLOW** | 用户能顺利安装、配置、使用、升级、卸载吗？ | 用户旅程（J1–J8） | 有运行证据时能；否则 `not-run` |
| **BOOST** | 有没有用好 Cordis？ | 实践库（LEV-1–8）+ 证据卡 | 没有插件级 pass/fail；证据是**按实践**的 |

三层是三条轨道，不是三档分数：它们回答不同问题，**不能相加成总分、证书或排名**。BOOST 尤其不是「插件等级」——一个插件可以登记某条实践的证据，但不会被贴「BOOST 等级」。

> 上游 `docs/levels.md` 记录的基线（2026-11-04）：DSH `0.2.2-alpha.3`（npm `alpha`）、Node `>=24.9.0`、Cordis `4.0.6-alpha.2`。这是指南写作时的快照，**不是现值**；dsh-ee v0.1 代码注释的推荐宿主是 DSH `0.2.2-rc.1` / Cordis `4.1.0`。两者都只作参考，实际以 `dsh --version` 与目标宿主为准。

## 1. ENTRY：兼容底线

ENTRY 检查包形态、入口、bundle patch、版本/peer 范围、元数据与标签；当声明 runtime 时，还要求真实启动。

### 1.1 两个 claim

| Claim | 含义 | 需要什么 |
|---|---|---|
| `static` / `recognized` | 包形态可以被识别 | 清单/入口的静态检查 |
| `runtime` | 它真的能启动 | 插件真实启动（L1 类）或 bundle boot（G0–G3 类） |

- `recognized` **不是**通过；bundle 也不能靠 import 服务端入口来证明，需要 G0–G3。
- 纯客户端包默认只能 build-only，除非有浏览器探针。
- `static` 不是 pass，`not-run` 不是 pass。

### 1.2 静态检查大致覆盖什么（摘要）

- `package.json`：name / version（严格 semver）/ license / 入口；`dsh.bundle.patch` 存在、在包内、可解析、目标行能被组合。
- 版本声明：`engines.dsh` / `@deepseek-ai/dsh*` peer 范围是否声明、是否与运行时匹配（口径见 [version-matrix.md](./version-matrix.md) §5）。
- 展示元数据、labels、禁止性声明（不得写 official/certified/secure/recommended/ranked 一类）。
- 安装形态：精确版本 / 可验证 tag / git commit pin；非 registry 依赖与安装脚本属于要披露的项。

具体检查 ID、阻断级别与修复模板由上游 `dsh-standards` 维护（正文 CC BY 4.0，见其 `31-DSH-ENTRY` 规范）；本页不复制条款。

## 2. FLOW：用户旅程

FLOW 决定「第一次运行、配置、升级、卸载」是否可理解。上游用八段旅程：

| ID | 旅程 | 最小证据（摘要） |
|---|---|---|
| J1 | 安装 / 卸载 | add → dump config → remove；不手改 profile |
| J2 | 启用 / 禁用 | 稳定 id；组合配置里能看到变化 |
| J3 | 配置 | 空配置能启动；非法配置 fail loud 且给字段路径；volatile 变更重启 0 次 |
| J4 | 首次成功 | 五分钟快速开始 + 带原因/下一条命令/文档锚点的错误 |
| J5 | 权限 | capabilities/labels/commitments/披露与 README 一致，无禁止性声明 |
| J6 | 升级 / 回滚 / 卸载 | 升级、回滚、卸载保数据、无残留 |
| J7 | 可观测性 | 正常、缺服务、端口冲突可诊断 |
| J8 | 客户端 / 服务端 | headless / web / 客户端禁用三种模式能工作或被明确拒绝 |

**证据纪律**：没有命令、退出码、日志哈希与时间戳的旅程行只是声明，不是证据。上游推荐的证据文件形状（`dsh-flow-evidence/2`）要求「一个 subject、一个 host、每旅程一条记录、带链指针」；具体字段由检查器文档定义。FLOW 的现状是 harness 仍在草案阶段：没跑的旅程是 `not-run`，部分完成也是 `not-run`，**不是软通过**。

## 3. BOOST：用好 Cordis（实践库，不是评分）

| ID | 实践 | 证据要看什么（摘要） |
|---|---|---|
| LEV-1 | 可逆副作用 | timer/连接/watcher/子进程都在 `ctx.effect()` 里且带 label；重启回到基线 |
| LEV-2 | 正确等待 | 必需服务进 `inject`；等待用 `ctx.inject()`；可选服务 `ctx.get()`；不轮询 |
| LEV-3 | 热配置 | 可调字段用 `volatile()` + `.get()`；改配置不重启插件 |
| LEV-4 | 事件纪律 | 事件名与 `@mode` 一致；waterfall 观察者调 `next()`；等待用 serial/bail/parallel；观察用 emit |
| LEV-5 | 接缝与隔离 | 消费走 seam；不 import 实现；多实例才 isolate/group；per-agent 用 `agent.ctx` |
| LEV-6 | 稳定前缀 / 组装 | 工具与提示词声明字节稳定；易变文本走 `systemPrompt.context`；组装复用 |
| LEV-7 | 有界并行 / 长任务 | 独立调用有界并行；长任务用 jobs；取消会传播 |
| LEV-8 | 组合 / 证据卡 | bundle patch 与 preset 表达差异；每条实践有可复现证据卡 |

**证据卡不是徽章**：证据是「按实践、按插件」的，要有基线（before）与之后（after）测量。上游要求 13 个字段（subject / host / method / scope / command / result / negativeControl / generatedAt / expiresAt / recordHash / claimLevel / issuer / selfAssessed）；字段名与校验规则以 `dsh-standards` 的 BOOST 文档为准。没有测量就写 `not-measured` / `incomplete`，不要编数字。

## 4. Security 是 scope，不是第四层

- 安全静态检查在 `dsh-standards` 里是独立 scope（`--scope security`，1.6.0+），**从不改变** ENTRY/FLOW/BOOST 的判定。
- 静态检查只能匹配已知源码形态，看不到运行时出网、文件读取或提示词污染；**没有安全 PASS 不等于安全**，只表示「这一轮静态检查没命中已知模式」。
- 运行时安全需要沙箱 + 网络策略 + 抓取，不是检查器能给的。不要把安全加成「第四层」，也不要写「已通过安全认证」。

## 5. 词汇纪律（不能吹大）

- `recognized` ≠ `runtime`
- `static` ≠ `runtime`
- `not-run` ≠ `pass`
- `boost-evidenced` ≠ 插件等级
- security check ≠ security audit
- A–D 一类的等级（如果存在）与 ENTRY/FLOW/BOOST 正交
- **不写** official / certified / verified / audited / secure / recommended / ranked

这些词不是文字游戏：企业审核、市场展示和 README 里一旦误用，就会把「静态检查」读成「认证」，把「社区指南」读成「官方标准」。

## 6. 怎么跑检查（作者侧）

```sh
# 只做 ENTRY 识别（不声明 runtime）
npx dsh-standards ./my-plugin --scope entry --entry-claim static

# 三层审计；FLOW/BOOST 只有提供证据时才报告
npx -p dsh-standards dsh-levels ./my-plugin --format json

# 固定版本的发布前静态检查（0.5.5 指南写法）
npx -y dsh-standards@<ver> check . --scope entry --entry-claim static --no-exec
```

检查器与条款：`dsh-standards`（npm / GitHub；代码 Apache-2.0、正文 CC BY 4.0）；规则文本：`dsh-plugin-spec`。本仓库不内置、也不在服务端执行检查器。

## 7. 在 dsh-ee 里怎么用（企业侧映射）

dsh-ee 只做**企业准入**，不做三层判定：

| 层 | dsh-ee 里对应什么 | dsh-ee 能证明 / 不能证明 |
|---|---|---|
| ENTRY | 发布前预检（`POST /api/me/packages/check`）：bundle/patch、engines、license、secrets、install-scripts、Cordis 依赖、体积等静态项 | 能证明包结构与静态风险；**不执行插件**，`recognized` ≠ runtime |
| FLOW | 审核、可见范围、下发方式、版本/下架、审计 | 能覆盖企业侧安装与治理流程；作者自己的配置/升级体验仍要作者给证据 |
| BOOST | 无 | dsh-ee 不做运行时基准，不应把 BOOST 当准入门槛 |
| security（scope） | 预检的密钥/安装脚本/敏感文件检查；企业内网运行审计 | 只是静态已知模式检查，不是渗透测试或安全认证 |

作者用法：发布前按 `dsh-standards` 跑 ENTRY，再按本页自查；提交 dsh-ee 时 `fail` 先修、`warn` 在审核说明里解释；**不要**在 dsh-ee 页面宣称「ENTRY 通过」「BOOST 达标」，也不要让审核状态暗示三层结论。完整的 dsh-ee 映射见 [`docs/plugin-quality.md`](../plugin-quality.md)。

## 8. 边界与来源

- 本页是**指南层**（`dsh-plugin-guide` 自有文本，Apache-2.0），不是标准层；三层模型的条款正文、检查器、版本矩阵在 `dsh-standards` / `dsh-plugin-spec` 维护，正文许可是 **CC BY 4.0**，需要 TASL + Changes + 同视图免责，因此本仓库只链接、不复制。
- 上游 `SKILL.md` 的定位：这个项目是 **guide，不是 judge**；它解释三层、把检查路由给 `dsh-standards`，不声称认证/审计/排名。
- 非官方声明：上游明确「非官方、无认证、无认可、无安全审计、无市场门禁」。本页沿用该边界。

---

> 改编自 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/levels.md`、`entry.md`、`flow.md`、`boost.md`、`SKILL.md`（原文：[levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)；Apache-2.0，版权归上游 contributors），有删改并中文化；条款正文归 `dsh-standards`（CC BY 4.0），本页只作概览与链接；不代表上游背书。同步日期 2026-11-12。
