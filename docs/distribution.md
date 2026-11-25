# DSH 桌面版分发与企业接入：设计（v0.1 草案，2026-11-02）

> 执行交接：[distribution-handoff.md](distribution-handoff.md)。
> 范围：员工从 dsh-ee 下载**官方标准版** DSH Desktop，按下载页引导装上企业接入插件，侧栏与首屏显示企业品牌。**第一期只做 macOS**，Windows 放第二期。
> 上游依据：`deepseek-ai/deepseek-harness`（MIT，commit ce7073d5，本地只读克隆 `.tools/upstream/deepseek-harness`）。相关规划：[agent-connect-product.md](agent-connect-product.md)、[agent-connect-tech.md](agent-connect-tech.md)。

## 0. 结论先行

### 内网离线导入

无法访问 download.deepseek.com 的部署，可在管理后台「DSH 分发」点击「导入离线包」，选择 mac-arm64、mac-x64 或 win-x64，上传 `.zip`、`.exe`、`.msi` 或 `.dmg` 文件并填写版本（文件名会自动预填，可修改）。可选填写官方 nightly.yml / latest.yml 的 sha512（base64 或十六进制）；校验失败不会保留上传内容。单包上限 2 GiB，上传时显示进度，无需先检查官方版本。

导入后标记为「离线导入」，员工可照常下载，管理员可设为推荐版本。同平台、同类型、同版本再次导入会替换原文件；不同版本不能共用文件名。离线版本无法重新从官方镜像，需再次导入；自动同步不会下载或覆盖这些记录，版本保留策略仍适用。

| 问题 | 方案 |
|---|---|
| 安装包从哪来 | 从 DeepSeek 官方更新清单同步，**不重新构建**。dsh-ee 下载后校验 sha512，存到本地，再给员工下载 |
| Mac 给员工什么文件 | 默认给清单里的 **zip**（有官方 sha512，解压就是签过名、公证过的 `.app`）。固定地址的 DMG 作为备选，只有 dsh-ee 自己算的 sha256，页面注明「未经官方哈希校验」 |
| 企业插件怎么装进 Desktop | 一行命令改用 **Desktop 自带的 `dsh`**（`<App>.app/Contents/Resources/runtime/cli/bin/dsh`）执行 `plugin --profile desktop add`。员工电脑不需要 Node、npm、pnpm |
| 怎么引导 | 下载页做成 4 步：下载 → 打开一次再退出 → 运行命令（浏览器登录）→ 重新打开，页面显示「已接入」 |
| 品牌 | 接入插件新增浏览器部分，占用 `sidebar.brand.mark`、`sidebar.brand.name`、`conversation.hero.brand.mark` 这三个位置，同时关掉官方品牌插件 `ui-brand-official`。品牌数据**运行时从 dsh-ee 拉取**，换 logo 不用发新版插件 |
| 版本 | 员工装好的 Desktop 会从 DeepSeek 官方自动更新（地址写死在安装包里），dsh-ee 管不了。「推荐版本」只决定下载页给哪个包，以及插件提醒员工版本过低 |

## 1. 现状与要修的问题

- 下载页 `web/src/pages/dsh-download.tsx` 和管理页 `web/src/pages/admin/distribution.tsx` 用的是前端假数据 `initialReleases`；下载按钮只弹提示，后端没有 `/dl/`。
- `setup.sh` 用 `dsh plugin --profile default add` 装插件，并要求电脑上有 node、npm、pnpm、dsh。**Desktop 读不到这样装的插件**：上游 `apps/desktop/README.zh.md`「安装归属」一节写明，Desktop 自己管理 `$DSH_HOME/profiles/desktop`，npm 安装的 dsh 改不了它。会话、设置、**凭据库**是共用的，插件安装是隔离的。
- 设备登录脚本 `setupDeviceJS` 靠 `node -e` 运行；电脑上没装 Node 就跑不了。
- `Settings.Distribution` 里的 `AllowCommunityDesktop`、`DshRelease.product = "core"` 用不上了：只剩官方 Desktop 这一个来源。

## 2. 上游事实（已核实）

