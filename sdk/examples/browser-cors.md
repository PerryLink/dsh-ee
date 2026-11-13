# 浏览器内 Harness：CORS 与鉴权片段

dsh-ee 默认不开启 CORS。浏览器里的第三方 Harness 直接调用 `/v1/*` 或 `/mcp` 时，需要管理员
在服务端配置白名单（#127，环境变量 `DSH_EE_CORS_ORIGINS`，逗号分隔完整 origin）：

```sh
DSH_EE_CORS_ORIGINS='https://harness.example.com,http://localhost:5173'
```

说明：

- 只覆盖 `/v1/*` 与 `/mcp`；其它路径（`/api/*`、网页）不加 CORS 头。
- 精确匹配 origin（含协议与端口），不支持 `*`、`null` 或路径通配。
- 不使用 `credentials`：令牌放在 `Authorization: Bearer` 头里，不依赖 Cookie。
- 服务端会暴露 `X-Request-Id`、`X-RateLimit-*`、`Retry-After`、`Dsh-EE-API-Version`
  等响应头，SDK 可读取（普通跨域响应默认读不到这些头）。
- `OPTIONS` 预检不消耗令牌限流额度（#129）。

## 前端片段

```ts
import { DshHarnessClient } from '@dsh-ee/harness-client'; // 本仓库内可用相对 dist 路径

const client = new DshHarnessClient({
  baseUrl: 'https://dsh-ee.example.com',
  token: sessionToken, // 由你的登录态/企业 SSO 拿到，放内存里，避免 XSS+localStorage 长期留存
  onResponse: (info) => {
    // 跨域下这些头需要服务端 Access-Control-Expose-Headers（#127 已配置）
    console.debug(info.status, info.requestId, info.rateLimit?.remaining);
  },
});

const models = await client.listModels();
const answer = await client.createChatCompletion({
  model: models[0].id,
  messages: [{ role: 'user', content: 'ping' }],
});
```

MCP 也走同一套 CORS：

```ts
const session = await client.connectMcp();
const tools = await client.listAllTools();
```

## 注意事项

- 浏览器里不要使用设备码轮询等待（页面可能被挂起）；设备码登录适合 CLI/桌面客户端。
- `Authorization` 头会触发预检；预检由服务端 CORS 中间件返回 204，不占令牌额度。
- 令牌不要拼进 URL、不要放进 `Referer` 会泄露的位置；按企业要求选择内存/会话存储。
- 如果出现浏览器控制台 CORS 报错，先确认服务端白名单包含**页面 origin**（不是 API 域名），
  并确认代理没有吞掉 `Access-Control-*` 响应头。
- `sdk/examples/generic-harness.mjs` 里的客户端代码在 Node 与浏览器通用（除 `process` 用法）。
