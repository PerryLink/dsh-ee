# checker 安装、命令、退出码与报告 JSON 字段

> 面向 dsh-ee 插件作者：在**本机**运行 `dsh-standards@1.6.9` 检查你的包，并读懂输出。
> dsh-ee 服务端**不执行** checker，不新增 Node 依赖；下面的命令只供本地复制。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/40-checker/README.md@19c410bd261d942c360386334d4841543a646579
Additional sources:
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/spec/schemas/conformance.schema.json
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/spec/schemas/advice.schema.json
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 重组为 dsh-ee 作者视角：安装（固定 1.6.9）→ 常用命令 → 退出码 → 报告字段 → 边界
- 删除上游内部测试数据、results/ 路径、脚本目录清单与开发/测试章节；这些与作者使用无关
- 报告 JSON 字段表按 1.6.9 的 schema 与 README 重写为事实描述，未复制 schema JSON 原文（上游 schema 为 Apache-2.0）
- 新增「报告字段之外：不要把这些字段读成等级」「dsh-ee 边界」两节（dsh-ee 侧新增文字，非上游）
- 命令统一固定 dsh-standards@1.6.9；上游 1.6.x 小版本行为差异以 release notes 为准
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、安装（固定版本）

要求：Node `>=24.9.0`。检查器零依赖、无构建步骤。

```sh
# 项目内固定版本（推荐写进 devDependencies，避免 npx 同名包冲突）
npm i -D dsh-standards@1.6.9

# 或全局安装
npm i -g dsh-standards@1.6.9

# 不安装也可以，用 npx 固定版本
npx dsh-standards@1.6.9 check .
npx -p dsh-standards@1.6.9 dsh-levels audit .
```

**不要**直接 `npx dsh-levels`、`npx dsh-plugin-verify`：npm 上这两个裸名属于其他项目。`dsh-levels` 必须通过 `-p dsh-standards@1.6.9` 指定包。npm 上早期的 `dsh-standards@0.0.3` 只是旧占位，应固定到 `1.6.9`。

两个公开命令：

| 命令 | 作用 |
|---|---|
| `dsh-standards check [dir]` | 作者入口：默认输出 Markdown；`--json` / `--html` |
| `dsh-standards <dir> --format json\|text\|sarif --out <file>` | 兼容的原始检查器入口（旧用法） |
| `dsh-levels audit [dir]` | 三层自检：输出 `dsh-level-audit/1`（ENTRY 自动，FLOW/BOOST 只读证据） |

## 二、常用命令

```sh
# 默认：作者入口，Markdown，静态检查，不执行插件代码
npx dsh-standards@1.6.9 check .

# 原始 dsh-conformance/1 JSON（给 CI / 存证）
npx dsh-standards@1.6.9 check . --json > report.json

# 单文件 HTML（内联 CSS、零外部资源、全转义）
npx dsh-standards@1.6.9 check . --html dsh-check.html

# 只做 entry 静态识别（不声明运行）
npx dsh-standards@1.6.9 check . --scope entry --entry-claim static

# entry 运行时：显式给 Cordis 运行时（会执行插件，作者自担）
npx dsh-standards@1.6.9 check . --scope entry \
  --runtime /path/to/cordis/lib/index.js --runtime-version <宿主版本>

# bundle：给 --host 才会跑 G0–G3；没有 host 只能是 incomplete
npx dsh-standards@1.6.9 check . --scope entry --host /path/to/app-boot/lib/index.js

# 安全扩展（opt-in；仍不执行插件代码）
npx dsh-standards@1.6.9 check . --scope security --json

# listing profile：需要市场索引条目 + 包名（否则 C-3 是 NA）
npx dsh-standards@1.6.9 check . --profile listing \
  --market-index ./market.json --plugin-name @acme/dsh-hello

# 三层自检（FLOW/BOOST 没证据文件就如实记 not-run / not-measured）
npx -p dsh-standards@1.6.9 dsh-levels audit . --format json > audit.json
```

补充规则：

