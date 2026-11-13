# 常见错误与排错

> **来源与许可**
> - 主来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`guide/plugin-dev-guide.md` §7.3（社区实测坑清单）、§7.1（git 安装构建）、`guide/unfixed-issues.md`（只取排错方法，不搬官方 issue 结论）
> - 上游文件（固定 commit，公开链接）：[guide/plugin-dev-guide.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/plugin-dev-guide.md)、[guide/unfixed-issues.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/unfixed-issues.md)、[docs/rules.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/rules.md)、[docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`docs/rules.md`（报告行读法，「不是 pass」的两种情形）、`docs/release-checklist.md`（发布检查）（Apache-2.0）。dsh-ee 预检/发布错误来自本仓库基线 `20aafbb` 的 `server/internal/registry/tarball.go`、`patch_check.go`、`cordis_check.go`、`server/internal/api/package_check.go`、`docs/plugin-authors.md`（自己的代码与文档）。
> - 修改说明：中文重排为「症状 → 原因 → 处理」；只保留排错所需的最小事实，不复制 `dsh-standards` 的检查条款正文（CC BY 4.0）；删除上游 issue 编号、社区仓库清单、本机绝对路径与带日期的生态统计；新增 dsh-ee 预检/发布错误一节。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档，不是认证或安全审计。命令与报错文案随宿主版本变化；以目标版本的实际输出与官方文档为准。

## 1. 排错纪律

- **先证据后结论**：`static ≠ runtime`，`recognized ≠ pass`，`not-run ≠ pass`。没有命令、产物、环境与日期的结论只能写成「未验证」。
- **先最小复现**：干净 `DSH_HOME` + 单个插件 + 一次启动。本机 profile 里别的插件会污染判断。
- **先看宿主版本**：`dsh --version` 决定了报错属于哪一代，很多「坏了」其实是跨代变更（见 [migration.md](./migration.md)）。
- **不猜**：拿不到行号/提交号时写「未核」，不要把社区帖子当官方结论。

## 2. 怎么读检查报告

`dsh-standards` / `dsh-levels` 一类检查器的报告通常分三层：

1. **blocking / FAIL**：阻塞项，先修。
2. **hygiene / WARN**：判断项，不自动失败，但要在 PR/审核说明里解释。
3. **partial / not checked / not-run**：静态没覆盖或没跑——**不是通过**。

报告行的通用结构是「ID → 级别/位置 → 说明 → 修法」。ID 的具体含义与修复模板由 `dsh-standards` / `dsh-plugin-spec` 维护（正文 CC BY 4.0，本仓库不搬条款），可对照上游 `rules.md` / `recipes.md` 或检查器输出的 `where` / `fix` 字段。

两种最容易误读的情形：

- `recognized` 只说明包形态能被识别，**不是** runtime 通过；runtime 需要真实插件启动或 bundle boot。
- 某组检查**全部 `skip`** 时是「未评估」，不能报告成功；零检查执行同样不算通过。

## 3. 安装期错误

| 症状 | 原因 | 处理 |
|---|---|---|
| `installation rejected: … is incompatible with dsh <ver>: peerDependencies …` | 插件声明的 `@deepseek-ai/dsh-*` peer 范围不覆盖当前宿主（自 0.1.7-rc.1 起安装期强制） | 按线补全范围；紧急时用 `dsh plugin --profile <p> allow-version <pkg@ver> --dsh-version <runtime> --accept-risk` 精确豁免（宿主升级后需重新授予）；见 [version-matrix.md](./version-matrix.md) §5 |
| 安装到远古版本 / 版本号对不上 | 子包 npm `latest` 标签冻结；或依赖写 `"latest"` | 写死版本或钉 `next`/`alpha`；查 `npm view <pkg> dist-tags`；见 [version-matrix.md](./version-matrix.md) §2.2 |
| `file:` 安装后改源码不生效、`update` 是 no-op | `file:` 安装载入的是**副本**（hoisted + 锁文件只记 version） | 调试期用 `--patch` 直挂源码；或每次 remove + add |
| Node 版本报错 / 启动即挂 | 宿主线要求 Node `>=24.9`（0.2.2 线）或 `^22.19.0`（更早 0.2.x）；Desktop 与 CLI 的 Node 可能不同 | 对齐 Node；发布前在目标 Node 冒烟 |
| profile 内出现「核心服务缺失 / settings service is absent」 | 共享实例的 dsh 包被写进 `dependencies`，profile 内副本遮蔽了运行时版本 | 共享实例的 dsh 包只放 `peerDependencies` + `devDependencies`；不要依赖 profile 里那份副本 |

## 4. 启动 / 运行期错误

| 症状 | 原因 | 处理 |
|---|---|---|
| 日志只有一行 `patch: entry <id> not found`，行为没生效 | 上游删行/改名后，按 id 覆盖的 patch 打空；**不报错** | `dsh --profile <name> --dump-config` 对照实际行；改用 `insert` 或新 id；升级后必跑 |
| `cannot get property "<service>" without inject` / 可选服务拿不到 | 没在 `inject` 声明，却用了 `ctx.<service>`；或把 `inject` 写成了函数 | `inject` 必须是静态数组且覆盖 `apply()` 触及的每个服务；可选服务用 `ctx.get(name)` |
| 插件激活被静默杀掉 / 只有一行 `web boot: 1 entry did not activate` | `apply()` 抛错（常见：写未声明的 ctx 属性、`inject` 不是数组、异步初始化顺序不对） | 把注册放进 `apply()` 的同步阶段/`ctx.effect`；UI 的 `dsh.client.inject` 用静态数组；用严格 ctx 单测断言 |
| 两个 Cordis / `Property 'tools' does not exist on type 'Context'` | 依赖里混入了另一份 cordis（scoped 与 unscoped 混用、从 `.pnpm` 副本解析） | 构建期把 cordis 解析到宿主 vendored 副本；独立包把 cordis 设为 peer；不要打包第二份 Cordis |
| UI slot 白屏 / 一个组件抛错整个区域空白 | 组件里 `require` 了 Harness Client 包；或样式/API 用了宿主不提供的形态 | 只渲染 slot 里的 React 组件；不 `require` Harness Client 包；样式只用 `--dsw-alias-*` token |
| 配置改了不生效 / 设置页没有字段 | 配置不是 `Volatile<T>`；或表单命名空间（= profile 条目 id）不对 | 暴露给用户的实时字段标 `volatile()`；用 `loader/volatile-update` 监听；命名空间用 profile 条目 id |
| 技能/命令没出现 | 只注册了工具（面向模型），没注册命令（面向人）；或技能注册未走 `ctx.skills` | 工具与命令是两套注册面；技能注册参考宿主文档 |
| 会话跑一段时间后工具/预设「丢了」 | 在会话运行期间改了 profile 的 `cordis.patch.yml`（HMR 重载） | 不要运行期改 profile patch；需要重启就整进程重启 |

## 5. 打包 / 发布期错误

| 症状 | 原因 | 处理 |
|---|---|---|
| `npm pack` 里缺入口/patch/`LICENSE`/README | `files` 白名单漏了；或构建产物在 `lib/` 但没先 build | `npm pack --dry-run` 逐项核；`files` 包含构建产物、`cordis.patch.yml`、`LICENSE`、`README` |
| 运行时找不到入口 / 产物残留 `.ts` 导入 | `main`/`types` 指向 `lib/...` 但 tsconfig 无 `outDir`；`tsc` 报错仍 emit；`rewriteRelativeImportExtensions` 未开 | 配 `outDir`；构建脚本 `tsc || exit 1` 或 `--noEmitOnError`；发布前 `grep -rE "from './[^']+\.ts'" lib/` |
| git 安装时构建失败 | `prepare` 依赖 monorepo 兄弟目录/类型检查/网络 | `prepare` 自包含：专用 tsdown 配置转译 `src/`；不假设兄弟包 |
| 展示标题/描述/图标不出现 | 缺 `locale/*.json` 的 `meta.*` 或导出的 `./icon`；图标 >256 KiB 或不在包内；子路径插件读了 `package.json` | 补 `locale/*.json` 与 `./icon`；子路径插件走 `locale` 与 `<subpath>/icon` |
| 插件行引用本包失败 | `cordis.patch.yml` 的 `name` 与 `package.json` 的 `name` 不一致（常见于改名后） | 两处严格一致；覆盖别人的行要保留原配置 |
| 无效配置导致整个 profile 加载失败 | 配置不符合 `Config` Schema | 配置项与 Schema 一致；misconfig 应 fail loud，不要静默吞掉 |

## 6. dsh-ee 预检 / 发布错误

预检接口 `POST /api/me/packages/check` 只解包检查、不写库、不占版本号。当前检查项（`registry.Check`，基线 `20aafbb`）与常见处理：

| 检查项 key | 含义 | 处理 |
|---|---|---|
| `bundle` | 有没有 `dsh.bundle`；`patch` 字段是否安全、文件是否在 tarball 内 | 缺则 fail（no-patch）；路径必须是包内相对路径 |
| `patch` | 真正解析 `dsh.bundle.patch`：YAML 数组、行结构、id 唯一、插入行 name 合法、至少一行引用本包 | 按报错的 `reason` 修 YAML/行结构；`name` 与包名一致 |
| `cordis` | 依赖里有没有把 Cordis/Cordis 插件打进 `dependencies` | 移除，改 peer/dev；多一份 Cordis 会导致服务注入与生命周期错乱 |
| `cordis-peer` | 声明的 `@deepseek-ai/cordis` peer 范围是否覆盖 dsh-ee 推荐宿主（当前 `4.1.0`） | 对齐范围；这条只是 warn |
| `skills` | 包内 `SKILL.md` 数量（warn） | 企业要按技能治理就单独发布能力；见 [faq.md](./faq.md) D4 |
| `install-scripts` | 有安装脚本（warn） | 在审核说明里写清安装脚本做什么；能用预构建产物更好 |
| `secrets` | 检出 `.env`/`.npmrc`/私钥/疑似 token（fail） | 从包里删除；自动检查会直接判失败 |
| `license` | 缺 `license`（warn） | 补 SPDX id |
| `engines` | 缺 `engines.dsh`（warn） | 写你验证过的范围；见 [version-matrix.md](./version-matrix.md) §1 |
| `dependencies` | 企业作用域外的依赖（信息） | 确认依赖闭包可镜像/可安装 |
| `size` | tarball > 20 MiB（warn） | 精简 `files`；不要把 fixtures/缓存打进包 |

发布相关：

| 症状 | 原因 | 处理 |
|---|---|---|
| 发布 403 | 包名不在企业作用域下 | 改包名到企业作用域；见 [`docs/plugin-authors.md`](../plugin-authors.md) §5 |
| 发布 409 | 版本号已存在（含驳回/下架） | 升版本号重发；版本号不可复用 |
| `can_submit=false` | 有硬冲突（包名/版本/作用域/同名包/归属/版本占用） | 看 `conflicts` 逐项修；不要提交 |
| 预检通过但审核被拒 | 审核是人工/策略判断，预检只覆盖静态项 | 看审核说明；预检通过不等于三层质量通过，更不是安全审计 |

## 7. 会话 / 数据错误

| 症状 | 原因 | 处理 |
|---|---|---|
| 旧构建打不开新会话日志（或反过来） | 会话格式 V4 的 required-on-read 事件；旧读方拒绝新日志 | 升级读方；或不要自己解析日志，改用 `ctx.sessionPersistence` / projection / `session/event` |
| 自己解压 `session.jsonl.zstd` 只有 1 行 | 多帧 zstd：每次写入追加一个 frame，单帧 API 只解第一帧 | 按魔数 `28 B5 2F FD` 逐帧扫描，或用宿主提供的逐帧 API；见 [version-matrix.md](./version-matrix.md) §6 |
| 插件加了事件后整个会话打不开 | 用新 `type` 追加会话事件（required-on-read），且 `Session.append()` 写不了 `ignorable` 标记 | 不要新增事件类型；从既有事件推导状态；见 [quick-reference.md](./quick-reference.md) §11 红线 10 |
| `agent.inject()` 被拒 | 用了退役的 `{ kind:'plugin', plugin:'…' }` source | 先 `declare module '@deepseek-ai/dsh-llm'` 声明自己的 `MessageSourceMap` kind；见 [migration.md](./migration.md) §B.8 |

## 8. 环境 / 平台

| 症状 | 原因 | 处理 |
|---|---|---|
| Windows 下 junction/软链创建失败 | `ln -s` 与 `cmd mklink /J` 在部分 shell 参数转换下失败 | 用 PowerShell `New-Item -ItemType Junction`；`@types` 不能整体 junction，直达真实路径 |
| 路径逃逸误报 | `path.resolve()` 返回反斜杠，与外部正斜杠比较恒 false | 比较前两侧都 `resolve()` 或统一分隔符 |
| vitest 找不到测试 | Windows 盘符小写（`c:/`） | 盘符大写 `C:/` |
| `DSH_PERMISSION_MODE=danger-full-access` 想写进模板 | 高风险模式（Windows 无沙箱后端时仅此可启动，且禁用审批） | 只用于可信本地开发机；不要写进模板/CI/共享机器 |
| `DSH_*` 环境变量放 `~/.dsh/.env` 启动报错 | 特殊环境变量必须由启动环境传入 | 由启动脚本/进程环境传入；凭据放 `$DSH_HOME/.credentials.yaml` |
| Node < 22.19/24.2 安装后静默深埋失败 | 部分版本没有友好的运行时版本门 | 升级 Node；全局安装替代 `npx`；用 `dsh --version` + `node --version` 记录环境 |

## 9. 推荐排查顺序

1. `dsh --version`、`node --version`、`uname -a`（或 Windows 版本）——记录环境。
2. `dsh --profile <name> --dump-config`——看组合是否真的含你的行、有没有 `patch: entry not found`。
3. 干净 `DSH_HOME` 单插件启动——排除 profile 污染。
4. 检查器：静态 ENTRY 报告 + 包内容 `npm pack --dry-run`。
5. 运行期日志：插件是否 `ACTIVE`、effect 数量、slot 是否注册；会话事件是否被拒。
6. dsh-ee 侧：预检 `scan`/`conflicts` → 审核说明 → 发布状态。

每一步都留命令输出/日志片段；不要用「重启试试」代替定位。跨代问题先回 [version-matrix.md](./version-matrix.md)，迁移类问题回 [migration.md](./migration.md)。

## 10. dsh-ee 与 DSH 的错误不要互相套

- dsh-ee 预检/审核的错误（bundle/secrets/版本占用/审核状态）不是 DSH 的检查结论；dsh-ee 不会替你跑 ENTRY/FLOW/BOOST。
- DSH 侧的安装拒绝/会话错误也不是 dsh-ee 的准入问题；两者排查入口不同。
- dsh-ee 扩展（`dsh-ee extension check` 的 `code`/`severity`/`where`/`fix`）是**另一套**错误体系，见 [`docs/plugin-authors.md`](../plugin-authors.md) 与 [`docs/plugins.md`](../plugins.md)。

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `guide/plugin-dev-guide.md` §7.3、`guide/unfixed-issues.md`（原文：[plugin-dev-guide.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/plugin-dev-guide.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/rules.md`、`docs/release-checklist.md`（Apache-2.0）；dsh-ee 侧错误表取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
