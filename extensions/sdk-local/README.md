# @dsh-ee/extension-local：端侧扩展 SDK

端侧扩展是跑在**员工自己电脑上**的扩展（[docs/plugins.md](../../docs/plugins.md) §8）：一个 npm 包，起一个本机 MCP 服务，Agent（DSH、Claude Code、Codex）直接调它，用员工自己的登录态访问没有接口、只在内网、只认员工身份的老系统。dsh-ee 服务端不运行它。

## 安装

```bash
npm install @dsh-ee/extension-local
```

SDK 是一个普通 npm 包（`@dsh-ee/extension-local`），不依赖仓库里的相对路径；发布到 npm 官方源或企业 npm 源后，扩展的 `package.json` 里写 `"@dsh-ee/extension-local": "^0.1.0"` 即可。企业内网通常从 dsh-ee 自带的 npm 源安装，按企业作用域配好 `.npmrc` 后同样是这条命令。

复制 [official/legacy-local](../official/legacy-local) 就能开始；它把 SDK 写成普通依赖（包名 + 版本），不需要 `file:` 路径。

```ts
import { defineExtension, ExtError } from "@dsh-ee/extension-local";

export default defineExtension({
  id: "acme/legacy-oa",
  version: "0.1.0",
  name: { zh: "老 OA", en: "Legacy OA" },
  package: "@acme/legacy-oa-local",
  tools: [
    { name: "list_forms", description: "列出待办", readOnly: true, handler: async () => [...] },
    // 没标 readOnly：执行前先经 dsh-ee 让员工在网页上确认
    { name: "submit_leave", description: "提交请假单", params: [{ name: "days", type: "number", required: true }], handler: async (args) => "已提交" },
  ],
});
```

入口里 `extension.serveStdio()`，Agent 的 MCP 配置写 `{"command": "npx", "args": ["-y", "@acme/legacy-oa-local"]}`。DSH 里不用手工改配置：接入插件 `dsh-connect` 提供 `/ee-local` 命令，把发布到企业 npm 源的端侧扩展注册成本机 MCP 服务（注册前会先让你确认，不覆盖已有服务）。Claude Code 用 [official/claude-code](../official/claude-code) 适配器写入 `.mcp.json`。清单 `dsh-ee.extension.json` 也建议随包发布（放进 `package.json` 的 `files`），供能力中心与检查工具读取。

## SDK 替你做的

- **MCP 服务**（stdio）：`initialize`、`ping`、`tools/list`、`tools/call`。
- **写操作确认**：没标 `readOnly` 的工具执行前调 dsh-ee 的 `POST /api/local/confirm`，员工在网页上同意、同样的参数再调一次才执行；没配 dsh-ee 或连不上就不执行。
- **审计回报**：每次调用后把扩展、版本、工具、耗时、成败报给 `POST /api/local/audit`，不带参数和返回内容。
- **错误码**：`throw new ExtError("authorize" | "forbidden" | "unavailable" | "invalid" | …, "说明")`，SDK 变成带 `code` 的工具结果。内部异常不把堆栈交给模型。
- **webSignIn**：弹出系统自己的登录页，员工照常登录，等指定的 cookie 出现后存在本机（`~/.dsh-ee/local/<扩展>/cookies.json`，权限 600），之后用 `cookieHeader(store.load(), url)` 带上。浏览器用 Playwright（可选依赖），`channel: "chrome"` 可以用本机装的 Chrome。

协议见 [docs/extension-protocol.md](../../docs/extension-protocol.md) §9。

## 环境变量

| 变量 | 作用 |
|---|---|
| `DSH_EE_URL` | dsh-ee 的地址 |
| `DSH_EE_TOKEN` | 员工的接入令牌（dsh-ee「我的令牌」里建） |
| `DSH_EE_HARNESS` | 调用方：`dsh` / `claude-code` / `codex`。DSH 自己有审批卡，默认设置下不再要服务端确认 |
| `DSH_EE_LOCAL_HOME` | cookie 存在哪，默认 `~/.dsh-ee/local` |

## 命令

```
dsh-ee-local manifest dist/extension.js -o dsh-ee.extension.json   # 生成清单，再用 dsh-ee extension check 检查
dsh-ee-local tools dist/extension.js                               # 列出工具
dsh-ee-local call dist/extension.js list_forms '{"status":"pending"}'
```

开发时用 `node --watch dist/index.js` 改了就重启。

## 发布 SDK（维护者）

SDK 的 `package.json` 已经带齐发布字段（`name`、`version`、`exports`、`types`、`files`、`license`、`engines`、`publishConfig`），`npm pack` 只带 `dist/`、`README.md`、`LICENSE`。发布前先 dry-run 看内容：

```bash
cd extensions/sdk-local
npm ci
npm pack --dry-run        # prepack 会先 tsc 构建 dist/，再列出 package 内容
npm publish               # 发到 npm 官方源；企业源用 --registry https://ee.acme.com/npm/
```

版本号按 [SemVer](https://semver.org/) 改（API 变化发 minor/major），发布后把仓库里的 `extensions/official/legacy-local` 等模板依赖范围同步过去。`dist/` 不提交到 git，由 `prepack` 在打包时生成。

## 这是弱保证

本机进程可以不问、不报（[docs/plugins.md](../../docs/plugins.md) §13 待定 2）。SDK 让正常写出来的扩展默认就问、就报；要求所有调用都经过服务端的企业，只用声明式和服务扩展。