- `check` 默认等价于 `--entry-claim static --no-exec --scope ds-c --profile package`；**不执行被检插件代码**。
- `check` 的 `--json` 与 `--html` 互斥；`--format` / `--out` 不转发给 `check`，用 `--json` / `--html`。
- `check --format json` 是**用法错误**（exit 3）；要 JSON 用 `--json`。
- `--runtime-version` 只是 anchor，不是执行开关；要执行必须显式 `--runtime` / `--host` / `--entry-claim runtime`。
- host 版本解析顺序：`--host-version` → `$DSH_HOST_VERSION` → 显式 `--allow-host-probe`（只跑 `dsh --version`）；都没有时报告写“host 版本未知”，`anchor.runtimeSource=default`、`suspect=true`。
- 其他 flag 原样转发给底层检查器，例如 `--scope`、`--profile`、`--market-index`、`--plugin-name`、`--install-spec`、`--no-exec`、`--cordis-version`。

## 三、退出码

| exit | 含义 | 是不是“失败” |
|---|---|---|
| `0` | 完成，无 FAIL（可能带 WARN；不会出现 `incomplete`） | 不是失败；但有 WARN 也不代表干净通过 |
| `1` | 完成，存在 FAIL | 是失败（有明确反例） |
| `2` | **incomplete**：强制项 not-run / ERROR，或检查器内部失败 | **不等于检查失败**；是“没查全”，不能当 PASS |
| `3` | 用法错误（参数冲突/未知参数） | 是调用错误，不是被检包的问题 |

关键点：

- **exit 2 ≠ 失败**。它表示这份报告没有完成覆盖（例如默认静态检查下 L1–L3 没跑），报告 `conclusion.status=incomplete`、`conclusion.exitCode=2`。**也不要把 exit 2 当通过。**
- **exit 0 ≠ PASS**。可能只有 WARN；结论门只在强制项覆盖完整时才给 `level`，而且 WARN 会写成 `DS-C (profile, warnings)`。
- 检查器在写报告后会强制退出（避免插件留下的 handle 挂住进程）；CI 里直接看 exit code 即可。

## 四、Markdown 报告怎么读

`dsh-standards check` 默认输出 Markdown，结构大致是：

1. `conclusion.headline`：一句话结论（含 blocking / hygiene 计数）；
2. 至多 3 条优先修复建议；
3. `buckets` 三栏全表：`blocking`（FAIL）、`hygiene`（WARN）、`notChecked`（NA / not-run / ERROR）；
4. `advice[]`：逐条修复建议（`why` / `fix` / `docsUrl`）；
5. 免责声明与“没查什么”。

`notChecked` 一栏就是证据边界：**没检查不等于通过**。默认静态检查下，plugin 的 C-4～C-9（L1–L3）会是 `not-run`，bundle 的 C-4～C-9 以静态子集 `PASS + partial` 计入（partial 仍不是 pass），C-3/C-14 是 NA。

## 五、报告 JSON 字段（`dsh-conformance/1`）

`check --json` 输出原始 `dsh-conformance/1` 文档。下面按 1.6.9 的 schema 列出**作者需要读的字段**；机器校验请直接用上游 schema（Apache-2.0）：
[conformance.schema.json@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/spec/schemas/conformance.schema.json)。

### 5.1 顶层字段

