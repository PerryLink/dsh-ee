# 作者自检清单：三层最短路径 + 评审一分钟

> 上游是 **三层开发建议与验收清单**（上游版本 `v1.1.0`，日期 2026-11-04，CC BY 4.0）。
> 本页把 ENTRY / FLOW / BOOST 落成作者能照做的清单：最短路径、逐层自检、评审一分钟。每层只回答自己的问题，没有证据就写 `not-run` / `not-measured`。

```text
Source: https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/35-三层开发建议与验收清单.md@19c410bd261d942c360386334d4841543a646579
License: CC BY 4.0 (https://creativecommons.org/licenses/by/4.0/)
© 2026 PerryLink
Changes:
- 改写为 dsh-ee 作者最短路径 + 评审一分钟清单：保留三层目标/每层 5 件/验收标准、ENTRY/FLOW/BOOST 清单、常见 ENTRY 失败与评审清单
- 删除上游内部文档引用（34/36/93/94/95/40-checker/scripts）、内部版本基线（0.2.2-alpha.3、commit、17 个外部 PR 证据）与“和现有文档关系”一节
- 命令从仓库内脚本改为公开 npm 固定版本（dsh-standards@1.6.9 / dsh-levels）
- 新增「dsh-ee 侧怎么用这张清单」与「诚实写法」两节（dsh-ee 侧新增文字，非上游）
- 保留上游版本号与日期；具体版本断言以你的目标宿主与上游当前版本为准
Date: 2026-11-12
Modified version: dsh-ee docs
非官方·非认证·非安全审计 / Not official, not a certification, not a security audit.
```

## 一、作者最短路径

| 层 | 目标 | 先做这 5 件 | 验收 | 命令（固定 1.6.9） |
|---|---|---|---|---|
| **ENTRY** | 装完能跑 | ① `dsh.bundle.patch` 可解析；② server/client 入口存在；③ peer 范围匹配当前 DSH；④ 不依赖安装脚本才能挂根行；⑤ 真实 boot G0–G3 | `ENTRY=runtime` 或至少 `recognized` | `npx dsh-standards@1.6.9 check . --scope entry --entry-claim static`（runtime 需 `--runtime` / `--host`） |
| **FLOW** | 用起来顺 | ① 安装/首跑可复现；② 配置有 schema 且报错可读；③ 失败可恢复；④ 升级/迁移有路径；⑤ README/卸载/客户端四态 | `J1–J8` 有记录（允许 `manual`） | `npx -p dsh-standards@1.6.9 dsh-levels audit . --format json`（FLOW 需 `--flow-evidence`） |
| **BOOST** | 用 Cordis 更可靠 | ① effect 收尾；② inject / `ctx.get` 正确；③ volatile 热配置；④ 事件 mode 正确；⑤ 并行/jobs/证据卡 | `LEV-*` 有 `measured` 证据卡 | `npx -p dsh-standards@1.6.9 dsh-levels audit . --boost-evidence boost.json --format json` |

**不要跳级，也不要把顺序读成成熟度**：

- `BOOST` 的漂亮基准不能替代 `ENTRY` 的 G0–G3；`ENTRY` 通过也不能证明 `FLOW`；
- `FLOW` 验旅程、`BOOST` 验实践，两者没有同一量纲，不存在“BOOST 比 FLOW 高/低”或“省 X%”的结论；
- `BOOST` 是指导性实践，目前比 FLOW 更不成熟，不是“最高级”。

## 二、ENTRY 清单（采用基线）

### 2.1 包与入口

- [ ] `package.json` 有 `name` / `version` / `license`；版本是 semver。
- [ ] `exports["."]` 指向真实存在的 server entry；bundle 还要 `dsh.bundle.patch`。
- [ ] `exports["./client"]` 存在时，`dsh.client.platform` 与入口一致；包根行存在（G4）。
- [ ] `engines.node` 覆盖目标 Node（当前 0.2.2 线是 `>=24.9.0`；更早的线不同，以目标宿主为准）。
- [ ] `peerDependencies` 的 `@deepseek-ai/dsh-*` 范围能容纳当前宿主；不要把宿主包放进 `dependencies`。
- [ ] 声明 `engines.dsh` 时，安装/启动只由它决定，且预发布参与匹配；范围要显式写预发布（如 `>=0.2.2-0 <0.3.0`）。
- [ ] 没有 `workspace:*` 之类的协议泄漏到发布 tarball 的 **runtime** 依赖（devDependencies 也应清理/不参与安装）。

