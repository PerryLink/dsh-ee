# 端侧扩展（local）：跑在员工电脑上的扩展

> 本文是 `server/internal/api/extensions.go` 里「端侧扩展是一个 npm 包，不在这里安装」提示指向的说明。设计取舍见 [plugins.md](./plugins.md) §8 与 §13；协议见 [extension-protocol.md](./extension-protocol.md) §9；SDK 用法见 [extensions/sdk-local/README.md](../extensions/sdk-local/README.md)。

## 1. 是什么

端侧扩展是跑在**员工自己电脑上**的扩展：一个 npm 包，起一个本机 MCP 服务（stdio），Agent（DSH、Claude Code、Codex）直接调它，用员工自己的登录态访问那些没有接口、只能在内网访问、只认员工身份的老系统。

它和服务扩展的区别只在运行位置：

| | 服务扩展 | 端侧扩展 |
|---|---|---|
| 跑在哪 | 企业自己部署的 HTTP 服务 | 员工电脑上的本机进程 |
| dsh-ee 是否运行它 | 不运行，只按协议调它 | 不运行，也调不到它 |
| 身份凭据 | `ee_jwt` / `user_secret` / `shared` / `oauth` | 员工自己的接入令牌 + 本机保存的系统登录态 |
| 调用路径 | Harness → dsh-ee `/mcp` → 扩展 | Harness → 本机扩展 → 企业系统 |

dsh-ee 服务端仍然不运行第三方代码；端侧扩展的调用不经过 dsh-ee，只有「写操作确认」和「调用摘要审计」两个动作会回到 dsh-ee。

## 2. 为什么不能从「安装扩展」上传

管理后台的「安装扩展」处理的是**声明式扩展**（zip / 目录）和**服务扩展**（服务地址）。端侧扩展是一个 npm 包，没有可上传的清单包或服务地址，安装接口会以 `kind.unsupported` 拒绝，并提示发布到企业 npm 源、在能力中心里按插件下发。

正确路径：

1. 用端侧 TypeScript SDK 写扩展，构建出 npm 包。
2. 发布到企业 npm 源（按企业作用域命名，见 [extensions/sdk-local/README.md](../extensions/sdk-local/README.md)）。
3. 管理员在能力中心按插件预载/审核、设置可见范围，员工侧按下发方式安装。
4. DSH 由接入插件写入本机 MCP 配置；其他 Harness 由适配器生成配置片段。安装、升级、签名策略与服务扩展一致，见 [plugins.md](./plugins.md) §9。

## 3. SDK 替你做了什么

SDK 包：[`extensions/sdk-local/README.md`](../extensions/sdk-local/README.md)，npm 包名 `@dsh-ee/extension-local`。

- **本机 MCP 服务**（stdio）：`initialize`、`ping`、`tools/list`、`tools/call`。
- **写操作确认**：没标 `readOnly` 的工具，执行前先调 dsh-ee 的 `POST /api/local/confirm`；员工在网页上同意、同样的参数再调一次才执行，一次同意只能用一次。
- **审计回报**：每次调用后把扩展、版本、工具、耗时、成败报给 `POST /api/local/audit`，不带参数和返回内容。
- **错误码**：`throw new ExtError("authorize" | "forbidden" | "unavailable" | "invalid" | …)`，SDK 转成带 `code` 的工具结果；内部异常不会把堆栈交给模型。
- **`webSignIn`**：弹出系统自己的登录页，员工照常登录（含扫码、短信），等指定的 cookie 出现后存在本机（`~/.dsh-ee/local/<扩展>/cookies.json`，权限 600）。

## 4. 环境变量

| 变量 | 作用 |
|---|---|
| `DSH_EE_URL` | dsh-ee 的地址 |
| `DSH_EE_TOKEN` | 员工的接入令牌（网页「我的令牌」里建） |
| `DSH_EE_HARNESS` | 调用方：`dsh` / `claude-code` / `codex` |
| `DSH_EE_LOCAL_HOME` | cookie 与本地状态存在哪，默认 `~/.dsh-ee/local` |

令牌按最小 scope 发放；不要把令牌和 cookie 写进仓库、日志或返回结果。

## 5. 协议要点

端侧扩展复用服务扩展协议（[extension-protocol.md](./extension-protocol.md#9-端侧扩展)），外加两个本机调用：

| 接口 | 请求 | 响应 |
|---|---|---|
| `POST /api/local/confirm` | `{"extension","version","tool","args","harness"}` | `{"approved": true}`；或 `{"approved": false,"id","url","message"}`；员工拒绝过的带 `rejected: true` |
| `POST /api/local/audit` | `{"extension","version","tool","ok","duration_ms","code","harness"}` | 204 |

- `confirm` 的确认和服务端写操作确认是同一张表、同一个确认页。
- 管理员把写操作确认设成 `non_dsh` 时，`harness` 为 `dsh` 的调用不用再确认（DSH 自己有审批卡）。
- 两个接口都用员工自己的接入令牌鉴权，不走扩展的 `ee_jwt`。

## 6. 审计与「弱保证」

- 本机调用不经过服务端，审计是**扩展主动回报**的摘要（扩展、版本、工具、耗时、成败，不含返回内容）。
- 本机进程可以不问、不报；SDK 让正常写出来的扩展默认就问、就报。这个边界写在 [plugins.md](./plugins.md#13-已定和待定) 的待定项里。
- 要求「所有调用都必须经过服务端确认/审计」的企业，不应依赖端侧扩展，应只使用声明式和服务扩展。

## 7. 模板与示例

| 资源 | 位置 |
|---|---|
| 端侧老系统模板（`webSignIn` + cookie + 写操作） | [`extensions/official/legacy-local`](../extensions/official/legacy-local) |
| Claude Code 适配器 | [`extensions/official/claude-code`](../extensions/official/claude-code) |
| SDK 命令与 API | [`extensions/sdk-local/README.md`](../extensions/sdk-local/README.md) |

## 8. 相关文档

- 扩展总览与选型：[plugins.md](./plugins.md) §4、§8
- 扩展协议：[extension-protocol.md](./extension-protocol.md)
- 凭据、可见范围与访问规则：[access-control.md](./access-control.md)
- 让 Agent 完成端侧扩展开发：[agent-dev.md](./agent-dev.md)
- 来源与许可：仓库根 [NOTICE](../NOTICE)、[docs/plugin-dev/SOURCES.md](./plugin-dev/SOURCES.md)
