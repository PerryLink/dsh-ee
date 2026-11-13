# 参与开发（contributing）

> 面向要给 dsh-ee 提 PR 的人：怎么把环境跑起来、改完跑哪些检查、CI 会跑什么、怎么出发布产物、最容易踩的坑。
>
> 以仓库里的 `Makefile`、`.github/workflows/`、`README.md`、`server/README.md`、`e2e/README.md` 和代码为准；本文只是把它们收在一页。设计文档的入读顺序见 [design-index.md](./design-index.md)，用户/管理员文档见 [dev/README.md](./dev/README.md)（随文档基础 PR #120 合入）。

## 1. 环境要求

| 组件 | 版本 | 依据 |
|---|---|---|
| Go | **1.27.3**（`server/go.mod` 的 `go` 指令；可用更新的 1.27.x） | `server/go.mod` |
| Node.js | **22**（CI 的 web/server/e2e 与 OceanBase 任务用 22） | `.github/workflows/ci.yml`、`oceanbase.yml` |
| pnpm | **12.15.0**（`web/package.json` 的 `packageManager`，用 `corepack enable` 启用） | `web/package.json` |
| 数据库 | **TiDB v8.5.8**（日常/CI）或 **OceanBase 4.3.5-lts MySQL 模式**（每周任务） | `.github/workflows/ci.yml`、`oceanbase.yml` |
| Docker（可选） | 跑容器、镜像、离线安装包时需要 | `Dockerfile`、`deploy/make-bundle.sh` |

本地开发只需要数据库，不需要 Redis、消息队列或对象存储：服务端是一个 Go 二进制，前端构建后嵌进二进制，tarball 和数据放在 `DSH_EE_DATA_DIR`。

## 2. 五分钟起后端

```bash
# 1) 数据库：任选一种
tiup playground v8.5.8 --tag dsh-ee --tiflash 0 --without-monitor --host 127.0.0.1
# 或只用 Docker 跑 TiDB
cd deploy && docker compose -f compose.yml -f compose.tidb.yml -f compose.dev.yml up -d tidb && cd ..

# 2) 配置
cd server
cp .env.example .env.local          # 填 DSH_EE_SECRET_KEY 与初始管理员密码
# DSH_EE_SECRET_KEY 用 openssl rand -base64 32 生成：必须是 32 字节 base64，
# 丢了它，已经加密存库的上游凭据/共享凭据就解不开了

# 3) 运行（首次启动自动建库、跑迁移、建管理员）
set -a; . ./.env.local; set +a
go run ./cmd/dsh-ee
# 等价的一步：仓库根目录 scripts/dev-backend.sh（会自己加载 server/.env.local）
```

`server/.env.example` 只列了最小几项；完整变量（`DSH_EE_PUBLIC_URL`、`DSH_EE_COOKIE_SECURE`、数据中台 DSN 等）见 [server/README.md](../server/README.md) 和 [.env.example](../server/.env.example)。`.env.local` 已被 `server/.gitignore` 忽略。

### 可选：演示数据

```bash
cd server
DSH_EE_SEED_ADMIN_PASSWORD='<管理员密码>' go run ./cmd/seed
# 可重复执行；员工初始密码用 DSH_EE_SEED_USER_PASSWORD（默认 dsh-ee-demo-2026）
# 上游 key 默认是占位值，要真跑模型时给 DSH_EE_SEED_DEEPSEEK_KEY / DSH_EE_SEED_GATEWAY_KEY
```

`cmd/seed` 走 HTTP 接口写数据（默认 `http://127.0.0.1:8080`，可用 `DSH_EE_API` 指定），所以服务端要先起来；管理员密码默认读 `DSH_EE_BOOTSTRAP_ADMIN_PASSWORD`，也可以用 `DSH_EE_SEED_ADMIN_PASSWORD` 覆盖。

## 3. 前端开发

```bash
corepack enable
cd web
pnpm install --frozen-lockfile
pnpm dev            # http://localhost:5188
```

Vite 把下面这些前缀代理到 **`http://127.0.0.1:8080`**（`web/vite.config.ts` 里的 `backend`，不是环境变量）：

`/api`、`/v1`、`/npm`、`/index`、`/setup.sh`、`/setup.ps1`

- 服务端换了端口/地址，要改 `web/vite.config.ts` 里的 `backend` 并重启 `pnpm dev`；
- `@workbench` 别名指向 `dsh-plugin/src`：网页版应用与 DSH 工作台共用同一套纯函数渲染；
- 开发服务器监听 `0.0.0.0:5188`，允许局域网访问（`allowedHosts: true`）；
- 前端文案在 `web/src/i18n/{zh,en}`，**英文文件必须镜像中文结构**，少一个键 `tsc` 就会失败。

