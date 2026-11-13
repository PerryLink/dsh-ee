# 扩展错误码与排障

> 面向扩展作者和接入管理员：工具调不通、扩展装不上、健康检查失败时先查这里。协议本身见 [extension-protocol.md](../extension-protocol.md)，一页速查见 [README.md](./README.md)。
>
> 本文只写当前版本实际会出现的错误；设计里才有、还没实现的错误契约不列。仓库级错误面索引见 [../errors.md](../errors.md)，本文只讲扩展协议层。

## 1. 三类错误面

| 出错的层 | 长什么样 | 谁处理 |
|---|---|---|
| 工具调用（`tools/call`） | `result.isError: true`，`result._meta["dsh-ee/error"].code` 是扩展给的错误码 | dsh-ee 按码换成给模型/员工看的中文文案 |
| 约定接口（`POST /ee/<名字>`） | HTTP 非 2xx，响应体是 `{"error": {"code": "...", "message": "..."}}` | dsh-ee 按码决定跳过 / 让员工授权 / 报错 |
| 扩展之外的调用面 | `/mcp` 的 JSON-RPC 错误（`-32700` 解析失败、`-32601` 不支持的方法、`-32602` 参数或工具不存在）；HTTP 401 / 403；`/api/*` 的 `{"error": "中文"}` | 调用方（Agent / 脚本） |

扩展代码不要用 5xx 表示业务失败：约定接口的业务错误用 §2 的错误码 + 合适的 HTTP 状态码（SDK 会按错误码给状态码），工具错误用带 `_meta` 的 `isError` 结果。

## 2. 扩展返回的错误码

清单里 `permissions` / `credential` 等写错属于静态问题，见 §4；下面是**运行时**扩展可以返回的错误码（Go SDK 常量见 `extensions/sdk-go/errors.go`，端侧 TS SDK 见 `extensions/sdk-local/src/errors.ts`）：

| code | 什么时候用 | dsh-ee 怎么处理 |
|---|---|---|
| `authorize` | 员工还没授权，或授权过期 | 返回「请先授权」+ 连接页链接（`/assets/<id>`），让员工本人完成授权 |
| `setup` | 管理员没配好（配置、共享凭据、服务地址不对） | 告诉员工联系管理员，不重试 |
| `forbidden` | 源系统判定这位员工没权限 | 原样告诉员工，不重试，也不换工具绕过 |
| `unavailable` | 源系统暂时不可用 | 联邦检索时跳过这个来源；工具调用提示可以稍后再试 |
| `not_found` | 文档 id / 记录不存在 | 同步时跳过这一篇 |
| `invalid` | 参数或配置不对 | 原样把错误信息交给调用方 |
| `not_implemented` | 没实现这个约定接口 | 当作不支持（联邦来源不支持某个接口时会走这条） |
| `unauthenticated` | 扩展没验过 dsh-ee 的 `ee_jwt` | 这是链路问题（扩展侧 `DSH_EE_URL`、时钟或公钥缓存），dsh-ee 按普通失败交给调用方；看扩展自己的日志 |
| `internal` | 扩展内部错误（SDK 对未分类错误的默认码） | 按普通失败交给调用方；不要自动重试 |

**连续失败熔断**：约定接口连续失败 5 次，扩展熔断 30 秒，期间的调用直接返回「这个扩展连续出错，已暂时停用」；只影响这一个扩展。

## 3. 安装 / 升级阶段的错误

| 现象 | 原因 | 处理 |
|---|---|---|
| `GET .../.well-known/dsh-ee-extension.json` 取不到（HTTP 非 200、超时、返回的不是清单） | 服务地址不对、服务没起来、地址前面多写了 `/mcp` | 确认地址是服务根地址（`http(s)://host:port`，不带路径/查询）；先 `curl` 清单地址 |
| 清单超过 1 MB | 附带了不该有的内容 | 精简清单，接口文档放 `openapi.yaml` |
| 安装时 409「这次升级有需要确认的变化」 | 服务地址变了、扩展新申请了反向权限、同版本内容变了、凭据模式变了、允许地址变了 | 管理员核对计划后带 `confirm:true` 重装；不确定就放弃这次升级 |
| 安装返回 `warnings` | 清单里的工具和 `/mcp` 的 `tools/list` 对不上，或只读标记不一致 | 按提示改清单或 MCP；以清单为准，MCP 多出来的工具不会收进来 |
| `fields_error` | 扩展带的扩展字段不适合当前配置 | 按返回信息修字段定义（对象、类型、access 只允许 select/tags/level） |
| 健康检查 `ok:false` | 扩展自己返回 `{"ok": false}` 或 `health` 调不通 | 看扩展日志；`health` 至少返回 `{"ok": true}`（`version` 可选） |
| 健康检查 `changed:true` | 扩展在跑的服务上换了清单/版本 | 确认新版本后重新安装；没确认前 dsh-ee 继续用装机时的清单 |
| 安装后工具没出现 | 清单里没声明这个工具、工具被管理员关掉、或员工没有这个连接的授权 | 看连接页的工具开关与授权范围；服务扩展再跑一次 `extension test` |

