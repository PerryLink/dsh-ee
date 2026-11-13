# 插件安全：信任边界与作者自查（dsh-ee 作者版）

> **来源与许可**
> - 主来源：[PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) 的 [docs/security.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/security.md)
> - 上游文件（固定 commit，公开链接）：[docs/security.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/security.md)、[SKILL.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/SKILL.md)、[docs/levels.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/levels.md)
> - 版本：`dsh-plugin-guide@0.5.5`；公开 pin：tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`（2026-11-08）
> - 许可：Apache-2.0（上游 `LICENSE`；上游 `NOTICE.md` 声明其自有文本以 Apache-2.0 发布；上游 `package.json` `"license": "Apache-2.0"`）
> - 版权：© 2026 dsh-plugin-guide contributors。上游 `LICENSE` 为 Apache-2.0 模板、未填写版权主体；PerryLink 仅对其拥有版权的材料声明版权。
> - 补充来源：`dsh-standards` 的 security scope 只做链接与摘要（其正文为 **CC BY 4.0**，本页**不复述条款**）；dsh-ee 企业侧事实来自本仓库基线 `20aafbb` 的 `server/internal/registry/tarball.go`、`cordis_check.go`、`server/internal/api/package_check.go`、`docs/plugin-authors.md`（自己的代码与文档，非上游改编）。
> - 修改说明：中文节选并改写；保留信任边界、最小权限、凭据、egress、文件/子进程/动态执行、提示注入、依赖与发布、用户侧检查与静态检查边界；删除上游的市场合约、内部工作区引用与具体 DSC 条款表；补充 dsh-ee 企业侧映射（预检/审核/企业 npm）；**有删改，未逐字复制**。
> - 同步日期：2026-11-12
>
> 本页不是 DSH 官方文档，不是安全审计、认证或背书。静态检查只能覆盖已知模式；没有运行时沙箱与抓取证据时，写 `not-run` / `not-measured`，**绝不写「安全」**。

## 0. 先读这一段：插件的信任边界

一个 DSH 插件运行在**宿主进程里、用同一个用户、拥有宿主的全部权限**。宿主不会对已加载的插件做沙箱：

- `import node:fs`、`fetch`、`child_process`、`process.env` 对插件始终可用；
- 沙箱只限制**工具子进程的文件写入**；网络与进程可见性不在它的词表里；
- `dsh.capabilities` 只是**声明**，宿主不读取也不强制；
- 检查器在需要 runtime 的路径上会 **import 插件**，检查路径没有沙箱，并继承 `HOME`。

结论：插件能读写文件、起进程、联网、读凭据库，**无论它是否声明了这些能力**。安全靠作者自律 + 发布方/企业审核 + 运行时收敛；「声明」不等于「隔离」。

## 1. 信任边界一览

| 事实 | 推论 |
|---|---|
| 插件与宿主同进程 | `node:fs` / `fetch` / `child_process` / `process.env` 始终可达 |
| `dsh.capabilities` 是声明 | 声明 `[]` 不会移除能力；多声明是 WARN，漏声明是静态 FAIL |
| 沙箱只覆盖工具子进程的文件写 | 它不是插件代码的边界，也不是网络策略 |
| 检查器会 import 插件 | `--runtime` 类检查会执行包代码；**不要在不信任的包/重要机器上跑** |
| 静态检查看已知源码形态 | 看不到运行时 egress、动态 URL、提示词是否真被污染 |
| 运行时安全需要收敛 | 网络策略沙箱 + 抓取/审计是观察真实行为的唯一方式 |

## 2. 最小权限与能力声明

- 只声明插件**真正触及**的封闭集合 ID；声明与静态触发要一致（漏声明会被检查器判为问题，多声明是 WARN）。
- 当前能力 ID（以上游 `docs/security.md` 的快照为例，共 15 个）：`fs:read`、`fs:write`、`shell`、`subprocess`、`network:egress`、`network:ingress`、`credentials:read`、`credentials:list`、`credentials:provider`、`llm:adapter`、`sessions:read`、`system-prompt:write`、`tools:register`、`remote:expose`、`plugin:install`。
- `dsh.permissions` / `security.permissions` **不是宿主字段**；如果要写披露块，标明 `draft`（给人/市场看，不是强制）。不写披露时，「缺省」表示**未声明**，不是「无权限」。
- `dsh.commitments` / `dsh.disclosure` 同理：`false` = 不承诺，缺省 = 未声明。不要把每个承诺都填 `true`；不成立的承诺比缺失更糟。
- 对照代码审一遍：注册了工具就应有 `tools:register`；读了 `ctx.fs` 就应有 `fs:read`。**复制模板留下的空数组会正好隐藏真正的漏声明。**

## 3. 凭据与敏感文件

- **按操作解析凭据引用**，经 `ctx.credentials` 的 seam 取；永远不要打印、日志、持久化，或把值放进 `argv`。
- UI 只应看到 `describe()`（存在性/可写性），不展示 `resolve()` 的值。
- **不要**读 `$DSH_HOME/.credentials.yaml`、`~/.ssh`、`~/.aws`、`~/.netrc`、`.npmrc` 等后备文件；不要枚举无关记录。
- 子进程默认拿到**净化后的父环境**；只有显式 `env` 条目才是 opt-in。不要「以防万一」把 token 转发给子进程。

```ts
import { credentialRef } from '@deepseek-ai/dsh-credentials'