## 4. 构建产物

仓库根的 `Makefile` 是唯一构建入口：

| 命令 | 做什么 | 产物 |
|---|---|---|
| `make plugin` | `dsh-plugin`：`npm ci` + `npm test` + `npm run build` | 生成 `server/internal/api/connectpkg/index.js`、`client.js`（两个文件都入库；CI 的 `web` job 会检查 `index.js` 有无未提交改动） |
| `make web` | `web`：`pnpm install --frozen-lockfile` + `pnpm build`（= `tsc && vite build`） | `web/dist` |
| `make embed` | 先 `web` + `plugin`，再把 `web/dist` 同步进 `server/internal/webui/build` | 嵌入目录 |
| `make build` | `embed` + `CGO_ENABLED=0` 构建单文件服务端 | `dist/dsh-ee`（版本来自 `git describe --tags --always --dirty`） |
| `make release` | 同上，交叉编译 linux/amd64 与 linux/arm64 | `dist/dsh-ee-linux-amd64`、`dist/dsh-ee-linux-arm64` |
| `make image` | `docker build --build-arg VERSION=… -t dsh-ee:$(VERSION)` | 本地镜像 |
| `make bundle ARCH=amd64` | 调 `deploy/make-bundle.sh`：镜像 + 数据库镜像 + compose + 安装脚本 + 静态二进制 + `SHA256SUMS` | `deploy/out/dsh-ee-<版本>-linux-<架构>.tar.gz` |
| `make clean` | 清 `dist/`、`web/dist/` 和嵌入目录 | — |

要点：

- `make build` 前会**清空再拷贝**嵌入目录（只保留 `.gitignore`），避免旧前端残留；
- `VERSION` 可覆盖：`make build VERSION=v0.1.0`；
- 单文件二进制用 `-trimpath`、`-ldflags "-s -w -X main.version=…"`；版本可以用 `dist/dsh-ee version` 或 `GET /healthz` 看到；
- 不用 `make` 时，前端也可以 `cd web && pnpm build`，后端 `cd server && go build ./cmd/dsh-ee`；但「前端嵌进二进制」只有 `make build` 会做。

## 5. 测试矩阵

### 5.1 Go 测试（依赖真实数据库）

```bash
# 完整：go vet ./... + go test ./...
DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4000)/' make test

# 只跑某个包/用例
cd server && DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4000)/' go test ./internal/api -run TestPackagePreflight -v
```

- **DSN 末尾必须是 `)/`**：测试会用 `strings.Replace(dsn, ")/", ")/<随机库名>", 1)` 建随机库（数据中台测试还建 `<库名>_data`），所以 DSN 形如 `root:@tcp(127.0.0.1:4000)/`，不能带库名；
- 每次测试建库、跑迁移、跑完 `DROP DATABASE`；账号要有建库/删库权限；
- **不设 `DSH_EE_TEST_DSN` 时数据库用例会 `t.Skip`**：看起来全绿，但集成测试其实没跑（CI 的 server job 会设 DSN）；
- 独立 Go module 另测：`extensions/sdk-go`、`extensions/official/ee-jwt-adapter`、`extensions/examples/demo-extension`（见 CI 的 `server` job）。

### 5.2 前端 / 接入插件 / 端侧扩展

```bash
cd web && pnpm build        # tsc + vite；i18n 缺键会在这里失败
cd dsh-plugin && npm ci && npm test && npm run build
cd extensions/sdk-local && npm ci && npm test && npm run build
cd extensions/official/legacy-local && npm ci --omit=optional && npm test
cd extensions/official/claude-code && node --test test/*.test.mjs
```

### 5.3 浏览器 E2E

```bash
# 方式一：连一个已经启动的服务端
ADMIN_PW='<管理员密码>' E2E_BASE=http://127.0.0.1:8080 CHROME=/usr/bin/google-chrome \
  node e2e/run.mjs            # 或只跑一个：node e2e/run.mjs 10-tokens

# 方式二：隔离模式（CI 用；每个测试文件起独立服务端、独立 DB、独立数据目录）
E2E_MANAGED=1 E2E_BIN=$PWD/dist/dsh-ee E2E_DSN='root:@tcp(127.0.0.1:4000)/' \
ADMIN_PW='<管理员密码>' E2E_BASE=http://127.0.0.1:8080 CHROME=/usr/bin/google-chrome \
  node e2e/run.mjs
```

