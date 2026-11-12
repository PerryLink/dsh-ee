# dsh-ee 运维手册

> 状态：按 `origin/main`（20aafbb，2026-11-12）核对代码与 `deploy/` 写成。**已实现**的按命令/配置写；**没有实现**的（备份工具、指标、日志轮转、多实例、自动回滚等）标「设计」，见 §12。文中标「PR #N，合并后生效」的能力来自对应独立 PR（#118 微应用嵌入 v0/v1、#122 CLI、#123 Harness 发现、#127 CORS、#128 MCP 分页/写确认结构化），**均未合入基线**；能力以对应 PR 合入为准。
>
> 相关文档：[architecture.md](./architecture.md)（组件与数据）、[cli.md](./cli.md)（命令）、[troubleshooting.md](./troubleshooting.md)（错误码与修复）、[deploy/README.md](../deploy/README.md)（官方部署说明）。

## 1. 部署前检查

| 项 | 要求 | 依据 |
|---|---|---|
| 操作系统 | Linux（amd64 / arm64）；容器镜像基于 alpine，二进制静态链接 | `deploy/make-bundle.sh`、`Makefile release` |
| 数据库 | TiDB 或 OceanBase（MySQL 模式的租户）；库不存在且账号有建库权限时会自动创建 | `server/internal/config/config.go`、`deploy/README.md` |
| 数据目录 | 一个可写目录（容器里是 `/data` 卷），放 npm tarball 与缓存 | `DSH_EE_DATA_DIR` |
| 内存/CPU | 数据库另算；dsh-ee 本身很轻（演示站实测约 20 MB 常驻）。OceanBase 单机最低 2 核 8 GB | `deploy/README.md`、`deploy/demo/README.md` |
| 端口 | 默认 `127.0.0.1:8080`（容器里 `0.0.0.0:8080`），只应通过反向代理对外 | `DSH_EE_ADDR`、`Dockerfile` |
| HTTPS | dsh-ee 只提供 HTTP；TLS 在反向代理/负载均衡终止 | `deploy/README.md`「HTTPS 与访问地址」 |
| 外网 | 不必须。上游模型、npm 回源、DSH 分发、dshfind 目录都可能需要外网；隔离网用离线包导入 | `server/README.md`「npm 源」 |

## 2. 部署

### 2.1 离线安装包（推荐给试点）

安装包 `dsh-ee-<版本>-linux-<amd64|arm64>.tar.gz` 里带镜像、compose 文件、`install.sh`、静态二进制和 systemd 单元，目标机器只需要 docker 与 docker compose v2。

```bash
tar xzf dsh-ee-v0.1.0-linux-amd64.tar.gz && cd dsh-ee-v0.1.0-linux-amd64
sha256sum -c SHA256SUMS

./install.sh tidb        # dsh-ee + 单机 TiDB（PD + TiKV + TiDB）
./install.sh oceanbase   # dsh-ee + 单机 OceanBase（宿主机至少 2 核 8 GB）
./install.sh external    # 只起 dsh-ee，连已有数据库：先在 .env 里填 DSH_EE_DSN
```

`install.sh` 第一次执行会生成 `.env`（`DSH_EE_SECRET_KEY`、`DSH_EE_BOOTSTRAP_ADMIN_PASSWORD` 等）并打印管理员密码；之后重复执行就是升级。启动后会轮询 `http://127.0.0.1:<端口>/healthz` 等就绪（最长约 16 分钟，OceanBase 首次启动慢）。

**`.env` 必须和数据库一起备份**：`DSH_EE_SECRET_KEY` 丢了，已存的上游模型 key、连接凭据就解不开了。

### 2.2 不用安装包：docker compose

```bash
cd deploy
cp ../server/.env.example .env      # OceanBase 还要 OB_TENANT_PASSWORD / OB_SYS_PASSWORD
docker build -t dsh-ee:latest ..    # 或 docker build --build-arg VERSION=… -t dsh-ee:…
docker compose -f compose.yml -f compose.tidb.yml up -d
```

