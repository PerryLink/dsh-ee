---
name: dsh-ee-extension-dev
description: 给 dsh-ee 写扩展，把企业的内部系统接进来，让员工的 Agent 能以本人的身份调用。当用户要「把某个系统接进 dsh-ee」「写一个 dsh-ee 扩展」「给内部接口包一层让 Agent 能用」时使用。覆盖声明式扩展（有 OpenAPI 文档、不写代码）、Go 服务扩展（要写逻辑、做知识源、接回调、OAuth）和 TypeScript 端侧扩展（只能用员工本机登录态的老系统）。
---

# 写 dsh-ee 扩展

dsh-ee 的扩展把企业自己的系统接进来，变成员工的 Agent 能调用的工具。这份技能带你把一个扩展从头做到通过检查。

**做完的唯一标准：`dsh-ee extension check <目录> --json` 的 `ok` 为 `true`，而且结果里没有你解释不了的条目；服务扩展还要 `dsh-ee extension test` 全部通过。** 不要自己宣布做完。

`dsh-ee` 不在 PATH 里时，命令里写它的完整路径。

## 1. 先判断用哪种扩展

先弄清三件事，再对照下表：对方系统有没有 OpenAPI 文档（或能不能导出）；调用时要不要区分是哪位员工；dsh-ee 的服务器能不能访问到它。需求里写了就照需求，没写再问用户。

| 情况 | 用 |
|---|---|
| 有 REST 接口，有或者能导出 OpenAPI 3.x 文档 | **声明式**（§2） |
| 要多步调用、加工返回结果、做知识源、接回调、OAuth 流程不标准 | **服务扩展**，用 Go SDK（§2a） |
| 只能在员工电脑上访问、要员工的登录态、要模拟浏览器 | **端侧扩展**，用 TypeScript SDK（§2b） |

能用声明式就用声明式：不写代码、不用部署。声明式做不了的事不要硬做，换服务扩展，或告诉用户哪一部分做不了、为什么。

## 2. 声明式扩展的流程

```
复制示例 → 换接口文档 → 写清单 → check → 按结果改 → check 通过 → 写交付说明
```

1. **复制示例**：`extensions/examples/demo-declarative/`（两个文件：`dsh-ee.extension.json`、`openapi.yaml`）。
2. **换成对方系统的接口文档**。JSON 和 YAML 都行，对方给的是什么格式就用什么格式，清单的 `openapi` 字段写它的文件名。只保留要开放给 Agent 的接口，其余的从文档里删掉：需求明确不让开放的要删；**需求没提到的也不开放**，在交付说明里列出来，由人决定要不要加。每个接口都要有 `operationId`，它就是工具名（字母、数字、下划线）：已有的照原样用，没有的补上，风格和已有的保持一致。
3. **写清单**。字段的完整说明不要凭记忆写，查规格：`dsh-ee schema get manifest`。必须想清楚的四件事见 §3。
4. **检查**：`dsh-ee extension check <目录> --json`。每条结果有 `where`（文件和位置）和 `fix`（具体改法），照着改，再跑一遍，直到 `ok` 为 `true`。检查项的含义：`dsh-ee schema get errors`。
5. **写交付说明**（§5），连同扩展目录一起交给管理员。安装由管理员做，你不装。

## 2a. 服务扩展的流程（Go）

```
init → 写场景测试 → 写代码 → test → dev 里按场景试 → report → 交付
```

1. `dsh-ee extension init <目录> --id 作者/名字 --kind service`，生成 `main.go`、`tests/basic.yaml` 和 `AGENTS.md`，照 `AGENTS.md` 做。每种能力的写法看 `extensions/examples/demo-extension`（假数据把所有能力做了一遍）。
2. **先把需求写成场景测试**（`tests/*.yaml`：谁、调什么、应该得到什么），再写代码。格式见 `docs/extension-protocol.md` §8。
3. 清单由 SDK 按注册的处理函数自动生成，不手写；`capabilities` 也是。凭据模式、只读标记、参数由谁填的规矩和声明式一样（§3）。
4. `dsh-ee extension test --run "go run ." <目录> --json`：先按协议逐项测，再跑场景测试。每条失败都有 `code`、`message`、`fix`。**全部通过才算做完。**
5. 手工试：`dsh-ee extension dev` 起模拟的 dsh-ee（不要数据库），`dsh-ee extension call <地址> <工具> --as 员工 --args JSON` 以某位员工的身份调。
6. `dsh-ee extension report <地址> > DELIVERY.md` 生成交付说明的底稿，补上测试输出和「请补充」的地方。
7. 交付的是代码仓库（或镜像）和交付说明。**部署和安装由人做**：管理员在「企业连接 → 安装扩展」里填服务地址。