- 需要 Playwright 能用的 Chrome/Chromium；Linux/WSL 上缺 `libnss3` 等系统库时浏览器起不来，这是环境问题不是用例失败；
- 隔离模式下失败会打印服务端日志末段；截图统一放 `e2e/shots/`（`E2E_SHOTS` 可改）；
- 隔离模式默认会 drop 测试库；`E2E_KEEP_DB=1` 保留，`E2E_MYSQL` 指定 `mysql` 客户端路径；
- `E2E_START_TIMEOUT_MS` 默认 30000，机器慢时可调大。

### 5.4 真实 DSH 场景

```bash
export DSH_BIN=/path/to/dsh          # 真实 DSH 二进制
export CHROME=/path/to/chrome
export DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4001)/'
node e2e/dsh/scenarios/run.mjs
```

结果按 DSH 版本记录（`result.json` 里的 `dshVersion`、`work/dsh-version.txt`），矩阵维护方法见 [compatibility.md](./compatibility.md)。

### 5.5 OceanBase

```bash
scripts/test-oceanbase.sh     # 需要 Docker 与足够内存
```

`oceanbase` workflow 手动触发或每周日跑；单机 mini 模式要 6 GB 内存左右，宿主机至少 2 核 8 GB。

## 6. 文档检查

docs 基础 PR #120 引入 `scripts/check-docs.mjs` 和 CI 的 `docs` job（只读，不需要安装依赖）：

```bash
node scripts/check-docs.mjs
```

它检查：

- `docs/**/*.md` 的**相对链接与锚点**（外部 URL 跳过；站内绝对路径跳过）；
- `docs/plugin-dev/SOURCES.md` 的 pin 表与改编清单（版本、commit、许可、本地引用）；
- `docs/dev/*.md` 的 front matter（`title` / `status` / `since` / `lang`）与 `docs/dev/README.md` 索引登记；
- 仓库根 `NOTICE` 的许可声明与指向。

写文档时注意：**锚点是按标题自动生成的小写 slug**，标题里有行内代码/斜杠时容易和预期不一致；改标题就顺手搜一下谁链接了这个锚点。文档 PR 用到的「配套 PR 尚未合入」链接，合并后要把白名单（`PENDING_LINKS`）删掉。

## 7. CI 会跑什么

| job | 触发 | 内容 |
|---|---|---|
| `web` | push / PR | Node 22 + corepack pnpm：`web` 的 install/build；`dsh-plugin` 的 install/test/build 并 `git diff --exit-code server/internal/api/connectpkg/index.js`；端侧 SDK/模板/Claude Code 适配器；上传 `web-dist` |
| `server` | push / PR | TiDB 8.5.8 服务容器；`go vet ./...` + `go test -count=1 ./...`（带 `DSH_EE_TEST_DSN`）；独立 module 的 vet/build |
| `e2e` | push / PR，`needs: web` | 下载 `web-dist` → 构建单文件服务端 → `E2E_MANAGED=1` 跑 `e2e/run.mjs`；失败上传 `e2e-shots` |
| `docker` | push / PR | 用占位 `.env` 校验三份 compose 与 `install.sh`；构建镜像；`docker run --rm dsh-ee:ci version` 必须等于 CI 版本号 |
| `setup-windows` | push / PR | `windows-latest` 上跑不依赖数据库的两个 PowerShell 测试（`TestSetupEnvPowerShell`、`TestSetupTokenArgPowerShell`） |
| `docs` | push / PR（#120 合入后） | `node scripts/check-docs.mjs` |
| `oceanbase` | 手动 / 每周日 | OceanBase 上跑 Go 集成测试 + e2e（脚本 `scripts/test-oceanbase.sh`） |

## 8. 发布流程

仓库目前**没有自动 Release workflow**（`.github/workflows/` 只有 ci / oceanbase / windows），发布是手工步骤；roadmap 的 M11（发布材料、自动产物、升级说明）还未完成，见 [roadmap.md](./roadmap.md) 阶段 C。当前可复现的流程：