const ref = credentialRef('MY_API_KEY')
const resolved = await ctx.credentials.resolve(ref) // 每次操作重新解析
if (!resolved) throw new Error('MY_API_KEY is not configured') // 不要打印 resolved.value

const info = await ctx.credentials.describe(ref) // UI 只读存在性/可写性
```

## 4. 出网（egress）

- 用**常量 allowlist**：精确主机名 + 只允许 `https`。不要用后缀/通配。
- **绝不 fetch 来自模型、工具结果或用户消息里的 URL**；用 `redirect: 'manual'`，每一跳都过同一 allowlist（有界），并拒绝跨 origin 发送凭据。
- 必须调第三方时，在 README/披露里写清：endpoint、保留哪些内容、是否转发会话文本。
- 宿主没有出网策略，`web_fetch` 能访问私有地址；这个 allowlist 是插件自己的纪律，**不是沙箱**。

```ts
const ALLOW = new Set(['api.example.com'])

export function checkedUrl(raw: string): URL {
  const url = new URL(raw)
  if (url.protocol !== 'https:' || !ALLOW.has(url.hostname)) {
    throw new Error('blocked endpoint')
  }
  return url
}

const res = await fetch(checkedUrl('https://api.example.com/v1/x'), { redirect: 'manual' })
```

## 5. 文件、子进程与动态执行

- **文件**：用 `ctx.fs`。先 `resolve()` 目标，再 `contains(workspace, target)`；拒绝 `..`、绝对路径与 symlink（先 `lstat`）；限制大小；临时文件权限 `0700`/`0600`（Windows 用 ACL）。
- **子进程**：默认不要起。必须起时用 `ctx.subprocess.spawn`：`argv` 数组、绝对可执行路径、cwd 在工作区内、净化环境、超时/abort、有界输出；**绝不拼接 shell 字符串**。
- **动态执行**：禁止 `eval`、`new Function`、`vm.runIn*`、`module._compile`、`process.binding`；动态 `import()` 只接受静态路径。
- 不要发布压缩过的 blob 或纯二进制代码；要发 wasm/native，就同时给出源码、构建方法与 `sha256`。
- **生命周期脚本是安装期的 RCE 面**：发布包不要带 `preinstall` / `install` / `postinstall` / `prepare`。真需要构建脚本时，绝不把下载内容 pipe 进 shell，也不用 `base64 -d` / `node -e` 隐藏行为。

```ts
const workspace = await ctx.fs.resolve(process.cwd())
const target = await ctx.fs.resolve(userPath)
if (!ctx.fs.contains(workspace, target)) throw new Error('path escapes the workspace')

