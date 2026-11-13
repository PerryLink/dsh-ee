# 在宿主页面里嵌入微应用（同站 iframe v0）

面向企业内部的宿主页面（门户、工作台、自研 Harness）用 iframe 嵌入 dsh-ee 的微应用网页版 `/a/<标识>`。

| 形态 | 现状 | 说明 |
|---|---|---|
| 同站 iframe | ✅ v0 已实现 | 同一站点（scheme + 可注册域）的宿主，例如 `harness.acme.internal` 嵌 `ee.acme.internal`；沿用现有 `SameSite=Lax` 会话，零认证改动 |
| 顶层打开 / 飞书工作台 | ✅ 已有 | 不是嵌入，作为失败兜底 |
| 跨站 iframe（不同站点） | ✅ v1 已实现（默认关闭） | 一次性 code + 内存 Bearer + postMessage 握手，见文末「跨站嵌入 v1」；需要 `DSH_EE_EMBED_CROSS_SITE=1` 且宿主后端保管每用户 `ee_at_` 令牌 |

> 同站按浏览器定义：scheme + 可注册域，**端口和子域不影响**。`http://localhost:3000` 与 `http://localhost:8080` 同站；`https://harness.acme.internal` 与 `https://ee.acme.internal` 同站；`https://harness.example.com` 与 `https://ee.example.com` **不同站**。

## 1. 前提

1. **HTTPS**：HTTPS 的宿主页面不能框 HTTP 的 dsh-ee（混合内容会被浏览器拦）。建议 dsh-ee 部署在 HTTPS 后面并设置 `DSH_EE_COOKIE_SECURE=true`。
2. **同站**：跨站 iframe 里 `SameSite=Lax` 的会话 cookie 不会被发送，页面会返回 `auth_required`。跨站场景不要用 v0，等 v1。
3. **登录状态**：同站 iframe 直接用 dsh-ee 的登录状态（用户先访问过 dsh-ee 或在顶层登录过）；跨站 iframe 不用 cookie，由宿主后端 mint 一次性 code（见文末 v1）。会话过期时 iframe 不会跳登录页，而是把 `dsh-ee:auth-required` / `dsh-ee:error` 发给宿主。

## 2. 服务端配置

`DSH_EE_EMBED_ORIGINS`：允许嵌 `iframe` 的宿主来源，逗号分隔的**完整 origin**。

```bash
# 例：同站宿主 harness.acme.internal，外加本地联调页面
DSH_EE_EMBED_ORIGINS='https://harness.acme.internal,http://localhost:5173'
```

规则（非法值会让服务启动失败，fail closed）：

- 只接受 `https://host[:port]`；`http` 只允许回环地址（`localhost` / `127.0.0.1` / `[::1]`，本地联调）。
- 必须是完整 origin：不能带路径、查询串、片段、用户名密码；不接受 `*`、`null`、`*.example.com` 这类通配符。
- 自动去空白、大小写归一化、去重；末尾一个 `/` 等价于不带路径。
- 不设置该环境变量时只允许同站嵌入（`frame-ancestors 'self'`）。
- 跨站嵌入 v1 还需要 `DSH_EE_EMBED_CROSS_SITE=1`（默认关闭，且必须同时配置来源，否则启动失败）；会话 TTL 用 `DSH_EE_EMBED_SESSION_TTL`（默认 `2h`，1m–8h）。详见文末「跨站嵌入 v1」。

**管理端覆盖（settings.embed）**：管理员可以在「系统设置 → 跨站嵌入」（`/admin/settings#settings-embed`）里追加来源、开关跨站、设置应用白名单与会话时长。有效配置 = 环境变量（只读基线）∪ 数据库设置：

| 项 | 合并规则 |
|---|---|
| `crossSite` | 数据库显式 true/false 覆盖环境变量；未设置时跟随 `DSH_EE_EMBED_CROSS_SITE` |
| `allowedOrigins` | 数据库来源**追加**在 `DSH_EE_EMBED_ORIGINS` 之后（环境变量只读，不能在网页上删改） |
| `apps` | 允许嵌入的应用 slug 列表；空 = 所有开放网页版的应用 |
| `sessionMinutes` | 非 0 时覆盖 `DSH_EE_EMBED_SESSION_TTL`（0 = 跟随部署配置） |

非法值在保存时拒绝；库里被手工改坏的设置会让服务**启动失败**（fail closed）。CSP 与 mint/exchange 读同一份有效配置，保存后立即生效（设置缓存 5 秒，保存时会主动失效）。