### 2.2 patch 与组合

- [ ] `cordis.patch.yml` 只做 `insert` / 合法 `override`；`insert` 的每个 `id` / `name` 可解析。
- [ ] 需要根行的插件，根行必须在**包内 patch 或明确的安装期 profile 写入**中二选一；不能假设用户会手写。
- [ ] override 的目标行在 base/profile 中存在；升级后跑 `--dump-config` 查 `patch: entry not found`。
- [ ] 不在 patch 里写死宿主私有路径/端口/密钥。

### 2.3 运行时

- [ ] 在隔离 `DSH_HOME` 里 `dsh plugin add` 成功；安装日志没有硬错误。
- [ ] 锁文件能通过 `pnpm install --frozen-lockfile`（改依赖必须同步 lock）。
- [ ] 真实 `boot()` 后目标行 `state===ACTIVE` 或 `disabled`；pending 行逐条列出缺的服务。
- [ ] 缺服务时插件不崩、不无限重试；服务恢复后能 ACTIVE。
- [ ] 报告带 `dsh` / `cordis` 版本、commit、产物 mtime；过期标 `stale`。

### 2.4 常见 ENTRY 失败

| 症状 | 根因 | 修法 |
|---|---|---|
| `dsh plugin add` 直接拒绝 | peer range 排除当前宿主 | 放宽上界到 `<0.3.0`，并做运行时探测 |
| `ERR_PNPM_IGNORED_BUILDS` | 依赖 postinstall 才能挂根行 | 把根行放进包内 patch，或文档化 `--allow-build` |
| `ERR_UNSUPPORTED_PROTOCOL workspace:` | 发布 tarball 带 workspace 协议 | 发布前清理 devDependencies / 锁文件 |
| G3 0/N pending | 只等 `webServer/connection/agentPresets` | 在 web profile 复测，或把行标 `disabled` 并写清 |
| `patch: entry not found` | override 目标不在 base | 改成 `insert`，或对准真实 `id` |

## 三、FLOW 清单（推荐采用）

FLOW 不是“文档写得多”，而是**用户旅程能复现**。每条至少记录：入口、预期、失败提示、恢复、证据。

- [ ] **J1 安装/移除**：`dsh plugin add` → `--dump-config` 出现层 → `remove` → 层/依赖消失；不手改 profile。
- [ ] **J2 启用/停用**：默认开/关可预期；稳定 `id`；开关前后 `dump` 差异正确。
- [ ] **J3 配置**：有 schema + 默认值；非法值加载期 FAILED 并带字段路径；volatile 热改不重启。
- [ ] **J4 首次成功**：README 5 分钟 quickstart；错误三要素（什么错 / 为什么 / 怎么修）。
- [ ] **J5 权限/披露**：capabilities / labels / commitments 与 README 一致；不冒充官方；禁用词 0。
- [ ] **J6 升级/回滚/卸载**：两个版本可升级；数据保留；回滚 ≤2 命令；卸载 0 残留。
- [ ] **J7 可观测/诊断**：启动一行 `name@version`；正常 / 缺服务 / 端口占用都有可读诊断；日志路径可查。
- [ ] **J8 client/server 形态**：headless / web / client-off 三态可用或明确拒绝；console error 0。

**FLOW 的诚实写法**：`FLOW=manual(J1–J8)` 或 `FLOW=runtime(J1–J8)`；没有跑就写 `not-run`，不要写“应该没问题”。

## 四、BOOST 清单（指导性实践）

每条都要**证据卡**：环境、输入规模、命令、负向控制、结果、版本锚、限制。

