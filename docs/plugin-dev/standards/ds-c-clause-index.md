# DS-C 条款索引（C-1…C-22 / DSC-01…22）

> 本页是 **DS-C 基线条款集**的索引与判定语义摘要，不是条款正文。
> DS-C 是 v1 的历史命名；v2 的三条采用深度是 ENTRY / FLOW / BOOST。DS-C **不是第四层、不是等级、不是证书**。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/31-DS-C条款.md@19c410bd261d942c360386334d4841543a646579
Additional sources:
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/40-checker/README.md
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 把上游 31 号文 §1–§3 与 docs/coverage.md 提炼为一张索引表（条款 ID、检查 ID、层、强制/咨询、一句话说明），不收录 22 条条款正文全文
- 删除上游行号、内部研究底稿引用、results/ 样本与“已知未完成项”研究备注；保留判定语义（profile、security scope、三值/NA、覆盖度、结论门）
- 删除 C 路 A1–A14 覆盖映射、Q3–Q5 建议答案、附录 A/B/C 等内部设计讨论
- 新增「作者怎么读这张表」与「dsh-ee 边界」两节（dsh-ee 侧新增文字，非上游）
- 条款正文与最新判定以上游固定 commit 链接为准；本页不保证与未来版本同步
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、先定位：DS-C 是什么

- DS-C 回答一个问题：**这个插件能装、能跑、不破坏宿主吗？** 它不回答“设计是否优雅 / 是否安全 / 是否更快”。
- DS-C 是**条款集合**，不是分数、不是排名、不是“高级/初级”标签。`DS-C` PASS 只表示：在给定 `(repo, commit, scanner, dsh, cordis)` 锚点上，对应 profile 的强制条款均被检查且通过。
- 上游版本：`v1.0.0-rc.2`（2026-11-07 增补 C-15…C-22 安全扩展）；注意这是**定稿候选**，不是认证标准。
- checker 的默认 scope 名仍是 `ds-c`，检查 ID 是 `DSC-01…22`；旧命名 `DS-C/R/G` 与未来的 ENTRY/FLOW/BOOST 命名存在别名关系，引用时以 checker 当前版本（本目录固定 `1.6.9`）为准。
- 两个不同对象：`dsh-plugin-spec` 是条款原文仓；`dsh-certification` 是另一条 A–D 评价记录（与三条轨道正交）。本页只讲 dsh-standards 的 DS-C。

## 二、两种评估上下文（profile）

同一条款在不同上下文下的可判性不同，报告必须写明 `mode` / `profile`：

| profile | 输入 | 强制条款 | C-3 |
|---|---|---|---|
| `package`（作者本地，默认） | 本地包目录或 tarball | 11 条：C-1、C-2、C-4～C-9、C-11～C-13 | NA |
| `listing`（市场条目） | 市场索引条目（format-1 JSON）+ 固定来源 | 12 条：上述 11 条 + C-3 | PASS/FAIL |

- `package` 模式**不能**知道这个包将以什么 install spec 被安装，所以 C-3 整体 `NA`（reason: install spec not supplied）；C-3.3/C-3.4 的静态证据只以 advisory 附在报告里。
- 两个 profile 的徽章必须分别写 `DS-C (package)` / `DS-C (listing)`，不得混写。
- `dsh.kind` 缺失时 checker 会推断并在报告写 `kindInferred: true`；推断不覆盖作者显式声明，冲突时以声明为准并记 WARN。

## 三、22 条索引

层（L0–L3）是检查所需的运行深度，不是等级。强制/咨询按 **默认 `ds-c` package profile** 标注；C-3 是 listing 属性；C-14 是 entry scope 专属运行证据。