| 事实 | 来源 |
|---|---|
| 更新清单：`https://download.deepseek.com/dsh-desk/feeds/{mac-arm64,mac-x64}/nightly-mac.yml`、`win-x64/nightly.yml`，2026-11-02 实测返回 200。`latest*.yml` 返回 404 | 实测；`apps/desktop/publishing/README.zh.md` |
| 清单字段：`version`、`files[].url / sha512 / size`、`releaseDate`。Mac 清单里只有 zip；当前版本 0.2.1-rc.5（2026-10-29） | 实测 |
| 首次安装用的固定 DMG：`https://download.deepseek.com/desktop/dsh-latest-macos-{arm64,x64}.dmg`，只有 ETag，没有官方哈希 | 实测；发布文档「固定安装包下载」 |
| 只有 Nightly 一个通道，没有 Linux Desktop | 发布文档 |
| 真机应用为 `DeepSeek Harness.app`（`com.deepseek.dsh`），自带的 `dsh` 在 `Contents/Resources/runtime/cli/bin/dsh`：首次在设置页直接完全退出也会初始化 profile；未初始化时插件命令会拒绝并给出提示 | Desktop README；S-M2、S-M6 真机验证（2026-11-02） |
| 自带的 Node 在 `Contents/Resources/runtime/primary-runtime/dependencies/node/bin/node` | `apps/desktop/src/command-management.ts`；S-M2 真机验证 |
| 欢迎页只在「没登录官方账号，并且没有任何已配置的 `apiKeyEnv` 凭据」时出现；导入 `DSHEE_TOKEN` 后重开可直接进入工作区并完成企业模型对话 | `apps/desktop/src/welcome-api.ts`、`welcome-backend.ts`；S-M3 真机验证 |
| 官方品牌插件只是占用这些位置。上游说明文档写明：使用其他品牌的部署，换一个占用同样位置的包。这些位置每个只能放一个，重复注册会报错 | `packages/client/ui-brand-official/README.zh.md`；`ui-slots/src/index.ts:1222` |
| 插件换不了的：窗口标题（构建时的 `DSH_CLIENT_TITLE`）、应用名称和图标、「关于 DeepSeek Harness」菜单、欢迎页、更新弹窗 | 同上；Desktop README |
| 品牌规范：不能用「DeepSeek Harness」全名，可以写「DSH」或「基于 DeepSeek Harness 构建」，不能暗示官方背书 | `BRAND_GUIDELINES.zh.md` |

## 3. 员工体验（Mac）

```
下载页（/dsh）
 ① 下载 DSH Desktop          [自动识别 Apple 芯片 / Intel]  ⬇ 下载（zip，约 330 MB）
    解压后拖进「应用程序」。sha512 已由 dsh-ee 按官方清单校验
 ② 打开一次，然后完全退出     ⌘Q。不用登录，也不用填 API key
 ③ 在「终端」运行：            curl -fsSL https://ee.example.com/setup.sh | sh   [复制]
    浏览器会打开确认页，确认后插件自动装好
 ④ 重新打开 DSH              ● 等待接入…  →  ✔ 已接入：MacBook-Pro · 插件 1.0.7
```

- 第 ④ 步的状态由页面轮询获得（§6.3）。员工在第 ③ 步完成浏览器登录后，状态会先变成「已登录，等待 DSH 启动」。
- 第 ② 步跳过时，脚本会给出明确提示：「请先打开一次 DSH 再退出，然后重新运行本命令」。
- 欢迎页：插件配置了 `apiKeyEnv: DSHEE_TOKEN`，第 ③ 步又已把令牌写进共用的凭据库，所以第 ④ 步重新打开时应该直接进工作区（S-M3 验证）。第 ② 步首次打开时会看到欢迎页，页面提示「直接退出即可」。

## 4. 同步与下载（后端）

### 4.1 数据

新表 `dsh_releases`（取代前端的 `initialReleases`）：

| 字段 | 说明 |
|---|---|
| `target` | `mac-arm64` / `mac-x64`（第二期加 `win-x64`） |
| `version`、`released_at` | 来自清单 |
| `file`、`size`、`sha512` | 来自清单；zip/exe |
| `upstream_url` | 官方地址，便于追溯 |
| `state` | `available`（清单里有，没下载）/ `downloading` / `mirrored` / `failed` |
| `recommended` | 每个 target 最多一个 |
| `local_path`、`mirrored_at`、`error` | — |