**作用范围**：只影响微应用网页版 `/a/<标识>` 的 CSP；其它 HTML（`/admin/*` 管理页、`/login`、`/go` 等）始终保持 `frame-ancestors 'self'`，配置过的宿主 origin **不能**框管理后台。

### 反代 / CDN 注意

- **不要**在反代再设置 `X-Frame-Options`（`ALLOW-FROM` 已废弃，和 `frame-ancestors` 叠加只会冲突）。
- **不要**在反代重复添加 `Content-Security-Policy`：多份 CSP 按交集执行，可能让这里的配置静默失效。
- 验收：`curl -sI https://ee.acme.internal/a/xxx | grep -i content-security-policy`，默认应看到 `frame-ancestors 'self'`，配置后带宿主 origin。

## 3. 宿主写法

```html
<iframe
  id="ee-frame"
  title="报名"
  src="https://ee.acme.internal/a/signup?embed=1&host=https%3A%2F%2Fharness.acme.internal&fit=auto&theme=dark"
  style="width: 100%; border: 0; min-height: 480px"
  referrerpolicy="strict-origin-when-cross-origin"
></iframe>
```

| 参数 | 必填 | 说明 |
|---|---|---|
| `embed=1` | 是 | 进入嵌入模式：隐藏返回、管理、演示身份条、CSV 导出、`dsh://` 链接；保留标题、页签、内容与表单 |
| `host` | 消息协议必填 | 宿主页面的 **origin**（`scheme://host[:port]`，`encodeURIComponent` 编码）。iframe 只把消息发到这个 origin；不传或非法时不发任何消息，页面仍可正常浏览 |
| `fit` | 否 | `fill`（默认，由宿主固定高度）或 `auto`（iframe 主动上报内容高度，见 `dsh-ee:resize`） |
| `theme` | 否 | `light` / `dark`：只作用于本次 iframe 渲染（不写 localStorage），首屏即生效 |

- `host` 必须与 `DSH_EE_EMBED_ORIGINS` 中配置的来源一致，否则 iframe 会被 CSP 直接拦下。
- 不要给 iframe 加 `sandbox`（或至少给足 `allow-scripts allow-same-origin allow-forms`）：嵌入依赖同站 cookie 与脚本。
- 移动端：窄屏（< 640px）建议宿主用顶层打开（`window.open` / 跳转）而不是 iframe；dsh-ee v0 不针对手机 iframe 做特殊布局或认证。

## 4. postMessage 协议（v1 + v2 增量）

通用约定：

- v1 消息形如 `{ v: 1, type: "dsh-ee:<name>", ... }`；v2 新增消息用 `v: 2`（老宿主按未知消息忽略，不破坏 v0/v1）。未知 `type`、不兼容的 `v` 一律忽略。
- **iframe → 宿主**：只发往 URL 参数 `host` 的 origin，绝不使用 `"*"`；`host` 缺省或非法时不发。
- **宿主 → iframe**：必须用 `iframe.contentWindow.postMessage(msg, dshEeOrigin)`；iframe 会校验 `event.origin === host origin` 且 `event.source === window.parent`，不匹配直接丢弃。
- v0 消息里不携带 cookie、token 或业务数据。