1. 选版本号（例如 `v0.1.0`），确认 `main` 上 `make test` / e2e / web 都过；
2. 打 tag（`git tag v0.1.0 && git push --tags`），`make release VERSION=v0.1.0` 产出两个架构的静态二进制；
3. `make image VERSION=v0.1.0`，需要分发镜像时推到内部 registry；
4. `make bundle VERSION=v0.1.0 ARCH=amd64`（arm64 再来一次；`WITH_DB=tidb` 可减小包体），产物在 `deploy/out/`，内含 `SHA256SUMS`；
5. 写 Release notes：行为变化、升级步骤、回滚说明、已知限制；升级/回滚口径见 [deploy/README.md](../deploy/README.md) 与 [server/README.md](../server/README.md)「升级与回滚」；
6. **升级必须保留 `.env`（`DSH_EE_SECRET_KEY`）**；迁移只增不改，回滚到旧服务端时旧版会忽略新增表和列，但新令牌（`ee_at_`）不可用，要改回旧令牌。

## 9. 常见坑

| 坑 | 现象 | 处理 |
|---|---|---|
| 没设 `DSH_EE_TEST_DSN` | `make test` 全绿，但数据库用例被 skip | 按 §5.1 设 DSN；看输出里的 `SKIP` |
| DSN 带库名或没有 `)/` | 测试报错/连到错误的库 | 用 `root:@tcp(127.0.0.1:4000)/` 这种形式 |
| `DSH_EE_SECRET_KEY` 不合法 | 启动失败「必须是 32 字节的 base64」 | `openssl rand -base64 32`，不要手写短字符串 |
| 忘了加载 `.env.local` | 启动报缺少 `DSH_EE_DSN` / 密钥 | `set -a; . ./.env.local; set +a`，或用 `scripts/dev-backend.sh` |
| 后端端口不是 8080 | 前端 `/api` 404 / 登录失败 | 改 `web/vite.config.ts` 的 `backend`，重启 `pnpm dev` |
| 改了前端但二进制没变 | 打开还是旧页面 | `make build`（会重新 `embed`）；只 `go build` 不会带前端 |
| 没提交接入插件构建产物 | CI `web` job 在 `git diff --exit-code` 处失败 | 跑 `make plugin`，把 `server/internal/api/connectpkg/index.js`、`client.js` 的改动一起提交 |
| i18n 缺键 | `pnpm build`（tsc）失败 | 中英两份一起加，结构镜像 |
| e2e 浏览器起不来 | `libnss3.so: cannot open shared object file` 等 | 装 Playwright 系统依赖；CI 用预装 Chrome |
| 隔离模式 e2e 数据库没清理 | 残留 `dsh_ee_test_*` 库 | 需要本机有 `mysql` 客户端；`E2E_MYSQL` 指定路径，`E2E_KEEP_DB=1` 反而保留 |
| 文档链接检查失败 | `broken-link` / `bad-anchor` | 按文件行号改；标题改了就同步锚点；见 §6 |
| 单文件构建依赖 CGO | 目标机跑不起来 | 保持 `CGO_ENABLED=0`（Makefile 已设置） |

## 10. 仓库结构

| 路径 | 内容 |
|---|---|
| [`server/`](../server) | Go 服务端：认证、RBAC、LDAP/OIDC/飞书、模型网关、npm 源、能力中心、MCP 网关、知识库、数据中台、扩展运行时 |
| [`web/`](../web) | Vite + React + Tailwind 管理端/工作台前端，中英双语 |
| [`dsh-plugin/`](../dsh-plugin) | `dsh-connect`：DSH 接入插件（MCP 桥、模型提供方、技能来源、`/ee-*` 命令、工作台渲染） |
| [`extensions/`](../extensions) | 扩展 SDK（Go / TS）、示例、官方扩展、清单规格、给 Agent 的技能 |
| [`e2e/`](../e2e) | Playwright 浏览器测试与真实 DSH 场景 |
| [`deploy/`](../deploy) | compose、离线安装包、systemd、演示站配置 |
| [`docs/`](./) | 设计稿、交付报告、验证清单；用户/开发文档在 `docs/dev`、`docs/plugin-dev`（随配套 PR 合入） |
| [`scripts/`](../scripts) | 开发/OceanBase 脚本、文档检查（#120 合入后） |

## 11. 提交 PR 前

1. 只改必要范围；同一 PR 不混无关重构（安全/权限改动按维护者的私下渠道处理，不要带利用步骤）；
2. 跑 §5 里受影响的最小测试集；数据库用例要真连库；CI 的 `web`/`server`/`e2e` 必须绿；
3. 改公开行为/配置/错误码时同步文档与 [design-index.md](./design-index.md) 的状态；未实现的能力标「设计」；
4. PR 正文写清楚：改了什么、为什么、怎么验证、行为变化与回滚方式；不写密钥、内部域名和未修复漏洞的利用步骤；
5. 文档 PR 跑 §6 的检查，并确认相对链接与锚点可解析。