const child = ctx.subprocess.spawn({
  argv: ['/usr/bin/git', 'status'],
  cwd: process.cwd(),
  stdio: { stdin: 'ignore', stdout: { maxBytes: 64_000 }, stderr: { maxBytes: 64_000 } },
  graceMs: 5_000,
  signal,
})
const outcome = await child.done
```

> 沙箱仍只覆盖子进程的文件写；插件自己的 `fetch` 与 `node:fs` 会绕过所有 seam。**不要把声明当隔离。**

## 6. 工具输出与提示注入

- 工具结果、网页、文件内容、会话文本都是**不可信数据**。
- 按工具的 `output.schema` 校验返回值（不匹配应是 `INVALID_TOOL_OUTPUT` 一类错误）。
- 外部文本进提示词前：固定 untrusted 前缀 + **每次调用随机边界** + JSON 转义；**绝不放进 system prompt**，绝不把外部文本当指令。
- 指令层级写在代码里：system > user > external data。模型输出保持「数据」：校验它、不要 shell 它、不要让它决定任意 URL。
- 渲染也是同一边界：用 `textContent` / React 转义，不用 `innerHTML`；日志与错误文本同样转义。
- 包裹只降低注入成功率，**不是隔离**。

```ts
import { randomUUID } from 'node:crypto'

const wrap = (s: string) => {
  const id = randomUUID() // 每次调用重新随机，避免复用边界
  return `<untrusted-${id}>\n${JSON.stringify(s).replaceAll('<', '\\u003c')}\n</untrusted-${id}>`
}
```

## 7. 依赖、构建与发布安全

- 提交 lockfile；用 `npm ci --ignore-scripts`（或 `--frozen-lockfile`）安装。
- 依赖保持最小；检查 typosquat/lookalike（`dsh`/`@deepseek-ai/dsh` 附近的相似 scope、编辑距离相近的名字）。
- 用 `files` allowlist，发布构建产物；固定 Node 与包管理器；让 tag 可复现构建。
- 发布用 npm trusted publishing（OIDC）+ `--provenance`；开 2FA、保护 tag、要求 review；**版本号不复用**（坏版本用 deprecate，不要重发同一版本）。

```sh
npm ci --ignore-scripts && npm test && npm pack --dry-run
npm audit --omit=dev
# 检查没有生命周期脚本
node -e "const s=require('./package.json').scripts||{}; if(s.preinstall||s.install||s.postinstall||s.prepare) process.exit(1)"
# 发布（CI）：trusted publishing + provenance
npm publish --access public --provenance
```

- `dist.integrity` 只证明 tarball 与 registry 一致；attestation 需要**验签 + 绑定的构建来源**。两者都不是「代码安全」的证明。

## 8. 漏洞响应（流程，不是检查项）

- 仓库带 `SECURITY.md`：私密渠道（如 GitHub Security Advisories）、支持版本、首次响应时限（如 7 天）、协调披露窗口（如 90 天）。
- 不要用公开 issue 收漏洞报告。
- 修复后：发 patch release + advisory + 致谢；泄露的 token 撤销并轮换；恶意版本 deprecate、请求市场/企业源下架，并保留证据（日志、包哈希、时间线）。

## 9. 用户/管理员侧：安装前、中、后

**安装前**读清单：`dsh.capabilities`、`dsh.labels`、`scripts`、`files`、`engines.dsh`、来源 pin；再看发布者与源码。`npm view` 一次只查一个字段（多字段查询可能把存在的字段报成空值）：

```sh
npm view pkg@1.2.3 version dsh scripts dist.integrity repository.url time.modified --json