- `compose.yml` 只起 dsh-ee；叠加 `compose.tidb.yml` 或 `compose.oceanbase.yml` 时数据库一起起。
- 数据卷：`dsh-ee-data`（容器内 `/data`）；TiDB 是 `tidb-data`，OceanBase 是 `ob-data` / `obd-cluster`。
- compose 里 `DSH_EE_DB_WAIT` 默认 `15m`，给数据库容器首次启动留时间。
- 数据库端口不对外暴露；生产请用独立部署的集群 + `./install.sh external`。

### 2.3 不用 docker：二进制 + systemd

```bash
install -m 755 dsh-ee /usr/local/bin/dsh-ee
useradd --system --home /var/lib/dsh-ee --create-home dsh-ee
install -d -m 700 /etc/dsh-ee
install -m 600 systemd/dsh-ee.env.example /etc/dsh-ee/dsh-ee.env   # 编辑 DSN、主密钥、管理员密码
cp systemd/dsh-ee.service /etc/systemd/system/
systemctl enable --now dsh-ee
```

systemd 单元（`deploy/systemd/dsh-ee.service`）已启用 `NoNewPrivileges`、`ProtectSystem=strict`、`ProtectHome`、`ReadWritePaths=/var/lib/dsh-ee`，并从 `/etc/dsh-ee/dsh-ee.env` 读配置。数据目录要落在 `ReadWritePaths` 里并归 `dsh-ee` 用户所有。

### 2.4 本地开发

```bash
# 起单机 TiDB（tiup 或 docker compose；项目里也有 deploy/compose.dev.yml 的 tidb）
~/.tiup/bin/tiup playground v8.5.8 --tag dsh-ee --tiflash 0 --without-monitor --host 127.0.0.1

cd server
cp .env.example .env.local         # 填 DSH_EE_SECRET_KEY：openssl rand -base64 32
set -a; . ./.env.local; set +a
go run ./cmd/dsh-ee                # 或 ../scripts/dev-backend.sh

DSH_EE_TEST_DSN='root:@tcp(127.0.0.1:4000)/' go test ./...   # 每次建随机库，跑完删除
go run ./cmd/seed                  # 可选：演示数据（角色、员工、额度、上游模型）
```

前端开发见根 README「Development」；要单文件产物在仓库根执行 `make build`。

### 2.5 反向代理与对外地址

代理后至少配置：

- `DSH_EE_PUBLIC_URL=https://<对外域名>`（npm tarball 下载地址、接入脚本、插件索引里的地址都按它生成；为空时按请求 Host/`X-Forwarded-*` 推断，生产不要依赖推断）；
- `DSH_EE_COOKIE_SECURE=true`；
- 或让代理传 `X-Forwarded-Proto`、`X-Forwarded-Host`、`X-Forwarded-For`；
- 请求体上限至少 200 MB（npm publish、离线包导入），或对 `/api/admin/upstream/import` 单独放开；
- 流式响应（`/v1/chat/completions` 的流式回答、`GET /mcp` 的事件流）要关闭代理缓冲，例如 nginx 的 `proxy_buffering off; proxy_read_timeout 600s;`（示例见 `deploy/demo/README.md`）。

## 3. 配置

全部配置来自环境变量（`server/internal/config/config.go`）。`server/.env.example`、`deploy/systemd/dsh-ee.env.example`、`deploy/install.sh` 生成的 `.env` 是三个起点模板。

