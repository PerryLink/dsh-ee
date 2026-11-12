# 扩展作者手册：从清单到上线

> 面向写 dsh-ee 扩展的人（以及替他们动手的 Agent）。本文只写**已经能用的做法和明确的边界**；扩展体系的设计、取舍、分批见 [plugins.md](./plugins.md)，协议细节见 [extension-protocol.md](./extension-protocol.md)，让 Agent 全程开发扩展的设计见 [agent-dev.md](./agent-dev.md)。
>
> 先分清两条生态：本文的**扩展**（extension，`dsh-ee.extension.json`）是把企业系统接进 dsh-ee；**DSH 插件**（cordis npm 包）是另一条生态，发布到企业 npm 源/能力中心，手册见 [plugin-authors.md](./plugin-authors.md)。两者的清单、检查器和发布流程都不同，别混用。

## 1. 先选形式

按「对方系统长什么样、要不要写代码、能不能从服务端访问到」判断：

```
对方系统有 REST 接口，且有（或能导出）OpenAPI 文档？
├── 是 ──→ 声明式（declarative）：只有清单 + 接口文档，不写代码、不部署
└── 不够/没有 OpenAPI
    ├── 需要多步调用、加工返回、知识源、回调、非标准 OAuth ──→ 服务扩展（service）
    └── 系统只在员工电脑/内网能访问，要用员工的登录态，或要模拟浏览器 ──→ 端侧扩展（local）
飞书/钉钉这类大多数企业都要接的系统 ──→ 内置（builtin，写在 dsh-ee 里，不对外）
```

| 形式 | 运行位置 | 交付物 | 适合 |
|---|---|---|---|
| 声明式 | dsh-ee 进程内自己发 HTTP | 一个目录：清单 + `openapi.yaml`（可选 `apps/*.yaml`、图标、README） | 有 OpenAPI 文档的内部系统；覆盖大多数「给内部接口包一层」的需求 |
| 服务扩展 | 企业自己部署的 HTTP 服务 | 代码仓库或镜像，服务地址由管理员安装时填 | 需要写逻辑；知识源、回调、非标准 OAuth、多步调用 |
| 端侧扩展 | 员工电脑上的 npm 包 | npm 包，经企业 npm 源下发 | 只能用员工本机登录态、只能内网、要模拟浏览器 |
| 内置 | dsh-ee 进程内 Go 代码 | 跟随 dsh-ee 升级 | 飞书、钉钉这类多数企业都要的（由 dsh-ee 维护者写，作者不需要选） |

服务端的三类（声明式、服务、内置）对 Harness 的可见面没有区别：都通过 dsh-ee 的 `/mcp` 暴露成 `连接名__工具名`。端侧扩展不同：它在本机由 Agent 直接通过 stdio MCP 调，不经过 dsh-ee 的 `/mcp`。拿不准时先试声明式——大多数有接口文档的系统到这里就够了；声明式做不了的（多步、加工、知识源、回调）再升级到服务扩展。

## 2. 选凭据

`credential.mode` 决定员工调用时 dsh-ee 往请求里放什么。判断顺序：

1. 对方能验 JWKS/JWT → **`ee_jwt`**（首选）。dsh-ee 现签一个 5 分钟有效的 RS256 JWT，`sub` 是当前员工；`aud` 的规则按形式不同（见下面「几个容易踩的点」第一条），对方验签就知道是谁。
2. 每个员工在源系统里有自己的 API token、必须按人调用 → **`user_secret`**。员工在连接页面的「我的凭据」里自己填，按人加密存放。
3. 只有一份公共凭据（只读报表、公共数据、知识同步、回调）→ **`shared`**。管理员配，可以存多份（生产/测试、不同子公司），当前按连接选用（按应用指定还没做）。
4. SaaS 走标准 OAuth（飞书、钉钉、Confluence 这类）→ **`oauth`**。员工在「我的凭据」里授权一次，dsh-ee 按人保存令牌、到期前刷新；Refresh token 和 client secret 留在 dsh-ee。

