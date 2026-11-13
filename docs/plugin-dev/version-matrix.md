# DSH / dsh-ee 版本适配矩阵

> **来源与许可**
> - 来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`guide/version-matrix.md`
> - 上游文件（固定 commit，公开链接）：[guide/version-matrix.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/version-matrix.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)、[docs/entry.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/entry.md)、[docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)、[SKILL.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/SKILL.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`docs/levels.md`、`docs/entry.md`、`docs/release-checklist.md`、`SKILL.md`（Apache-2.0）——用于校正 `engines.dsh` / Node 口径；dsh-ee 侧事实来自本仓库基线 `20aafbb` 的 `server/internal/registry/*.go`、`docs/compatibility.md`（原创/代码事实，非上游改编）。
> - 修改说明：中文节选并改写；删除上游的社区数字、`references/`/`downloads/` 本地路径与带日期的生态统计；把「npm 当前值」明确标为写作时快照；新增 dsh-ee 视角矩阵、推荐宿主与验证状态、`engines.dsh` 口径修正（0.5.5 指南：自 DSH 0.2.1-alpha.4 起该字段存在时单独决定）。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档。版本值会过期，**使用前必须重跑 §7 的自检命令**；冲突时以目标宿主实际行为与官方原文为准。

## 0. 口径

- 上游测量时间：2026-10-09（官方版本线）/ 2026-10-13（文档与镜像同步）—引自 `dsh-primer@0.4.3`。
- 上游补充口径：`dsh-plugin-guide@0.5.5` 的基线为 DSH `0.2.2-alpha.3`（npm `alpha`）、Node `>=24.9.0`（自 DSH 0.2.2-alpha.2 起）、Cordis `4.0.6-alpha.2`。
- dsh-ee 侧事实时间：仓库基线 `20aafbb`（2026-11-12）；dsh-ee 自身没有 git tag，版本由构建注入（`Makefile` 的 `VERSION ?= git describe --tags --always --dirty`，`-ldflags -X main.version=`），`/healthz` 返回构建版本。
- 本文只写**有出处**的数字；取不到的值写「未核」，不写推测值。官方说法与社区实测冲突时分别标注。

## 1. dsh-ee 视角：宿主 / Cordis / Node / 验证状态

**dsh-ee 不是 DSH 的替代品**：DSH 推理与工具调用在员工机的 DSH 宿主里跑，dsh-ee 负责企业 npm 源、准入、审核、权限与审计。因此「dsh-ee 支持哪条 DSH 线」= 企业内 DSH 客户端允许的宿主范围。

| dsh-ee 版本/基线 | 推荐 DSH 宿主 | Cordis | Node | 验证状态 |
|---|---|---|---|---|
| v0.1（基线 `20aafbb`，2026-11-12） | `0.2.2-rc.1`（代码注释口径：`server/internal/registry/cordis_check.go:9-11`） | `4.1.0`（同上，预检 `cordis-peer` 的 `want` 值） | DSH 0.2.2 线要求 `>=24.9.0`；更早 0.2.x 线可用 `^22.19.0` | **待跑**：`docs/compatibility.md` 的矩阵只有骨架，尚无已跑完的行（0.2.1-rc.5 / 0.2.2-rc.1 / 0.2.2 三行均为「待跑」） |
| dsh-ee 接入插件（`dsh-plugin/`） | 随员工机 DSH | `@deepseek-ai/cordis` 4.0.6-alpha.1（lockfile 实测） | — | 与上述矩阵同批验证，未单列 |

使用建议：

1. **企业包 `engines.dsh` 写你实际验证过的范围**，至少覆盖 dsh-ee 推荐宿主与你验证的宿主；预检只检查「有没有声明」，不校验范围内容（`server/internal/registry/tarball.go:272-276`）。没有声明会 warn。
2. 推荐宿主是**代码注释里的推荐值**，不是硬性准入；真正的硬约束来自 DSH 宿主自己的 `engines.dsh` / peer 校验（见 §5）。
3. 兼容性矩阵的行只有实际跑过 `e2e/dsh/scenarios/run.mjs` 才应填写，且必须记录 DSH 版本与失败步骤；`unknown`（`DSH_BIN --version` 失败）不能当作矩阵证据（`docs/compatibility.md`）。

## 2. 先认版本：五个「最新」不是同一个东西

这是插件作者**第一个**要做的判断。五个数可以不相等，peer 范围按线分叉。下表是上游 2026-10-09 快照值，仅作读法示例，**不是现值**：

| # | 说法 | 快照值（2026-10-09） | 怎么查 | 性质 |
|---|---|---|---|---|
| 1 | npm `latest` | `0.2.0-rc.2` | `npm view @deepseek-ai/dsh dist-tags` | 默认装的就是这个 |
| 2 | npm `next` | `0.2.0-rc.2` | 同上 | 与 latest 同值 |
| 3 | npm `alpha` | `0.2.1-alpha.2` | 同上 | 最新发布 |
| 4 | master 最新 tag | `dsh-v0.2.1-alpha.2` | GitHub `/tags` | 源码线的名字 |
| 5 | **本机实际** | — | `dsh --version` | **唯一真正要紧的数** |

### 2.1 没有「稳定版」这回事

- 上游 2026-10-09 的快照：全部 31 个 npm 版本都带 `-alpha.N` / `-rc.N` 后缀，全部 26 个 GitHub Release 都是 `prerelease: true`。
- 所以 npm 的 `latest` 标签在这里**不代表稳定**——它只是「默认安装会拿到的那一个」。
- 给作者的有效读法：**以 `dsh --version` 为准，按目标宿主那条线读**；官方 `AGENTS.md` 声明 API 处于 pre-stable，插件作者无法「等它稳定」，只能**钉版本 + 声明区间**。

### 2.2 陷阱：子包的 `latest` 是冻住的

主包 `@deepseek-ai/dsh` 每次发版会重发子包到 `alpha`/`next`，但**每个子包的 `latest` 标签可能停在它第一次被设置的那个版本**。裸装子包会装到远古版本。上游 2026-10-09 快照里的例子：

| 包 | `latest`（快照，可能已过时） | `next` | `alpha` |
|---|---|---|---|
| `@deepseek-ai/dsh` | `0.2.0-rc.2` | `0.2.0-rc.2` | `0.2.1-alpha.2` |
| `@deepseek-ai/dsh-tools` | `0.0.1-rc.1` | `0.2.0-rc.2` | `0.2.1-alpha.2` |
| `@deepseek-ai/dsh-session` | `0.0.1-rc.1` | `0.2.0-rc.2` | `0.2.1-alpha.2` |
| `@deepseek-ai/dsh-base` | `0.0.1-rc.1` | `0.2.0-rc.2` | `0.2.1-alpha.2` |

**结论三条**：

1. 永远不要写 `"@deepseek-ai/dsh-tools": "latest"` 或裸 `pnpm add @deepseek-ai/dsh-session`；写死版本或显式钉 `next`/`alpha`，并查当期值。
2. **不要依赖子包版本号判断宿主代次**——判断代次只认 `dsh --version`。
3. 共享实例的 dsh 包一律 `peerDependencies` + `devDependencies`（**绝不 `dependencies`**），按 §5 选区间。

## 3. 破坏性变更断层（只列「哪一版开始不一样」）

| 断层 | 引入版本（官方 `upgrade-guide/v<X>/` 目录名） | 落到哪一版 | 影响面 |
|---|---|---|---|
| 即插即拔基础设施 | `0.1.6-alpha.2` | 同版 | `dsh-hmr` / `dsh-plugin-manager` / `ui-plugin-manager` 一次性出现；新增 `ctx.hmr` / `ctx.pluginManager` / `ctx.profileContext` |
| peer 从「声明」变「运行时门禁」 | `0.1.7-rc.1` | 同版 | 不匹配的插件行被直接 `disabled`，模块永不导入 |
| 会话格式 V4 + 本世代破坏性变更 | `0.1.7-alpha.1` | 同版 | `SESSION_FORMAT_VERSION` 3→4、settings 重写、`readBytes`、Messages-only 适配器 |
| 0.2.0 世代清理 | `0.2.0-rc.2` | 下一版 | 移除 runtime invariants、schedule bundle 退役、子路径插件不再读 `package.json`、新增 `no-response` 错误码 |
| 0.2.1-alpha.2 大批变更 | `0.2.1-alpha.1` | `0.2.1-alpha.2` | **19 条**破坏性变更（清单见 [migration.md](./migration.md) §B.9） |

> ⚠ **官方 `upgrade-guide/v<X>/` 的目录名 `X` 是「变更被引入的版本」**，描述的是从 X 升到**下一个** release 的变更——原文措辞是 *“In vX, … was so; the next release no longer…”*。别把目录名当目标版本。
>
> ⚠ **`0.1.5-rc.3` 是时间线上的陷阱**：它在 `0.1.6-alpha.2` **之后**发布（2026-09-22），是给留在 0.1.5 线用户的回补版本，也是唯一没有 GitHub Release 的 tag。**按发布时间推断版本新旧会出错，必须按 semver 比较。**

## 4. `0.2.1-alpha.1 → 0.2.1-alpha.2` 的 19 条变更（索引）

要点名（逐条症状与修法见 [migration.md](./migration.md) §B.9）：`subagent-activations`、`tool-presentation-mode`、`web-listener-trust-config`、`working-directory`、`session-title-provider-strategy`、`session-title-adapters`、`ssh-helper-launch`、`team-direct-inbox`、`instruction-home-override`、`experimental-package-composition`、`native-subagent-bundle-tools`、`python-ptc-sandbox`、`python-optional-resources`、`plugin-option-placement`、`terminal-command-labels`、`deepseek-model-discovery`、`new-session-generations`、`mock-messages-events`、`desktop-test-auth-popup`。

**适用判定**：`dsh --version` 落在 `0.2.1-alpha.2` 或更新才需要逐条核对；停在 `0.2.0-rc.2` 及更早与这批变更无关。出处是官方 `docs/upgrade-guide/v0.2.1-alpha.1/<item>/guide.md` 与 release `dsh-v0.2.1-alpha.2`。

## 5. 兼容范围：`engines.dsh`、peer 与安装期校验

两条硬事实（按 0.5.5 指南校正后的口径）：

1. **`engines.dsh` 存在时优先**：自 DSH `0.2.1-alpha.4` 起，宿主读取 `package.json` 顶层的 `engines.dsh` 并**以它为准**（prerelease 也参与）；没有该字段时，回退到 `@deepseek-ai/dsh*` 的 peer 依赖范围。
2. **peer 范围会被安装期强制**：`dsh plugin add` 会拿运行时版本比对插件声明的 `@deepseek-ai/dsh-*` 范围，不匹配即**拒绝安装**并打印声明的范围与豁免命令。
3. 上游 0.4.3 的旧指南曾写「`engines.dsh` 当前不强制」；0.5.5 指南已更新为「自 0.2.1-alpha.4 起单独决定」。**两处口径不同，按新口径记为：`engines.dsh` 优先、peer 兜底，二者都可能影响安装/加载。**范围只能表达「能不能装」，**表达不了某个 API 在不在**——仍要配运行时探测。

### 5.1 上游共用区间（六子句）

```
>=0.1.2-rc.1 <0.2.0 || >=0.1.5-alpha.1 <0.2.0 || >=0.1.6-0 <0.2.0 || >=0.1.7-0 <0.2.0 || >=0.2.0-0 <0.3.0 || >=0.2.1-0 <0.3.0
```

每个子句对应一条发布线；**不要写开区间上界**（如 `>=0.1.2-rc.1 <0.3.0`）：它跨过整代破坏性变更，门禁放行但运行时会炸。上界必须按世代切（`<0.2.0` / `<0.3.0`）。

### 5.2 我该声明什么区间

| 你的插件用了什么 | 下界至少要到 |
|---|---|
| 只用 Cordis 基础能力（tools/events/services/effects） | `0.1.2-rc.1` |
| 用了 `ctx.hmr` / `ctx.pluginManager` | `>=0.1.6-0` |
| 依赖 peer 门禁本身或 0.1.7 的 API | `>=0.1.7-0` |
| 用了 0.2.0 世代接口（settings 重写后） | `>=0.2.0-0` |
| 用了 §4 里任何一条新形态（如 `webStartup`、`BundleInfo.official`） | `>=0.2.1-0` |
| 面向 dsh-ee 企业分发 | 覆盖 dsh-ee 推荐宿主（当前参考 `0.2.2-rc.1`）与你实际验证的宿主；用 `engines.dsh` 声明，并在审核说明里写验证的宿主版本 |

### 5.3 被拒绝安装时

`dsh plugin add` 拒绝安装后，唯一的出路是**按 `<包@精确版本> × <宿主版本>` 授予精确豁免**（不是范围），宿主升级后要重新授予：

```sh
dsh plugin --profile <profile> version-exemptions
dsh plugin --profile <profile> allow-version <pkg@version> --dsh-version <runtime> --accept-risk
dsh plugin --profile <profile> revoke-version <pkg@version>
```

## 6. 会话格式（persistence）谱系

插件若读写会话日志，必须认这个表。

| 格式 | 首次写入的版本 | 规模（root/type，上游快照） | 说明 |
|---|---|---|---|
| V0 | `0.1.2-rc.1` | 54 / 415 | 历史 V0 参考快照 |
| V1 | 从未发布 | — | 没有 tag 的 writer version 是 1 |
| V2 | `0.1.3-alpha.2` | 56 / 435 | — |
| V3 | `0.1.5-alpha.1` | 60 / 467 | — |
| **V4** | `0.1.7-alpha.1` | 63 / 597 | 当前；`SESSION_FORMAT_VERSION` 3→4 |

**三个读数陷阱**（上游 0.4.3 记录，供排错参考）：

1. 逐版本持久化档案冻结在 `0.1.5-rc.2`；之后新版本的唯一官方历史是 `docs/upgrade-guide/**`。
2. V4 之后仍有 `same-version` 记录（不改格式号也能加类型），其中部分事件是 **required-on-read**——旧构建会拒绝打开含该事件的日志。
3. **多帧 zstd**：`session.jsonl.zstd` 每次写入追加一个 zstd frame，单次 `zstdDecompressSync` 只能解出第一帧；必须按魔数 `28 B5 2F FD` 切分，或用宿主提供的逐帧 API。更稳的做法是**不自己解析日志**，改用 `ctx.sessionPersistence` / session projection / `session/event` 监听。

## 7. 自检与维护节奏

```sh
# 五个「最新」里的前四个
npm view @deepseek-ai/dsh dist-tags
npm view @deepseek-ai/dsh-tools dist-tags     # 换包名即可查子包 latest 是否冻结

# master 最新 tag（GitHub API）
curl -s https://api.github.com/repos/deepseek-ai/deepseek-harness/tags | head -c 2000

# 官方 upgrade-guide 目录（新断层会新增一个版本目录）
curl -s https://api.github.com/repos/deepseek-ai/deepseek-harness/contents/docs/upgrade-guide | head -c 2000

# 本机实际版本 —— 唯一真正要紧的那个
dsh --version
# dsh-ee 构建版本
curl -s https://<dsh-ee>/healthz
```

**维护节奏**：官方发版频繁，每次发版后按 `docs/upgrade-guide/**`（无 index、无入链，必须主动看）+ `docs/persistence-changes/**` + GitHub Releases 三处增量更新本页与 [migration.md](./migration.md)。

## 8. 未核事项（不要当成已知）

- 本文 §2、§6 的数字是上游 2026-10-09/10-13 快照；**现值未核**，以 §7 命令结果为准。
- dsh-ee 的 DSH 兼容矩阵（`docs/compatibility.md`）尚无已跑完的行；「推荐宿主 0.2.2-rc.1 / Cordis 4.1.0」是代码注释口径，不是验证结论。
- 上游没有 `CHANGELOG.md`，逐版本说明只在 GitHub Releases 的 body 里；本页不复述。
- dsh-ee 自身版本由构建注入、无 tag；发布包版本号与文档快照日期的对应关系以构建记录为准。

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `guide/version-matrix.md`（原文：[version-matrix.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/version-matrix.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/levels.md`（Apache-2.0）；dsh-ee 侧事实取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