| 环境变量 | 默认 | 必填 | 说明 |
|---|---|---|---|
| `DSH_EE_DSN` | — | 是 | MySQL 协议 DSN，如 `user@tenant:password@tcp(ob-host:2881)/dsh_ee` 或 `root:@tcp(127.0.0.1:4000)/dsh_ee`。库不存在且账号有权限时会尝试创建 |
| `DSH_EE_SECRET_KEY` | — | 是 | 32 字节 base64 主密钥，`openssl rand -base64 32`。用于 AES-256-GCM 加密存库的上游 key 与连接凭据；**丢了不可恢复** |
| `DSH_EE_ADDR` | `127.0.0.1:8080` | 否 | 监听地址；容器里固定 `0.0.0.0:8080` |
| `DSH_EE_BOOTSTRAP_ADMIN` | `admin` | 否 | 用户表为空时创建的管理员用户名 |
| `DSH_EE_BOOTSTRAP_ADMIN_PASSWORD` | 空 | 首次 | 初始管理员密码；用户表非空后不再使用 |
| `DSH_EE_COOKIE_SECURE` | `false` | HTTPS 时是 | 会话 cookie 加 `Secure`；走 HTTPS 必须设 `true` |
| `DSH_EE_SESSION_TTL` | `168h` | 否 | 网页会话有效期 |
| `DSH_EE_PUBLIC_URL` | 空 | 生产建议 | 对外地址，如 `https://ee.example.com`；生成 tarball/脚本/索引里的外部地址 |
| `DSH_EE_DATA_DIR` | `./data` | 否 | 数据目录；容器里 `/data` |
| `DSH_EE_DB_WAIT` | `2m` | 否 | 启动时等数据库就绪的最长时间，超时启动失败 |
| `DSH_EE_DATA_DSN` | 主库名 + `_data` | 否 | 数据中台库（可写账号）；设 `off` 关闭数据中台 |
| `DSH_EE_DATA_READ_DSN` | 自动创建只读账号 | 否 | 数据中台只读账号；账号没权限建用户时由 DBA 提供 |
| `DSH_EE_ADMIN_MODE_UNIT` | `1h` | 否 | **只给自动化测试**调整「管理模式 1/8 小时」里「1 小时」的长度，生产不要设 |
| `DSH_EE_DEMO_MODE` 等 `DSH_EE_DEMO_*` | 关闭 | 否 | 仅公开演示站使用（登录页显示初始账号、限制访问本机等）；`server/internal/config/config.go` 有完整列表 |

说明：

- **没有**从配置文件读取的设置（除 systemd/compose 的 env 文件）。大多数业务设置（npm 源、审核模式、登录方式、LDAP/OIDC/飞书、品牌、写确认方式、连接范围等）存在数据库里，在网页「系统设置」等页面改。
- **尚未合入基线的配置项**（合并后生效；当前设置不生效）：`DSH_EE_EMBED_ORIGINS`（微应用嵌入的宿主来源）、`DSH_EE_EMBED_CROSS_SITE`（跨站嵌入 v1 总开关，默认关闭，开启时必须同时配 origins）、`DSH_EE_EMBED_SESSION_TTL`（嵌入会话 TTL，默认 `2h`，允许 `1m`–`8h`）——以上见 PR #118；`DSH_EE_CORS_ORIGINS`（浏览器内第三方 Harness 的 CORS 白名单，默认关闭，见 PR #127）。合入后嵌入接线见 [troubleshooting.md](./troubleshooting.md) §6、[embedding.md](./embedding.md) 与 [cross-site-embedding.md](./cross-site-embedding.md)，CORS 见 §4.2。
- 数据库账号需要建库/建用户权限（数据中台会自动建 `<主库名>_data` 和只读账号）；若 DBA 不给，按 §3 的 `DSH_EE_DATA_*` 显式配置。

## 4. 首次启动与初始化

1. 确认数据库可达、`DSH_EE_DSN`/`DSH_EE_SECRET_KEY` 已设置。
2. 启动服务：首次会自动建库（如权限允许）、建表、按顺序执行迁移（`schema_migrations` 记录版本）。
3. 用户表为空且设置了 `DSH_EE_BOOTSTRAP_ADMIN_PASSWORD` 时创建初始管理员；没设置则日志告警「还没有任何用户」，此时应先补密码再重启。
4. 打开 `https://<域名>/login` 用初始管理员登录，改密码，按需要建角色/用户/模型接入/审核模式。
5. 验证：

