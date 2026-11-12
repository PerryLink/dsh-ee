# 端侧老系统模板（TypeScript）

一个没有接口、只认员工自己登录态的老 OA：员工第一次用时弹出 OA 自己的登录页，登录后 cookie 存在他自己的电脑上，之后带着 cookie 发 HTTP 请求取数和提交。写操作执行前经 dsh-ee 让员工确认。用的是 [端侧 SDK](../../sdk-local)。

## 复制它

1. 改 `package.json` 的 `name`，`src/extension.ts` 里的 `ID`、`package`、`OA_URL`、`SESSION_COOKIE`。
2. 把 `list_forms`、`submit_leave` 换成对你们系统的请求，只返回模型需要的字段。只读的才标 `readOnly`。
3. `npm install && npm test`，再 `npm run manifest` 生成清单，`dsh-ee extension check . --json` 通过。

## 给员工用

包经企业的 npm 源下发。员工的 Agent 配置里：

```json
{ "command": "npx", "args": ["-y", "@acme/legacy-oa-local"], "env": { "DSH_EE_URL": "https://ee.acme.com", "DSH_EE_TOKEN": "${DSH_EE_TOKEN}", "DSH_EE_HARNESS": "claude-code" } }
```

Claude Code 用 [official/claude-code](../claude-code) 一条命令写好。第一次调用时会弹出登录窗口，要装 Playwright（`npm i playwright && npx playwright install chromium`）。
