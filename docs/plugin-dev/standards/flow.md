# FLOW 顺畅规范：用户旅程能不能装、配、用、升级、卸载

> 上游规范是 **DSH-FLOW 顺畅规范**（v2 的第二套，上游版本 `v2.0.0-rc.1`，日期 2026-10-31）。
> 本页是面向 dsh-ee 插件作者的改编摘要，不是上游原文，也不是“通过率/等级”。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/32-DSH-FLOW顺畅规范.md@19c410bd261d942c360386334d4841543a646579
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 精简为作者摘要：保留 FLOW 的定位、八条最小集（R1–R8 → J1–J8）、判定协议、证据与文案要求、判定状态
- 删除上游内部 runtime 统计（fixture/真实包 PASS 数字）、results/ 路径与“当前 runtime”证据段落；这些会过期，且不构成对外结论
- 删除上游内部研究文件引用与“下一步 harness”计划；保留“未跑 = not-run”的判定规则
- 新增「dsh-ee 审核怎么用 FLOW」与「dsh-ee 边界」两节（dsh-ee 侧新增文字，非上游）
- 保留上游版本号与日期便于追溯
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、它回答什么

- `DSH-ENTRY` 保证“能装、能跑”；`DSH-FLOW` 保证“好用”；`DSH-BOOST` 保证“用 Cordis 做得更省”。
- FLOW 的判据不是“条款写得多”，而是**用户旅程能复现**：每个 journey 记录命令、环境、结果、日志路径、时间。
- FLOW 不是：认证、排名、通过率、市场门禁、完整契约审计。未跑 = `not-run`，不冒充 PASS。

## 二、八条最小集（R1–R8）

| ID | 要求 | 判定（J） | 为什么算“顺畅” |
|---|---|---|---|
| R1 安装/移除 | 一条命令 add/remove；不手改 profile；不强制 `allowBuilds`；发布预构建 | J1：add → `--dump-config` 出现层 → remove → 层/依赖消失 | 安装是 100% 必经 |
| R2 启用/停用 | 稳定 `id`；危险能力默认关；写清热生效 vs 需重启 | J2：默认 → 开 → 关 → dump diff | 减少“改了没用”的试错 |
| R3 配置 | schema + 默认值；坏配置 FAILED + 字段路径；volatile 热改 | J3：空配置 boot；非法配置路径；0 restart | 配置是最大摩擦 |
| R4 首次成功 | README 5 分钟 quickstart；错误三要素 | J4：五类故障逐条检查 | 第一次成功决定留存 |
| R5 权限 | capabilities/labels/commitments/disclosure + README Permissions | J5：字段闭集 + README 一致 + 禁用词 0 | 权限不清晰会吓退用户 |
| R6 升级/回滚/卸载 | SemVer / CHANGELOG / engines；卸载 0 残留；回滚 ≤2 命令 | J6：v1 → 数据 → v2 → 回滚 → 读数据 | 升级/卸载是长期成本 |
| R7 可观测 | 启动一行 `name@version`；失败/PENDING 可诊断；日志路径 | J7：正常 / 缺服务 / 端口占用 | 出问题能自己修 |
| R8 client/server | `dsh.client` 三态；headless/sdk 可用或明确拒绝 | J8：headless / web / 关闭 client；console error 0 | 同一插件多种运行形态 |

> 注意：R1–R8 是作者要做的**最小集**；不是每条都能自动判定。上游目前的 harness 尚未覆盖全部 runtime 项，很多项会是 `not-run` 或 `manual`。

## 三、J1–J8 判定协议