规矩：调用人是 `c.User`（验过签的令牌），不要让模型通过参数指定替谁操作；出错用 `sdk.Errorf(sdk.CodeXxx, …)`；不要把 `c.Credential`、`c.Shared`、`c.Config` 里的密钥打进日志或放进返回；知识源的 cursor 要单调往前，不要用页码。

## 2b. 端侧扩展的流程（TypeScript）

只在「对方系统没有接口、只能用员工自己的登录态、只在员工电脑上访问得到」时用。

1. 复制 `extensions/official/legacy-local`（模板：员工在弹出的窗口里登录一次，之后带着他的 cookie 发 HTTP 请求）。改 `id`、`package`、系统地址和要的 cookie 名字。
2. 工具写在 `src/extension.ts` 的 `defineExtension({ tools })` 里。**只读的才标 `readOnly: true`**：没标的，SDK 执行前会先经 dsh-ee 让员工在网页上确认，确认不了就不执行。
3. 出错用 `throw new ExtError("authorize" | "forbidden" | "unavailable" | "invalid", "…")`；只把模型需要的字段返回出去。
4. `npm test`（模板带了假 OA 和假 dsh-ee 的测试）全部通过；`npm run manifest` 生成清单，再 `dsh-ee extension check <目录> --json` 通过。
5. 交付的是 npm 包和交付说明，包经企业的 npm 源下发。员工的 Agent 配置里写 `npx <包名>`，并设好 `DSH_EE_URL`、`DSH_EE_TOKEN`（Claude Code 用 `extensions/official/claude-code` 一条命令写好）。

规矩：员工的 cookie 只存在他自己的电脑上，不要发给任何别的地方；不要绕过 SDK 自己起 MCP 服务（那样就没有确认和审计）。

## 3. 清单里必须想清楚的四件事

### 请求发往哪里（`servers`）

写对方系统的接口根地址。dsh-ee 只往这里列出的地址发请求，凭据也只带给它们。不知道真实地址就问用户，不要编。

### 用谁的身份（`credential.mode`）

| 模式 | 什么时候用 |
|---|---|
| `ee_jwt` | 对方能验 JWT（自研系统，或能加一层适配）。**优先用它**：dsh-ee 为当前员工现签一个 5 分钟有效的令牌，不用保存任何人的密码。对方要核对令牌是签给谁的，就写 `audience`；对方需要部门、角色时写 `claims`（`dept` / `roles` / `idp_sub`），用不上的不要写 |
| `user_secret` | 员工各自有 API token。要写 `hint`，告诉员工去哪里生成 |
| `shared` | 只有一份公共凭据。只适合所有人都能看的只读数据：源系统分不清是谁在调 |

按人鉴权的系统不要用 `shared` 凑合。员工身份之外还要一份服务账号的 key 时，用 `credential.shared` 另放一个请求头。`shared` 模式下对方要求把 key 放在特定请求头（比如 `X-API-Key`）时，写 `"credential": { "mode": "shared", "header": "X-API-Key" }`；不写就由管理员安装时选。

对方要求放在特定的请求头里时写 `credential.header`：值会原样放进去，前面不加 `Bearer`。共享凭据的值由管理员安装时填，清单里只声明它放在哪个请求头，不用在 `config` 里再写一项。

用 `ee_jwt` 时，对方系统验签要用到这些，写进交付说明：公钥在 `<dsh-ee 的访问地址>/.well-known/jwks.json`；签发方（`iss`）是 dsh-ee 的访问地址；`aud` 是清单里的 `audience`；用户名在 `preferred_username`，邮箱在 `email`，部门在 `dept`，角色在 `roles`。dsh-ee 的访问地址你不知道就写「由管理员提供」。

### 哪些工具是只读的（`tools[].readOnly`）

