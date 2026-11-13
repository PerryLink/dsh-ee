# 迁移指南：包名/世代变更与 dsh-ee 侧迁移

> **来源与许可**
> - 主来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`MIGRATION.md`、`LEGACY-0.3.42.md`、`guide/migration-0.2.md`
> - 上游文件（固定 commit，公开链接）：[MIGRATION.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/MIGRATION.md)、[LEGACY-0.3.42.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/LEGACY-0.3.42.md)、[guide/migration-0.2.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/migration-0.2.md)、[README.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/README.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`README.md` FAQ「0.3.x 的老用户怎么办」、`docs/levels.md`（Apache-2.0）。dsh-ee 侧迁移规则来自本仓库基线 `20aafbb` 的 `docs/plugin-authors.md`、`server/internal/registry/tarball.go`（自己的文档与代码事实）。
> - 修改说明：中文节选并改写；`MIGRATION.md`/`LEGACY-0.3.42.md` 由英文转为中文摘要；`migration-0.2.md` 保留 §0–§13 的事实与 19 条 seam 表并压缩叙述；删除 `references/`/`downloads/` 本地路径、社区文章与带日期的生态统计；新增 dsh-ee 侧迁移一节。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档。迁移结论以官方 `docs/upgrade-guide/**` 与目标版本的 GitHub Release 原文为准；本文是社区快照，冲突时官方优先。

## A. 先分清两个迁移：包名迁移 vs 世代迁移

| 你遇到的是 | 看哪节 | 一句话 |
|---|---|---|
| 以前装过 `dsh-plugin-guide@0.3.x`，现在不知道该装什么 | [§A1](#a1-旧包名dsh-plugin-guide-03x--dsh-primer) | 旧知识库/CLI 改名为 `dsh-primer`；`dsh-plugin-guide` 0.4.0+ 是另一个产品 |
| 插件要在 DSH `0.1.6-alpha.2` → `0.2.x` 之间升级 | [§B](#b-迁移到-02-世代02x) | 会话格式 V4、settings 重写、invariants 删除、客户端 API 改名 |
| 已发到 dsh-ee 企业源，要换版本/改归属 | [§C](#c-dsh-ee-侧迁移企业源) | 版本号不可复用；改归属/可见范围走管理员 |

## A1. 旧包名：dsh-plugin-guide 0.3.x → dsh-primer

上游 `MIGRATION.md` 的原文要点（中文摘要）：

- **新包**：`dsh-primer`（npm 包名与仓库名）；**旧包**：`dsh-plugin-guide@0.3.x` 标记 deprecated，指向新包。
- **旧代码留在历史里**：npm 版本不能在包之间迁移，旧 tarball 仍在 npm 上（最后一个是 0.3.42），供既有安装使用。
- **两个 CLI 命令都能用**：`dsh-primer` 与 legacy alias `dsh-plugin-dev`。
- **旧名字被复用了**：`dsh-plugin-guide` 0.4.0 **不是**旧 agent skill 的兼容升级，而是另一个产品（ENTRY/FLOW/BOOST 指南 + guide 技能）；不要把 0.4.0 当作 0.3.42 的更新。
- 上游 0.5.5 的 FAQ 补充：0.3.x 已 deprecated 但仍可安装；锁在 `^0.3` 的 profile 不会自动升级；旧的构建/脚手架技能与 `dsh-plugin-dev` CLI 现在归 `dsh-primer`。

**对你的操作要求**：

1. 依赖/文档里出现 `dsh-plugin-guide@0.3.x` 的安装命令时，改成 `dsh-primer`；不要再 pin 0.3.39 这类旧快照。
2. 需要「指南层」（ENTRY/FLOW/BOOST）时才是 `dsh-plugin-guide@0.4.0+`；需要「开发知识库 + 脚手架/检查器」时是 `dsh-primer`。
3. profile 升级后跑 `dsh --profile <name> --dump-config`，确认旧的 `dsh-plugin-guide` 行已被移除、新行已挂上。
4. 旧的 `dsh-plugin-dev` 命令仍然可用（legacy alias），但新文档/CI 建议写 `dsh-primer`。

> 本目录的来源 pin（公开 tag/commit）：`dsh-primer@0.4.3` = tag `v0.4.3` / `8750d31718c16601514e59fe1831de73cfed3d20`（npm `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`）；`dsh-plugin-guide@0.5.5` = tag `v0.5.5` / `9f0a8a10b76a872f1a82570734a6b166661fa38b`。老的 `dsh-plugin-guide@0.3.39` 只用于对照，**不 pin、不搬正文**。

## B. 迁移到 0.2 世代（0.2.x）

> 适用对象：所有针对 DSH `0.1.6-alpha.2` 及更早版本开发的第三方插件（bundle / 纯 cordis 插件 / UI 插件 / MCP 配置包）。
> 上游基线：`d743267`（tag `dsh-v0.2.1-alpha.2`，2026-10-09）；区间起点 `ddefc45`（tag `dsh-v0.1.6-alpha.2`）。§B.1–§B.8 覆盖 `0.1.6-alpha.2` → `0.2.1-alpha.1`；§B.9 覆盖 `0.2.1-alpha.1` → `0.2.1-alpha.2`（19 条）。
> 权威优先级：官方 `docs/upgrade-guide/**` > 本节 > 社区文章。

### B.0 先做这三件事

1. **确认宿主版本**：`dsh --version`；`0.1.6-alpha.2` 及以前 = 旧基线；`0.1.7-alpha.1` 起已含本世代全部破坏性变更（会话格式 V4）。
2. **跑 `dsh --profile <name> --dump-config`**，看有没有 `patch: entry <id> not found`——这是本世代最容易踩的静默失效（插件行被上游删掉/改名后，你的 override 不再命中任何行且不报错）。
3. 按下面逐项对照。破坏性变更集中在四块：**会话格式 V4、settings 重写、invariants 删除、客户端 slot / Remote API 改名**。

### B.1 版本与发布线

- 上游快照（2026-10-09）：master 最新 tag `dsh-v0.2.1-alpha.2`；npm `latest`=`next`=`0.2.0-rc.2`；npm `alpha`=`0.2.1-alpha.2`；**不存在稳定版**（全部 31 个 npm 版本与 26 个 GitHub Release 都是 prerelease）。
- Node 门槛：`^22.19.0 || >=24.0.0`；跟随 0.2.2-alpha.3 的包可用 `>=24.9.0`（当前 0.2.2 线要求 `>=24.9.0`）。
- DSH 的包清单只有 `dsh.bundle` / `dsh.profile` / `dsh.client` 三组键；**不存在 `dsh.version` 字段**。
- 官方仓库没有 `CHANGELOG.md`，逐版本说明只在 GitHub **Releases** 的 body 里；`docs/upgrade-guide/**` 没有 index、没有入链，必须主动看。
- npm 陷阱：库包子包（`dsh-tools`、`dsh-base`、`dsh-headless`、`dsh-session-persistence-jsonl`、`dsh-session`、`dsh-settings`、`dsh-workflow` …）的 `latest` 长期冻在远古版本；写 `"latest"` 会装错。见 [version-matrix.md](./version-matrix.md) §2.2。
- 跨线 peer 范围：`^0.1.x` 与 `^0.2.x` 互不覆盖，要同时支持两条线必须写两侧范围（六子句规范形见 [version-matrix.md](./version-matrix.md) §5.1）。

### B.2 会话格式 V3 → V4（影响最深）

| 事实 | 值 |
|---|---|
| `SESSION_FORMAT_VERSION`（旧基线） | `3` |
| `SESSION_FORMAT_VERSION`（`dsh-v0.1.7-alpha.1` 起） | **`4`** |
| 首次写入 V4 的 tag | `dsh-v0.1.7-alpha.1`（2026-09-22） |
| 转换包 | `@deepseek-ai/dsh-session-format-v3-to-v4`（npm 自 `0.1.7-alpha.1` 起） |

对插件作者的后果：

- **V3 读方拒绝 V4 日志**：自己解析 `session.v*.jsonl.zstd` 的插件，遇到 0.1.7+ 写的日志必须先经 V3→V4 转换。
- **tool 结果不再是 content block**：V4 把 user-role 里的 `tool-result` 提升为 **tool-role 消息**（必填 `toolCallId`，可选 `isError`），并从 content-block union 移除；同时新增 `developer/message` 事件、`turn/end.reason: 'forked'`，`request/header.system` 键被禁止（旧日志里的该键会被读方拒绝）。
- **message source 改为生产者自有**：旧的 plugin wrapper 被 producer-owned `source.kind` 取代。
- 迁移是**非破坏性**的：写打开会在不改动前代文件的前提下发布当前后继；V4 内部的兼容性变更只能通过新的 acknowledgement 记录，**破坏性变更必须再 bump 写入版本**。
- **插件实践**：要同时支持两条线，就要能处理两种事件形态；更稳的做法是**别自己解析日志**——用 `ctx.sessionPersistence` / session projection / `session/event` 监听，把版本差异留给宿主。

### B.3 Settings 全面重写（第三方插件最大的破坏点）

`packages/settings/settings-file` 被整体删除，`ctx.settings` 换了类型：

| 旧（≤ `0.1.6-alpha.2`） | 新（≥ `0.1.7-alpha.1`） |
|---|---|
| `@deepseek-ai/dsh-settings` + `dsh-settings-file`（`$DSH_HOME/settings.yaml`） | `@deepseek-ai/dsh-settings` + **`dsh-config-editor`**（直接编辑 Cordis patch） |
| `ctx.settings` = 用户文档注册表 | `ctx.settings` = `SettingsForms extends Service` |
| `register(ns, schema, opts)` / `installSection(...)` | **已删除**——表单由 profile 条目的 `Config` schema **自动派生** |
| `SettingsScope<T>` / `SettingsRegisterOptions`（含 `applies`） | **已删除**；`applies` 恒为 `'live'` |
| — | `configure(...)`、`describe(...)`、`update(...)`、`replace(...)`、`mutate(...)`、`writable`、`documentPath`、`prepareDocument()`；事件 `settings/document-updated`；错误 `SettingsConflictError`（`SETTINGS_CONFLICT`） |
| 客户端 `ctx.settingsScope`、slot `settings.plugin.item` | **删除** → slot `settings.plugins.tab` |

新机制要点：

- **表单命名空间 = profile 条目 id**（不是插件名）：同一插件挂两行、id 不同 → 两张表单。
- **普通（非 volatile）字段不进表单**，只有 `Volatile<T>` 字段会；schema 写法 `z.<type>().volatile()`，读用 `.get()`，跨字段校验用 `.check()`，变更监听 `loader/volatile-update`。
- 旧 `$DSH_HOME/settings.yaml` 会**一次性导入**当前 profile，然后重命名为 `settings.yaml.imported`。

### B.4 运行时 invariants 全部删除

- `@deepseek-ai/dsh-invariants` 包、`ctx.invariants` 服务、`InvariantRegistry` / `InvariantInstaller` / `InvariantFailure` / `InvariantError`、以及每个 `<package>/invariant` 子路径导出都不存在；整棵 `packages/runtime-diagnostics/` 被删。
- `sdk-minimal` profile 里相关五行消失：**仍指向这些 id 的 patch 会打印 `patch: entry <id> not found`**（不报错，只是不生效）。
- 四个 emitter 从「重抛」改为「记录并继续」；靠 `INVARIANT` 异常做失败传播的插件必须换成自己的通道。

### B.5 Bundle / 清单格式

- `dsh.bundle.patch` 接受**有序数组**：`{ "patch": ["./base.patch.yml", "./web.patch.yml"] }`，按序作为同一层应用，每个文件里的相对插件路径**相对该文件所在目录**解析。
- 层顺序：`dsh.profile.bundles` 顺序 → profile 自己的 `cordis.patch.yml` → `$DSH_HOME/cordis.patch.yml` → 各 `--patch`（argv 顺序）。**按 id 覆盖时整段 `config` 被替换，不做深合并。**
- 展示元数据契约：包根读 `package.json` 的 `name`/`description`/`icon`，或导出 `./locale/*.json` 的 `meta.title`/`meta.description` 与 `./icon`；图标支持 SVG/PNG/JPEG/WebP，**≤ 256 KiB 且落在包内**。子路径插件**永远不读** `<subpath>/package.json`。
- `dsh.client` 键未变（`platform` / `inject`（仅排序）/ `immediately` / `external`），但客户端半侧只挂在**说明符恰为裸包名**的那一行上。

### B.6 组合行变化（会让旧 override 打空）

`packages/bundle/base/cordis.patch.yml` 本次删/增的行：

| 删除的行 `name` | 新增的行 `name` |
|---|---|
| `@deepseek-ai/dsh-llm-deepseek` | `@deepseek-ai/dsh-llm-deepseek-api-key` |
| `@deepseek-ai/dsh-settings-file` | `@deepseek-ai/dsh-llm-deepseek-account` |
| | `@deepseek-ai/dsh-authorization` |
| | `@deepseek-ai/dsh-deepseek-account-platform` |
| | `@deepseek-ai/dsh-config-editor` |
| | `@deepseek-ai/dsh-settings` |
| | `@deepseek-ai/dsh-otel` |

**Web 组合新增 preset 分叉**：`packages/bundle/web-app/presets/{standard,minimal,ptc,cordis}.patch.yml`。行集**因 preset 而异**——`standard`/`cordis`/`ptc` 声明时钟读取与四个 `schedule_*` 工具，`minimal` 两者都没有。假设「Web 组合行集固定」的插件要改成按 preset 探测。

可选用 bundle 的两次反向变动（都在本区间）：`v0.1.7-rc.2` 出现过 `@deepseek-ai/dsh-experimental-schedule-bundle`；`v0.2.0-rc.2` 又撤掉它，加载 profile 时会自动从 `dsh.profile.bundles` 移除并重写 `package.json`；针对 `time-context` 的顶层 override 不再命中任何行。

### B.7 客户端（UI 插件）破坏点

- **Slot 变化**：新增 `plugins.bundle.config`、`plugins.bundle.activation`、`plugins.add.actions`、`plugins.detail.actions`、`plugins.detail.badge`、`plugins.detail.section`、`conversation.input.activity`、`conversation.header`、`conversation.header.leading`、`shell.bottom`、`shell.leading`、`shell.quota-notice`、`sidebar.workspaces.session.menu.item`、`sidebar.workspaces.session.row.action`；**删除** `conversation.session.header.leading`（→ `conversation.header.leading`）、`settings.plugin.item`（→ `settings.plugins.tab`）。
- **workspace-files Remote**：`readAll` / `readRelated` → **`readBytes(path, { range?, baseFile? })`**（经 Connection 二进制 RPC 返回原生 `Uint8Array`）；`changes()`（工作区级）→ `changes(path)`（单目标）；`read` / `list` 未变。
- **右侧栏**新增 `keepMounted`、`tab.actions.bindCommands(commands)`、`tab.refreshShortcut`、`focusedTarget` / `commandTarget`；保留语义变化（当前会话与已初始化 `keepMounted` 的后台会话各留独立座位，重载恢复布局）。
- **输入区统计注册 ID 变化**（0.2.1-alpha.1 官方破坏性变更）：原来的 `stats` 入口拆成 **`activity`** 与 **`usage`** 两个独立入口；整行覆盖旧 `stats` 的插件必须改，否则统计区无声消失。
- **`dsh.client.inject` 必须是静态数组**：曾有一个插件把 `inject` 导出成函数 → 声明为零依赖 → `apply()` 抛 `cannot get property "sidebarRightTabs" without inject` → 日志只留一行 `web boot: 1 entry did not activate` → 自愈机制把整个 bundle 禁用。
- 高频坑：`apply()` 里往 `ctx` 上写**未声明**的属性会抛错并可能静默杀掉整个插件激活；可选服务一律 `ctx.get(name)`；Remote 服务调用是位置参数不是对象参数；工具 `parameters` 是 DSH 方言，**不是 JSON Schema**。

### B.8 工具 DSL 新增项（无改名）

| 符号 | 变化 |
|---|---|
| `ToolDefinition.projectContent?(exec, result): ContentBlock[] \| undefined` | **新增**：在 `tools/post-execute` 之前安装执行期准备好的内容；策略替换仍权威 |
| `ToolSchema.deferLoading?: true` | **新增**标记，经 `schemaOf()` 进入模型可见 schema |
| `PreToolDecision` 的 `ask` 变体 | **变化**：新增 `displayReason?: { readonly en: string; readonly [locale: string]: string }`；`reason` 是审计用批准理由 |
| `MessageSourceMap` 增强点 | **新增**（`@deepseek-ai/llm`）；tool registry 声明 `'tool-registry': { kind: 'tool-registry' }` |

`agent.inject()` 的 source 包装被退役（V4 拒绝）：

```ts
// ❌ 旧写法：kind: 'plugin' 这个 catch-all 已被删除
agent.inject({ content, source: { kind: 'plugin', plugin: 'my-plugin' } })

// ✅ 新写法：先声明合并自己的 kind
declare module '@deepseek-ai/dsh-llm' {
  interface MessageSourceMap { 'my-plugin': { kind: 'my-plugin' } }
}
agent.inject({ content, source: { kind: 'my-plugin' } })
```

`agent.inject()` 本身只是 `send(input, 'next-step', false)`——不唤醒；要唤醒用 `followup()`（下一回合）或 `steer()`（下一步）。

### B.9 `0.2.1-alpha.1 → 0.2.1-alpha.2`：19 条破坏性变更

> **适用判定**：`dsh --version` 落在 `0.2.1-alpha.2`（或更新）时逐条核对；停在 `0.2.0-rc.2` 及更早的**与本节无关**。官方出处 `docs/upgrade-guide/v0.2.1-alpha.1/<item>/guide.md`。
> ⚠️ 目录名陷阱：`v0.2.1-alpha.1/` 是**变更被引入时仓库的版本号**，描述从该版升到下一版的变更——即这 19 条**落在 `0.2.1-alpha.2`**。

| ID | 官方项 | 受影响面 | 症状（不修的后果） | 修法 |
|---|---|---|---|---|
| L1 | `subagent-activations` | 子代理工具/服务 | `backgroundMode` / `enableRunInBackground` / `run_in_background` 失效；`dsh-subagent-in-process-driver` 包被删；`ctx.subagents.start()` 不存在 | 删两个配置键与工具参数；`startActivation({ …, delivery: 'caller' })` 后 `await activation.result`；自定义本地 provider 实现 `prepareContinuable()` |
| L2 | `tool-presentation-mode` | 工具呈现 | `mode: both` 被拒绝；`DSH_TOOLS_MODE=both` 与 `ctx.tools.presentAs('both')` 同样被拒 | 改为 `native` 或 `ptc` |
| L3 | `web-listener-trust-config` | Web profile / 覆盖层 | `webRuntime` 服务消失 → 注入它的 patch 永久等待；`ctx.webRuntime.lanAddresses` 没了；通配 host 加载期被拒 | `inject: [webRuntime]` → `[webStartup]`；`trustedHosts` 换服务；通配 host 换具体 IP 字面量 |
| L4 | `working-directory` | 提示词模板 / ACP·SDK 子代理 | 保留 `{{cwd}}` 的自定义模板在发出模型请求前失败；`config.cwd` 被移除 | 删提示词里的目录子句；`cwd` 改由子请求传或先调 `working_directory({ cd })` |
| L5 | `session-title-provider-strategy` | 会话标题 | 三个导出符号消失；`targetWords`/`targetCjkCharacters` 移出共享 Config | 改用 `ctx.sessionTitle.register({ id, automatic, generate })`，自己搭 system/input |
| L6 | `session-title-adapters` | LLM 适配器 | 标题请求改走 `ctx.llm.prepareCall()`；只挂 `llm/stream` 中间件的组合不再被服务 | 用 `ctx.llm.registerAdapter()` 注册标题路由；`reasoning.efforts` 从低到高排 |
| L7 | `ssh-helper-launch` | SSH / 远端 PTC | `node`/`bootstrapPath`/`bootstrapHash` 三键消失；要求 SSH protocol 2 | 改 `launch: { kind: 'node-script' }` 或 `{ kind: 'executable' }`；helper 与 hash 必须同改 |
| L8 | `team-direct-inbox` | Agent Teams（**数据丢失**） | `send_message` 只回 `{"sent":true}`；`maxPendingMessagesPerMember` 移除；未投递的排队消息永不派发 | ⚠️ 升级前让待发消息跑完；升级后显式重发 |
| L9 | `instruction-home-override` | 指令加载 | `dsh-agent-instructions` 的 `dshHome` 字段移除，残留键被静默忽略 | 删 `dshHome`；改用进程环境变量 `DSH_HOME`；`--dump-config` 复核 |
| L10 | `experimental-package-composition` | profile 组合 | 安装依赖不再含 hook 桥；装了不等于挂上；只「启用」已删行的 patch 不会把它插回来 | 显式装 peer；选 bundle 先于覆盖其行；用完整 `insert` 替代对已删行的 override |
| L11 | `native-subagent-bundle-tools` | Bundle 元数据（**类型破坏**） | 构造/严格校验 `BundleInfo` 必须补两个必填字段 `official`/`availability`，`PluginPackageRef` 同理 | 补必填字段；删同名冗余 Host 行 |
| L12 | `python-ptc-sandbox` | Python PTC | Python runtime 要求 `sandbox` + `sandboxPolicy` 服务，默认 `read-only`；受限运行以 `sandbox-unavailable` 失败 | 挂 `dsh-sandbox-local` + `dsh-sandbox-policy`；设 `sandbox-policy` 行的 `mode`/`workspaceRoot` |
| L13 | `python-optional-resources` | Python 运行时资源 | wheels 不再内置 Office sidecar 与 authoring 解释器；SDK 启动不再下载 | 用前显式 `download_office()` / `download_primary_runtime()`；离线预置 `DSH_RESOURCE_CACHE` |
| L14 | `plugin-option-placement` | CLI 调用 | `dsh plugin add <pkg> --profile <name>` 现在直接失败 | 改为 `dsh plugin --profile <name> add <package>` |
| L15 | `terminal-command-labels` | 终端 UI 插件（**类型破坏**） | `TerminalBlockLabels` 新增必填 `commandLine(line: number): string` | 给每个 labels 对象补 `commandLine` |
| L16 | `deepseek-model-discovery` | 模型发现 | `deepseek-official` 路由无 key 时返回空目录 → Desktop/Web 隐藏 DeepSeek 组 | 配 key 或用 `apiKeyEnv` 指定的环境变量；消费方处理空目录 |
| L17 | `new-session-generations` | 客户端集成 | 新建会话同时新建 Agent，不再复用空会话 | 改预设后用 New Session；要复用用 `openWorkspace`/`connectWorkspace` |
| L18 | `mock-messages-events` | 测试支撑 | mock server 在结构化 SSE 负载旁同时发具名 Messages 事件 | 消费具名事件与其 `data` 字段 |
| L19 | `desktop-test-auth-popup` | Desktop 测试构建 | 测试认证弹窗默认关闭 | 需要时设 `updates.allowTestAuthPopupWindow: true` 并完全重启 |

另有一条回填（在 `v0.2.0-rc.2/` 目录里、但基线之后才加入）：`bundled-python-tests` —— 打包的 NumPy/pandas **测试目录**被移除；要跑上游测试套件请另建 Python 环境。

### B.10 升级检查单

- [ ] `dsh --version` ≥ `0.1.7-alpha.1`（要 V4），或明确停留在 `0.1.6-alpha.2` 及以前并锁死。
- [ ] `dsh --profile <name> --dump-config` 无 `patch: entry … not found`。
- [ ] 删掉所有 `@deepseek-ai/dsh-invariants` / `<pkg>/invariant` 的 import 与行；失败传播换成自己的通道。
- [ ] `ctx.settings.register/installSection`、`SettingsScope`、`applies: 'restart'` 全部替换为 `SettingsForms` + `Volatile<T>`。
- [ ] UI：`settings.plugin.item` → `settings.plugins.tab`；`conversation.session.header.leading` → `conversation.header.leading`；`readAll`/`readRelated` → `readBytes`；`changes()` → `changes(path)`。
- [ ] 自己解析会话日志的代码：处理 V4 tool-role 消息、`developer/message`、`turn/end.reason: 'forked'`；或改为消费 `session/event` / projection。
- [ ] 不再用新 `type` 追加会话事件。
- [ ] 补展示元数据：`locale/*.json` 的 `meta.title`/`meta.description` + 导出的 `./icon`（≤ 256 KiB、包内），并核 `files` 白名单。
- [ ] 若把 patch 拆成多文件：改成 `dsh.bundle.patch` 数组，确认每个文件里的相对路径按各自目录解析。
- [ ] 若覆盖 `schedule` / `time-context` / `ui-schedule` / `schedule_*`：按 §B.6 核 preset 分叉与 bundle 撤并。
- [ ] 依赖里没有 `"latest"` 形式的 `@deepseek-ai/*`。
- [ ] peer 范围覆盖每条线一个 clause；不要写跨代开区间；知道它会被安装期强制。
- [ ] `pnpm pack` 后装进干净 `DSH_HOME` profile 冒烟。

## C. dsh-ee 侧迁移（企业源）

dsh-ee 的「迁移」不是 DSH 宿主升级，而是**企业 npm 源里的包版本/归属变化**。规则来自 [`docs/plugin-authors.md`](../plugin-authors.md) 与 dsh-ee 发布接口：

- **版本号不可复用**：包发布后同一版本号永远不能重发；被驳回、下架的版本号也不能复用——修完必须升版本号。迁移/改名时同样如此。
- **改包名/移归属**：企业 npm 作用域由系统设置决定（默认 `@company`）；换作用域或转移归属属于管理员操作，不是重新 `npm publish` 就能完成。
- **审核与可见范围**：上架后管理员可改可见范围（部门/角色/人）与下发方式（自选/自动安装）；旧版本的下架/回滚按审核流程处理。
- **升级 DSH 宿主后**：企业包要重写 `engines.dsh`/peer 范围并**升版本号**再发布；旧版本号不能复用。`engines.dsh` 会被预检记录并在审核页展示。
- **两种来源不要混**：上游镜像（管理员预载公共 npm 包，镜像不改包内容）与企业托管（作者发布到企业作用域）是两条路径；迁移时先确认包属于哪种来源。
- **`dsh-ee` 扩展不是 DSH 插件**：扩展的清单/协议/升级走 `dsh-ee extension ...` 与 [`docs/plugins.md`](../plugins.md)，与本节的插件迁移无关。

## D. 已知分歧与未核实（诚实标注）

- 上游无法核实项（保留自 0.4.3）：未解包已发布 npm tarball 逐字节确认产物与 tag 一致；仓库无 `CHANGELOG.md`，没有官方逐版本 release notes 可引。
- `ctx.agent` 在两个基线都**不存在**（不是「被移除」）。
- `docs/upgrade-guide/` 没有 index、也没有任何入链——必须主动发现。
- 本页 §B 的事实以上游 0.4.3 快照为准；DSH 后续版本可能已新增变更，迁移前务必对照目标宿主的官方 `upgrade-guide/**` 与 Release 原文。

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `MIGRATION.md`、`LEGACY-0.3.42.md`、`guide/migration-0.2.md`（原文：[migration-0.2.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/migration-0.2.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `README.md` FAQ（Apache-2.0）；dsh-ee 侧规则取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
