# 证据边界与常见误读

> 本页回答两个问题：checker 的输出**能证明什么、不能证明什么**，以及最容易读错的几个结论。
> 所有状态词以 dsh-standards 1.6.9 的输出为准；本页不引入 dsh-ee 自己的等级或评分。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/status-taxonomy.md@19c410bd261d942c360386334d4841543a646579
Additional sources:
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md
- https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/SUPPORTED.md
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 合并上游 status-taxonomy.md 的状态词表与 docs/coverage.md 的读法，改为「证据边界 + 常见误读」问答结构
- 删除上游逐条失败类别表、报告模板与内部 results/ 引用；保留“状态词不等于通过”的规则
- 只引用 SUPPORTED.md 的结论性事实（可归属 runtime 格子为空），不复制其矩阵表格与内部证据编号
- 新增「dsh-ee 里怎么用这些证据」一节（dsh-ee 侧新增文字，非上游）
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、状态词表：先分清“通过 / 没跑 / 不适用 / 环境挡住”

| 状态 | 含义 | 等于“通过”吗 |
|---|---|---|
| `recognized` | 只做了静态识别 | 不等于（没有运行证据） |
| `static` | 静态检查跑了 | 不等于（没有运行证据） |
| `runtime` | 真的 boot/journey 跑过 | 对该 scope 是通过 |
| `manual` | 人工执行并记录旅程 | 仅对 FLOW 有效 |
| `measured` | 基准跑过并记录 | 仅对 BOOST 有效 |
| `blocked` | 环境/宿主/服务缺失 | **不等于** |
| `failed` | 被检对象失败 | **不等于** |
| `not-run` | 该跑没跑 | **不等于** |
| `not-measured` | 没有 BOOST 证据 | **不等于** |
| `not-applicable` / `NA` | 形态或上下文不适用 | **不等于** |
| `stale` | 证据过期或 host 锚点变了 | **不等于** |
| `withdrawn` | 作者/项目撤回 | **不等于** |

规则：

1. `blocked` ≠ `failed`：基础设施/宿主问题不是插件的错，也不能当通过。
2. `not-run` ≠ `not-applicable`：“没跑”和“不该跑”是两件事。
3. `static` ≠ `runtime`；`client-module-loaded` ≠ 界面渲染完成。
4. `measured` 只用于 BOOST；`manual` 只用于 FLOW；不要互相借用。
5. `stale` 由读者依据 `expiresAt` / 锚点判断，不要盲信写者。
6. 一个 `PASS` 需要命令/产物；跳过永远不能被升级成通过。

## 二、报告层面的三条硬规则

1. **`not-run` / `NA` / `partial` / `ERROR` 都不是 PASS。** 报告里的 `coverage.notRun` 把这些都算进去；`buckets.notChecked` 会列出来。
2. **exit 2 不是失败，也不是通过。** 它表示 incomplete（强制项没跑全/检查器内部失败），`conclusion.status=incomplete`。
3. **exit 0 不是干净通过。** 可能带 WARN；`conclusion.level` 会写 `(warnings)`，只有强制项覆盖完整才给 `level`。

## 三、证据归属：谁说的、能不能信

- **自声明（self-attested）**：结构自洽 + 绑定此包，但**不证明真跑过**。
- **CI 归属不是证明**：runner 上的 `GITHUB_RUN_ID` / `GITHUB_REPOSITORY` / `GITHUB_SHA` 来自环境变量，是自我声明，不升级 `claims.claimLevel`。
- **sandbox 只做 containment**：`--sandbox` 用隔离临时 HOME/`DSH_HOME`、`unshare`、`prlimit` 等降低风险；它不是 seccomp/cgroup/OCI 镜像/签名，不是认证，也不是安全审计。任一隔离项缺失就写 `sandbox-degraded`，仍是 self-attested。
- **锚点变了，证据就作废**：`repo/commit/scanner/dsh/cordis` 任一变化就是另一把尺子；旧报告不能当新版本的证据。
- **上游支持矩阵的现状**：`SUPPORTED.md` 由结果生成，其“支持矩阵”目前没有可归属的 `runtime` 格子——这说明 **PASS 不可移植**，不是“大家都通过了”。请以该页当前内容为准，不复制具体格子。
- **过期的证据等于没有**：`expiresAt` 过了、或宿主版本/commit 对不上，按 `stale` 处理，不采信。

## 四、常见误读（FAQ）

### 1）“check 过了 = 认证 / 等级 / 官方认可？”

不是。checker 是作者本地工具；`DSH-ENTRY/FLOW/BOOST` 是三条采用深度，不是三个等级；没有认证、没有官方背书、没有排名。层名受上游 mark policy 约束，用时必须同视图带免责声明。