| 方向 | type | 载荷 | 说明 |
|---|---|---|---|
| iframe → 宿主 | `dsh-ee:ready` | `{ app: { slug }, theme, supports: ["connect-v1"] }` | 嵌入页一加载就发（早于认证）。同站宿主可据此收起 loading；跨站宿主据此 mint code 再发 connect |
| 宿主 → iframe | `dsh-ee:connect` | `{ code }` | 仅跨站 v1：宿主后端 mint 的一次性 code（60 秒，绑定用户+应用+宿主 origin） |
| iframe → 宿主 | `dsh-ee:session` | `{ user: { display_name }, limited, expires_in? }` | 会话可用（cookie 或 exchange 成功）后发一次；不含 token |
| iframe → 宿主 | `dsh-ee:auth-required` | `{ reason: "missing" \| "expired" }` | 没有可用会话/会话过期：宿主 mint 新 code 后重新发 connect（只重试一次） |
| iframe → 宿主 | `dsh-ee:resize` | `{ height, width }` | 仅 `fit=auto`：ResizeObserver 上报内容高度，节流到一帧，`height` 上限 4000px |
| iframe → 宿主 | `dsh-ee:error` | `{ code, message, retryable }` | 失败态，见下表；同一条错误只发一次 |
| 宿主 → iframe | `dsh-ee:theme` | `{ theme: "light" \| "dark" }` | 切换主题，只改 DOM、不写 localStorage |
| iframe → 宿主 | `dsh-ee:theme` | `{ theme }` | 确认当前实际主题（初始一次 + 每次变更） |
| iframe → 宿主 | `dsh-ee:hello` (v2) | `{ protocol: 2, supports: [...], app: { slug }, theme }` | 能力声明：`connect-v1` / `navigate` / `navigate-request` / `open-external` / `logout` / `deeplink` / `theme` / `resize`。老宿主忽略即可 |
| 宿主 → iframe | `dsh-ee:navigate` (v2) | `{ path: "/a/<slug>?tab=…" }` | 只允许本应用 `/a/<slug>`；iframe 用 `history.pushState`/前端路由切换，**不整页刷新**（刷新会丢内存 Bearer） |
| iframe → 宿主 | `dsh-ee:navigate-request` (v2) | `{ path }` | iframe 内点击同源、但不属于本应用的站内链接时发出，由宿主决定顶层打开/路由 |
| iframe → 宿主 | `dsh-ee:open-external` (v2) | `{ url }` | 只发 `https://` 或登记过的 `dsh://`；宿主确认后再打开，iframe 不直接跳 |
| 宿主 → iframe | `dsh-ee:logout` (v2) | `{}` | 只撤销本次嵌入会话（cookie 会话模式下不会退出网页登录） |
| iframe → 宿主 | `dsh-ee:logged-out` (v2) | `{ revoked: boolean }` | 撤销结果：`revoked=true` 表示 ee_emb_ 会话已在服务端删除 |
| iframe → 宿主 | `dsh-ee:deeplink` (v2) | `{ path }` | mint 时传了 `next`（站内路径），exchange 后 iframe 消费并 pushState 到该路径；此消息用于通知宿主 |

`dsh-ee:error` 的 `code`：

| code | 含义 | retryable | 宿主建议 |
|---|---|---|---|
| `auth_required` | 未登录或会话过期（iframe 内 401 / 跨站没拿到 code） | false | 跨站宿主走 v1 握手；仍失败则在**顶层**打开应用（不要在 iframe 里打开登录页） |
| `forbidden` | 应用不存在、未开放网页版，或当前用户不在可用范围（404/403） | false | 提示无权限，或引导申请权限 |
| `load_failed` | 网络不可达、5xx 等 | true | 显示重试；页面内也有「重试」按钮 |
| `embed_code_invalid` / `embed_code_expired` | 跨站握手的 code 无效/过期/已用 | 过期可重试一次 | 重新 mint 后发新的 connect；仍失败则顶层打开 |
| `embed_disabled` / `origin_not_allowed` | 服务端没开跨站嵌入，或宿主来源不在白名单 | false | 检查 `DSH_EE_EMBED_CROSS_SITE` / `DSH_EE_EMBED_ORIGINS`（mint 阶段返回，通常先于 iframe） |
| `embed_scope` | 嵌入会话访问了白名单外的接口 | false | 宿主/前端问题；嵌入会话只能看被嵌入应用的数据 |
| `app_not_allowed` | 应用不在管理端配置的嵌入白名单里 | false | 管理员在系统设置里加白名单 |
| `not_embed_session` | 对非嵌入会话调用 `/api/auth/embed/logout` | false | 只在 iframe 内调用 |

服务端错误体（mint / exchange / logout / 嵌入会话白名单）统一带可观测字段：

```json
{ "error": "宿主来源不在允许列表里", "code": "origin_not_allowed", "stage": "mint", "request_id": "9f1c2ab34d5e6f70" }
```

`X-Request-Id` 响应头与 `request_id` 一致，便于把宿主日志与服务端审计对账；`stage` 取值 `mint` / `exchange` / `logout` / `session`。

宿主侧参考代码：

```js
const EE_ORIGIN = "https://ee.acme.internal";
const frame = document.getElementById("ee-frame");

window.addEventListener("message", (event) => {
  if (event.origin !== EE_ORIGIN || event.source !== frame.contentWindow) return;
  const msg = event.data;
  if (!msg || msg.v !== 1) return;
  switch (msg.type) {
    case "dsh-ee:ready":
      // 收起 loading；可在这里根据 msg.theme 同步宿主主题
      break;
    case "dsh-ee:resize":
      // fit=auto：宿主决定是否按高度撑开，注意自己也要有上限
      frame.style.height = `${Math.min(msg.height, 4000)}px`;
      break;
    case "dsh-ee:error":
      // msg.code === "auth_required" 时在顶层登录；forbidden 提示权限；load_failed 可重试
      break;
    case "dsh-ee:theme":
      // iframe 当前主题；宿主可据此切自己的主题
      break;
  }
});

// 宿主 → iframe：等 iframe load 之后再发（或收到 ready 后发）
frame.addEventListener("load", () => {
  frame.contentWindow.postMessage({ v: 1, type: "dsh-ee:theme", theme: "dark" }, EE_ORIGIN);
});
```