| 模式 | 调用时对方收到 | 谁填 | 注意 |
|---|---|---|---|
| `ee_jwt` | `Authorization: Bearer <短期 JWT>`（声明式可用清单 `credential.header` 换头；服务扩展固定 Authorization） | 不需要人填 | 对方要实现 JWKS 验签，并且必须核对 `aud`；`aud` 怎么取见下一条 |
| `user_secret` | 员工自己的凭据 | 员工（连接页面的「我的凭据」） | 用 `credential.hint` 告诉员工去哪里拿 |
| `shared` | 管理员配的共享凭据 | 管理员 | 源系统分不清是谁，适合只读公共数据 |
| `oauth` | 只给 access token | 员工授权 + 管理员填 client_id/secret | 标准流程清单里写 `oauth`；不标准流程由服务扩展实现 `oauth/*` 约定接口 |

几个容易踩的点：

- **`ee_jwt` 的 `aud` 按形式取不同的值，对方必须核对**：
  - **服务扩展**：每次调用都带 `ee_jwt`，`aud` **固定是清单的 `id`**（Go SDK 默认按这个值验）；清单里的 `credential.audience` 对服务扩展不生效，不要靠它改 `aud`。
  - **声明式扩展**：`aud` 用清单里的 `credential.audience`，不写就用连接标识（安装时可以改的连接名）。
- **服务扩展会原样拿到 `user_secret` / `shared`**：这两类凭据没有短期形式，安装时 dsh-ee 会把「会交给扩展什么」明确展示给管理员。能换成 `ee_jwt` 或 `oauth` 就不要用这两类。
- **可以同时要两份**：清单 `credential.shared` 声明一份共享凭据（比如服务账号 API Key）放在另一个请求头，和员工身份并存；两份不能放在同一个头里。
- **凭据不落盘、不打日志、不放进返回结果**：`extension test` 会用假凭据检查有没有泄漏。
- 清单里不要给 `secret` 类型的配置写默认值（`config.secret_default` 检查会报）。

## 3. 工具契约：readOnly / fill / keep

### 3.1 `readOnly`：只在确定只读时标

- **只有确实不改数据的工具才标 `"readOnly": true`。没标的一律当写操作**：调用前要员工确认，装好后默认不启用。漏标的代价是多确认一次，不会少确认。
- 声明式扩展里工具来自 OpenAPI 的 `operationId`；清单里没列出的接口按接口文档默认处理（只有 GET 算只读）。写数据的接口不要标成只读。
- `dsh-ee extension check` 会反过来提醒：标了只读但名字像写操作（`submit_`、`create_`、`delete_` 等开头）或对应接口不是 GET 的工具，报 `tool.readonly_suspect`。如果确实只读，用清单的 `checkIgnore` 写清楚理由。

### 3.2 `fill`：参数由谁填

| `fill` | 谁填 | 会不会出现在给模型的工具定义里 | 典型用途 |
|---|---|---|---|
| `model`（默认） | 模型 | 会 | 查询条件、用户当场说的内容 |
| `app` | 搭应用的人，在应用配置里固定 | **不会**，模型看不到也改不了 | 每个应用不同的值：项目编号、查询范围 |
| `admin` | 管理员安装扩展时统一设置 | **不会**；安装时出现在配置表单里 | 整个企业只有一个值：公司代码、租户 |

`app` / `admin` 参数要写 `label`（给填的人看）；`default` 是预先填好的值，填的人可以改，模型和员工改不了。接口文档里这些参数照原样留着（`required` 不用改），dsh-ee 会在调用时补上、并且不把它们放进给模型的工具定义。

### 3.3 `keep`：只保留要返回的字段

返回给模型的字段越多，模型越容易被无关字段带偏，也越费上下文。`keep` 只保留列出的字段：

- 返回对象：写第一层字段名，如 `["id", "title", "status"]`。
- 返回列表包了一层（`{"items": [...], "total": 1}`）：写 `items.id`、`items.title`，第一层要留的 `total` 也要写上。
- 到第二层为止；接口文档没写返回里有哪些字段时不要猜。

### 3.4 其他清单约定