# 静态 security scope（不执行插件代码；具体语义以 dsh-standards 为准）
npx dsh-standards check ./pkg --scope security --json
```

- 空 capabilities = **未声明**，不是「无能力」。
- `prompt-modification` / `third-party-model-provider` / `remote-access` / `red-team` / `mirror` 一类 label 是作者的风险提示，不是安全许可。

**安装中**：从一次性 profile 开始，加 `--ignore-scripts`（它只阻止安装脚本，不阻止宿主加载的代码）。GUI 的 build 审批只显示包名、不显示脚本正文——先 `npm pack` 解包读 `scripts/*` 再批。CLI 继承整个环境，从干净 shell 启动、不要带 token。新会话默认 `workspace-write` + `ask`；不要切 `danger-full-access` + `never`。`DSH_PERMISSION_MODE=read-only` 约束的是工具子进程，不是插件。

```sh
dsh plugin --profile lab add pkg@1.2.3 --ignore-scripts
dsh --profile lab --dump-config
dsh plugin --profile lab remove pkg
```

**安装后**：切回 `workspace-write`，观察安装日志与会话里的工具调用，不用的包及时移除；检查 profile 的 `package.json`/lockfile 与凭据记录。异常时先停用、保留日志与包哈希，走私密渠道报告，**不要先公开细节**。

## 10. dsh-ee 企业侧补充

- 企业预检（`POST /api/me/packages/check`）只解包做**静态**检查，不执行插件代码。与安全相关的检查项：`secrets`（`.env`/`.npmrc`/私钥/疑似 token → fail）、`install-scripts`（warn）、`bundle`/`patch`、`cordis`（把 Cordis 打进依赖 → warn）、`license`、`engines`、`size`。
- 预检**不是**安全审计：它看不到运行时 egress、动态 URL、真实提示词污染，也不判断作者可信度。审核通过不等于「安全认证」；页面与 README 不要写「已通过安全认证」。
- 企业包发布建议：企业 npm scope + 可发布令牌最小化；公共 npm 侧尽量用 trusted publishing/provenance；**版本不可复用**（含驳回/下架版本）；发布说明里写清 `engines.dsh`、安装脚本（如有）和外部 endpoint。
- 企业安装侧：用 dsh-ee 的可见范围（部门/角色/人）与下发方式收敛暴露面；敏感连接用 dsh-ee 的连接凭据，不要把 token 嵌进插件包；企业审计能追到人、扩展/插件版本、工具与凭据模式。
- `security` 是 `dsh-standards` 的一个 scope，**不是第四层**；ENTRY/FLOW/BOOST 的判定不受它影响。企业审核可以要求作者提供 `SECURITY.md`、漏洞响应流程与发布证据，但 dsh-ee 不替作者做安全审计。

## 11. 静态检查不能证明什么

- URL 是否真的被 fetch、重定向去了哪里；
- 包外文件是否被读/写；
- 依赖自己的安装脚本是否运行过；
- 模型是否真的被注入文本影响；
- 作者是否可信（签名在人工验证密钥链与构建来源之前只是自述证据）。

运行时安全需要**带网络策略的沙箱 + 审计轨迹**。Windows 没有 `unshare` 一类原语，收敛更弱；只限制文件写的沙箱不是网络边界。没有证据就写 `not-run` / `not-measured`，**绝不写「secure」**。

## 12. 词汇纪律

不要写 `secure` / `certified` / `official` / `audited` / `100% risk-free`，也不要用盾牌/对勾暗示认证。使用固定免责声明：**非官方 · 非认证 · 非安全审计 · 非背书 · 无担保**。

---

> 改编自 [PerryLink/dsh-plugin-guide](https://github.com/PerryLink/dsh-plugin-guide) v0.5.5（tag `v0.5.5` = commit `9f0a8a10b76a872f1a82570734a6b166661fa38b`）`docs/security.md`（原文：[security.md](https://github.com/PerryLink/dsh-plugin-guide/blob/9f0a8a10b76a872f1a82570734a6b166661fa38b/docs/security.md)；Apache-2.0，版权归上游 contributors），有删改并中文化；`dsh-standards` 的 security scope 只链接、不复述条款（CC BY 4.0）；dsh-ee 侧补充取自本仓库基线 `20aafbb`；不代表上游背书。同步日期 2026-11-12。