```bash
curl -fsS https://<域名>/healthz          # {"status":"ok","version":"…"}
dsh-ee version                             # 二进制版本（容器里 docker compose exec dsh-ee dsh-ee version）
```

## 5. 升级

### 5.1 升级前

1. 读新版本发布说明与 `docs/` 里与本次升级相关的变更。
2. **备份**：数据库 + `DSH_EE_DATA_DIR` + `.env`（至少主密钥；见 §6）。
3. 只保留一个实例在跑：多个实例同时首次启动会争抢迁移（先起一个，完成后再扩容）。
4. 记下当前版本（`dsh-ee version`）和当前 `.env` 备份位置。

### 5.2 离线安装包升级

```bash
tar xzf dsh-ee-<新版本>-linux-amd64.tar.gz && cd dsh-ee-<新版本>-linux-amd64
cp /path/to/旧目录/.env .        # 保留主密钥、DSN、管理员密码
sha256sum -c SHA256SUMS
./install.sh                     # 载入新镜像、更新 .env 里的 DSH_EE_IMAGE、重建容器
curl -fsS https://<域名>/healthz
```

`install.sh` 的升级分支只改 `.env` 里的 `DSH_EE_IMAGE`，然后 `docker compose up -d`；数据在 compose 卷里不受影响。

### 5.3 二进制 + systemd 升级

```bash
install -m 755 dsh-ee /usr/local/bin/dsh-ee.new && mv /usr/local/bin/dsh-ee.new /usr/local/bin/dsh-ee
systemctl restart dsh-ee
journalctl -u dsh-ee -n 100 --no-pager
```

### 5.4 升级过程中的数据库迁移

- 服务启动时自动执行未应用的迁移。迁移历史上有新增表/列，也有删列、改菜单等变更（例如 `0003_rbac.sql`、`0013_build.sql`），**不是纯新增**；升级前按 §6 备份，并读本版本发布说明。
- 迁移失败会直接启动失败，日志里带错误；先修复数据库权限/连接再重启，**不要**手工改 `schema_migrations`。
- 迁移执行期间服务不可用；大表加列/建索引的耗时取决于数据量，建议在维护窗口升级，并在升级前按 §6 备份。

### 5.5 回滚

回滚 = 重新部署上一版二进制/镜像 + 保留原 `.env`（主密钥、DSN 不变）：

```bash
# 安装包方式
cd dsh-ee-<旧版本>-linux-amd64
cp /path/to/当前目录/.env .
./install.sh
# 或二进制方式：把旧 dsh-ee 换回去，systemctl restart dsh-ee
```

回滚前必须知道：

- 旧版通常忽略新版本新增的表和列（`server/README.md` 的升级说明），但迁移不保证只增；跨版本回滚前用备份与发布说明确认没有不兼容变更。
- **回滚后 `ee_at_` 统一令牌可能不可用**（`server/README.md`「升级与回滚」）：需要临时改回旧 `ee_npm_` / `sk-ee_` 令牌，或前滚回新版本。
- **没有自动回滚/降级迁移**；仓库没有保留上一版本的安装包，需自行存档。
- 数据库本身不做降级。跨大版本回滚建议直接用升级前的备份恢复到新库，而不是回滚二进制。

## 6. 备份与恢复（运维流程，不是内置功能）

> dsh-ee **没有内置备份/恢复命令**，也没有备份任务调度（[roadmap.md](./roadmap.md) 阶段 C 列了「备份与恢复说明并演练一次」，仍是待办）。下面是基于外部数据库工具的运维流程，工具名随数据库部署方式不同而变。

### 6.1 要备份什么