| LEV | 改什么 | 证据 |
|---|---|---|
| LEV-1 可逆 | 定时器/连接/watcher/子进程进 `ctx.effect()`；不模块级持有 | restart N 次 `getEffects()` 回基线；reload 100 次泄漏 0 |
| LEV-2 等待 | 必需服务 `inject`；可选 `ctx.get()`；禁轮询 | 缺服务 0 TypeError；恢复后 1 epoch ACTIVE |
| LEV-3 热配置 | 可调字段 `volatile()` + `.get()` | 配置变更 restart 0、effect dispose 0 |
| LEV-4 事件 | 事件名/`@mode` 一致；waterfall 调 `next()` | mode 不一致 0；监听器故障隔离 1/1 |
| LEV-5 seam/隔离 | 只经 seam；多实例才 isolate/group | consumer 改动 0；两 realm 2/2；`leakedServices 0` |
| LEV-6 前缀/装配 | 工具/提示逐字节稳定；易变信息走 `systemPrompt.context` | prefix 复用 bytes/tokens；两次 boot schema diff 0 |
| LEV-7 并行/长活 | 独立调用有界并行；长活 jobs；取消贯穿 | `T≈max(T_i)`；kill 后 100% 停止 |
| LEV-8 组合/证据卡 | bundle patch/preset 表达差异；每实践附卡 | 新增层 1；单行启停不影响其他；证据卡可复跑 |

**BOOST 的诚实写法**：`BOOST=measured(LEV-3, LEV-6)`；引用真实 provider 结果时必须同时写“构造臂、单包/单模型/单宿主、未测延迟、未对账账单”；没有真实 provider 就写 `provider-capture (synthetic)`。可靠性只认 LEV-1 这类配对实测；性能与费用未对账，不能写成具体倍数或金额。

## 五、评审者一分钟清单

1. 先问：**哪一层？证据状态？** 只答“兼容/高级”的退回。
2. 看 `ENTRY`：G2 peer、G3 ACTIVE、G4 client root、G5 新鲜；`partial` 要逐条列缺的服务。
3. 看 `FLOW`：J1–J8 有没有记录；`not-run` 不能写成推荐。
4. 看 `BOOST`：证据卡有没有负向控制 / 版本锚 / 限制；没有就不是 `measured`。
5. 看措辞：有没有 `official` / `certified` / `verified` / `audited` / `secure` / `recommended` / `ranked`；有没有把 `static` 写成 `runtime`。
6. 看冲突：作者自报与机器结果不一致时，以可复现 JSON 为准。

## 六、诚实写法

| 情况 | 写什么 | 不要写 |
|---|---|---|
| 只跑了默认静态 check | `static` / `incomplete`（exit 2） | `通过` / `PASS` / `ENTRY 达标` |
| runtime 只跑了一部分 | `partial` + 缺的服务列表 | `runtime` |
| FLOW 没有证据 | `not-run` | `应该没问题` / `推荐` |
| BOOST 没有真实 provider | `provider-capture (synthetic)` / `not-measured` | `省 X%` / `性能提升 X 倍` |
| 证据过期或锚点变了 | `stale` | 继续当现值 |
| 只有作者自述 | `self-declared` + 日期 | `verified` / `audited` |

## 七、dsh-ee 侧怎么用这张清单

- dsh-ee 的发布预检（`POST /api/me/packages/check`）是**另一套静态检查**，结果只决定 `can_submit` / `will_publish` / `requires_review`；它不产出 ENTRY/FLOW/BOOST 层级，也不替代本清单。
- 审核时把本清单当**提问清单**，不是打分表或准入分：可以要求作者给出命令/日志/证据文件（`self-declared` + 日期），但不要把它转成上架排序、等级或 badge。
- dsh-ee 服务端不执行 checker；上面的命令都在作者本机运行。
- 不把 `static` / `not-run` / `WARN` 写成“通过”；对外文案带 `非官方 · 非认证 · 非安全审计`。

## 上游原文与相关页

- 上游原文：[35-三层开发建议与验收清单.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/35-三层开发建议与验收清单.md)
- 上游三层模型：[34-三层分级模型-GB-GBT-GBZ类比.md@19c410b](https://github.com/PerryLink/dsh-standards/blob/19c410bd261d942c360386334d4841543a646579/34-三层分级模型-GB-GBT-GBZ类比.md)
- 本目录：[ENTRY](./entry.md)、[FLOW](./flow.md)、[BOOST](./boost.md)、[checker 命令](./checker-cli.md)、[capabilities/labels 声明面](./capabilities-and-labels.md)、[证据边界](./evidence-and-misreadings.md)

**非官方 · 非认证 · 非安全审计。**