| J | 输入 | PASS | WARN | FAIL |
|---|---|---|---|---|
| J1 | 临时 `DSH_HOME`、profile、tarball | add → dump → remove 全成功 | 需 `allowBuilds` 但有提示 | 手改 profile / remove 残留 |
| J2 | 默认配置、开关 | 默认可用；开关后 dump 差异正确 | 需重启但已写明 | 开关无效 / 崩溃 |
| J3 | `{}`、非法值、volatile 字段 | 空配置启动；非法 FAILED 带路径；0 restart | 迁移/未知键行为未写明 | 静默用默认值 / 崩溃 |
| J4 | README、日志、错误 | 5 分钟内成功；错误三要素 | 有步骤但缺错误处理 | 无 quickstart / 无法启动 |
| J5 | manifest、README | 字段闭集、README 一致、禁用词 0 | 字段缺失但 README 说明 | 冒充官方 / 权限不说明 |
| J6 | 两个版本、数据 | 升级/回滚/卸载全成功 | 需手工迁移但有文档 | 数据丢失 / 无法回滚 |
| J7 | 正常 / 缺服务 / 端口占用 | 三种都有可读诊断 | 只覆盖正常 | 崩溃无日志 |
| J8 | headless / web / client off | 三态可用或明确拒绝 | 一态未测 | 宣称支持但崩溃 |

**适用性**：J2/J3 只在插件暴露顶层 `enabled` 或配置 schema 时可判定；否则记 `not-run`，不判 FAIL。**未跑 = `not-run`，不冒充 PASS。**

## 四、证据与文案

### 4.1 README 最小结构

`Quickstart` / `Permissions` / `Config` / `Upgrade & Uninstall` / `Troubleshooting`。

### 4.2 错误三要素

1. **原因**（字段 / 服务 / 文件 / 端口）；
2. **下一条命令或链接**；
3. **文档锚**。

反例：`Error: failed`。
正例：`缺少 services.llm；先 dsh plugin add @deepseek-ai/dsh-llm，或把 inject 改成可选；见 README#permissions`。

### 4.3 权限文案

- 声明不是门控；安装即运行代码；
- 高风险能力写“能做什么 / 不能做什么 / 怎么关”；
- 不写“已认证 / 官方 / 安全”。

## 五、判定状态

| 状态 | 含义 |
|---|---|
| `runtime` | 八个 journey 全部有 pass 证据 |
| `failed` | 至少一个 journey 有可归因的 FAIL |
| `not-run` | 没有证据，或不足八个 pass；不是 PASS |
| `manual` | 由人执行并记录了旅程；只对 FLOW 有效 |

每个 journey 记 `pass` / `fail` / `not-run` / `manual`；没有 `flow-warn` 这种软通过。不设“通过率 / 等级”；不做市场门禁。

## 六、dsh-ee 审核怎么用 FLOW

dsh-ee 没有 FLOW 字段，也不自动跑旅程。审核时可以把 J1–J8 当作**提问清单**，不是打分表：

- 至少确认安装、升级、卸载、配置错误恢复、权限披露；
- 要求作者给出可复现的命令/日志/录屏或证据文件（`self-declared` + 日期）；
- 企业侧再叠加可见范围、下发方式和调用审计；
- 不把 FLOW 结论写进上架排序或门禁。

## 七、边界

- 本页对应上游 `v2.0.0-rc.1` 快照；上游的 harness / journey 脚本会演进，版本固定为 `dsh-standards@1.6.9` 时以该版本实际输出为准。
- FLOW 的“pass”是指**该 scope 下的证据可复现**，不是认证、不是安全结论、不是市场推荐。
- 无法量化的内容（开发人时、维护成本、“少踩坑”）不冒充 PASS。

## 上游原文与相关页

- 上游原文：[32-DSH-FLOW顺畅规范.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/32-DSH-FLOW顺畅规范.md)
- 上游证据与 harness：[40-checker/README.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/40-checker/README.md)、[docs/status-taxonomy.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/status-taxonomy.md)
- 本目录：[ENTRY](./entry.md)、[BOOST](./boost.md)、[checker 命令与报告字段](./checker-cli.md)、[证据边界与常见误读](./evidence-and-misreadings.md)

**非官方 · 非认证 · 非安全审计。**
