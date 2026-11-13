#!/usr/bin/env sh
# dsh-ee 安装 / 升级脚本，在离线安装包解压目录里执行。
#
#   ./install.sh tidb        dsh-ee + 单机 TiDB 集群（默认）
#   ./install.sh oceanbase   dsh-ee + 单机 OceanBase（宿主机至少 8 GB 内存）
#   ./install.sh external    只起 dsh-ee，连接已有数据库：先在 .env 里填 DSH_EE_DSN
#
# 第一次执行会生成 .env（主密钥、管理员密码），之后再执行只做升级：载入新镜像并重建容器。
# .env 里的 DSH_EE_SECRET_KEY 丢了，已存的上游模型 key 就解不开了，请和数据库一起备份。
set -eu
cd "$(dirname "$0")"

db="${1:-}"
if [ -f .env ] && [ -z "$db" ]; then
  db="$(sed -n 's/^DSH_EE_DB=//p' .env)"
fi
db="${db:-tidb}"
case "$db" in
  tidb) files="compose.yml:compose.tidb.yml" ;;
  oceanbase) files="compose.yml:compose.oceanbase.yml" ;;
  external) files="compose.yml" ;;
  *) echo "用法：$0 [tidb|oceanbase|external]" >&2; exit 2 ;;
esac

command -v docker >/dev/null || { echo "需要先安装 docker" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "需要 docker compose v2（docker compose 子命令）" >&2; exit 1; }

version="$(cat VERSION 2>/dev/null || echo latest)"
if [ -f images.tar.gz ]; then
  echo "· 载入镜像（离线包）"
  gunzip -c images.tar.gz | docker load
fi

rand() { head -c 64 /dev/urandom | od -An -tx1 | tr -d ' \n' | cut -c1-"$1"; }

if [ ! -f .env ]; then
  echo "· 生成 .env"
  admin_pw="$(rand 20)"
  umask 077
  {
    echo "# 由 install.sh 生成。DSH_EE_SECRET_KEY 加密存库的上游 key，请和数据库一起备份。"
    echo "DSH_EE_DB=$db"
    echo "COMPOSE_FILE=$files"
    echo "DSH_EE_IMAGE=dsh-ee:$version"
    echo "DSH_EE_SECRET_KEY=$(head -c 32 /dev/urandom | base64)"
    echo "DSH_EE_BOOTSTRAP_ADMIN=admin"
    echo "DSH_EE_BOOTSTRAP_ADMIN_PASSWORD=$admin_pw"
    echo "# 员工访问的地址；部署在 HTTPS 反向代理后面时改成 https://…（cookie 会自动加 Secure）"
    echo "DSH_EE_PUBLIC_URL="
    echo "# 留空时按 PUBLIC_URL 自动判断；显式 true/false 可覆盖"
    echo "DSH_EE_COOKIE_SECURE="
    echo "DSH_EE_PORT=8080"
    if [ "$db" = oceanbase ]; then
      echo "OB_TENANT_PASSWORD=$(rand 24)"
      echo "OB_SYS_PASSWORD=$(rand 24)"
    fi
    if [ "$db" = external ]; then
      echo "# 例：user@tenant:password@tcp(ob-host:2881)/dsh_ee 或 root:@tcp(tidb-host:4000)/dsh_ee"
      echo "DSH_EE_DSN="
    fi
  } > .env
  echo "  初始管理员：admin / $admin_pw （登录后请修改）"
else
  # 升级：只更新镜像版本
  sed -i.bak "s/^DSH_EE_IMAGE=.*/DSH_EE_IMAGE=dsh-ee:$version/" .env && rm -f .env.bak
fi

if [ "$db" = external ] && ! grep -q '^DSH_EE_DSN=.' .env; then
  echo "external 模式需要先在 .env 里填 DSH_EE_DSN，然后重新执行 $0" >&2
  exit 1
fi

echo "· 启动（${files}）"
docker compose up -d

port="$(sed -n 's/^DSH_EE_PORT=//p' .env)"
port="${port:-8080}"
printf "· 等待 dsh-ee 就绪"
i=0
until wget -qO- "http://127.0.0.1:$port/healthz" >/dev/null 2>&1 || curl -fs "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ $i -gt 200 ]; then
    echo; echo "等待超时，查看日志：docker compose logs dsh-ee" >&2
    exit 1
  fi
  printf "."
  sleep 5
done
echo
echo "dsh-ee 已启动：http://<本机地址>:$port"