## 4. 静态检查

`dsh-ee extension check <目录> [--json]` 做静态检查（不运行扩展代码）。先看离线规格：

```bash
dsh-ee schema list                 # 有哪些规格
dsh-ee schema get manifest         # 清单规格
dsh-ee schema get errors           # 全部检查项（离线，62 条）
dsh-ee extension check <目录> --json
```

退出码：`0` 通过、`1` 有 error 项、`2` 用法错误。`--json` 里每条带 `code`、`where`（出问题的清单位置）、`fix`（怎么改）。

常见检查项：

| code | 典型原因 |
|---|---|
| `manifest.missing` / `manifest.invalid_json` | 目录里没有 `dsh-ee.extension.json`，或不是合法 JSON |
| `kind.invalid` / `kind.field_mismatch` | `kind` 不是 declarative/service/local，或写了别的形态才有的字段 |
| `id.invalid` / `version.invalid` | `id` 不是「作者/名字」，或 `version` 不是严格语义化版本 |
| `openapi.missing` / `openapi.invalid` / `servers.mismatch` | 声明式扩展的接口文档缺失/不合法，或文档里的 servers 不在清单允许列表 |
| `service.url_invalid` | 服务扩展没写服务地址，或地址形状不对 |
| `credential.header_conflict` | 共享凭据和员工身份放进了同一个请求头 |
| `credential.claims_invalid` | `ee_jwt` 要带的员工信息不在 `dept` / `roles` / `idp_sub` 里 |
| `config.secret_default` | `secret` 类型的配置项在清单里写了默认值 |
| `permissions.invalid` | 申请了不存在的反向权限（只有 `kb` / `approval` / `audit` / `notify` / `endpoint`） |
| `capabilities.invalid` | 写了不存在的能力，或声明式扩展声明了做不了的能力 |
| `tool.readonly_suspect` | 工具标了 `readOnly`，但名字像写操作或接口方法不是 GET；确认无误可用 `checkIgnore` 写理由忽略 |
| `tool.readonly_unmarked` | GET 接口对应的工具没标 `readOnly`（默认会被当写操作） |
| `tool.unknown` / `param.unknown` / `param.fill_invalid` | 清单里的工具/参数和接口文档对不上，或 `fill` 不是 `model` / `app` / `admin` |
| `keep.wrapped` | 返回的列表包了一层，`keep` 直接写每一项的字段会取空 |
| `fields.invalid` | 扩展字段的对象/类型/`access` 写错（access 只允许 select / tags / level） |
| `protocol.unsupported` | 清单 `protocol` 不是当前支持的 `1` |
| `ignore.invalid` | `checkIgnore` 里的检查项不存在、不能忽略，或没写理由 |

## 5. 协议测试

`dsh-ee extension test` 做协议一致性测试：先 `check`，再把扩展跑起来按协议逐项测。

```bash
# 给目录：先 check；服务扩展加 --run 让 dsh-ee 把它跑起来再测
dsh-ee extension test <目录> --run "go run ." [--credential 值] [--config JSON] [--write] [--json]
# 给地址：测一个已经在跑、且 DSH_EE_URL 指向模拟环境（或真实 dsh-ee）的服务扩展
dsh-ee extension test http://127.0.0.1:9000 [--json]
```

退出码：`0` 全部通过、`1` 有失败、`2` 用法错误。默认只读、不真的调写工具；`--write` 才会连写工具也真调一次。

常见失败项（共 19 项）：

