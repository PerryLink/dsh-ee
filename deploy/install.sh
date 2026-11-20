#!/usr/bin/env sh
# dsh-ee 安装 / 升级脚本。
#
# 在线安装（拉 GHCR 镜像，不用下载离线包）：
#   curl -fsSL https://raw.githubusercontent.com/hikariming/dsh-ee/main/deploy/install.sh | sh
#   curl -fsSL …/install.sh | sh -s -- oceanbase      # 选数据库，参数同下
#   会在当前目录下建 dsh-ee/（可用 DSH_EE_DIR 改），以后在那里执行 ./install.sh 升级。
#   DSH_EE_VERSION 指定版本（默认最新发布），DSH_EE_IMAGE 指定镜像，DSH_EE_PORT 指定端口。
#
# 离线安装：在离线安装包解压目录里执行 ./install.sh。
#
#   ./install.sh tidb        dsh-ee + 单机 TiDB 集群（默认）
#   ./install.sh oceanbase   dsh-ee + 单机 OceanBase（宿主机至少 8 GB 内存）
#   ./install.sh external    只起 dsh-ee，连接已有数据库：先在 .env 里填 DSH_EE_DSN
#
# 第一次执行会生成 .env（主密钥、管理员密码），之后再执行只做升级：更新镜像并重建容器。
# .env 里的 DSH_EE_SECRET_KEY 丢了，已存的上游模型 key 就解不开了，请和数据库一起备份。
set -eu

repo="hikariming/dsh-ee"
here="$(dirname "$0")"
if [ -f "$here/VERSION" ] && [ -f "$here/compose.yml" ]; then
  # 离线包：VERSION 只在离线包里有
  mode=bundle
  cd "$here"
else
  mode=online
  if [ ! -f "$here/compose.yml" ] || [ ! -f "$here/.env" ]; then
    # curl | sh：在 DSH_EE_DIR（默认 ./dsh-ee）里安装
    mkdir -p "${DSH_EE_DIR:-dsh-ee}"
    cd "${DSH_EE_DIR:-dsh-ee}"
  else
    cd "$here"
  fi
  command -v curl >/dev/null || { echo "在线安装需要 curl" >&2; exit 1; }
fi

db="${1:-}"
if [ -f .env ] && [ -z "$db" ]; then
  db="$(sed -n 's/^DSH_EE_DB=//p' .env)"
fi
db="${db:-tidb}"
case "$db" in
  tidb) files="compose.yml:compose.tidb.yml" ;;
  oceanbase) files="compose.yml:compose.oceanbase.yml" ;;
  external) files="compose.yml" ;;
  *) echo "用法：install.sh [tidb|oceanbase|external]" >&2; exit 2 ;;
esac

command -v docker >/dev/null || { echo "需要先安装 docker" >&2; exit 1; }
docker compose version >/dev/null 2>&1 || { echo "需要 docker compose v2（docker compose 子命令）" >&2; exit 1; }

if [ "$mode" = bundle ]; then
  version="$(cat VERSION)"
  image="dsh-ee:$version"
  if [ -f images.tar.gz ]; then
    echo "· 载入镜像（离线包）"
    gunzip -c images.tar.gz | docker load
  fi
else
  version="${DSH_EE_VERSION:-}"
  if [ -z "$version" ]; then
    # 最新发布：跟随 /releases/latest 的跳转拿到 tag
    version="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest" | sed -n 's#.*/tag/##p')"
    [ -n "$version" ] || { echo "查不到最新版本，请用 DSH_EE_VERSION=v0.x.y 指定" >&2; exit 1; }
  fi
  image="${DSH_EE_IMAGE:-ghcr.io/$repo:$version}"
  raw="${DSH_EE_RAW_BASE:-https://raw.githubusercontent.com/$repo/$version/deploy}"
  echo "· 下载 $version 的部署文件"
  for f in compose.yml compose.tidb.yml compose.oceanbase.yml install.sh; do
    curl -fsSL "$raw/$f" -o "$f.tmp" || { rm -f ./*.tmp; echo "下载 $raw/$f 失败" >&2; exit 1; }
  done
  for f in compose.yml compose.tidb.yml compose.oceanbase.yml; do mv "$f.tmp" "$f"; done
  # 早于在线安装的版本里 install.sh 只认离线包，不能拿它替换自己
  if grep -q '^  mode=online' install.sh.tmp; then
    mv install.sh.tmp install.sh
  else
    rm -f install.sh.tmp
    [ -f install.sh ] || cp "$0" install.sh 2>/dev/null || curl -fsSL "https://raw.githubusercontent.com/$repo/main/deploy/install.sh" -o install.sh
  fi
  chmod +x install.sh
  echo "· 拉取镜像 $image"
  docker pull "$image"
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
    echo "DSH_EE_IMAGE=$image"
    echo "DSH_EE_SECRET_KEY=$(head -c 32 /dev/urandom | base64)"
    echo "DSH_EE_BOOTSTRAP_ADMIN=admin"
    echo "DSH_EE_BOOTSTRAP_ADMIN_PASSWORD=$admin_pw"
    echo "# 员工访问的地址；部署在 HTTPS 反向代理后面时改成 https://…（cookie 会自动加 Secure）"
    echo "DSH_EE_PUBLIC_URL="
    echo "# 留空时按 PUBLIC_URL 自动判断；显式 true/false 可覆盖"
    echo "DSH_EE_COOKIE_SECURE="
    echo "DSH_EE_PORT=${DSH_EE_PORT:-8080}"
    if [ "$db" = oceanbase ]; then
      echo "OB_TENANT_PASSWORD=$(rand 24)"
      echo "OB_SYS_PASSWORD=$(rand 24)"
    fi
    if [ "$db" = external ]; then
      echo "# 例：user@tenant:password@tcp(ob-host:2881)/dsh_ee 或 root:@tcp(tidb-host:4000)/dsh_ee"
      echo "DSH_EE_DSN="
    fi
  } > .env
else
  # 升级：只更新镜像版本
  sed -i.bak "s#^DSH_EE_IMAGE=.*#DSH_EE_IMAGE=$image#" .env && rm -f .env.bak
fi

if [ "$db" = external ] && ! grep -q '^DSH_EE_DSN=.' .env; then
  echo "external 模式需要先在 $(pwd)/.env 里填 DSH_EE_DSN，然后在该目录执行 ./install.sh" >&2
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
host="$(hostname -I 2>/dev/null | awk '{print $1}')"
echo "dsh-ee $version 已启动：http://${host:-<本机地址>}:$port"
if [ -n "${admin_pw:-}" ]; then
  echo "  初始管理员：admin / $admin_pw （登录后请修改）"
fi
echo "  安装目录：$(pwd)；升级时在这里执行 ./install.sh"