| 对象 | 内容 | 丢失后果 |
|---|---|---|
| 数据库 | 全部业务表（账号、令牌、npm 版本、连接、审计、用量…） | 灾难性；只能恢复到备份时间点 |
| `DSH_EE_DATA_DIR` | `blobs/`（已发布 tarball）、`brand/`、`dsh-releases/` 等 | 数据库里记录的已发布版本无法下载；品牌图丢失 |
| `.env` / 环境配置 | `DSH_EE_SECRET_KEY`、`DSH_EE_DSN`、管理员密码等 | 主密钥丢失 = 已存的上游 key、连接凭据永久解不开 |

### 6.2 建议流程

1. **数据库**：用数据库自己的备份工具做一致性备份（示例：TiDB 用 BR 或 Dumpling；OceanBase 用 OBDUMPER 或 OBS 备份；具体命令以对应版本官方文档为准）。至少要能恢复出一个与备份时刻一致的库。
2. **数据目录**：`rsync -a --delete` 或存储快照。建议在数据库备份完成后的短窗口内做，减少「库里有版本、blobs 里没有」的不一致。
3. **环境配置**：`.env`（或 systemd 的 env 文件）单独加密保存，和数据库备份分开存放；至少确认主密钥可读。
4. **记录版本**：`dsh-ee version` 与安装包版本一起记入备份说明。
5. **演练**：在测试环境按 §6.3 恢复一次，确认能登录、能下载已发布 tarball、能调用模型。

### 6.3 恢复到新实例

1. 准备新机器/新数据库；把备份恢复到数据库。
2. 把 `blobs/` 等内容放回 `DSH_EE_DATA_DIR`（目录属主要能被运行用户读写）。
3. 用**同一份** `DSH_EE_SECRET_KEY` 配置 `.env`，其余变量按新环境改（`DSH_EE_DSN`、`DSH_EE_PUBLIC_URL`、`DSH_EE_COOKIE_SECURE`）。
4. 启动服务并验证：
   - `GET /healthz` 返回 ok；
   - 管理员能登录；「我的令牌」列表存在；
   - 从 npm 源下载一个已发布版本的 tarball（例如 `curl -f https://<域名>/npm/@<作用域>/<包名>/-/<包名>-<版本>.tgz`，带作用域；按是否要求令牌加 `Authorization`）；
   - `/v1/models` 用有效令牌能返回模型；
   - `/admin/audit` 能看到历史审计。

### 6.4 注意事项

- **不一致的窗口**：数据库备份晚于 blobs 备份 → 恢复后可能出现「版本记录在、tarball 不在」；反之只是多了孤儿文件（可清理）。出现前者时，重新发布或从离线包导入。
- **主密钥不能换**：换 `DSH_EE_SECRET_KEY` 等于让所有已加密凭据失效；换之前要重新录入上游 key / 连接凭据。
- **审计/用量表会持续增长**：备份体积和恢复时间随之增长；保留期设置尚未实现（设计）。

## 7. 日志

| 运行方式 | 看日志 |
|---|---|
| docker compose / 离线包 | `docker compose logs -f dsh-ee`（数据库容器同理） |
| systemd | `journalctl -u dsh-ee -f`、`journalctl -u dsh-ee --since '1 hour ago'` |
| 直接跑二进制 | stderr（slog TextHandler），可 `2>&1` 重定向到文件 |

日志格式与字段（`server/cmd/dsh-ee/main.go`、`server/internal/api/server.go`）：

- 每个请求一行：`msg=http method=GET path=/api/… status=200 ms=…`（不记 query、不记请求体/令牌）。
- 启动/关闭：`dsh-ee 已启动 addr=… version=…`、`正在关闭`；启动失败 `启动失败 err=…` 后进程退出。
- 数据库未就绪：`数据库还没就绪，3 秒后重试`；超过 `DSH_EE_DB_WAIT` 后失败。
- 后台任务/告警：清理过期会话失败、写审计日志失败、上游请求失败/切换、数据中台没开起来、镜像任务失败等。