### 2）“exit 2 = 检查失败？”

不是。exit 2 是 **incomplete**：强制项没跑全（例如默认静态检查没有 L1–L3 证据），或检查器内部失败。它既不是失败，也**不能当通过**。

### 3）“`recognized` / `static` 就是 PASS？”

不是。它们只表示“能被识别 / 静态检查跑了”，没有运行证据；`interop-static` 也不是运行通过。

### 4）“有 WARN 就是通过（只是不干净）？”

不是。WARN 不阻断（exit 0），但不是 PASS；报告会给带 `(warnings)` 的 `level`，不能对外宣称“通过检查”。

### 5）“`--scope security` 通过 = 安全审计 / 没有安全问题？”

不是。security scope 是静态/元数据线索检查：C-15/16/17 可 FAIL，C-21 只 WARN，其余 advisory。它只降低风险，**不证明无恶意**；同进程插件仍可动态读取远程代码。不是安全审计，不是认证。

### 6）“BOOST 有 measured = 性能更好 / 省钱 X%？”

不是。BOOST 是实践库与证据卡；证据只对给定锚点、给定方法有效。不能换算成费用节省，不能当生产基线，也不能和 FLOW 比高低。

### 7）“DS-C 是第四层 / 第四个等级？”

不是。DS-C 是 v1 的基线条款集，checker 默认 scope 仍叫 `ds-c`；ENTRY/FLOW/BOOST 才是 v2 的三条采用深度。DS-C 不是第四层。

### 8）“dsh-ee 的发布预检通过 = dsh-standards 通过？”

不是两回事。dsh-ee 预检是另一套静态检查（结构、安装脚本、密钥、License、体积等），只决定 `can_submit` / `will_publish` / `requires_review`；它不产出 `dsh-conformance/1`，不能映射层级。

### 9）“`dsh-levels audit` 默认会给我三层结论？”

不会。ENTRY 由 checker 自动判定；FLOW / BOOST 只读 `--flow-evidence` / `--boost-evidence`，没有证据就写 `not-run` / `not-measured`，工具不会替作者升级。

### 10）“报告里有 `level` 字段，所以这是官方分级？”

不是。`level` 是展示用标签（例如 `DS-C (package, warnings)`），来自 checker 的结论门；它只对当前 profile/scope 的条款覆盖有效，不是等级证书。`dsh-report/1` 还明确禁止 `grade`/`level` 字段。

### 11）“上游是 PerryLink 的，PerryLink = 官方？”

不是。PerryLink/dsh-standards 是社区项目，与 DeepSeek 官方无关；dsh-ee 也是社区/企业项目。不要把社区项目写成官方标准或官方认证机构。

### 12）“把 results 里的数字抄进 README 当卖点？”

不要。上游 `results/` 属于未随文本许可单独授权的材料，本目录不复制；数字会随锚点过期，也不是对外结论。自己跑出的报告要带命令、锚点、日期。

## 五、dsh-ee 里怎么用这些证据

- **只在作者本机跑**：dsh-ee 服务端不执行 checker，不接 Node 依赖；本目录只给命令。
- **不把证据变成准入/排序**：dsh-ee 的 `can_submit` / `will_publish` / `requires_review` 不由 checker 报告决定；不展示层级、分数、排名、badge。
- **如果将来要展示作者证据链接**：只读、`self-declared`、带日期与过期说明，并同视图带 `非官方 · 非认证 · 非安全审计`；不改变准入与排序。该能力目前不在 dsh-ee 范围内。
- **文案禁词**：`official / certified / secure / recommended / ranked / 通过 / 认证 / 官方`（在“非…”的免责声明里除外）。层名出现时必须同视图带 `status + scope + date + evidence` 与免责声明。

## 六、一句话

**checker 输出的是“在某个锚点上、某个 scope 内、查到了什么”，不是“这个东西合格/安全/被认证”。**

## 上游原文与相关页

- 状态词表：[docs/status-taxonomy.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/status-taxonomy.md)
- 默认覆盖口径：[docs/coverage.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/docs/coverage.md)
- 支持矩阵（由结果生成）：[SUPPORTED.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/SUPPORTED.md)
- 层名使用规则（上游 governance，本目录未复制）：[governance/mark-policy.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/governance/mark-policy.md)
- 本目录：[ENTRY](./entry.md)、[FLOW](./flow.md)、[BOOST](./boost.md)、[checker 命令与报告字段](./checker-cli.md)

**非官方 · 非认证 · 非安全审计。**