- `id` 是 `作者/名字`（小写字母、数字、连字符），版本号严格 semver；`dsh-ee extension init` 会校验。
- 说明文字可以写字符串，也可以写 `{"zh": …, "en": …}`。
- 工具出错时带约定的 `code`（`authorize` / `setup` / `forbidden` / `unavailable` / `not_found` / `invalid` / `not_implemented` / `unauthenticated`），dsh-ee 会转成模型看得懂的结果。详见 [extension-protocol.md](./extension-protocol.md) §6。
- 完整字段和每个字段的说明：`dsh-ee schema get manifest`；检查项列表：`dsh-ee schema get errors`。仓库副本在 [extensions/schemas/manifest.schema.json](../extensions/schemas/manifest.schema.json)。

## 4. 本地开发循环

扩展的判定只有一条：`check` 和 `test` 全部通过。常用命令（`dsh-ee` 是主仓库构建出的二进制）：

| 命令 | 作用 | 说明 |
|---|---|---|
| `dsh-ee extension init <目录> --id 作者/名字 [--kind declarative\|service]` | 生成一个能改的扩展模板（带 `AGENTS.md`） | 目录要为空；端侧扩展没有脚手架，复制 [legacy-local](../extensions/official/legacy-local/) |
| `dsh-ee extension check <目录> [--json]` | 静态检查：清单、接口文档、清单和 MCP 工具是否一致、只读标记是否可疑、知识源接口是否完整 | **只收目录**；有 error 退出码为 1；`--json` 每条带 `code`/`severity`/`where`/`message`/`fix` |
| `dsh-ee extension test <目录\|地址> [--run "启动命令"] [--json]` | 把扩展按协议逐项测：清单、验签、MCP、约定接口、错误码、凭据不外泄，再跑 `tests/*.yaml` 场景测试 | 给目录时先做 `check`；服务扩展目录要 `--run` 把服务跑起来；给地址时测一个已经在跑的服务；`--write` 会真的调写工具、可能改数据 |
| `dsh-ee extension dev [--listen 地址]` | 起一个**模拟的 dsh-ee**：预置员工（`zhangsan`、`lisi`、`wangwu`、`admin`）、JWKS、假凭据，不需要数据库 | 另开终端启动扩展，把 `DSH_EE_URL` 指向它打印的地址 |
| `dsh-ee extension call <地址> <工具> [--as 员工] [--args JSON]` | 以模拟环境里某位员工的身份调一次扩展的工具 | 扩展启动时的 `DSH_EE_URL` 要指向模拟环境 |
| `dsh-ee extension pack <目录> [-o 文件]` | 声明式扩展打成 zip，算出校验和 | 只有声明式扩展要打包；服务扩展交付代码仓库或镜像 |
| `dsh-ee extension report <目录\|地址>` | 生成给管理员看的交付说明（Markdown） | 先补上测试结果和权限理由再交 |
| `dsh-ee schema list` / `dsh-ee schema get <名字>` | 查清单、检查项等机器能读的规格 | 编译在二进制里，离线可用 |

声明式扩展没有代码，`check` 通过就是合格；`test` 对它也只做 `check`。服务扩展还要跑 `test` 的协议一致性测试和场景测试。场景测试示例：

```yaml
# tests/basic.yaml
- name: 本部门员工能看到待办
  as: zhangsan
  call: list_forms
  args: { status: pending }
  expect:
    ok: true
    count: { gte: 1 }
- name: 没授权时提示授权
  credential: none
  call: list_forms
  expect:
    error: authorize
```

端侧扩展的命令在 `@dsh-ee/extension-local` 自己的 CLI 里（`dsh-ee-local manifest / tools / call`），开发时用 `node --watch` 重启；写操作确认、审计回报由 SDK 负责。见 [extensions/sdk-local/README.md](../extensions/sdk-local/README.md)。

命令里**没有** `sign` / `verify`：签名是设计，还没实现（见 [plugins.md](./plugins.md) §9.3）。不要按文档里的签名命令写 CI。

## 5. 交付和上线

### 5.1 交付物

