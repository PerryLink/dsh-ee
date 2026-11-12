# 扩展协议（第 1 版）

> 这是服务扩展和端侧扩展与 dsh-ee 之间的协议，两套 SDK（Go：`extensions/sdk-go`；TypeScript：`extensions/sdk-local`）都按它实现。
> 用 SDK 写扩展不需要读这份文档；用别的语言自己实现时照着它做，再用 `dsh-ee extension test` 验证。
> 设计和取舍见 [plugins.md](./plugins.md) §4.2、§6、§7、§8。

## 1. 服务扩展是什么

企业自己部署的一个 HTTP 服务。dsh-ee 不运行它，只按下面的约定调它：

| 地址 | 作用 |
|---|---|
| `GET /.well-known/dsh-ee-extension.json` | 清单（§2），管理员安装时填服务地址，dsh-ee 从这里取 |
| `POST /mcp` | 工具，标准 MCP（Streamable HTTP，只回 JSON）：`initialize`、`ping`、`tools/list`、`tools/call` |
| `POST /ee/<名字>` | 工具之外的约定接口（§5），JSON 收发 |

清单里 `"protocol": "1"` 表示实现的是这一版协议，不写当作 1。

## 2. 清单

格式和声明式扩展的清单相同，规格用 `dsh-ee schema get manifest` 查。服务扩展：

- `kind` 是 `service`，`service.url` 写部署后的地址（安装时以管理员填的为准）；
- `capabilities` 列出实现了哪些约定接口：`tools`、`validate`、`options`、`health`、`knowledge.documents`（`kb/browse`、`kb/changes`、`kb/document`）、`knowledge.search`（`kb/search`）、`oauth`、`endpoint`。SDK 按注册了哪些处理函数自动生成；
- `tools` 里每个工具都要在 `/mcp` 的 `tools/list` 里出现。两边不一致时以清单为准，健康检查里报警。只读的工具才标 `readOnly`，没标的一律当写操作；
- 健康检查时 dsh-ee 重新取清单。清单的摘要变了就记下新版本，**新增的工具、写操作、权限、凭据模式的变化先不启用**，等管理员重新安装确认。

## 3. dsh-ee 怎么证明自己

每个请求（MCP 和约定接口）都带 `Authorization: Bearer <ee_jwt>`：

- RS256 签名，公钥在 `<dsh-ee 访问地址>/.well-known/jwks.json`，按 `kid` 选；
- `iss` 是 dsh-ee 的访问地址，`aud` 是清单里 `credential.audience`（没写就是扩展的连接名），`exp` 是 5 分钟后；
- 员工调用时 `sub`、`preferred_username`、`name`、`email` 是这位员工，`dept`、`roles` 按管理员的设置带；
- 没有员工的调用（健康检查、保存配置前的检查、知识同步、回调转发）是 `"sub": "system", "sys": true`。

扩展**必须验签**。验不过返回 401 和 `{"code": "unauthenticated"}`。SDK 已经做了；本机开发可以设 `DSH_EE_INSECURE=1` 跳过，生产上绝不能开。

## 4. dsh-ee 交给扩展的凭据（§7.4）

| 请求头 | 什么时候有 | 内容 |
|---|---|---|
| `X-DSH-EE-Credential` | 凭据模式是 `user_secret` 或 `oauth` 的员工调用 | 员工在源系统的凭据：`user_secret` 原样；`oauth` 只给 access token，refresh token 和 client secret 留在 dsh-ee |
| `X-DSH-EE-Shared-Credential` | 清单写了 `credential.shared` 或模式是 `shared` | 管理员配的共享凭据 |
| `X-DSH-EE-Config` | 管理员填过配置（清单 `config`） | 配置的 JSON，base64 编码；`secret` 类型的值已解密 |

这些只在这一次请求里有效。扩展不能落盘、不能打日志、不能放进返回结果（`extension test` 会查）。

## 5. 约定接口

请求和响应都是 JSON。失败时返回非 2xx 和 `{"code": "...", "message": "..."}`，`code` 见 §6。

| 接口 | 请求 | 响应 |
|---|---|---|
| `validate` | `{"config": {...}}`（还没保存的配置） | `{}`；配置不对返回 400 和 `invalid` |
| `options` | `{"field": "tenant"}` | `{"options": [{"value": "...", "label": "..."}]}` |
| `health` | `{}` | `{"ok": true}`，可以带 `version` |
| `kb/browse` | `{"parent": ""}`（空是最上层） | `{"items": [{"id", "title", "kind": "folder" \| "document", "hasChildren"}]}` |
| `kb/changes` | `{"scope": ["节点 id"], "cursor": "..."}`（第一次 cursor 为空） | `{"items": [{"id", "title", "folder", "deleted"}], "cursor": "...", "hasMore": false}` |
| `kb/document` | `{"id": "..."}` | `{"id", "title", "format": "markdown" \| "text", "content", "url", "fields": {...}}` |
| `kb/search` | `{"query": "...", "limit": 8}` | `{"items": [{"id", "title", "snippet", "url"}]}` |
| `oauth/authorize-url` | `{"redirectUri", "state"}` | `{"url": "..."}` |
| `oauth/exchange` | `{"redirectUri", "params": {回调带回来的参数}}` | `{"accessToken", "refreshToken", "expiresAt", "account"}` |
| `oauth/refresh` | `{"refreshToken"}` | 同上 |
| `oauth/revoke` | `{"tokens": {"accessToken", "refreshToken", "expiresAt", "account"}}`（员工离职、取消授权时，尽力而为） | `{}` |
| `endpoint` | `{"method", "path", "query", "headers", "body"}`（body 是 base64） | `{"status", "headers", "body"}` |

