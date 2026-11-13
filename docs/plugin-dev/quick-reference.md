# DSH 插件开发速查表

> **来源与许可**
> - 来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`guide/quick-reference.zh-CN.md`
> - 上游文件（固定 commit，公开链接）：[guide/quick-reference.zh-CN.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/quick-reference.zh-CN.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)、[docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)、[SKILL.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/SKILL.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本 `SKILL.md`、`guide/` 等以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 正文是 Apache-2.0 模板、未填写版权主体，此处按贡献者群体标注；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`docs/levels.md`、`docs/release-checklist.md`、`SKILL.md`——只用于校正版本口径与证据纪律（Apache-2.0）。
> - 修改说明：中文节选并改写；删除上游的营销/榜单/家族互链、`references/`/`downloads/` 本地路径、带日期的 npm 版本现值、社区生态章节；把版本数字统一收口到 [version-matrix.md](./version-matrix.md)；按较新的 0.5.5 指南校正 `engines.dsh`/peer 口径；补充 dsh-ee 企业侧速查。**有删改，未逐字复制。**
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档，不是认证、评分或安全审计。精确 API 以目标宿主版本的官方文档与会话内 inspection（`cordis_inspect_list` / `cordis_inspect_query`）为准。

## 1. 最小插件骨架

```ts
import type { Context } from '@deepseek-ai/cordis'
import Schema from '@deepseek-ai/schemastery'
import { defineTool } from '@deepseek-ai/dsh-tools'

export const name = 'my-plugin'          // 必须
export const inject = ['tools']          // 依赖的服务；无依赖可省略

export interface Config { limit: number }
export const Config: Schema<Config> = Schema.object({
  limit: Schema.number().default(10),    // Schemastery schema，不是普通对象
})

export function apply(ctx: Context, config: Config) {
  ctx.effect(() => {                     // 注册 = effect，卸载自动撤销
    const timer = setInterval(() => {}, 1000)
    return () => clearInterval(timer)
  })
  ctx.on('tools/result', (exec, result) => { /* ... */ })
  ctx.tools.register(defineTool({
    name: 'my_tool',
    description: '…',
    parameters: { x: { type: 'string', required: true } },  // DSH 方言，不是 JSON Schema
    output: { schema: { type: 'string' }, render: (_a, v) => [{ type: 'text', text: v }] },
    async execute(args, exec) { return `hi ${args.x}` },    // 尊重 exec.signal
  }))
}
```

其他形态：对象 `export default { name, inject, apply }`；类 `class X extends Service { static inject = [...]; constructor(ctx) { super(ctx, 'key') } }`（提供服务时用）。

## 2. `ctx` 核心 API（Cordis）

| 用途 | API |
|---|---|
| 注册自定义资源 + 清理 | `ctx.effect(() => disposer)` |
| 监听事件 | `ctx.on(name, handler)`（自动清理） |
| 广播 / 无返回值 | `ctx.emit(name, payload)` |
| 短路取值 | `ctx.bail(name, input)`（首个非 null/false/undefined 即止） |
| 顺序取值 | `await ctx.serial(name, input)` |
| 管线 | `await ctx.waterfall(name, input, init)`；监听器**必须 `next()`** |
| 子上下文 | `ctx.extend(meta)` / `ctx.isolate(name, label?)` / `ctx.intercept(name, config)` |
| 挂子插件 | `ctx.plugin(plugin)` → Fiber；`await fiber.dispose()` |
| 可选服务查询 | `ctx.get('metrics')?.x()` |
| 日志 | `ctx.logger(name)` |

生命周期：`PENDING → LOADING → ACTIVE`（apply 抛错 → `FAILED`）；`ACTIVE → UNLOADING → DISPOSED`。依赖服务消失 → 自动卸载；恢复 → 自动重载。

## 3. 事件派发五模式

| 模式 | await | 顺序 | 返回值 |
|---|---|---|---|
| emit | 否 | 注册序 | 无 |
| waterfall | 否 | 注册序 | 有（**不调 `next()` 即短路**） |
| parallel | 是 | 并行 | 无 |
| serial | 是 | 注册序 | 有（首个非空即止） |
| bail | 否 | 注册序 | 有（首个非 null/false/undefined 即短路，并停止后续监听器） |

类型声明（declaration merging）：

```ts
declare module '@deepseek-ai/cordis' {
  interface Events { 'my/event': (p: { id: string }) => void }
  interface Context { myService: MyService }   // 提供服务时
}
```

## 4. `cordis.yml` 与分层

```yaml
# scratch-plugin/cordis.yml（--patch 覆盖层）
- insert:
    - id: hello
      name: '/absolute/path/to/scratch-plugin/src/my-plugin.ts'
      config: { greeting: 'Hi' }

# bundle：package.json 的 "dsh": {"bundle":{"patch":"./cordis.patch.yml"}}
#           patch 可以是文件路径，也可以是有序数组
# profile："dsh": {"profile":{"bundles":["@deepseek-ai/dsh-base","my-bundle"]}}
# 生效顺序：bundles → profile cordis.patch.yml → $DSH_HOME/cordis.patch.yml → --patch
# 按 id 覆盖时整行 config 被替换（非深合并）；覆盖方必须重述全部键
# !!js 表达式（双感叹号）在注入服务就绪后求值；disabled 每次挂载时求值
```

安装形态（0811 起 `repository-plugin` 机制已移除，只剩两条通道）：

- **bundle 插件**（`"dsh":{"bundle":{"patch":"..."}}`）→ `dsh plugin add <pkg>` 进 profile 的 `dsh.profile.bundles` 层栈，重启生效。
- **纯 cordis 插件**（无 `dsh.bundle`）→ `dsh plugin add <pkg>` 装依赖 + 往 profile `cordis.patch.yml` 加 insert 行，**配置 HMR 实时生效**。
- git 源：`dsh plugin add "github:owner/repo#<sha>&path:<subdir>"`（pin commit；`prepare` 自包含构建 + profile `pnpm-workspace.yaml` 的 `allowBuilds`；npm/tarball 免构建许可）。

> ⚠️ **不要在会话运行期间改 profile 的 `cordis.patch.yml`**：HMR 会重载，实测会丢 preset 工具、杀掉在途 turn，最坏重建 `sessionController` 失败并需整进程重启。也不要手写 profile 的 `package.json`、不要在 profile 目录里跑 pnpm。

## 5. 常用内建服务（`ctx` 键）

`sessions` 会话日志/内存库 · `systemPrompt` 提示词组装 · `tools` 工具注册与受控执行管线 · `agents` Agent 注册表 · `agentLoop` 循环驱动器 · `llm` 模型适配器注册表 · `skills` 技能注册表 · `commands` 人类斜杠命令 · `approval` 一次性审批 · `jobs` 后台任务 · `fs` 文件系统缝 · `shell` bash 执行缝 · `subprocess` 子进程缝 · `terminals` PTY · `sandbox` 进程限制缝 · `codeRuntime` 代码执行 · `sessionPersistence` 持久化 · `settings` / `credentials` / `workspaceRegistry` / `goals` / `planMode` / `subagents` / `workflowEngine` / `storage`。

完整清单与每个方法的精确签名：以官方文档 `docs/subsystems/*.md` 与 `docs/cordis-api/*` 为准，或用会话内 `cordis_inspect_query` 查实时定义。

## 6. 工具策略管线（执行顺序）

```
tools/pre-execute (waterfall, allow|deny|ask) → ctx.tools.guard() (单调 deny)
→ tools/execute (包裹，仅可换 exec.signal) → execute(args, exec)
→ tools/post-execute (换 content/value/阻断/附上下文) → finalizeContent
→ tools/result (只读观察) → 持久化 tool/result (会话事件)
```

选择规则：策略门用 `pre-execute`；不可翻案的 deny 用 `guard`；超时/重试/指标用 `execute`；改结果用 `post-execute`；审计/采集用 `result`。

Code Mode：`await tools.<name>(args)` 免费获得；成功 = 最终规范 JSON 值；失败 = `ToolCallError(name, toolName, message)`。

## 7. UI 卡片（纯函数！只依赖 args (+result)，禁止 I/O / 时钟 / 随机）

- `presentCall(args)` → `{card:'generic',title,kind?,rawInput?,content?,locations?}` | `{card:'terminal',title,description?,cwd?}` | `{card:'diff',title,diffs,locations?}`
- `presentResult(args,{content,isError,meta?})` → generic / terminal / diff / search(`shape:'matches'|'paths'`) / read / web(`kind:'search'|'fetch'`)
- 回放元数据：`output.presentationMeta(args, value)` → 持久化 `tool/result.meta`

## 8. 三层能力缝（seam）模板

- Definition（`dsh-my-cap`）：`export abstract class MyCapService extends Service { constructor(ctx) { super(ctx, 'myCap') } abstract execute(req): Promise<res> }` + Context 声明合并。
- Provider（`dsh-my-cap-local`）：`export function apply(ctx) { ctx.plugin(class MyCapLocal extends MyCapService { ... }) }`。
- Consumer（`dsh-tool-my-cap`）：`inject = ['tools', 'myCap']`，`ctx.tools.register(defineTool({ ... execute: args => ctx.myCap.execute(...) }))`。

规则：**不提前拆**；Provider 与 Consumer 互不依赖；默认值走显式 `resolve(request): Spec`。

## 9. LLM 适配器要点

`class MyAdapter extends LlmAdapter { async *stream(options): AsyncIterable<StreamChunk> }` → `ctx.llm.registerAdapter(['provider'], adapter)`。

Chunk 协议：`block-start` → `text-delta*` → `block-end`（完整块）→ … → `usage`（在 finish 前）→ `finish`（最后；`reason: {kind:'stop'|'tool-calls'}`）。无法满足的字段抛带稳定 code 的 `LlmError`。

## 10. 打包与展示元数据（发布前必须补齐）

- bundle 清单 `"dsh": { "bundle": { "patch": "./cordis.patch.yml" } }`；`patch` 可为有序数组，每个文件的相对路径按各自目录解析。
- 展示元数据：`locale/*.json` 的 `meta.title` / `meta.description` + 导出的 `./icon`（SVG/PNG/JPEG/WebP，≤ 256 KiB、realpath 后必须在包内）。子路径插件**不读**自己的 `package.json`。
- 共享实例的 dsh 包只放 `peerDependencies` + `devDependencies`，绝不放 `dependencies`（profile 内副本会静默遮蔽运行时版本，核心服务注册失败）。
- 版本/兼容声明：`engines.dsh` 与 `@deepseek-ai/dsh*` peer 范围的含义见 [version-matrix.md](./version-matrix.md) §5；**0.2.2 线要求 Node >= 24.9**。
- git 安装需要自包含的 `prepare` 脚本（不假设 monorepo 兄弟目录）；npm/tarball 分发免 `allowBuilds`。

## 11. 红线（违反 = 错误行为或挂门禁）

1. **注册必须走** `ctx.effect()` / `ctx.on()` / 服务 `register()`（返回 disposer）。注册在别的 ctx（如 `agent.ctx`）上的有**两个拥有者**——disposer 也要留在自己插件的 effect 里。
2. **waterfall 监听器必须调 `next()`**；不调 = 故意短路。改写 `agent/pre-step` 决策要**展开**（`{ ...decision, messages }`），否则 `startsRequestSeries` 之类字段会丢。
3. **用最弱的够用机制**：`ctx.tools.restrict()`（只移除）< `ctx.tools.guard()`（只拒绝）< waterfall 改写（依赖顺序）< `system-prompt/assemble`（整段替换）。最终结果只看 `tools/result`；只有要变换结果才用 `tools/post-execute`。
4. **模型可见 ⟺ 已记录**：新模型可见输入必须新增会话事件（`SessionEventMap`）——**由宿主侧新增**，不是插件自己写（见第 10 条）。
5. **不得硬编码可调参数**（判断：`cordis.yml` 能否改）；misconfig fail loud。要暴露给用户的实时字段用 `Volatile<T>` + `loader/volatile-update`（表单命名空间 = profile 条目 id）。
6. **独立插件包**：cordis 是 `peerDependency`（与宿主同身份；scoped `@deepseek-ai/cordis` 与 unscoped `cordis` 混用会「双 Cordis 分裂」）；ESM；发布带 `lib/` 或 tarball。**共享实例的 dsh 包只放 peer + dev，绝不放 `dependencies`**。兼容范围写法与强制点见 [version-matrix.md](./version-matrix.md) §5。
7. **必须交展示元数据**：`locale/*.json` 的 `meta.title` / `meta.description` + 导出的 `./icon`（≤ 256 KiB、包内）。子路径插件不读自己的 `package.json`。
8. **文档双语成对**；工具描述/提示词即行为；非平凡变更加 Agent Note；提交前跑最小检查集。
9. **跨边界 opaque id 用 branded**（`Branded<B>` from `dsh-brand`），从不裸 `string`。
10. **绝不要用新的 `type` 追加会话事件**：`SessionEventMap` 成员默认 required-on-read——不认识该事件类型的 build 会拒绝整份日志，除非事件带信封的 `ignorable: true`，而运行时 `Session.append()` 无法写这个标记。插件状态从既有事件推导，或放进 inspection 找到的 storage 服务。只有结构格式变更才 bump `SESSION_FORMAT_VERSION`（当前 `4`）；对 `SessionEvent` 的 `switch` 落入文档化 `default`，**禁用 `assertNever`**（merge-extensible union）。
11. **`agent.inject()` 的 `source` 不再接受**退役的 `{ kind: 'plugin', plugin: '<name>' }` 包装——先用 `MessageSourceMap` 声明合并自己的 kind。`inject` 不唤醒 agent（要唤醒用 `followup()` / `steer()`）。
12. **绝不在会话运行期间写 profile 的 `cordis.patch.yml`**；不要手写 profile 的 `package.json`；不要在 profile 目录里跑 pnpm。
13. **UI 插件**：只渲染 slot 里的 React 组件（禁止 iframe 托管页面）；样式只用 `--dsw-alias-*` 主题 token；不 `require` 任何 Harness Client 包（抛错组件会把 slot 搞白）；`dsh.client.inject` **必须是静态数组**。
14. **可选服务用 `ctx.get(name)`**；`ctx.<service>` 只对你自己 `inject` 里声明过的服务合法（写未声明的 ctx 属性会抛错并可能静默杀掉整个插件激活）。性能上 per-session 状态放 `ctx.sessionProjections`；实时 token 读 `agent/assistant-stream`；**不轮询 `agent/status`**。

## 12. 常用命令

```sh
dsh --version                          # 先确认宿主版本
dsh --profile web                      # 启动 Web UI
dsh --profile headless "任务文本"       # 一次性无界面执行
dsh --profile <name> --dump-config     # 打印实际生效的插件树；查 patch: entry not found
dsh plugin --profile <name> add <pkg>  # 安装 bundle（--profile 必须在转发参数之前）
dsh plugin --profile <name> remove <pkg>
dsh plugin --profile <name> install    # 仅补装/修复 profile 依赖
pnpm dsh web --patch ./scratch-plugin/cordis.yml   # 仓库内开发调试
```

被 peer 范围拒绝安装时（唯一的自救路径，按 `<包@精确版本> × <宿主版本>` 精确配对）：

```sh
dsh plugin --profile <profile> version-exemptions       # 列出已授予的豁免
dsh plugin --profile <profile> allow-version <pkg@ver> --dsh-version <runtime> --accept-risk
dsh plugin --profile <profile> revoke-version <pkg@ver>
```

更多命令与分工见 [cli-matrix.md](./cli-matrix.md)。

## 13. dsh-ee 企业侧速查

- 发布前先**预检**：`POST /api/me/packages/check`（网页「分享插件 → 网页上传」里点预检）；不写库、不占版本号，返回 `can_submit` / `will_publish` / `requires_review` + `scan` / `conflicts`。
- 发布：`npm publish --registry https://<dsh-ee>/npm/`（包名在企业作用域下），或在网页上传 `.tgz`。
- 版本号不可复用（含驳回/下架）；审核与可见范围由管理员配置。详见 [`docs/plugin-authors.md`](../plugin-authors.md)。
- **dsh-ee 扩展不是 DSH 插件**：`dsh-ee extension ...` 那套命令用于声明式/服务/端侧扩展，见 [`docs/plugins.md`](../plugins.md)。

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `guide/quick-reference.zh-CN.md`（原文：[quick-reference.zh-CN.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/quick-reference.zh-CN.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/levels.md`（Apache-2.0）；不代表上游背书。同步日期 2026-11-12。