DMG 另设一个 `kind = dmg` 的行：版本取下载时清单的 `version`，哈希由 dsh-ee 计算，`verified = false`。

设置项 `Distribution` 改为：`Enabled`、`AutoSync`（`off` / `notify` / `mirror`，默认 `notify`）、`KeepVersions`（默认 3）、`OfferDmg`（默认 false）。删掉 `AllowAlpha`、`AllowCommunityDesktop`：官方只有 Nightly 通道，没有社区版。

### 4.2 同步任务

- 每 6 小时，加上管理员手动「立即检查」，拉一次清单，新版本记为 `available`。
- `AutoSync = mirror` 时自动下载：流式写到 `<data>/dsh-releases/<target>/<file>.part`，边写边算 sha512，与清单一致才改名并标记 `mirrored`；不一致就删除文件，标记 `failed`，写审计日志。
- 只请求 `download.deepseek.com`，不跟随跨域重定向。出站请求复用现有的上游代理设置（`Upstream`）。
- 每个 target 最多保留 `KeepVersions` 个已下载的版本；推荐版本不会被清理。
- 现有的 `kickConnect` / `runConnect` 已经是「后台启动 + 定时」的写法，同步任务照这个写。

### 4.3 下载

- `GET /dl/{target}/{file}`：需要登录（浏览器会话，权限 `PortalView`），支持 `Range`（文件约 330 MB）。返回 `Content-Disposition: attachment`，带 `Digest` / `X-Checksum-Sha512` 头。
- `GET /api/dsh/releases`：员工下载页用。只返回 `mirrored` 状态的版本；每个 target 的推荐版本排在最前。
- 管理接口：`GET /api/admin/dsh-releases`、`POST /api/admin/dsh-releases/check`、`POST /api/admin/dsh-releases/{id}/mirror`、`DELETE …/{id}`（删本地文件）、`POST …/{id}/recommend`。都需要 `SettingsManage`，写审计日志。

### 4.4 页面

- 管理页（`admin/distribution.tsx`）：一个 target 一张表，列出版本、日期、大小、状态（含下载进度）和推荐；右上角是「立即检查」和自动同步开关。
- 员工页（`dsh-download.tsx`）：换成 §3 的 4 步引导。用 `navigator.userAgentData.getHighEntropyValues(['architecture'])` 判断 Apple 芯片还是 Intel，判断不了就把两个都列出来，并说明「 → 关于本机」里怎么看。Windows 和 Linux 显示「即将支持」。

## 5. 一行命令：Desktop 路线

`setup.sh` 检测到 macOS 上装有 Desktop 时走新路线，否则仍走现有的 CLI 路线，CI 和 `--token` 不受影响。

```
1. 找 Desktop：/Applications/<App>.app、~/Applications/<App>.app（名字由 S-M2 确认）
   ↳ DSH_DESKTOP_APP 环境变量可以覆盖
   DSH=<App>/Contents/Resources/runtime/cli/bin/dsh
   NODE=<App>/Contents/Resources/runtime/primary-runtime/dependencies/node/bin/node
2. 检查 Desktop 是否在运行（pgrep -f "<App>/Contents/MacOS"）→ 在运行就提示 ⌘Q，等待，最多 2 分钟
3. 检查 $DSH_HOME/profiles/desktop 是否存在 → 不存在就提示「先打开一次再退出」，然后退出
4. 设备登录：用 $NODE 运行现有的 setupDeviceJS（不需要系统装 Node）→ 写入 token.pending
5. 直接写 ~/.npmrc（registry、作用域、_authToken），不再调用 npm config。保留其他行，权限设为 600
6. curl $BASE/api/dsh/whoami 校验令牌，替代 npm whoami
7. "$DSH" plugin --profile desktop add "$CONNECT_PKG@latest"
8. 提示：重新打开 DSH
```