建议的失败兜底：

- 监听 `dsh-ee:error`；`auth_required` 时顶层打开登录/直接 `window.open` 应用地址（去掉 `embed` 参数）后重新加载 iframe。
- 8 秒内没有 `ready` 也没有 `error` 时，显示「打开应用」按钮（iframe 被 CSP 拦时浏览器不会给宿主任何事件）。

## 5. 失败态与「在浏览器打开」

- 嵌入模式下 iframe **不会**跳转 `/login`：未登录/过期时给出 `auth_required` 和错误页，页面上有「在浏览器打开」（顶层打开，去掉嵌入参数）。
- 加载失败、无权限同样有错误页和重试入口；错误码见上表。
- 移除这些条件后，用户也可以直接用 `https://ee.acme.internal/a/<标识>` 顶层访问，行为与以前一致。

## 6. 验收清单

1. `curl -sI https://ee.acme.internal/a/<标识>` 的 CSP 含 `frame-ancestors 'self' <配置的 origin>`；`curl -sI https://ee.acme.internal/admin/...` 仍只有 `'self'`。
2. 宿主页 iframe 加载后：隐藏返回/管理/导出，标题与页签正常。
3. 宿主控制台能收到 `dsh-ee:ready`、`dsh-ee:theme`、`dsh-ee:resize`（`fit=auto` 时）；v2 宿主还能收到 `dsh-ee:hello`，`navigate`/`logout` 生效。
4. 管理端「系统设置 → 跨站嵌入」保存后：`curl -sI` 的 CSP 立即带上新来源；`/api/admin/embed-settings` 能看到 env 基线与有效值。
5. 嵌入会话 logout 后原 token 调 `/api/auth/me` 返回 401（服务端会话已删）。
4. 退出 dsh-ee 会话或换无痕窗口：iframe 显示失败态，宿主收到 `auth_required`，iframe 的地址仍停在 `/a/...`。
5. 移动端窄屏：宿主按策略顶层打开。

## 7. 跨站嵌入 v1（不同站点的 iframe）

不同站点的宿主（例如 `https://harness.example.com` 嵌 `https://ee.example.com`）里，`SameSite=Lax` 的网页会话 cookie 不会在 iframe 中发送。v1 用一条 **cookie-free** 的认证面解决：宿主后端 mint 一次性 code → iframe exchange 出只存内存的 Bearer。

**前提（硬性）**

- 宿主是**可信的企业系统**，宿主**后端**保管每个用户的 dsh-ee 令牌（`ee_at_`，含 `agent` scope）。浏览器里永远不出现 `ee_at_`。
- 服务端开启 `DSH_EE_EMBED_CROSS_SITE=1`，并在 `DSH_EE_EMBED_ORIGINS` 里列出宿主 origin（HTTPS；本地联调允许 `http://localhost[:port]` / `http://127.0.0.1[:port]` / `http://[::1][:port]`）。
- HTTPS 是硬前提；iframe 的 CSP `frame-ancestors` 由 dsh-ee 自己加，反代不要重复写 CSP/XFO。

**流程**

```
宿主(origin H)                                dsh-ee(origin E)
宿主 UI ──① 要 code ──► 宿主后端 ──② POST /api/embed/sessions ──► E
宿主 UI ◄──③ {code}（60s，绑定 H + app）────────┘   （Bearer 每用户 ee_at_）
宿主 UI ──④ <iframe src="E/a/<slug>?embed=1&host=H&…"> ──► SPA
SPA ──⑤ dsh-ee:ready{supports:["connect-v1"]} ──► 宿主 UI
宿主 UI ──⑥ dsh-ee:connect{code} ──► SPA
SPA ──⑦ POST /api/auth/embed/exchange {code}（同源，匿名）──► E
SPA ◄──⑧ {token:"ee_emb_…", limited:true, expires_in}（不 Set-Cookie）
后续 /api 请求带 Authorization: Bearer ee_emb_…（只存在 SPA 内存里）
```

**服务端接口**

