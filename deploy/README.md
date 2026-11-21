# 部署 dsh-ee

dsh-ee 是一个文件：前端嵌在 Go 二进制里，同一个端口提供界面、接口（`/api`、`/v1`）、npm 源（`/npm/`）与 DSH 插件索引（`/index/dsh-index.json`）。只依赖一个 MySQL 协议的数据库（OceanBase MySQL 模式或 TiDB）和一个数据目录（npm 包的 tarball）。

## 一条命令在线安装

机器能访问 GitHub 与 GHCR 时最省事，只需要 docker 与 docker compose v2：

```bash
curl -fsSL https://raw.githubusercontent.com/hikariming/dsh-ee/main/deploy/install.sh | sh                  # dsh-ee + 单机 TiDB
curl -fsSL https://raw.githubusercontent.com/hikariming/dsh-ee/main/deploy/install.sh | sh -s -- oceanbase  # 或 external
```

脚本在当前目录下建 `dsh-ee/`（`DSH_EE_DIR` 可改），下载该版本的 compose 文件，拉取 `ghcr.io/hikariming/dsh-ee:<版本>`，生成 `.env` 并启动，最后打印访问地址与管理员密码。可选环境变量：

| 变量 | 作用 |
| --- | --- |
| `DSH_EE_VERSION` | 安装的版本，默认最新发布，如 `v0.3.0` |
| `DSH_EE_IMAGE` | 换镜像，例如内网镜像仓库里的副本 |
| `DSH_EE_PORT` | 宿主机端口，默认 8080 |
| `TIDB_REGISTRY` | TiDB 镜像来源，默认 Docker Hub 的 `pingcap`，拉不动时可换镜像站 |

升级：在 `dsh-ee/` 里执行 `./install.sh`（或 `DSH_EE_VERSION=v0.x.y ./install.sh`），会下载新版本的 compose 文件、拉新镜像、重建容器，`.env` 与数据不动。

## 用离线安装包（推荐给内网环境）

安装包 `dsh-ee-<版本>-linux-<amd64|arm64>.tar.gz` 里有 dsh-ee 与数据库的镜像、compose 文件、安装脚本、静态二进制与 systemd 单元。目标机器只需要 docker 与 docker compose v2，不需要外网。

```bash
tar xzf dsh-ee-v0.1.0-linux-amd64.tar.gz && cd dsh-ee-v0.1.0-linux-amd64
sha256sum -c SHA256SUMS

./install.sh tidb        # dsh-ee + 单机 TiDB（PD + TiKV + TiDB）
./install.sh oceanbase   # dsh-ee + 单机 OceanBase（宿主机至少 2 核 8 GB）
./install.sh external    # 只起 dsh-ee，连接已有数据库：先在 .env 里填 DSH_EE_DSN
```

第一次执行会生成 `.env`（主密钥 `DSH_EE_SECRET_KEY`、初始管理员密码），并打印管理员密码。**`.env` 要和数据库一起备份**：主密钥丢了，已存的上游模型 key 就解不开了。

升级：解压新版本的安装包，把旧目录里的 `.env` 拷过去，再执行一次 `./install.sh`（会载入新镜像、重建 dsh-ee 容器，数据在 docker 卷里不受影响）。

### 正式环境的数据库

OceanBase 官方镜像只用于测试与试点。正式环境请用 OBD / OCP 部署 OceanBase 集群，建一个 MySQL 模式的租户，然后 `./install.sh external`，DSN 形如：

```
DSH_EE_DSN='dsh_ee@your_tenant:password@tcp(ob-host:2881)/dsh_ee'
```

库不存在时 dsh-ee 会尝试创建；没有建库权限时先手工 `CREATE DATABASE dsh_ee DEFAULT CHARACTER SET utf8mb4`。

### 数据中台