- 不再检查 pnpm：Desktop 自带 pnpm。
- `token.pending` 由插件在启动时导入凭据库（阶段 A 已实现），Desktop 和 CLI 共用这个凭据库。
- 要是 S-M4 发现 Desktop 自带的 pnpm 不读 `~/.npmrc`，备选是先用 curl 下载 tarball，再 `dsh plugin add ./file.tgz`；能不能装本地文件也要验证。
- 现在的 Desktop 用户已经按旧方式装过 default profile，脚本照常提示「已在 desktop profile 里装好」，旧的 default profile 安装不动。

## 6. 接入插件的改动

### 6.1 品牌（浏览器部分）

> 2026-11-09 补充：首屏标题、logo 外形、配色与不重启生效见 [branding.md](branding.md)。

- dsh-ee 设置里新增 `Brand`：`Name`（≤ 24 字，**拒绝包含「DeepSeek Harness」**，界面提示可以写「某某 AI · 基于 DSH」）、`Logo`（SVG/PNG ≤ 256 KB，正方形，SVG 要先清理掉脚本）、`Enabled`。
- 接入插件新增浏览器入口 `client.js`，在 `dsh-plugin/` 里用 esbuild 构建；`package.json` 加 `dsh.client`（写法参照 `ui-brand-official` 的 `"dsh": { "client": { "inject": [...], "platform": "web" } }`），注册上面三个位置。
- 数据**运行时拉取**，不写死在包里：浏览器部分向插件的服务端部分要品牌数据（服务端部分已经轮询 `/api/dsh/catalog`，在返回结果里加上 `brand: { name, logoUrl, revision }` 即可）。logo 用 `<img src="{base}/api/dsh/brand/logo?rev=…">` 显示，图片请求不受 CORS 限制，这个接口公开访问。S-M1 已通过，写法以用户提供的样例为准（见交接文档 §2）。
- 拉不到数据、或者 `Brand.Enabled = false` 时，显示一个中性的图标和组织名，**不会回退成官方 logo**（官方品牌插件已经关掉了）。
- `cordis.patch.yml` 里加一行，把 `ui-brand-official` 设为 `disabled: true`；只在 `Brand.Enabled` 时加，写法以 S-M1 样例为准。

### 6.2 版本提醒

- `catalog` 返回结果里加上 `desktop_recommended`；插件启动时用 Desktop 自己的版本比较，低于推荐版本就在 `/ee-doctor` 里给出警告。不弹窗，Desktop 会自己更新。
- 接入插件本身不会自动更新。品牌改成运行时拉取以后，品牌改动不需要发新版插件；其他改动仍按现在的方式，由 `/ee-doctor` 提醒员工去插件页更新。

### 6.3 「已接入」状态

- 插件请求时已经带了 `X-DshEE-Client: dsh-connect/<version>`；再加一个 `X-DshEE-Host: desktop/<desktopVersion>`（如果拿得到）。
- 令牌表记录 `last_client`、`last_host`、`last_seen_at`（复用 `LastUsedAt` 的写入逻辑，并限制写入频率）。
- `GET /api/me/dsh-clients`：返回当前用户最近 10 分钟有活动的设备，供下载页第 ④ 步轮询（每 3 秒一次，最多 5 分钟）。

## 7. 前置验证（Mac，动手前做）

在一台干净的 Mac 上，用独立的 `DSH_HOME`，装官方 0.2.1-rc.5 Desktop，逐项验证。用到的脚本放 `e2e/dsh/spikes/desktop/`。

