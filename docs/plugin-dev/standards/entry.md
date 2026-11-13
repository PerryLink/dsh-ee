# ENTRY 入门规范：能不能被识别、安装、启动

> 上游规范是 **DSH-ENTRY 入门规范**（v2 的第一套，上游版本 `v2.0.0-rc.1`，日期 2026-10-31）。
> 本页是面向 dsh-ee 插件作者的改编摘要，不是上游原文，也不是任何形式的“通过证书”。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/31-DSH-ENTRY入门规范.md@19c410bd261d942c360386334d4841543a646579
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 精简为作者摘要：保留“三轨不是三级”的定位、entry profile 的 run set/blocking、按 kind 的运行方式、结论状态与边界
- 删除上游内部研究文件引用（92/90/99/35/89 等）、内部 18 包样本统计与 results/ 数据；这些不是对外结论且会随证据过期
- 删除非规范性“当前证据”段落与“下一步”研究计划，只保留作者能执行的判定语义
- 新增「dsh-ee 作者怎么自检」「dsh-ee 发布预检不是 ENTRY」两节（dsh-ee 侧新增文字，非上游）
- 保留上游版本号与日期便于追溯；上游升级后应重新核对而不是假定兼容
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、它回答什么

- 第一套规范，回答：**现有插件不改代码，能不能被 DSH 识别、安装、至少跑起来。**
- 它是三条采用深度之一，不是三个等级之一；三套规范回答不同问题，不能合并成总分，也不能互相替代。
- ENTRY 不是：认证、排名、市场准入门槛、完整 DSH-ENTRY 的强制要求，也不是安全审计。

上游三条轨道的定位（本目录另有 [FLOW](./flow.md) / [BOOST](./boost.md) 摘要）：

| 轨道 | 回答 |
|---|---|
| DSH-ENTRY | 能不能装、能不能跑 |
| DSH-FLOW | 装得上、配得动、用得顺、卸得净 |
| DSH-BOOST | 有没有用好 Cordis、有没有可复跑证据 |

## 二、entry profile

`entry` 是 DSH-ENTRY 的**兼容档**，不是完整 DSH-ENTRY。

### 2.1 run set

| 检查 | package 模式 | listing 模式 |
|---|---|---|
| `DSC-01`（ENT-1） | 必跑 | 必跑 |
| `DSC-02`（ENT-2） | 必跑；C-2.1 / C-2.4 只 WARN | 同左 |
| `DSC-03`（ENT-3） | NA | 必跑 |
| `DSC-11/12/13` | 必跑；字段类 WARN/NA | 同左 |
| `DSC-14`（ENT-14） | 必跑；plugin L1 / bundle G0–G3 | 同左 |

### 2.2 blocking（可导致 exit 1）

- `C-1.1` / `C-1.2` / `C-1.4`
- `C-2.2` / `C-2.3`：只在 **runtime claim** 下 blocking（声明不满足 / `workspace:` 发布包，宿主会拒绝安装或跳过）
- listing 的 `C-3.1/3.2/3.5`
- `DSC-14` 可归因 FAIL（只限 **runtime claim**：plugin 相对文件缺失 / apply 抛错；bundle patch、行解析或真实 boot 失败）

**不 blocking（WARN）**：`C-11.3`、`C-12.1` hard hit、`C-13.4`。安全/品牌问题归 FLOW/治理轨道，entry 不做安全审计。

**WARN/NA 的典型项**：`C-2.1`、`C-2.4`、`C-1.5/1.6`、`C-11.1/2/3/5`、`C-12.1/2/3/4`、`C-13.1/2/3/4/5`、S2 `unmatchedTargets`、C-7 dispose 残留。

### 2.3 按 kind 的“运行”与结论

| kind | 运行证据 | 结论 |
|---|---|---|
| `plugin` | L1 ACTIVE + L2 归零（L2 失败只 WARN） | `interop-runtime` |
| `bundle` | G0 patch 解析 + G1 组合/行解析 + G2 gate + G3 真实 boot 目标行 ACTIVE/disabled | `interop-runtime (bundle)` |
| `client` | 仅 `exports["./client"]` + root row（静态） | `interop-static (client, build-only)` |
| `preset` | 组合层，不是可安装包 | `NA` |
| `skill` | 纯 skill NA；有入口 + apply 才按 plugin | `NA` 或 `interop-runtime` |
| `theme` / `mcp-config` | 静态资源 / 外部进程 | `NA` |
| `unknown` | 无法判定 | `NA + manual` |