**没有实现的**：日志级别开关、文件轮转、syslog/webhook 外发、慢请求日志、请求 ID（设计）。需要长期保留时用容器日志驱动或 journald 的轮转配置在外部处理。**审计日志在数据库里，不在进程日志里**，见 [architecture.md](./architecture.md) §6。

## 8. 健康检查

| 端点 | 说明 |
|---|---|
| `GET /healthz` | 公开，返回 `{"status":"ok","version":"<版本>"}`；Docker `HEALTHCHECK` 与 `install.sh` 都用它判断就绪 |

- **它只证明 HTTP 进程活着，不检查数据库**。数据库断了 `/healthz` 仍然 200，但登录/接口会报错。
- 没有 `/readyz`、没有指标端点（设计）。
- 负载均衡建议：用 `/healthz` 做存活探针；不要把它当业务健康探针。业务健康可以人工用登录页、`/v1/models`（带令牌）、或 `GET /api/me/doctor`（登录用户的自检，见 [troubleshooting.md](./troubleshooting.md)）。

## 9. 容量与扩展

### 9.1 会增长的东西

| 位置 | 增长来源 | 观察方式 |
|---|---|---|
| 数据库 | `usage_logs`（每次模型调用）、`audit_logs`（每个操作）、`npm_downloads`（每次下载）、`connection_calls`、知识库正文、应用数据行 | 数据库监控；`SELECT COUNT(*)` / 表大小 |
| `DSH_EE_DATA_DIR` | 每个已发布/镜像版本的 tarball、上游缓存、DSH 安装包镜像 | `du -sh <data dir>`、`du -sh <data dir>/blobs` |
| 进程内存 | 上游 packument 缓存、知识库索引、流式连接、MCP 事件流 | 容器/进程 RSS |

### 9.2 单实例边界（当前实现）

- 登录限流、上游 packument 内存缓存、预载/导入任务、知识库索引都在进程内存里；**多实例会行为不一致**。
- tarball 在本地磁盘；多实例需要共享存储（NFS 等）或对象存储——对象存储是设计，没有实现。
- 多个实例同时首次启动会争抢数据库迁移：先起一个完成迁移，再起其余实例。
- 没有 `/mcp`、`/v1` 的每令牌限流；没有独立的队列/worker 进程。

### 9.3 扩容与容量规划建议

- 先扩容数据库（TiDB/OceanBase 按各自最佳实践加节点），dsh-ee 进程本身不是瓶颈。
- 给 `DSH_EE_DATA_DIR` 留足空间：至少按「已发布版本数 × 平均 tarball 体积 × 2」估，另外留上游缓存与离线包余量。
- 审计/用量表按合规要求制定清理策略（保留期功能未实现，设计）；清理前先确认没有报表依赖。
- 大流量前检查反向代理的请求体上限、超时和缓冲设置（§2.5）。

## 10. 安全运维

