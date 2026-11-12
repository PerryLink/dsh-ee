---
title: DSH 插件发布
status: draft
since: 2026-11-12
lang: zh-CN
---

# DSH 插件发布到 dsh-ee

面向在企业内部分发 DSH 插件的作者。dsh-ee 有两种插件来源：**上游镜像**（插件已在 npm 发布，管理员预载进企业源，不改包内容）和**企业托管**（内部插件发布到企业 npm 作用域，经审核后上架）。本文以企业托管为主；上游镜像同样要满足清单要求。

完整字段说明和逐条失败原因见 [plugin-authors.md](../plugin-authors.md)；本文只给 dsh-ee 视角的流程摘要。

## 1. 包的最小要求

- `name` 在企业作用域下（默认 `@company/...`），`version` 是严格 semver。
- `files` 必须包含 `cordis.patch.yml` 和构建产物；npm 安装不执行 `prepare`，所以构建要在 `npm publish` 前完成。
- `engines.dsh` 写**实际验证过**的 DSH 范围；DSH 安装时用它判断兼容性，dsh-ee 审核页会展示。版本线与支持情况见 [dsh-primer 的版本矩阵](https://github.com/PerryLink/dsh-primer/blob/8750d31718c16601514e59fe1831de73cfed3d20/guide/version-matrix.md)（来源 pin 见 [SOURCES.md](../plugin-dev/SOURCES.md)）。
- `dsh.bundle.patch` 是包内相对路径，不能是绝对路径、不能含 `..`；浏览器端插件再声明 `dsh.client`。
- 不要打包 `cordis` / `@deepseek-ai/cordis` 进 `dependencies`，也不要放 `.env`、`.npmrc`、私钥或真实 token。

最小示例和每个字段的含义见 [plugin-authors.md](../plugin-authors.md) §1、§2。

## 2. 发布前预检

网页「分享插件 → 网页上传」选完 `.tgz` 后先点**预检**（`POST /api/me/packages/check`）。预检不写库、不占版本号，也不执行插件代码。

预检结果分三类：

- **自动检查**：`dsh.bundle` / patch、安装脚本、疑似密钥、license、`engines.dsh`、Cordis 依赖、体积等；
- **发布冲突**：包名、版本号、企业作用域、同名上游包、归属、版本占用；
- **结论**：`can_submit`（没有硬冲突，发布接口会接受）、`will_publish`（开放模式会直接上架）、`requires_review`（进入审核）。

有硬冲突先修；检查项失败可以先修，也可以仍提交审核，但版本号会被占用。预检是静态检查，**不是安全审计**，也不等于上游 ENTRY/FLOW/BOOST 的任何结论（见 [standards.md](./standards.md)）。

## 3. 发布

```bash
# 方式一：命令行发布到企业 npm 源
npm publish --registry https://<dsh-ee>/npm/

# 方式二：打包后在网页「分享插件 → 网页上传」
npm pack
```

发布前确认：`.npmrc` 配了企业源和发布令牌；包名在系统设置的企业作用域下；包内容用 `npm pack` 检查过。

## 4. 审核、上架与版本

- 默认**白名单模式**：发布后是待审核，管理员通过后才进 packument、tarball 和索引。
- **开放模式**：没有 fail 项的包发布即上架；有 fail 项的仍待审核。
- 上架后管理员设置可用范围（部门 / 角色 / 人）和下发方式（自选 / 自动安装）。
- 版本号发布后不可复用：被驳回、下架的版本也不能重发，修完请升版本。

## 5. 本地验证清单

发布前至少做一遍：

- `npm pack` 后看 `.tgz` 内容，确认 `cordis.patch.yml`、`lib/`、`README`、`LICENSE` 都在。
- 在干净的 DSH profile 里安装一次，确认 DSH 能启动、工具/命令/界面可用。
- 卸载、重装、升级（新版本号）各走一次。
- 带浏览器端的插件在 Desktop 和 `dsh web` 各看一次；目标环境是内网时再断网装一次。
- 包内带 `skills/` 的插件：这些技能由插件运行时注入 DSH，不会出现在 dsh-ee 能力中心，也不能在 dsh-ee 里按技能单独授权（见 [plugin-authors.md](../plugin-authors.md) §3）。

## 6. 与上游规范的关系

dsh-ee 不做评分、等级或认证。ENTRY / FLOW / BOOST 是上游作者/市场工具，dsh-ee 只维护企业侧映射（见 [plugin-quality.md](../plugin-quality.md)），不把审核状态说成「通过 ENTRY」或「BOOST 达标」。作者想自查，按 [standards.md](./standards.md) 在本地跑上游检查器。

## 7. 常见失败

发布 403 / 409、审核页 `bundle` / `secrets` fail、插件不生效、Node 版本报错等，逐条原因见 [错误与 FAQ](./errors-faq.md) 的「发布与审核」一节。