几条约定：

- **`kb/changes` 的 cursor** 表示「这之前的都给过了」，要单调往前；没有新变化时返回空列表和同一个 cursor。同一个 cursor 再问一次不能漏掉之后的变化。dsh-ee 一页处理完才前进，中途失败下一轮从这一页重来。一轮最多 200 页、5000 篇。
- **同步没有员工**：`kb/browse`、`kb/changes`、`kb/document` 在同步时用系统令牌和共享凭据。**联邦检索有员工**：`kb/search` 和联邦来源的 `kb/document` 带这位员工的令牌和凭据，源系统按他自己的权限返回。每个来源 3 秒的上限。
- **`kb/document` 的 `fields`** 只能写清单 `fields` 里声明过的字段（[access-control.md](./access-control.md) §6.0），其余丢弃。
- **`endpoint`**：第三方打到 `https://<dsh-ee>/x/<随机串>/<path>` 的请求原样转过来，不带员工身份；验签（比如飞书的加密和签名）由扩展自己做。请求体上限 1 MB，每个地址每分钟 120 次。
- 标准 OAuth 2.0 不需要实现 `oauth/*`：清单写 `oauth.authorizeUrl`、`tokenUrl`、`scopes`，dsh-ee 自己完成。

## 6. 错误码

工具结果出错时在 `_meta` 里放 `{"dsh-ee/error": {"code": "..."}}`，约定接口出错时在响应体里放 `code`：

| code | 含义 | dsh-ee 怎么处理 |
|---|---|---|
| `authorize` | 这位员工还没授权或过期了 | 告诉员工去连接页面授权 |
| `setup` | 管理员没配好 | 让员工联系管理员 |
| `forbidden` | 源系统说没权限 | 原样告诉员工，不重试 |
| `unavailable` | 源系统暂时不可用 | 联邦检索时跳过这个来源 |
| `not_found` | 东西不存在 | 同步时跳过这一篇 |
| `invalid` | 参数或配置不对 | 原样告诉调用方 |
| `not_implemented` | 没实现这个约定接口 | 当作不支持 |
| `unauthenticated` | 令牌没验过 | dsh-ee 自己的问题，记日志 |

## 7. 超时和熔断

每次调用沿用连接的超时（默认 30 秒），结果上限 64 KB（约定接口的响应上限 8 MB）。一个扩展连续失败 5 次就熔断 30 秒，期间它的工具直接返回 `unavailable`，别的扩展和 dsh-ee 不受影响。

## 8. 场景测试

`dsh-ee extension test <目录|地址> --run "启动命令"` 先按本协议逐项测（清单、验签、MCP、每个声明了的约定接口、错误码、凭据不外泄），再跑目录里 `tests/*.yaml` 的场景测试：

```yaml
- name: 员工只看到自己的审批单
  as: zhangsan            # 模拟环境里预置的员工，默认 zhangsan
  credential: demo-123    # 员工凭据；none 表示不带；不写用 --credential 给的
  call: list_forms
  args: { status: pending }
  expect:
    ok: true
    count: { gte: 2 }     # 返回的列表长度：eq / gte / lte
    contains: ["F-1001"]
- name: 没填凭据时提示授权
  credential: none
  call: list_forms
  expect:
    error: authorize
```

全部通过才算合格。测试用的是 `dsh-ee extension dev` 的模拟环境，不需要数据库，CI 里也能跑。

## 9. 端侧扩展

端侧扩展跑在员工电脑上（[plugins.md](./plugins.md) §8），是一个 npm 包，用 stdio 提供 MCP 服务（按行收发 JSON-RPC），Agent 直接调它，不经过 dsh-ee。清单里 `kind` 是 `local`，`package` 是包名。

它用员工自己的接入令牌（环境变量 `DSH_EE_URL`、`DSH_EE_TOKEN`，调用方是哪个 Agent 写在 `DSH_EE_HARNESS`）调 dsh-ee 的两个接口：

| 接口 | 请求 | 响应 |
|---|---|---|
| `POST /api/local/confirm` | `{"extension", "version", "tool", "args", "harness"}` | `{"approved": true}`；或 `{"approved": false, "id", "url", "message"}`，把 `message` 原样交给模型；员工拒绝过的带 `"rejected": true` |
| `POST /api/local/audit` | `{"extension", "version", "tool", "ok", "duration_ms", "code", "harness"}` | 204 |

- **没标 `readOnly` 的工具执行前必须先问 `confirm`**，`approved` 不是 `true` 就不执行。员工在 `/confirm/<id>` 页面上同意后，同样的参数再调一次才放行，一次同意只能用一次。管理员把写操作确认设成 `non_dsh` 时，`harness` 是 `dsh` 的不用再确认（DSH 自己有审批卡）。
- **每次调用后报 `audit`**：只有摘要，不带参数和返回内容。
- 这是弱保证（[plugins.md](./plugins.md) §13 待定 2）：本机进程可以不问、不报。SDK 让正常写出来的扩展默认就问、就报。
- 员工的登录态（cookie）只存在他自己的电脑上，不经过 dsh-ee。SDK 的 `webSignIn` 负责弹出登录窗口、取 cookie。
