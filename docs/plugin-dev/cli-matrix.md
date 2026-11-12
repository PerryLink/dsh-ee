# CLI 与工具链矩阵

> **来源与许可**
> - 主来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`README-zh.md`（§Knowledge base、§CLI toolchain、§Keeping it fresh）、`guide/family-toolchain.md`
> - 上游文件（固定 commit，公开链接）：[README-zh.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/README-zh.md)、[guide/family-toolchain.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/family-toolchain.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)、[docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)、[SKILL.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/SKILL.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`docs/levels.md`、`docs/release-checklist.md`、`SKILL.md`（Apache-2.0）。dsh-ee 命令表来自本仓库基线 `20aafbb` 的 `server/cmd/dsh-ee/cli.go`、`cli_ext.go`、`cli_config.go` 与 `docs/plugin-authors.md`（代码/自己的文档，非上游改编）。
> - 修改说明：中文节选并改写；删除上游家族工具链里的评分/认证/排行榜类工具、社区链接与带日期的生态统计；只保留「问题 → 所有权 → 命令」路由；新增 dsh-ee CLI 一节并区分「DSH 通用命令」与「dsh-ee 企业侧命令」；不做整篇翻译。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档。命令参数会随上游版本变化；以 `--help` 与目标版本的官方文档为准。`static ≠ runtime`、`not-run ≠ pass`。

## 0. 三层工具，各管各的

| 层 | 管什么 | 代表入口 | 说明 |
|---|---|---|---|
| 认知层 | 契约、红线、版本差异、迁移路径 | 本目录、上游 guide/skill | 解释「该问什么」，不替你做判定 |
| 机械层 | 脚手架、静态检查、安装冒烟、版本探索 | `dsh-primer` CLI、`dsh-standards` checker | 只对能机械验证的事负责；输出可进 CI |
| 企业层 | 企业 npm、预检、审核、可见范围 | dsh-ee 网页/API、`npm publish --registry`、`dsh-ee extension ...` | 只管企业准入与治理；不改变 DSH 宿主判定 |

原则：**一个事实只有一个所有者**。本页只做「问题 → 该用哪个命令」的路由，不复述检查项定义；检查项定义归 `dsh-standards` 与其检查器。

## 1. DSH 宿主命令（所有作者都会用到）

| 命令 | 作用 | 什么时候用 |
|---|---|---|
| `dsh --version` | 打印实际宿主版本 | **第一步**；决定读哪代文档、写什么兼容范围 |
| `dsh --profile <name>` | 启动指定 profile（如 `web`、`headless`） | 本地调试、验收 |
| `dsh --profile <name> --dump-config` | 打印实际生效的插件树 | 排 `patch: entry <id> not found`；升级后必跑 |
| `dsh --profile headless "任务文本"` | 一次性无界面执行 | 快速冒烟 |
| `dsh plugin --profile <name> add <pkg>` | 安装 bundle/插件（转发给 pnpm） | 安装、升级 |
| `dsh plugin --profile <name> remove <pkg>` | 卸载 | 清理、重装 |
| `dsh plugin --profile <name> install` | 只补装/修复 profile 依赖 | profile 自愈 |
| `dsh plugin --profile <name> version-exemptions` | 列出已授予的精确版本豁免 | 被 peer 范围拒绝安装后 |
| `dsh plugin --profile <name> allow-version <pkg@ver> --dsh-version <runtime> --accept-risk` | 授予 `<包@精确版本> × <宿主版本>` 豁免 | 同上；宿主升级后需重新授予 |
| `dsh plugin --profile <name> revoke-version <pkg@ver>` | 撤销豁免 | 清理 |
| `pnpm dsh web --patch ./scratch-plugin/cordis.yml` | 仓库内开发：用覆盖层挂本地源码 | 改源码即时调试（不写 profile） |

> ⚠️ `--profile` 必须写在转发给 pnpm 的参数**之前**：`dsh plugin add <pkg> --profile <name>` 在 0.2.1-alpha.2 起会直接失败（见 [migration.md](./migration.md) L14）。
>
> 会话内的 `plugin_manager` 工具（`list_plugins` / `list_bundles` / `install_bundle` / `set_plugin` / `remove_bundle`）比 shell 更完整地返回安装/覆盖/pending 状态；但非 Full access 下可能需要审批。免审批的运行时核对优先用 `cordis_inspect_query`。

## 2. 插件作者工具链（上游社区工具）

> 这些是社区 CLI，不是 DSH 官方命令；包名与参数会变。首次使用先 `--help`。

### 2.1 `dsh-primer` CLI（原 `dsh-plugin-dev`）

包 `dsh-primer` 同时提供 `dsh-primer` 和 legacy alias `dsh-plugin-dev` 两个 bin，子命令相同：

| 命令 | 作用 | 备注 |
|---|---|---|
| `dsh-primer new <name> [--lang ts\|js] [--dir <path>] [--force] [--git]` | 脚手架生成 TS/JS 插件仓库：契约模板、Schemastery Config、tests、tsdown/vitest、注释版 `cordis.patch.yml`、README | 幂等；无 `--force` 拒绝覆盖非空目录 |
| `dsh-primer check [--cwd <dir>] [--json] [--strict]` | 静态检查：`cordis.patch.yml` 合法性、`package.json` 元数据、`dsh.bundle.patch` 指向、peer/engines/files、工程红线模式 | 输出可被 CI 消费的 JSON |
| `dsh-primer verify [--cwd <dir>] [--dsh <bin>] [--pnpm <bin>]` | `pnpm pack` 后装进干净 `DSH_HOME` profile，做安装/启动/卸载冒烟 | 对齐 `verify:self-contained`；失败给日志尾部与建议 |
| `dsh-primer seams <query> \| --since <version>` | 查某个 API 在目标宿主里是否存在 | 版本迁移时用来确认接缝 |

可调项（上游声明「无硬编码可调参数」）：`--dsh` / `--pnpm` / `--timeout` / `--smoke-timeout`，以及环境变量 `DSH_PLUGIN_DEV_TEMPLATES`、`DSH_PLUGIN_DEV_DSH`、`DSH_PLUGIN_DEV_PNPM`、`DSH_PLUGIN_DEV_TIMEOUT`（默认 300000 ms）、`DSH_PLUGIN_DEV_SMOKE_TIMEOUT`（默认 120000 ms）。

### 2.2 `dsh-standards` 检查器（ENTRY / 三层审计）

| 命令 | 作用 | 备注 |
|---|---|---|
| `npx dsh-standards <plugin-dir> --scope entry --entry-claim static` | 只做 ENTRY 识别（不声明 runtime） | 不执行插件代码 |
| `npx -y dsh-standards@<ver> check . --scope entry --entry-claim static --no-exec` | 固定版本跑静态 ENTRY | 0.5.5 指南的发布清单写法；`--no-exec` 明确不执行 |
| `npx -p dsh-standards dsh-levels <plugin-dir> --format json` | 三层审计；FLOW/BOOST 只有提供证据时才报告 | 没证据的层保持 `not-run` / `not-measured` |
| `npx dsh-standards ... --scope security`（1.6.0+） | 静态安全 scope | **不是**第四层；不构成安全审计 |

检查报告的读法（blocking / hygiene WARN / partial / not checked）见 [errors.md](./errors.md) §2。规则原文由 `dsh-plugin-spec` 维护，本仓库**不搬条款正文**（CC BY 4.0），只链接。

### 2.3 脚手架与社区工具（按需）

- `create-dsh-plugin`（上游社区脚手架，npm）与官方文档「develop/basic」四步教程可以二选一；用前确认版本与目标宿主匹配。
- 官方自带的插件开发技能（`cordis-plugin-development`）随宿主分发，提供「工作区写 bundle → `plugin_manager.install_bundle` → `cordis_inspect_query` 核对」的官方路径；不要手写 profile 的 `package.json` / `cordis.patch.yml`。
- 本页不列社区评分/认证/排行榜类工具：它们不属于「让插件跑起来」的最小工具链，且容易把分数误读成官方结论。

## 3. dsh-ee 命令（企业侧）

dsh-ee 是单二进制：不带子命令时启动服务；带子命令时执行离线操作（不启动服务、不连数据库）。完整 `--help` 以 `dsh-ee help` 为准；下表来自基线 `20aafbb`（2026-11-12）。

| 命令 | 作用 | 什么时候用 |
|---|---|---|
| `dsh-ee version` / `dsh-ee --version` | 打印构建版本 | 对版本、报障 |
| `dsh-ee extension init <目录> --id 作者/名字 [--kind declarative\|service]` | 新建扩展（带 AGENTS.md） | 写 dsh-ee 扩展（**不是 DSH 插件**） |
| `dsh-ee extension check <目录> [--json]` | 检查扩展清单、接口文档、只读标记、凭据写法 | 提交扩展前；有错误时退出码 1 |
| `dsh-ee extension test <目录\|地址> [--run "启动命令"] [--credential 值] [--config JSON] [--write] [--json]` | 把服务扩展跑起来按协议逐项测 | 扩展上线前 |
| `dsh-ee extension dev [--listen 地址]` | 起一个模拟 dsh-ee（不要数据库） | 扩展本地开发 |
| `dsh-ee extension call <地址> <工具> [--as 员工] [--args JSON] [--credential 值] [--config JSON]` | 以员工身份调一次扩展工具 | 手测 |
| `dsh-ee extension pack <目录> [-o 文件]` | 声明式扩展打 zip、算校验和 | 交付 |
| `dsh-ee extension report <目录\|地址>` | 生成给管理员看的交付说明（Markdown） | 交付 |
| `dsh-ee config export\|plan\|test\|apply\|explain\|simulate …` | 把权限配置当文件改 | 企业权限配置 |
| `dsh-ee admin recover` | 在服务器上生成 1 小时有效的恢复账号 | 把自己锁在外面时 |
| `dsh-ee schema list [--json]` / `dsh-ee schema get <名字>` | 列出/打印机器可读规格 | Agent 与工具集成 |

> **没有** `dsh-ee dev` / `dsh-ee docs` / `dsh-ee example` 这类顶层命令；也不要把它和 DSH 的 `dsh` 命令混用。企业侧发布 DSH 插件的入口是网页/HTTP 预检与 `npm publish --registry`（§4），不是 `dsh-ee extension`。

## 4. 企业侧发布入口（dsh-ee，不是 CLI）

| 入口 | 作用 | 说明 |
|---|---|---|
| 网页「分享插件 → 网页上传」→ 预检 | 上传 `.tgz` 先预检 | 前端调 `POST /api/me/packages/check`；不写库、不占版本号 |
| `npm publish --registry https://<dsh-ee>/npm/` | 发布到企业 npm 源 | 包名必须在企业作用域下；详见 [`docs/plugin-authors.md`](../plugin-authors.md) |
| 网页「分享插件 → 网页上传」→ 发布 | 上传 `.tgz` 发布 | 与 npm publish 效果一致 |
| 审核页 | 管理员通过/驳回、可见范围、下发方式 | 默认白名单模式；版本号不可复用 |

预检会返回 `can_submit` / `will_publish` / `requires_review` 与逐项 `scan` / `conflicts`；检查项与失败处理见 [faq.md](./faq.md) §4 与 [errors.md](./errors.md) §6。

## 5. 退出码与机器可读输出

| 命令 | 机器可读 | 退出码 |
|---|---|---|
| `dsh-primer check` | `--json`；`--strict` 把 warn 也当失败 | 由 CLI 决定；CI 里以 `--strict` 为准 |
| `dsh-standards check` / `dsh-levels` | `--format json` / JSON 输出 | 非 0 表示有阻塞项（具体语义见检查器文档） |
| `dsh-ee extension check` | `--json`（每条带 `code`、`severity`、`where`、`message`、`fix`） | 有错误 = 1；用法错误 = 2 |
| `dsh-ee version` | 纯文本 | 0 |

## 6. 问题 → 工具路由

| 你的问题 | 先用 | 再用 |
|---|---|---|
| 这个插件包结构对不对 | `dsh-primer check` | `dsh-standards --scope entry` |
| 装了能不能起来 | `dsh-primer verify`（干净 profile 冒烟） | 手动 `dsh --profile` + `--dump-config` |
| 目标宿主这一版有没有这个 API | `dsh-primer seams` | 会话内 `cordis_inspect_query` |
| 插件质量三层怎么自查 | `dsh-levels` | [quality-layers.md](./quality-layers.md) |
| 为什么安装被拒绝 | [version-matrix.md](./version-matrix.md) §5 | [errors.md](./errors.md) §3 |
| 能不能进企业源、要不要审核 | dsh-ee 预检（网页/API） | [`docs/plugin-authors.md`](../plugin-authors.md) |
| 企业系统怎么接进来 | `dsh-ee extension ...` | [`docs/plugins.md`](../plugins.md)（**不是** DSH 插件） |

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `README-zh.md` 与 `guide/family-toolchain.md`（原文：[README-zh.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/README-zh.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/levels.md`、`docs/release-checklist.md`（Apache-2.0）；dsh-ee 命令表取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