| 任务 | 做法 |
|---|---|
| 保护主密钥 | `.env` 权限 600、和数据库备份分开加密保存；不要提交到仓库；轮换主密钥会让已加密凭据失效，必须重新录入 |
| 初始管理员 | 首次登录立即改密码；不要长期用 `.env` 里的初始密码 |
| 令牌 | 在 `/tokens`（员工）或 `/admin/tokens`（管理员）查看、吊销；按部门/设备批量吊销；旧 `ee_npm_`/`sk-ee_` 令牌权限较宽，新集成用 `ee_at_` |
| 员工禁用 | `/admin/users` 禁用后立即踢下线，API key 与 npm 令牌失效 |
| 管理权限 | 用角色分配最小权限；紧急情况用管理模式（1/8 小时）而不是长期给 admin scope；防提权规则见 [architecture.md](./architecture.md) §5.3 |
| 把自己锁在外面 | 在能连数据库的服务器上执行 `dsh-ee admin recover`（读与服务和相同的环境变量），生成 1 小时有效的恢复账号；用完改好角色即弃用 |
| 写确认 | `/admin/settings` 或 `PUT /api/admin/write-confirm`：`non_dsh`（默认）/ `always` / `off`；第三方自动化写操作前评估 |
| 反向代理 | 正确设置 XFF；限制来源；审计里的 IP 依赖直连对端是回环/内网时的 XFF 最后一跳 |
| 数据中台 | 只读账号由 dsh-ee 自动创建并加密保存；DBA 接手时用 `DSH_EE_DATA_READ_DSN` 显式提供 |
| 恢复账号 | `dsh-ee admin recover` 每次生成新的 `recover-*` 用户，审计记 `admin.recover`（`via=cli`），到期自动停用 |

`dsh-ee admin recover` 的输出示例：

```text
已生成恢复账号（2026-11-12 14:05 前有效，到期自动停用）：
  用户名：recover-1a2b3c4d
  密码：  …
用它登录网页，把管理角色重新分好后就不要再用。
```

## 11. 常见运维任务速查

| 我要… | 做什么 |
|---|---|
| 看版本 | `dsh-ee version`；容器里 `docker compose exec dsh-ee dsh-ee version` |
| 重启 | `docker compose restart dsh-ee`；systemd `systemctl restart dsh-ee` |
| 看日志 | §7 |
| 判断进程是否活 | `curl -fsS http://127.0.0.1:8080/healthz` |
| 看数据库连不上 | 日志里的 `连接数据库: …`；检查 DSN、网络、库权限；`DSH_EE_DB_WAIT` 内会重试 |
| 升级 | §5；升级前备份 §6 |
| 回滚 | §5.5；注意 `ee_at_` 令牌与新增表 |
| 备份/恢复 | §6（外部工具流程，非内置） |
| 解锁管理员 | `dsh-ee admin recover` |
| 吊销某个人的令牌 | `/admin/tokens`；或员工自己在 `/tokens` 吊销 |
| 关掉/打开数据中台 | `DSH_EE_DATA_DSN=off` 重启；开不起来只影响这一块 |
| 检查插件发布链路（PR #122，合并后生效） | `dsh-ee doctor`、`dsh-ee package check <tgz>`（见 [cli.md](./cli.md)；能力以 #122 合入为准） |
| 排查报错 | [troubleshooting.md](./troubleshooting.md) |

## 12. 明确没有实现（设计）

有独立 PR、尚未合入基线的能力：微应用嵌入 v0/v1（#118）、`package check`/`doctor`（#122）、Harness 发现（#123）、浏览器内 CORS 白名单（#127）、MCP `tools/list` 分页与写确认 `_meta`（#128）；能力以对应 PR 合入为准。下表是真正没有实现、也没有对应 PR 的部分。

| 能力 | 现状 |
|---|---|
| 内置备份/恢复命令、定时备份、演练脚本 | 没有；按 §6 用外部工具 |
| 自动升级、降级迁移、版本回滚工具 | 没有；手工换二进制/安装包 |
| 指标（Prometheus）、链路追踪、慢请求日志、请求 ID | 没有；只有文本日志与 `/healthz` |
| 日志级别/轮转/syslog/Webhook | 没有；在外部日志设施处理 |
| 审计导出、保留期、外发 | 没有；只有 `/admin/audit` 页面查询 |
| 多实例/共享存储/对象存储 | 没有；单实例、本地磁盘 |
| 零停机升级、滚动升级 | 没有；升级期间服务不可用 |
| 容量/限流面板、每令牌限流 | 没有；见 [architecture.md](./architecture.md) §7 |
| 备份一致性编排（数据库 + blobs 同一快照） | 没有；靠运维窗口与流程约束 |
