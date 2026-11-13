# 端侧老系统模板（TypeScript）

一个没有接口、只认员工自己登录态的老 OA：员工第一次用时弹出 OA 自己的登录页，登录后 cookie 存在他自己的电脑上，之后带着 cookie 发 HTTP 请求取数和提交。写操作执行前经 dsh-ee 让员工确认。用的是 [端侧 SDK](../../sdk-local)，依赖写成普通 npm 包（`@dsh-ee/extension-local` + 版本），不是仓库里的相对路径。

## 复制它

1. 改 `package.json` 的 `name`，`src/extension.ts` 里的 `ID`、`package`、`OA_URL`、`SESSION_COOKIE`。`@dsh-ee/extension-local` 的版本范围按企业 npm 源里实际发布的版本写。
2. 把 `list_forms`、`submit_leave` 换成对你们系统的请求，只返回模型需要的字段。只读的才标 `readOnly`。
3. `npm install && npm test`，再 `npm run manifest` 生成清单，`dsh-ee extension check . --json` 通过。
4. 把 `dsh-ee.extension.json` 一起发布（已经在 `package.json` 的 `files` 里），再把包发到企业 npm 源。

在本仓库里开发时，SDK 还没发布到 npm，模板不提交 `package-lock.json`；先构建 SDK，再把依赖临时指到本地目录跑测试（别把 `file:` 路径提交）：

```bash
cd extensions/sdk-local && npm ci && npm run build
cd ../official/legacy-local
npm pkg set 'dependencies.@dsh-ee/extension-local=file:../../sdk-local'
npm install && npm test
npm pkg set 'dependencies.@dsh-ee/extension-local=^0.1.0'
```

SDK 发布到 npm 后，直接 `npm install && npm test` 就会从 npm 源解析。

## 给员工用

包经企业的 npm 源下发。员工在 DSH 里用接入插件的命令注册成本机 MCP 服务（先预览，再执行；不覆盖已有服务）：

```
/ee-local add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa
/ee-local add @acme/legacy-oa-local@0.1.0 --id acme/legacy-oa --yes
```

其他 Agent 的 MCP 配置里：

```json
{ "command": "npx", "args": ["-y", "@acme/legacy-oa-local"], "env": { "DSH_EE_URL": "https://ee.acme.com", "DSH_EE_TOKEN": "${DSH_EE_TOKEN}", "DSH_EE_HARNESS": "claude-code" } }
```

Claude Code 用 [official/claude-code](../claude-code) 一条命令写好。第一次调用时会弹出登录窗口，要装 Playwright（`npm i playwright && npx playwright install chromium`）。