| 字段 | 含义 |
|---|---|
| `schema` | 固定 `"dsh-conformance/1"` |
| `standard` | `{id, version}`：标准族（如 `DS-C`）与条款文档版本 |
| `subject` | 被检对象：`mode`（package/listing）、`profile`、`kind`、`kindInferred`、`kindEvaluated`、`name`、`version`、`path`；`source` 为 1.6.8 起可选的输入形态 |
| `anchor` | 五部分锚点 + 来源：`repo`、`commit`、`scanner`、`dsh`、`cordis`、`runtime`、`runtimeSource`（cli/host/fallback/default）、`hostVersion`、`suspect`；作者路径还会带 `hostVersionKnown`、`hostVersionSource`（flag/env/probe/none） |
| `g4` | 静态 G4 client 形态：`{hasClient, ok, result, reason}` 或 `null` |
| `checks[]` | 检查项数组（至少 1 项）；字段见 5.2 |
| `coverage` | 覆盖度计数；字段见 5.3 |
| `buckets` | 阅读分组：`blocking[]` / `hygiene[]` / `notChecked[]`（可附 `partial[]`） |
| `metrics` | `{checked, total, declaredFields}`；`declaredFields` 是 5 个声明面（capabilities/labels/commitments/disclosure/engines.dsh）的声明个数，只作上下文 |
| `conclusion` | 结论；字段见 5.4 |
| `advice[]` | **加性**修复建议，`--no-advice` 可关闭；**永不改变判定**；每项 `{key, gate, severity, title, why, fix, docsUrl, source, version, evidence, reason, occurrences, advisory}`，`evidence` ≤10 条 |
| `notChecked[]` | **加性**扁平视图：`buckets.notChecked` + 部分检查（`coverage=partial`），含 `howToEnable`；旧消费者可忽略 |
| `runtimeChecks` | **加性**运行块（显式运行时运行的 `executed`、`modes`、`durationMs`、`isolation`、`reason`、`failure`、`checks`、`entry`） |
| `safety` | 安全默认/沙箱相关块（按版本输出） |
| `security` | security 块；字段见 5.5 |

### 5.2 `checks[]` 单项

| 字段 | 含义 |
|---|---|
| `id` | 检查 ID，形如 `DSC-01`…`DSC-22` |
| `clause` | 条款 ID，形如 `C-1`、`C-2.2` |
| `layer` | 检查所需运行层：`L0` / `L1` / `L1+L2` / `L2` / `L3`（不是等级） |
| `result` | `PASS` / `WARN` / `FAIL` / `NA` / `ERROR` |
| `coverage` | `full` / `partial` / `not-run`；`partial` 不是 pass |
| `reason` | 人类可读原因 |
| `evidence` | 证据字符串数组 |
| `details[]` | 子项明细：`{item, result, reason, ...}`（部分版本带 `vendor` 等加性字段） |
| `method` | `M`（机器）/ `M+H`（机器 + 人工）/ `H`（人工） |
| `advisory` | 是否为咨询项：咨询项**永不改变判定** |
| `howToEnable` | 加性：让该 NA/partial 项能跑起来的命令/参数 |

### 5.3 `coverage`

| 字段 | 含义 |
|---|---|
| `total` | `checks[]` 总数 |
| `mandatory` | 当前 scope/profile 的强制项数 |
| `checked` | 结果为 PASS/WARN/FAIL 的项数 |
| `pass` / `warn` / `fail` / `na` / `error` | 各结果计数 |
| `advisory` | 产生 WARN 的咨询项数（是 `warn` 的子集，不改变判定） |
| `notRun` | `coverage=not-run/partial`、ERROR 与缺失强制项的总和（包含 `error`） |
| `entryBlocking` | entry scope 的阻断前提摘要；`ds-c`/`mvp` 下为 `null` |
| `partial` | 加性：`coverage=partial` 的项数（与 `checked`/`notRun` 有重叠，作为显式诊断） |

### 5.4 `conclusion`

| 字段 | 含义 |
|---|---|
| `headline` | 一句话结论 |
| `level` | 展示用标签（如 `DS-C (package, warnings)`）；无法声明时为 `null` |
| `status` | `pass` / `warn` / `incomplete` / `fail` |
| `exitCode` | `0` / `1` / `2`（与进程 exit 一致） |
| `profile` | `package` / `listing` / `both` |
| `scope` | `ds-c` / `entry` / `mvp` / `security` |
| `subset` | 仅 MVP 子集为 `"MVP-13"`，否则 `null` |
| `staticOnly` | 是否只跑了静态 |
| `entryRuntime` | `require` / `server-entry` / `null` |
| `entryClaim` | `runtime` / `static` / `null` |
| `reason` | 结论原因 |

### 5.5 `security` 块

