# 开发循环：从零到发布（dsh-ee 作者版）

> **来源与许可**
> - 主来源：`PerryLink/dsh-primer`（[GitHub](https://github.com/PerryLink/dsh-primer)）`guide/plugin-dev-guide.md` §2（工程形态、清单、分层、常用命令）、§7（打包/安装/分发）、§10（从零到发布）
> - 上游文件（固定 commit，公开链接）：[guide/plugin-dev-guide.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/plugin-dev-guide.md)、[docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)
> - 版本：`dsh-primer@0.4.3`；公开 pin：tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`（2026-11-03）；npm 发布记录的 `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`（用于核对 npm 发布物；该 commit 与 tag 树仅发布 workflow 不同）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布）
> - 版权：© 2026 dsh-primer contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) `0.5.5`（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`，2026-11-08）`docs/release-checklist.md`（发布前清单）、`docs/levels.md`（证据纪律）（Apache-2.0）。dsh-ee 侧流程来自本仓库基线 `20aafbb` 的 `docs/plugin-authors.md`、`server/internal/api/package_check.go`、`server/internal/registry/tarball.go`（自己的文档与代码事实）。
> - 修改说明：中文节选并改写；删除 `references/`/`downloads/` 本地路径与社区生态段；把「从零到发布」重排成可执行循环，补上 dsh-ee 预检/发布步骤；版本数字统一指向 [version-matrix.md](./version-matrix.md)。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档，不是认证或安全审计。命令与字段随宿主版本变化；以目标版本的官方文档与 `--help` 为准。

## 0. 前置检查（每次开始前 30 秒）

```sh
dsh --version            # 决定读哪代文档、写什么兼容范围
node --version           # 0.2.2 线要求 >=24.9；更早 0.2.x 可用 ^22.19.0
dsh --profile <name> --dump-config | head   # 现有 profile 是否健康、有没有 patch: entry not found
```

把这三个值记在 PR/审核说明里。宿主版本是后面所有判断的基准（见 [version-matrix.md](./version-matrix.md)）。

## 1. 选开发形态

| 形态 | 适合 | 安装方式 | 注意 |
|---|---|---|---|
| **仓库内 scratch 插件** | 学教程、临时验证、改本地源码 | `pnpm dsh web --patch ./scratch-plugin/cordis.yml` | 不产生 npm 包；每次改源码重启/重载 |
| **独立插件包（bundle）** | 要分发的正式形态 | `dsh plugin --profile <p> add <pkg>`，进 `dsh.profile.bundles` 层栈 | 重启生效；发布到 npm/企业源 |
| **纯 cordis 插件**（无 `dsh.bundle`） | 只需插一行组合 | `dsh plugin add <pkg>` 后写 profile `cordis.patch.yml` | 配置 HMR 实时生效 |
| **会话内直接装**（0.2 世代官方路径） | 在 agent 会话里快速装 bundle | `plugin_manager.install_bundle`（绝对包目录） | 官方明确：不要手写 profile 的 `package.json`/`cordis.patch.yml`，不要在 profile 目录跑 pnpm |

> 一个包只能选一条通道：**有 `dsh.bundle` 就按 bundle 装**；没有才走纯 cordis 的 insert 行。两条通道混写会出现「装了但没挂上」。

## 2. 最小骨架（发布形态）

```text
hello-plugin/
├── package.json       # dsh.bundle → cordis.patch.yml；可选 dsh.client + icon + exports
├── cordis.patch.yml   # - insert: [{ id, name: 'dsh-hello-plugin' }]
├── index.js           # export const name / export function apply（或构建产物入口）
├── locale/en.json     # { "meta": { "title": "...", "description": "..." } }
├── icon.svg           # 导出的 ./icon，≤ 256 KiB 且必须在包内
└── LICENSE / README.md
```

发布前必须满足：

- [ ] `name` / `version`（严格 semver）/ `license`（SPDX）/ 入口字段齐全；`files` 白名单包含构建产物、`cordis.patch.yml`、`LICENSE`、`README`。
- [ ] 有 `dsh.bundle.patch`（相对路径、包内、不含 `..`/反斜杠）；patch 解析为 YAML 数组，且至少一行 `name` 等于本包名。
- [ ] 展示元数据：`locale/*.json` 的 `meta.title` / `meta.description` + 导出的 `./icon`（≤256 KiB、realpath 在包内）。子路径插件不读自己的 `package.json`。
- [ ] 兼容声明：`engines.dsh` 写你验证过的线；`@deepseek-ai/dsh*` peer 范围与它一致（口径见 [version-matrix.md](./version-matrix.md) §5）。
- [ ] 共享实例的 dsh 包只放 `peerDependencies` + `devDependencies`，**不放 `dependencies`**（profile 内副本会静默遮蔽运行时版本）。
- [ ] 有 `prepare`/构建脚本时保证 git 安装自包含（不假设 monorepo 兄弟目录）。

## 3. 本地调试循环

```sh
# 1) 改源码 → 用覆盖层直挂（推荐，不污染 profile）
pnpm dsh web --patch ./scratch-plugin/cordis.yml

# 2) 看实际组合
dsh --profile <name> --dump-config
#   出现 patch: entry <id> not found = 你的 override 打空了（上游删行/改名，静默失效）

# 3) 运行时核对（免审批优先）
#    会话内用 cordis_inspect_list / cordis_inspect_query 查服务、事件、工具、slots、主题
```

已知硬约束：

- **不要在会话运行期间改 profile 的 `cordis.patch.yml`**：HMR 会重载，实测会丢 preset 工具、杀掉在途 turn，最坏让 `sessionController` 永久失效（只能整进程重启）。
- **不要手写 profile 的 `package.json`**、不要在 profile 目录里跑 pnpm；安装/组合交给 `dsh plugin` / `plugin_manager`。
- `file:` 安装载入的是**副本**（`dsh plugin add file:../my-plugin` 后改源码不生效）；调试期用 `--patch` 直挂或每次 remove + add。
- 改完 `inject` / 新增服务调用后，用一次干净启动验证；可选服务用 `ctx.get(name)`，不要写未声明的 `ctx.<name>`。

## 4. 机械检查（提交/发布前）

```sh
# 上游社区 CLI：静态检查 + 打包后干净 profile 冒烟
dsh-primer check --json
dsh-primer verify

# ENTRY 静态检查（不执行插件代码；固定版本更可复现）
npx -y dsh-standards@<ver> check . --scope entry --entry-claim static --no-exec

# 看包内容与文件清单
npm pack --dry-run
```

读报告顺序：`blocking` / FAIL → 先修；`hygiene` WARN → 判断项；`partial` / `not checked` → 静态没覆盖，不能当通过。`static ≠ runtime`、`not-run ≠ pass`。详见 [errors.md](./errors.md) §2。

## 5. 安装冒烟（静态检查证明不了「装得上」）

```sh
# 干净 DSH_HOME，避免污染本机 profile
DSH_HOME=/tmp/dsh-plugin-test dsh plugin --profile web add ./company-dsh-my-plugin-0.1.0.tgz
DSH_HOME=/tmp/dsh-plugin-test dsh --profile web
```

然后逐项确认：

- [ ] 插件出现在 profile 里，DSH 能启动。
- [ ] 注册的工具/命令/界面可见可调用。
- [ ] 卸载、重装、升级（新版本号）都正常。
- [ ] 带浏览器端的插件在 Desktop 与 `dsh web` 各看一次。
- [ ] 目标环境内网时，断网安装一次，确认依赖闭包完整。

## 6. 发布前清单

完整版见 [release-checklist.md](./release-checklist.md)（改编自 `dsh-plugin-guide` 0.5.5 的 release checklist）。最小集合：

- [ ] `dsh --version` 与插件声明范围一致；`--dump-config` 无 `patch: entry not found`。
- [ ] `npm pack --dry-run` 文件清单正确，无 fixtures / 本地 profile / secrets / 缓存。
- [ ] ENTRY 静态检查无 blocking；WARN 有解释。
- [ ] 干净 `DSH_HOME` 安装/启动/卸载冒烟通过。
- [ ] README 写清扩展点、配置、模型可见效果与失败恢复；无「官方/认证/安全」类声明。

## 7. 发布到 dsh-ee（企业侧）

1. **预检**：网页「分享插件 → 网页上传」选 `.tgz` → 预检（`POST /api/me/packages/check`）。它只解包检查，**不写库、不占版本号**。看 `can_submit` / `will_publish` / `requires_review` 与 `scan` / `conflicts`。
2. **发布**：`npm publish --registry https://<dsh-ee>/npm/`（包名在企业作用域下），或网页上传 `.tgz` 发布。
3. **审核**：默认白名单模式 = `pending`，管理员通过后才进 packument / tarball / `/index/dsh-index.json`；开放模式无 fail 项直接上架。
4. **上架后**：管理员配可见范围（部门/角色/人）与下发方式（自选/自动安装）；调用进审计。
5. **版本纪律**：版本号不可复用（含驳回、下架）；修完升版本号。`engines.dsh` 会在审核页展示，没有声明会 warn。

预检的检查项（`registry.Check`，基线 `20aafbb`）：`bundle`、`patch`、`cordis`、`cordis-peer`、`skills`、`install-scripts`、`secrets`、`license`、`engines`、`dependencies`、`size`。它们只覆盖企业准入需要的静态项，**不是** ENTRY/FLOW/BOOST 判定，也不是安全审计。企业侧细节见 [`docs/plugin-authors.md`](../plugin-authors.md)。

## 8. 升级循环（宿主变了之后）

1. `dsh --version` 确认新线；按 [version-matrix.md](./version-matrix.md) 对号。
2. `dsh --profile <name> --dump-config` 查 `patch: entry <id> not found`（上游删行/改名后 override 静默失效）。
3. 逐条对照 [migration.md](./migration.md) 的破坏性变更清单；能改的改，不能改的写进 README 限制。
4. 更新 `engines.dsh` / peer 范围，**升版本号**后再发布；dsh-ee 侧旧版本号不能复用。
5. 重跑 §4–§5 的检查与安装冒烟，把宿主版本写进审核说明。

## 9. 一轮完整示例（命令串）

```sh
# 1. 脚手架（社区 CLI；也可手写）
dsh-primer new company-dsh-my-plugin
cd company-dsh-my-plugin

# 2. 本地调试
pnpm dsh web --patch ./scratch-plugin/cordis.yml

# 3. 静态检查 + 打包 + 干净 profile 冒烟
dsh-primer check --json
dsh-primer verify

# 4. 发布到企业 npm 源
npm publish --registry https://<dsh-ee>/npm/

# 5. 去 dsh-ee 网页确认审核状态与可见范围
```

## 10. dsh-ee 企业侧的三个差异（别混淆）

- **发布对象不同**：DSH 插件（cordis npm 包）发布到企业 npm；dsh-ee 扩展（declarative/service/local）用 `dsh-ee extension ...` 治理。两套模型，不要互相套流程。
- **准入不同**：DSH 侧靠宿主 peer/engines 校验与检查器；企业侧还叠加 dsh-ee 预检 + 审核 + 可见范围，且版本号不可复用。
- **证明不同**：DSH 侧的 `static` 不是 runtime；dsh-ee 的审核通过也不等于三层质量通过，更不是安全认证。

---

> 改编自 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `guide/plugin-dev-guide.md`（原文：[plugin-dev-guide.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/plugin-dev-guide.md)；Apache-2.0，版权归上游 contributors），有删改；补充来源 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/release-checklist.md`（Apache-2.0）；dsh-ee 侧流程取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