| 条款 | 检查 ID | 层 | 默认 DS-C | 一句话查什么 |
|---|---|---|---|---|
| C-1 清单与入口可解析 | `DSC-01` | L0 | 强制 | manifest / name / version / license / 入口 / `dsh.bundle.patch` / 导出形态 |
| C-2 版本约束声明且兼容 | `DSC-02` | L0 | 强制 | `engines.dsh` 或 `@deepseek-ai/dsh*` peer 被 runtime 满足 |
| C-3 安装来源可解析、可固定、可追溯 | `DSC-03` | L0 | listing 强制 | install spec / 市场索引条目 / 供应链固定性；package 模式 NA |
| C-4 可装载并进入 ACTIVE | `DSC-04` | L1 | 强制 | 装载到 ACTIVE |
| C-5 声明的 provide 必须真实注册 | `DSC-05` | L1 | 强制 | 声明的 `provide` 真实注册 |
| C-6 注册型副作用必须由 fiber 承载 | `DSC-06` | L1+L2 | 强制 | effect 拓扑 / 私有注册面 |
| C-7 卸载后可观测残留归零 | `DSC-07` | L2 | 强制 | 卸载后 effect / store / registry 归零 |
| C-8 重挂幂等、冷重装可重复 | `DSC-08` | L3 | 强制 | restart 幂等 + 冷重装 |
| C-9 声明 vs 实测交叉校验 | `DSC-09` | L1 | 强制 | 声明与实测交叉 |
| C-10 无 import 期副作用 | `DSC-10` | L0 | **咨询（advisory）** | import 期副作用静态信号；**永不 FAIL、不改变结论** |
| C-11 权限面声明（capabilities） | `DSC-11` | L0 | 强制 | `dsh.capabilities` + 高信号触发 |
| C-12 凭据与全局 seam 的声明一致性 | `DSC-12` | L0 | 强制 | backing file / `listRecords` / 全局 seam；并读 `dsh.commitments`/`dsh.disclosure` 形状 |
| C-13 高风险类别强制标注 | `DSC-13` | L0 | 强制 | `dsh.labels` / `upstream` / 禁用语标签 |
| C-14 运行证据 | `DSC-14` | L1 | entry scope 专属 | plugin 用 L1；bundle 用 G0–G3（`--host`）；ds-c 默认 NA |
| C-15 生命周期脚本 | `DSC-15` | L0 | security scope | 安装期脚本风险；security scope 下可 FAIL |
| C-16 动态执行 | `DSC-16` | L0 | security scope | `eval` / `Function` / `vm` / 危险子进程；security scope 下可 FAIL |
| C-17 敏感文件 | `DSC-17` | L0 | security scope | 直接读 `~/.ssh`、`$DSH_HOME` 凭据、`.aws`、`.npmrc` 等；security scope 下可 FAIL |
| C-18 外联端点 | `DSC-18` | L0 | **咨询（advisory）** | 非 https、裸 IP、webhook/paste/ngrok、未披露端点；永不 FAIL |
| C-19 混淆与二进制 | `DSC-19` | L0 | **咨询（advisory）** | 二进制 sha256、超长单行、大块 base64、高熵；只登记不判恶 |
| C-20 提示词注入 | `DSC-20` | L0 | **咨询（advisory）** | 外部文本进 systemPrompt、指令样字面量、无 untrusted 包裹；语义不可判 |
| C-21 供应链 | `DSC-21` | L0 | security scope（WARN-only） | 同形字/近似 scope、近似名、元数据缺失；**强制项但永不 FAIL、不阻断** |
| C-22 来源与完整性 | `DSC-22` | L0 | **咨询（advisory）** | 只读 `dist.integrity` / `dsh.attestation` / `dsh.signature` 字段是否存在；不验签、不比 digest |

> 注：C-10 与 C-18/C-19/C-20/C-22 是 advisory；C-3 只在 listing 判定；C-14 只在 entry scope 判定。表中“默认 DS-C”列不等于 `--scope security` 下的判定。

## 四、security scope 是独立、opt-in 的，不是第四层