| 字段 | 含义 |
|---|---|
| `scope` | `security`（`--scope security`）或 `advisory`（默认 scope 内的咨询块） |
| `status` | `pass` / `warn` / `fail` / `incomplete` |
| `checks[]` | 结构同 `checks[]`；`DSC-15/16/17` 在 security scope 下可 FAIL，`DSC-21` 强制但 WARN-only |
| `blocking[]` / `warn[]` / `advisory[]` / `notRun[]` | 按结果分列；security 没有独立 `partial[]`，看 `checks[].coverage` |
| 边界 | 默认 scope 下 FAIL 封顶 WARN，不进 `buckets.blocking`；security PASS 不等于安全审计 |

### 5.6 三层自检 `dsh-level-audit/1`

`dsh-levels audit` 输出另一份 schema（`dsh-level-audit/1`），核心字段：

| 字段 | 含义 |
|---|---|
| `schema` | `"dsh-level-audit/1"` |
| `generatedAt` | 生成时间 |
| `target` | `{dir}` |
| `levelsSpec` | 引用的 `spec/levels.json` 版本 |
| `levels.entry` / `levels.flow` / `levels.boost` | 每层状态（如 `recognized` / `not-run` / `not-measured`） |
| `attestation` | FLOW 证据的归属（如 `ci` / `self-declared`；CI 环境变量是自我声明，不是证明） |
| `subject` | 证据主体绑定结果（带 `--dir` 时重算/比对包身份） |
| `nextActions` | 下一步建议 |
| `notClaimed` | 明示“不声明”的列表（官方标准 / 认证 / 安全审计 / 排名 / 生产基线） |
| `checker` | 底层 checker 的调用信息：`invoked`、`exit`、`schema`、`anchor`、`coverage` |

其他机器契约：`dsh-levels/1`（证据）、`dsh-report/1`（明确禁止 `grade`/`level` 字段）、`dsh-flow-evidence/2`、`dsh-advice/1`。作者通常只需要 `dsh-conformance/1` 与 `dsh-level-audit/1`。

## 六、报告字段之外：不要把这些字段读成等级

- `layer: L0…L3` 是“检查需要多深的运行”，不是插件等级；
- `coverage` / `buckets` / `metrics` 是**覆盖度与阅读分组**，不是分数；`declaredFields` 只作上下文；
- `advice[]` 是修复建议，**永不改变判定**；`--no-advice` 关掉它也不改 exit code；
- `security` 块不改变默认 DS-C 判定，也不代表安全审计；
- `notClaimed` 是上游明示的“不声明”列表，出现它不代表通过任何认证。

## 七、平台与已知限制

- **Windows**：`unshare` / `prlimit` 不可用，runtime/sandbox 相关检查会 `not-run` / `degraded`；静态检查（默认 `check`）仍可跑。上游支持矩阵的 Windows 行固定 `not-run`，不要据此声称 runtime 结论。
- **Node**：包声明 `engines.node >=24.9.0`；Node 22 只在上游自测/静态矩阵中有记录。
- **tarball**：检查器目标只支持**目录**；npm 包/ tarball 解包不在当前版本内，作者请先解包或用仓库目录。
- **cwd 默认**：`check` 不带目录时检查当前目录。
- 上游 1.6.x 仍在迭代，字段以**加性**变化为主；固定版本运行，升级前看 release notes。

## 八、dsh-ee 边界

- dsh-ee **不在服务端或浏览器执行** checker：Go 服务端不带 Node 运行时，checker 的 `--runtime/--host` 会执行插件代码，属于作者本机行为。
- dsh-ee **不新增服务端依赖**，不接 checker 的 Node 包；本页命令只是可复制文本。
- dsh-ee 的发布预检（`POST /api/me/packages/check`）是另一套静态检查；它的结果与 `dsh-conformance/1` **不是同一份报告**，不能互相换算。
- dsh-ee 不展示层级/分数/排名，也不把报告字段变成准入或排序依据。

## 上游原文与相关页

- 检查器说明：[40-checker/README.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/40-checker/README.md)
- 默认查什么：[docs/coverage.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md)
- 报告 schema：`spec/schemas/conformance.schema.json`、`spec/schemas/advice.schema.json`（同 commit）
- 本目录：[DS-C 条款索引](./ds-c-clause-index.md)、[证据边界与常见误读](./evidence-and-misreadings.md)、[ENTRY](./entry.md)

**非官方 · 非认证 · 非安全审计。**