### 2.4 结论状态（重点）

| 状态 | exit | level |
|---|---|---|
| recognized（static claim，无 runtime 声明） | 0 | `DSH-ENTRY recognized` |
| runtime verified + anchor 匹配 | 0 | `DSH-ENTRY runtime` |
| runtime verified + anchor 不匹配 | 0 | `DSH-ENTRY runtime (unanchored)` |
| composition only | 0 | `DSH-ENTRY runtime (composition)` |
| static / not-run | 2 | `DSH-ENTRY static` |
| blocked（host/fixture 环境问题） | 2 | `DSH-ENTRY unavailable` |
| plugin 可归因 FAIL | 1 | `DSH-ENTRY failed` |

**永远不会输出“通过”级别的 `DSH-ENTRY`；`static` 不是通过。** 另外：

- `interop-static` 只表示“可识别/可安装”，不是通过；只有 `interop-runtime` 才是该 scope 的通过状态。
- `--treat-as-plugin` 只是 server-entry 代理，不等于 bundle；bundle 必须 G0–G3。
- `blocked` 是环境/宿主问题，不是插件失败，也不等于通过。
- 报告必须带 runtime commit 与产物 mtime；旧结果不能当现值。

## 三、dsh-ee 作者怎么自检

在**本机**、固定版本运行（完整命令见 [checker-cli.md](./checker-cli.md)）：

```sh
# 1) 只做静态识别：能识别、能安装，但不声明运行
npx dsh-standards@1.6.9 check . --scope entry --entry-claim static

# 2) 跑 plugin 的 L1：需要一份已构建的 Cordis 运行时（作者自备）
npx dsh-standards@1.6.9 check . --scope entry \
  --runtime /path/to/cordis/lib/index.js --runtime-version <你的宿主版本>

# 3) bundle：默认要求 --host 才会跑 G0–G3；没有 host 只能是 incomplete
npx dsh-standards@1.6.9 check . --scope entry --host /path/to/app-boot/lib/index.js
```

- 默认 `check` 是 `--entry-claim static --no-exec`，**不会执行你的插件代码**；运行时项记 `not-run`，报告是 `DSH-ENTRY static` / `incomplete`，**不是** “passed”。
- 第 2/3 条命令会显式执行插件；只在你自己可信的目录里跑，且理解它加载的是你的代码。
- 读结论时以报告里的 `conclusion` / `anchor` / `coverage` 为准；`recognized` 不是 runtime。

## 四、dsh-ee 发布预检不是 ENTRY

dsh-ee 的发布预检（`POST /api/me/packages/check`）是**另一套**静态检查：看 tarball 结构、`dsh.bundle.patch`、`engines.dsh`、安装脚本、密钥、License、体积等，结果只决定 `can_submit` / `will_publish` / `requires_review`。

- 它不执行插件代码，也不产出 ENTRY/FLOW/BOOST 层级；
- 它通过**不代表** `DSH-ENTRY runtime`，更不代表任何认证；
- dsh-ee 服务端不运行 checker，也不新增 Node 依赖。

## 五、边界

- 不做安全审计或运行时沙箱；只做装载期一致性与运行冒烟。
- 不排名、不打分、不做市场门禁；entry 是兼容底线。
- `recognized` / `static` / `not-run` / `blocked` 都不是通过。
- 上游规范会演进；本页对应上游 `v2.0.0-rc.1` 快照，命令固定 `dsh-standards@1.6.9`，升级前先对照上游。

## 上游原文与相关页

- 上游原文：[31-DSH-ENTRY入门规范.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/31-DSH-ENTRY入门规范.md)
- 上游条款定义与检查映射：[40-checker/README.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/40-checker/README.md)、[docs/coverage.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md)
- 本目录：[DS-C 条款索引](./ds-c-clause-index.md)、[checker 命令与报告字段](./checker-cli.md)、[证据边界与常见误读](./evidence-and-misreadings.md)

**非官方 · 非认证 · 非安全审计。**