- `dsh-standards check <dir> --scope security` 把 C-15～C-22 加入 `checks[]`（默认 14 项 → 22 项）。
- 它**不改变** DS-C 的 11/12 条强制集合，也**不改变** `DS-C` 徽章；它不能撤销 `DS-C` PASS，`DS-C` PASS 也不能证明 security 通过。
- 判定：C-15/C-16/C-17 在 security scope 下可 FAIL（blocking）；C-21 是强制项但 **WARN-only、不阻断**；C-18/C-19/C-20/C-22 是 advisory。
- 不传 `--scope security` 时，这 8 项只在报告的 `security` 块里以 advisory 模式运行，FAIL 封顶 WARN，不进 `checks[]`、不改变 exit code。
- security scope **仍不执行插件代码**（C-15 只读脚本文本，不跑 `npm install`）。
- security 的 PASS 只表示“本次静态/元数据覆盖内未见该类线索”，**不是安全审计**。实际网络流量、审批绕过、同进程逃逸、client DOM、`!!js` 配置等没有机械判据。

## 五、三值、NA 与覆盖度（禁止假绿）

| 结果 | 含义 |
|---|---|
| `PASS` | 有证据满足 |
| `WARN` | 部分满足 / 替代声明 / 证据不足；不阻断，但不等于 PASS |
| `FAIL` | 有明确反例；exit 1 |
| `NA` | 形态/上下文不适用；不是 PASS |
| `ERROR` | 检查器失效或该跑没跑；计入 `notRun`，exit 2 |

- 报告里的 `coverage` 统计报告携带的全部检查（`pass/warn/fail/na/notRun/error`）；`coverage.notRun` 包含 ERROR、未跑与缺失的强制项。
- 单条检查的 `coverage` 是 `full` / `partial` / `not-run`：`partial` 表示只跑了一部分子项，**同样不是 pass**（它仍计入 `notRun`，并单列到 `coverage.partial` / `buckets.partial`）。
- **结论门**：`--scope ds-c` 只有在 profile 对应强制项全部 `coverage: full`（允许显式 NA）时才给 `level`；否则 `status: incomplete`、exit 2。WARN 仍给 `level`，但写 `DS-C (profile, warnings)`，不冒充干净通过。

## 六、作者怎么读这张表

1. 先看 **profile**：本地自检默认 `package`；没有 install spec / 市场条目时 C-3 一定是 NA。
2. 再看 **scope**：默认 `ds-c` 看 14 项；要跑安全扩展才加 `--scope security`（仍不执行代码）。
3. 再看 **coverage**：`not-run` / `NA` / `partial` 都不是通过；强制项没跑全就是 `incomplete`（exit 2）。
4. 最后看 **anchor**：`repo/commit/scanner/dsh/cordis` 变了就是另一把尺子；旧报告不能当新版本的证据。
5. 你只对**自己包**的检查结果负责；不要用 `DS-C` 字面宣称“通过认证 / 安全 / 官方”。

## 七、dsh-ee 边界

- dsh-ee 不内置这套条款的检查器，不执行 checker，不在服务端保存 `DS-C` 结论。
- dsh-ee 的发布预检（`POST /api/me/packages/check`）是另一套静态检查，结果只决定 `can_submit` / `will_publish` / `requires_review`；**不能**映射成 DS-C PASS/FAIL。
- dsh-ee 不展示 `DSC-xx` 等级、不排名、不做准入门槛、不发 badge。
- 条款正文与最新判定以上游为准；本页对应上游 `v1.0.0-rc.2` 快照，命令固定 `dsh-standards@1.6.9`。

## 上游原文与相关页

- 条款正文：[31-DS-C条款.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/31-DS-C条款.md)
- 默认查什么（22 项口径）：[docs/coverage.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md)
- 检查器与 CLI：[40-checker/README.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/40-checker/README.md)
- 命名决定：[89-三套标准命名决定.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/89-三套标准命名决定.md)
- 本目录：[checker 命令与报告字段](./checker-cli.md)、[证据边界与常见误读](./evidence-and-misreadings.md)

**非官方 · 非认证 · 非安全审计。**