- 声明式扩展：`dsh-ee extension pack` 打出的 zip（清单 + 接口文档 + 图标/应用/README），或者同时选中清单和接口文档；`report` 生成的交付说明补上测试结果。
- 服务扩展：代码仓库或镜像，外加 `report` 的交付说明；管理员装的时候填部署好的服务地址。
- 端侧扩展：npm 包，经企业 npm 源下发；`dsh-ee` 服务端不接受把端侧扩展当普通扩展安装。
- 交付说明（`report`）会列出：身份和版本、清单摘要、请求会发往哪些地址、用哪种凭据和会交给扩展什么、工具及读写标记、申请的反向权限、附带的扩展字段、安装时要填什么。**申请反向权限（`kb`/`approval`/`audit`/`notify`/`endpoint`）时补一句为什么需要**。

### 5.2 上线：管理员看到的安装计划

安装永远是管理员在网页「企业连接 → 安装扩展」里做的，作者不能自己装到生产。安装计划会展示：身份和版本、请求发往哪些地址、凭据模式与凭据交接、申请的反向权限、工具和写操作、扩展会带哪些字段、管理员要填的配置。管理员确认后才装成连接并记审计。

声明式扩展升级 = 再装一次同一个 `id`；服务扩展额外多一次健康检查。以下变化属于**敏感变更**，必须管理员再次确认（`confirm`）才能生效：

- 服务地址变了；
- 新申请了反向权限；
- 版本号没变但清单/接口文档内容变了；
- 凭据模式变了；
- 请求可以发往新的地址。

新版本新增的写操作工具、以及「从只读变成写」的工具，升级后默认停用，等管理员确认后再打开。

### 5.3 升级和回滚边界

- 服务扩展由企业自己部署和升级；dsh-ee 在定时健康检查里重新取清单，发现变化只记录新版本，**新增的部分先不启用**，等管理员确认。
- 声明式升级后不会保留上一版，也没有一键回滚入口（设计，未实现）。升级前请自己留好旧版 zip 或仓库 tag。
- 签名、自动升级策略、企业目录都还没实现（[plugins.md](./plugins.md) §12「有人要才做」）。在安装页看不到签名状态，不要对外声称「已签名」或「已审核」。
- 端侧扩展的包签名、自动升级也还没实现；按企业 npm 源和员工端的安装策略下发。

## 6. 边界和判定器

- **dsh-ee 不在服务端运行任何第三方代码**。声明式扩展由 dsh-ee 发请求，服务扩展在企业自己的服务里，端侧扩展在员工电脑上；写扩展不需要申请在 dsh-ee 进程里跑代码。
- 扩展的判定器是 `dsh-ee extension check` + `extension test`；**不要**拿 dsh-standards 的 ENTRY/FLOW/BOOST 去套扩展，它不是 cordis 包，那套检查器的对象是 DSH 插件（见 [plugin-quality.md](./plugin-quality.md)）。
- 端侧扩展是**弱保证**：本机进程可以不问、不报。要求所有调用都经过服务端、强审计的企业，只用声明式和服务扩展。
- 作者不要在代码里写死凭据，不要把 `admin`/`shared` 凭据打进仓库；测试用假凭据，`extension test` 会检查泄漏。

## 7. 相关文档

- 扩展的设计、取舍和批次：[plugins.md](./plugins.md)
- 服务/端侧扩展协议：[extension-protocol.md](./extension-protocol.md)
- 让 Agent 从头开发扩展：[agent-dev.md](./agent-dev.md)
- 非 DSH Harness 怎么接 dsh-ee：[harness-integration.md](./harness-integration.md)
- 示例：[声明式示例](../extensions/examples/demo-declarative/README.md)、[服务扩展示例（Go）](../extensions/examples/demo-extension/)、[端侧模板](../extensions/official/legacy-local/)
- Go SDK：[extensions/sdk-go](../extensions/sdk-go/)；端侧 TS SDK：[extensions/sdk-local/README.md](../extensions/sdk-local/README.md)
- 给 Agent 的技能：[dsh-ee-extension-dev/SKILL.md](../extensions/skills/dsh-ee-extension-dev/SKILL.md)
