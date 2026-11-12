# 发布前检查清单（DSH 侧 + dsh-ee 侧）

> **来源与许可**
> - 主来源：`PerryLink/dsh-plugin-guide`（[GitHub](https://github.com/PerryLink/dsh-plugin-guide)）`docs/release-checklist.md`
> - 上游文件（固定 commit，公开链接）：[docs/release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)、[guide/plugin-dev-guide.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/plugin-dev-guide.md)、[guide/version-matrix.md](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/version-matrix.md)
> - 版本：`dsh-plugin-guide@0.5.5`；公开 pin：tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`（2026-11-08）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布；上游 `package.json` `"license": "Apache-2.0"`）
> - 版权：© 2026 dsh-plugin-guide contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：[PerryLink/dsh-primer](https://github.com/PerryLink/dsh-primer) `0.4.3`（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`；npm `gitHead` = `fa2b74234530193c6cdeb82265167328ce6beb90`）`guide/plugin-dev-guide.md` §7、§10（打包/安装/分发）与 `guide/version-matrix.md`（兼容范围）（Apache-2.0）；dsh-ee 侧清单来自本仓库基线 `20aafbb` 的 `docs/plugin-authors.md`、`server/internal/api/package_check.go`、`server/internal/registry/tarball.go`（自己的文档与代码事实）。
> - 修改说明：中文节选并改写；删除上游的具体第三方 CI Action 名称与徽章示例，改成固定版本检查器的通用 CI 写法；把清单拆成「DSH 通用」与「dsh-ee 企业侧」两段；新增版本号不可复用、预检与审核等企业规则。
> - 同步日期：2026-11-12
>
> 本清单是**自查**，不是认证、评审或排名。检查通过不等于运行时通过，也不等于安全审计。

## 0. 什么时候用

- 第一次 `npm publish` 前。
- 提交到 dsh-ee 企业源/市场前。
- 升级 DSH 宿主或改兼容范围后（重新跑一遍）。

先记录环境：`dsh --version`、`node --version`、目标 profile 名。

维护多个包、做批次发布、排查 registry 行为或需要回滚时，配合 [release-engineering.md](./release-engineering.md) 一起看（本页负责单包发布前自查）。

## 1. Manifest（`package.json`）

- [ ] `name` / `version`（**严格 semver**，如 `0.1.0`，不能是 `v0.1` 或区间）/ `license`（SPDX）/ 入口字段齐全。
- [ ] `engines.dsh` 写你实际验证过的 DSH 线；`@deepseek-ai/dsh*` peer 范围与它一致（口径与写法见 [version-matrix.md](./version-matrix.md) §5）。
- [ ] `dsh.manifestVersion`（如果使用）、`dsh.bundle.patch`、按需的 `dsh.capabilities` / `dsh.labels` 存在；capabilities 只声明代码真正用到的（空数组表示不用）。
- [ ] `repository` / `homepage` 等溯源字段齐全（企业审核要能追到仓库）。
- [ ] 共享实例的 dsh 包只放 `peerDependencies` + `devDependencies`，**不放 `dependencies`**。
- [ ] `files` 白名单包含：入口/构建产物、`cordis.patch.yml`、`package.json`、`LICENSE`、`README`；不含 fixtures、本地 profile、secrets、构建缓存。

## 2. 包内容

```sh
npm pack --dry-run
```

逐项读文件清单：

- [ ] 入口文件在包里，`main` / `exports` / `dsh.bundle.patch` 指向的文件都存在。
- [ ] `cordis.patch.yml` 在包里；`name` 与包名一致；YAML 是数组且至少一行引用本包。
- [ ] `locale/*.json` 的 `meta.title` / `meta.description` 与导出的 `./icon`（≤ 256 KiB、realpath 在包内）存在；子路径插件不依赖自己的 `package.json`。
- [ ] `LICENSE` 与 `README` 随包分发。
- [ ] 无 `.env` / `.npmrc` / 私钥 / token / 本机绝对路径 / 测试 fixtures。
- [ ] 无 `.ts` 残留导入：发布前 `grep -rE "from './[^']+\.ts'" lib/`（或等价检查）。

## 3. 静态 ENTRY 检查（不执行插件）

```sh
# 固定版本更可复现；--no-exec 表示不运行插件代码
npx -y dsh-standards@<ver> check . --scope entry --entry-claim static --no-exec
```

读报告顺序：

1. `blocking` / FAIL —— 先修。
2. `hygiene` WARN —— 判断项，不是自动失败；在 PR/审核说明里解释。
3. `partial` / `not checked` —— 静态没覆盖，**不能当通过**。

注意：报告只覆盖 ENTRY 层，不说明 FLOW（运行体验）与 BOOST（Cordis 实践），也不是安全审计。

## 4. 安装冒烟（静态检查证明不了「装得上」）

```sh
# 用干净 DSH_HOME，避免污染本机 profile
DSH_HOME=/tmp/dsh-plugin-test dsh plugin --profile demo add .
DSH_HOME=/tmp/dsh-plugin-test dsh --profile demo
```

- [ ] 插件出现在 profile 里，DSH 能启动。
- [ ] 注册的工具/命令/界面可见可调用。
- [ ] 卸载、重装、升级（新版本号）都正常。
- [ ] `dsh --profile demo --dump-config` 无 `patch: entry <id> not found`。
- [ ] 已发布的情况下，用 registry 里的 tarball 而不是 checkout 再跑一次。
- [ ] 带浏览器端的插件在 Desktop 与 `dsh web` 各看一次；目标环境内网时断网安装一次。

## 5. 可选 CI（advisory）

CI 里跑固定版本检查器即可，不要求把检查结果当强制门禁（是否 `fail-on` 由你决定），token 保持只读：

```yaml
name: entry-check
on: [pull_request]
permissions:
  contents: read
jobs:
  entry:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: npx -y dsh-standards@<ver> check . --scope entry --entry-claim static --no-exec
```

更完整的本地检查（脚手架静态检查 + 打包后干净 profile 冒烟）可用社区 CLI `dsh-primer check` / `dsh-primer verify`，见 [cli-matrix.md](./cli-matrix.md) §2。

## 6. 本清单**不**做的事

- 不在静态步骤安装或执行包。
- 不审计安全、权限或供应链来源。
- 不排名、不打分、不认证、不背书。
- 干净的静态报告不是运行时结果；运行时通过也不是安全评审。

## 7. dsh-ee 企业侧发布清单

在公共 npm/本地验证之后，再走企业侧（细节见 [`docs/plugin-authors.md`](../plugin-authors.md)）：

- [ ] 包名在系统设置的企业作用域下（默认 `@company`）；`.npmrc` 配了企业源与可发布令牌。
- [ ] 先**预检**（网页「分享插件 → 网页上传」→ 预检，接口 `POST /api/me/packages/check`）：只解包检查、不写库、不占版本号。
- [ ] 预检结果逐项处理：`can_submit=false` 先修硬冲突；`scan` 的 `fail` 先修，`warn` 在审核说明里解释；确认 `will_publish` / `requires_review` 符合预期。
- [ ] `engines.dsh` 已声明（预检会记录并在审核页展示，缺了会 warn）；写的是你验证过的宿主版本。
- [ ] 版本号**没有**被用过（包括被驳回/下架的版本）；同一版本号永远不能重发。
- [ ] 发布：`npm publish --registry https://<dsh-ee>/npm/`，或网页上传 `.tgz`。
- [ ] 审核通过后确认可见范围（部门/角色/人）与下发方式（自选/自动安装）。
- [ ] 包内 `SKILL.md` 文件会在预检里提示（`skills` warn）——它们不会出现在能力中心，也不能按技能单独授权；要按技能治理请单独发布能力（见 [faq.md](./faq.md) D4）。
- [ ] 不要在任何地方宣称「ENTRY 通过 / BOOST 达标 / 已通过安全认证」；dsh-ee 审核状态只表示企业准入结论。

## 8. 常见返工点

| 返工点 | 先检查 |
|---|---|
| 装不上 / 安装被拒 | peer/`engines.dsh` 范围（[version-matrix.md](./version-matrix.md) §5） |
| 装上了不生效 | `cordis.patch.yml` 的 `name` 与包名、配置 Schema、`--dump-config`（[errors.md](./errors.md) §4） |
| 审核 bundle/patch fail | patch 是否在 tarball 内、路径是否安全、YAML 结构（[errors.md](./errors.md) §6） |
| secrets fail | 包里是否含 `.env`/`.npmrc`/私钥/疑似 token |
| 403 / 409 | 企业作用域 / 版本号已存在（[errors.md](./errors.md) §6） |
| 升级后行为变了 | 迁移清单（[migration.md](./migration.md)） |

---

> 改编自 `PerryLink/dsh-plugin-guide` v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`） `docs/release-checklist.md`（原文：[release-checklist.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/release-checklist.md)；Apache-2.0，版权归上游 contributors），有删改并中文化；补充来源 `PerryLink/dsh-primer` v0.4.3（tag `v0.4.3` = commit `8750d31718c16601514e59fe1831de73cfed3d20`） `guide/plugin-dev-guide.md`（Apache-2.0）；dsh-ee 侧清单取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