| 方法/路径 | 认证 | 请求 | 成功 | 失败 code |
|---|---|---|---|---|
| `POST /api/embed/sessions` | `Authorization: Bearer ee_at_`（需 `agent` scope、human 账号） | `{app, host_origin, next?}` | `{code, expires_in:60, app, user, session_ttl}` | `embed_disabled` / `origin_not_allowed` / `app_not_found` / `app_not_web` / `forbidden` |
| `POST /api/auth/embed/exchange` | code 即凭证；必须 `Content-Type: application/json` | `{code}` | `{token:"ee_emb_…", token_type:"Bearer", expires_in, app, user, limited:true}`；**不 Set-Cookie** | `embed_code_invalid` / `embed_code_expired` / `embed_disabled` |
| `POST /api/auth/embed/logout` | `Authorization: Bearer ee_emb_…` | `{}` | `204`（删会话 + 审计） | `401` |

- code 60 秒、一次性、只存哈希在内存（**单实例**；多实例部署需要把 code 放到共享存储，见「限制」）。
- `ee_emb_` 会话默认 2 小时（`DSH_EE_EMBED_SESSION_TTL`，1m–8h），不提供 refresh；过期/被吊销后 iframe 发 `dsh-ee:auth-required {reason:"expired"}`，宿主重新 mint 一次。
- 嵌入会话 = **limited 会话**（员工权限、无 `keys.self`、无管理权限），并且只能访问路由白名单：`GET /api/auth/me`、`GET /api/auth/methods`、`GET /api/settings`、`GET /api/me/menus`、`GET /api/me/apps/{slug}/web`、`POST /api/me/apps/{slug}/source`、`/api/apps/{slug}/data…`（表读写/导出）、`POST /api/auth/embed/logout`；其余接口 403 `{"code":"embed_scope"}`。
- 审计：mint 记 `embed.session_create`；exchange 成功记 `auth.login`（`via=embed`，含 app/host_origin、limited）；logout 记 `embed.revoke`；拒绝记 `embed.deny`。不记 code/令牌明文。
- 改密码、禁用账号、解绑身份会同时删除该用户的网页会话与嵌入会话。

**宿主后端 mint 示例**

```bash
# 宿主后端（可信环境）持有当前用户的 ee_at_ 令牌；浏览器不接触它
curl -sS -X POST https://ee.acme.internal/api/embed/sessions \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $USER_EE_AT_TOKEN" \
  -d '{"app":"signup","host_origin":"https://harness.example.com"}'
# → {"code":"…","expires_in":60,"app":{"slug":"signup","title":"报名"},"user":{…},"session_ttl":7200}
```

**宿主页面处理 connect / auth-required / session**

```js
const EE_ORIGIN = "https://ee.acme.internal";
let minted = 0;
const frame = document.getElementById("ee-frame");

async function mintCode() {
  const r = await fetch("/api/ee-embed/mint", { method: "POST" }); // 宿主自己的后端
  const { code } = await r.json();
  frame.contentWindow.postMessage({ v: 1, type: "dsh-ee:connect", code }, EE_ORIGIN);
}

window.addEventListener("message", (event) => {
  if (event.origin !== EE_ORIGIN || event.source !== frame.contentWindow) return;
  const msg = event.data;
  if (!msg || msg.v !== 1) return;
  switch (msg.type) {
    case "dsh-ee:ready":
      if (msg.supports?.includes("connect-v1")) mintCode(); // 跨站：拿到 code 才能取数
      break;
    case "dsh-ee:session":
      // 已登录（跨站时 limited=true）；可以收起 loading
      break;
    case "dsh-ee:auth-required":
      // 会话过期：重新 mint 一次；仍失败则顶层打开应用
      if (++minted <= 1) mintCode();
      else location.assign(EE_ORIGIN + "/a/signup");
      break;
  }
});
```

**限制与安全边界**

- 单实例内存 code：多实例部署前需要把 code/会话放共享存储（本版未做）。
- 每用户 `ee_at_` 是硬前提；不要用共享服务令牌 mint（会让所有嵌入变成令牌主人的身份、审计失真）。
- `ee_emb_` Bearer 只存在 iframe 内存：不写 cookie / localStorage / URL；不通过 postMessage 回传给宿主。
- 不引入 `SameSite=None`、不放开 CORS、不做匿名公开链接、不把 dsh-ee 反代到宿主域名；现有网页会话与 CSRF 防线不变。
- 移动端（视口 < 640px 或移动 UA）建议宿主顶层打开而不是 iframe；任何失败态都提供「在浏览器打开」。