「企业连接 → 数据中台」用一个单独的库存放上传的表。默认在主库同一个服务器上建 `<主库名>_data`（如 `dsh_ee_data`），并用主库账号自动建一个只有这个库 SELECT 权限的账号（`<库名>_reader`）来执行数据集查询，密码随机生成、加密存在设置里。

- 账号没有建库或建用户的权限时：请 DBA 建好库和只读账号，填 `DSH_EE_DATA_DSN`（可写账号，用于上传）和 `DSH_EE_DATA_READ_DSN`（只读账号）；
- 不需要这块：`DSH_EE_DATA_DSN=off`；
- 开不起来时只影响数据中台，页面上会说明原因，其余功能照常。

### HTTPS 与访问地址

dsh-ee 本身只提供 HTTP，放在企业的反向代理 / 负载均衡后面终止 TLS。代理后面需要：

- `.env` 里设 `DSH_EE_PUBLIC_URL=https://dsh.example.internal`（npm 的 tarball 下载地址按它生成），`DSH_EE_COOKIE_SECURE=true`；
- 或者让代理传 `X-Forwarded-Proto` 与 `X-Forwarded-Host`。只有来自可信代理的这些头才会被采信，默认只信任本机回环地址；
  反向代理不在同一台机器（dsh-ee 在容器里由宿主机 nginx 转发、k8s ingress、云负载均衡）时，要把代理地址或网段填进
  `DSH_EE_TRUSTED_PROXIES`（如 docker 默认网桥 `172.17.0.0/16`），否则审计与按 IP 限流记录的客户端地址是代理地址。
- 允许较大的请求体（npm publish 与离线包导入，至少 200 MB），并关闭对 `/api/admin/upstream/import` 的读超时或调大。

### 登录限流与监控

- 登录失败先按用户名锁（15 分钟 5 次），再按客户端 IP 固定窗口限流：默认 15 分钟 50 次，只统计用户名或密码错误，触发后返回 429 并带 `Retry-After`。公司很多人共用一个出口 IP 时可调大 `DSH_EE_LOGIN_IP_LIMIT`，或设成 `0` / `off` 关闭；窗口长度用 `DSH_EE_LOGIN_IP_WINDOW` 调整。
- 需要接 Prometheus 时设 `DSH_EE_METRICS=true`，从本机或内网抓 `GET /metrics`；指标只有进程运行信息、登录失败 / 限流计数、跨站嵌入的活跃会话 / 拒绝计数，以及网关记账成功的推理 / 缓存命中 token 累计数，不含账号、IP、模型或具体请求内容。多实例部署时 `dsh_ee_embed_sessions_active` 是数据库里的全库值，聚合用 `max()` 而不是 `sum()`；其余计数在进程内累计，重启清零、多实例各自独立。

### 健康检查与探针