| 编号 | 要验证的事 | 通过标准 | 不通过时的备选 |
|---|---|---|---|
| S-M1 ✅ 用户 2026-11-02 确认通过 | 树外组合包能关掉 `ui-brand-official`，并用自己的浏览器入口占用三个品牌位置；浏览器部分能拿到服务端部分的数据 | 侧栏和首屏显示测试 logo 和名称，控制台没有「already has a registration」报错 | 关不掉：问上游有没有官方做法 / 提 issue；拿不到数据：品牌写进包配置，换品牌时发新版插件 |
| S-M2 ✅ 2026-11-02 真机通过 | 应用为 `DeepSeek Harness.app`，bundle id `com.deepseek.dsh`；内置 dsh 与 node 路径和设计一致 | 初始化后及 Desktop 运行中 `plugin --profile desktop list` 均成功 | 无需备选；未初始化时命令会明确提示先打开 Desktop |
| S-M3 ✅ 2026-11-02 真机通过 | 令牌写入 `token.pending` 并重开 Desktop | 不出现官方登录欢迎页；令牌被导入；企业模型完成一轮对话 | 无需备选 |
| S-M4 ✅ 2026-11-02 真机通过 | 自带 pnpm 读取隔离 `.npmrc` 中的作用域 registry 和 `_authToken`，从 dsh-ee 私有源安装 | `plugin list` 能看到 `@company/dsh-connect` | 直连成功，不采用 tarball 备选 |
| S-M5 ✅ 2026-11-02 真机通过 | 同版本 Safari 下载应用带 quarantine；`spctl` 显示 `accepted / Notarized Developer ID` | 通过 `open --env` 用隔离配置启动，无 Gatekeeper 报错 | 无需备选；M1 后仍需从 dsh-ee 页面复验 |
| S-M6 ✅ 2026-11-02 真机通过 | 首次打开停在设置页，直接 ⌘Q | 退出后 `profiles/desktop` 存在，`plugin list` 成功 | 无需备选；第 ② 步可直接退出 |

S-M1 已通过；S-M2、S-M4 决定方案能不能成立，必须先做；S-M3、S-M5、S-M6 只影响引导文案。

## 8. 分阶段

| 阶段 | 内容 | 验收 |
|---|---|---|
| **M0 验证** | §7 的 S-M1～S-M6，结果写进本文 §2 | 每项有结论，备选方案已选定 |
| **M1 同步与下载** | §4：`dsh_releases` 表、同步任务、`/dl/`、管理页和员工页接真数据（Mac target） | 单元测试：清单解析、sha512 不一致时拒绝、清理旧版本、`Range` 请求；e2e：用本地 fake 清单走通同步 → 推荐 → 下载 |
| **M2 一行命令 + 引导页** | §5 的 Desktop 路线；§6.3 的状态检测；下载页 4 步 | 在真 Mac 上从零走完 §3，第 ④ 步显示「已接入」；现有 `setup_test.go` / CLI 路线测试不回归 |
| **M3 品牌** | §6.1 的设置项、logo 接口、浏览器入口、关掉官方品牌插件 | 真 Mac 上侧栏和首屏是企业品牌；换 logo 后不用重装，重启 DSH 就生效；名称里含「DeepSeek Harness」时被拒绝 |
| M4 Windows（第二期） | `win-x64` 同步，`setup.ps1` 的 Desktop 路线 | 另行规划 |

M1 和 M3 的服务端部分可以同时做；M2 依赖 M0 的 S-M2、S-M4；M3 依赖 S-M1。

## 9. 不做的事

- 重新构建或打包 Desktop，改应用名称、图标和更新地址。
- 管控 Desktop 的自动更新。
- 内网离线导入安装包（以后可以在管理页加「上传」，校验方式相同）。
- Linux、Intel Mac 以外的特殊架构；npm 版 `@deepseek-ai/dsh` 的分发（CLI 路线保持现状）。

## 10. 风险

| 风险 | 应对 |
|---|---|
| 上游 Desktop 还在开发者预览阶段，profile 目录、自带 CLI 的路径、品牌插件的 id 都可能变 | 脚本按版本识别路径；`/ee-doctor` 检查品牌是否生效；同步到新版本时，管理页提示「推荐前请先在测试机上验证」 |
| 官方只有 Nightly 通道，推荐的可能是 rc 版 | 页面标注「预发布」；推荐版本由管理员决定 |
| 官方清单格式变了或地址失效 | 同步失败时在管理页显示原因；已下载的版本不受影响 |
| 关掉官方品牌插件被认为不合规 | 名称强制带「基于 DSH」之类的说明，不允许出现完整品牌名；关于菜单等处仍是官方品牌 |
| 约 330 MB 的文件占用磁盘和带宽 | `KeepVersions` 限制保留数量；`Range` 支持断点续传；只同步 Mac 的两个 target |