- **只读的工具才标 `"readOnly": true`，没标的一律当写操作**，调用前要员工确认。
- 接口方法不是 GET、或名字像写操作的，不要标只读。`check` 会报 `tool.readonly_suspect`。
- 确实只读的例外（比如用 POST 传查询条件的搜索接口）：标 `readOnly`，并在 `checkIgnore` 里按工具名写明理由：`{"code": "tool.readonly_suspect", "tool": "<工具名>", "reason": "…"}`。之后这个工具当作只读，检查结果里会留一条 `tool.readonly_override` 的说明，把你的理由原样给装扩展的管理员看。这一条是预期的，不用消掉。**不要为了让检查通过去忽略一个真的会改数据的接口。**
- 清单里没列出来的接口，按接口文档的默认处理（只有 GET 算只读）。

### 参数由谁填（`tools[].params[].fill`）

- `model`（默认）：模型填。
- `app`：搭应用的人固定，模型看不到也改不了。用于「这个应用只能查 A 项目」这类限制。要写 `label`。
- `admin`：管理员安装扩展时统一设置，对所有人生效，模型看不到也改不了。要写 `label`。它会直接出现在安装时的配置表单里，不用在 `config` 里再写一遍。

写法：

- `tools[].params` 里**只列你要补充或限制的参数**，没列的照接口文档。
- `name` 写接口的路径参数或查询参数的名字；JSON 请求体里的字段写成 `body.<字段名>`（只到第一层）。
- 整个企业只有一个值的（公司代码、租户）用 `admin`；每个应用可以不同的（项目、范围）用 `app`。
- 「对我们来说永远是某个值」：`"fill": "admin"` 加 `"default": <那个值>`。`default` 是预先填好的值，管理员安装时可以改，模型和员工改不了。它的 JSON 类型要和参数一致：字符串加引号，数字和 `true` / `false` 不加。
- 同一个 `admin` 参数在几个工具上都要写（比如每个接口都有的公司代码），安装时只填一次，各处的 `default` 要一样。
- `app` 和 `admin` 的参数模型看不到，接口文档里的 `required` 不用改。
- 不该让模型决定的参数（范围、租户、项目）不要留给 `model`，也不要靠工具说明里写「请固定填某某」来约束。

## 4. 让模型用得好

- 接口文档里的说明写得含糊时，在 `tools[].description` 里改写：说清楚这个工具查什么、什么时候该用、返回什么。接口文档没写的事（返回结构、状态的含义）不要自己编；确实要靠推断的，写进交付说明里让人确认。接口文档没写 `responses` 也不用补。
- 参数的说明在 `tools[].params[].description` 里改写；请求体里更深一层的字段，直接改接口文档里的说明。
- 知道返回里有哪些字段、又嫌太多时，用 `keep` 只留模型需要的：写第一层的字段名；列表包了一层（`{"items": [...], "total": 1}`）时写成 `items.id`、`items.title`、`total`，直接写 `id`、`title` 会把返回清空（`check` 报 `keep.wrapped`）。接口文档没写返回结构就不要用 `keep`。
- 不需要的接口从接口文档里删掉，不要全量开放。
- 文字写中文一种就够；要公开分发的扩展，中英文都写。

## 5. 交付说明

通过检查后，写一份给管理员看的说明：

- 这个扩展做什么，提供哪些工具，**哪些是写操作**；
- 请求会发往哪些地址；
- 用哪种凭据模式，管理员（或员工）安装后要填什么、去哪里拿；
- `check` 的输出（身份、sha256、0 个错误）。sha256 就是 `--json` 输出里的 `digest`，只由清单和接口文档决定，交付说明放在同一个目录里不影响它；
- 对着 `check` 的输出核对一遍：`credential` 是不是你要的凭据用法，每个工具的 `fixed`（不由模型填的参数）和 `keep` 是不是你要的；
- 你拿不准、自己定的地方（比如 `id` 里的作者名：一般用公司或组织的简称，需求里没给就按域名取一个并写明）；
- 你做不了、留给人处理的事。

## 6. 不能做的事

- 不要把任何凭据、token、密码写进清单、接口文档或说明里。需要真实凭据时停下来问人。
- 不要申请用不上的东西（`permissions`、`claims`）。
- 不要为了通过检查而改掉事实：把写操作标成只读、把不存在的接口写进去、编造服务器地址。
- 检查没通过时不要说做完了。