| code | 说明 | 怎么改 |
|---|---|---|
| `test.manifest_unreachable` / `test.manifest_invalid` | 取不到清单 / 清单没通过静态检查 | 先修 §4 |
| `test.tools_mismatch` / `test.readonly_mismatch` | 清单工具与 MCP 不一致，或只读标记不一致 | 两边对齐；以清单为准 |
| `test.fill_leaked` | `fill: app/admin` 的参数出现在给模型的工具定义里 | 参数不要暴露给模型；SDK 会自动处理 |
| `test.capability_mismatch` | 声明了能力但没实现，或实现了没声明 | 去掉声明或补实现（SDK 自动生成 capabilities） |
| `test.auth_not_enforced` | 不带令牌、过期、`aud`/签名不对的请求没被拒绝 | 每个请求都要验 `ee_jwt`；确认没设 `DSH_EE_INSECURE=1` |
| `test.tool_crashed` / `test.tool_bad_args` / `test.error_code_missing` | 工具 5xx/断连/返回不是合法 MCP、坏参数没报错、报错没带码 | 统一用 SDK 的错误码返回 |
| `test.authorize_expected` | 凭据模式要员工凭据，没带凭据时没返回 `authorize` | 处理函数里发现凭据为空就 `sdk.Errorf(sdk.CodeAuthorize, …)` |
| `test.kb_changes` / `test.kb_document` / `test.kb_browse` / `test.kb_search` | 知识源增量翻页、取正文、目录、检索结构不对 | 按 [extension-protocol.md](../extension-protocol.md) §5 的 cursor / 结构改 |
| `test.credential_leaked` | 凭据出现在响应或日志里（canary 检查） | 不落盘、不打日志、不放返回结果 |
| `test.unstable` | 并发或重复调用出错 | 处理函数会被并发调用，共享状态要加锁 |
| `test.scenario_failed` / `test.scenario_invalid` | 目录里 `tests/*.yaml` 场景测试失败 / 写法不对 | 看 `--json` 的 `fix`；场景格式见协议文档 §8 |

场景测试的固定环境：模拟 dsh-ee 里预置了 `zhangsan`（默认）、`lisi`、`wangwu`、`admin` 四位员工；`credential: none` 表示不带员工凭据，`call` 是工具名，`expect` 支持 `ok`、`error`、`contains`、`count`（`eq` / `gte` / `lte`）。

## 6. 运行期排查

| 现象 | 先查什么 |
|---|---|
| 工具调用返回 `authorize` | 员工是否在连接页（`/assets/<id>`）完成授权；OAuth 令牌是否已过期 |
| 工具调用返回 `setup` | 扩展配置、共享凭据、服务地址是否完整；管理员在连接页重新保存一次会触发 `validate` |
| 工具调用返回 `unavailable` | 源系统是否可用；是否刚触发熔断（连续 5 次失败后 30 秒）；看扩展日志 |
| 扩展工具不在 `tools/list` | 清单里声明了吗；连接的可用范围/授权是否覆盖这位员工；管理员的工具开关 |
| `/mcp` 返回 403「你的账号没有使用工作台的权限」 | 令牌是否有 `connections` scope；账号是否有工作台权限 |
| `/mcp` 返回 401 | 令牌无效/过期/吊销；重新生成令牌 |
| 服务扩展每个请求都 401 `unauthenticated` | 扩展侧 `DSH_EE_URL` 是否指向正确的 dsh-ee 地址；服务器时钟是否偏差过大（`exp`/`nbf`）；JWKS 是否缓存太久 |
| 写操作没执行，返回 `isError` 和确认链接 | 这是正常的服务端写确认：员工在网页确认后，用相同参数再调一次 |
| 回调地址打不通（404/429/413） | 扩展是否申请了 `endpoint` 权限并声明能力；地址随机串是否完整；请求体是否超 1 MB；是否超过每分钟 120 次 |
| 端侧扩展 `approved:false` | 员工还没在 `/confirm/<id>` 同意，或刚拒绝过；写确认参数不能变 |
| 知识源同步一直没内容 | 扩展是否声明 `knowledge.documents`；`kb/changes` 的 cursor 是否单调往前；是否超过 200 页/5000 篇 |
| 联邦检索结果少/标「暂时不可用」 | 扩展是否声明 `knowledge.search`；员工是否在可用范围、是否已授权；来源是否超过 3 秒 |

## 7. 边界

- 扩展不能读写 dsh-ee 的数据库，也不能调用 dsh-ee 的管理接口。清单里的 `permissions`（`kb` / `approval` / `audit` / `notify` / `endpoint`）是安装时给管理员看的申请项；当前真正生效的是 `endpoint`（决定是否生成回调地址）和端侧扩展用员工令牌调用的 `/api/local/confirm`、`/api/local/audit`。扩展反向调 dsh-ee 的完整权限校验仍在设计阶段，不要依赖。
- 凭据只在当次请求头里有效，扩展不能落盘、打日志、放进返回结果；`test.credential_leaked` 会用 canary 检查。
- 没有员工身份的调用（健康检查、配置检查、知识同步、回调转发）是 `sub: "system"`，扩展不要把它当成某位员工。