dsh-ee 提供两个不用登录的探针，语义不同（响应形状见 [../server/README.md](../server/README.md#健康检查)）：

- `GET /healthz`：**liveness**，只证明进程能响应，恒为 200；用来判断「要不要重启这个实例」。
- `GET /readyz`：**readiness**，检查数据库连接、迁移是否已到当前二进制的最新版本、数据目录是否可写、主密钥是否已加载，整体超时 2 秒；全部通过 200，任一失败 503 `{"status":"unready",…}`。用来判断「要不要把流量打给这个实例」。

Kubernetes：

```yaml
livenessProbe:
  httpGet: {path: /healthz, port: 8080}
  periodSeconds: 15
  timeoutSeconds: 3
  failureThreshold: 3
readinessProbe:
  httpGet: {path: /readyz, port: 8080}
  periodSeconds: 10
  timeoutSeconds: 3
  failureThreshold: 2
```

Docker / Compose：镜像内置的 `HEALTHCHECK` 打 `/healthz`（容器是否活）；`restart: unless-stopped` 不会因为 unhealthy 而重启，readiness 仍由前面的负载均衡调 `/readyz`。如果希望 `docker ps` 里看到的健康状态也反映依赖，可在 compose 里覆盖成一个 readiness 检查：

```yaml
healthcheck:
  test: ["CMD", "wget", "-qO-", "http://127.0.0.1:8080/readyz"]
  interval: 15s
  timeout: 3s
  retries: 3
```

systemd：没有内置 readiness 探针，`/healthz` 只用于存活；需要就绪判断时由外部监控 / 负载均衡调 `/readyz`，或按 [systemd/dsh-ee.service](systemd/dsh-ee.service) 里的注释在启动后跑一次。

## 不用 docker

```bash
install -m 755 dsh-ee /usr/local/bin/dsh-ee
useradd --system --home /var/lib/dsh-ee --create-home dsh-ee
install -d -m 700 /etc/dsh-ee
install -m 600 systemd/dsh-ee.env.example /etc/dsh-ee/dsh-ee.env   # 编辑：DSN、主密钥、管理员密码
cp systemd/dsh-ee.service /etc/systemd/system/
systemctl enable --now dsh-ee
```

## 在线使用 GHCR 镜像

每次 push 发布 tag，image workflow 校验 tag 在主干上后，自动构建并推送 linux/amd64 与 linux/arm64 镜像。
完整版本标签如 `v0.3.0`；稳定版另更新 `0.3` 与 `latest`，预发布（rc / beta / alpha 等）不更新浮动标签。
镜像可以匿名拉取，上面的[一条命令在线安装](#一条命令在线安装)用的就是它。已有数据库时也可以只跑一个容器，只需要 DSN：

```bash
docker run -d --name dsh-ee -p 8080:8080 -v dsh-ee-data:/data \
  -e DSH_EE_DSN='root:@tcp(tidb-host:4000)/dsh_ee' \
  ghcr.io/hikariming/dsh-ee:v0.3.0
docker logs dsh-ee 2>&1 | grep 初始化码
```

没设 `DSH_EE_SECRET_KEY` 时，dsh-ee 把主密钥生成到数据目录的 `secret.key`（这里是 `dsh-ee-data` 卷里的 `/data/secret.key`），**要和数据库一起备份**。
没有任何用户、也没设 `DSH_EE_BOOTSTRAP_ADMIN_PASSWORD` 时，启动日志会打印一次性的初始化码；用浏览器打开 dsh-ee，登录页会变成「创建管理员」，填初始化码即可。初始化码只在这次进程里有效，重启会换新的；首次初始化时请只起一个实例。

Compose 会按主机架构选择镜像。正式环境建议固定完整版本，不使用 `latest`。
已存在的 tag 可手动触发 image workflow 补发，见 [发布流程](../docs/RELEASING.md) §4。

## 自己打安装包

在能上网、装了 docker（含 buildx）、Go 与 Node 的机器上：

```bash
make bundle VERSION=v0.1.0 ARCH=amd64     # 或 ARCH=arm64（鲲鹏等）
WITH_DB=tidb make bundle VERSION=v0.1.0   # 只带 TiDB 镜像，包小一些
```

产物在 `deploy/out/`。离线包仍手工构建、归档，不由 GHCR workflow 自动生成或上传。

镜像的基础镜像版本由 `Dockerfile` 决定：Go 阶段固定为 `golang:1.27.4-alpine`（与 `server/go.mod` 的 `go 1.27.4` 对齐，避免浮动 tag 换 patch 后构建环境漂移），前端阶段用 `node:22-alpine`（与 CI 的 Node 22 一致），pnpm 由 `web/package.json` 的 `packageManager` 经 corepack 固定。升级 Go/Node 大版本时要把 `go.mod`、`Dockerfile` 与 CI 一起改，同一 commit 才能复现同一镜像。

## 开发环境直接用 compose

```bash
cd deploy
cp ../server/.env.example .env   # 填 DSH_EE_SECRET_KEY 等；OceanBase 还要 OB_TENANT_PASSWORD / OB_SYS_PASSWORD
docker build -t dsh-ee:latest ..
docker compose -f compose.yml -f compose.tidb.yml up -d
```
