#!/usr/bin/env bash
# ARTEX 一键远程部署：本地打包 → 上传 → 远端构建镜像并启动。
#
# 用法：./deploy.sh <user@host> [目标架构]
# 可选环境变量：
#   REMOTE_DIR=artex          远端部署目录
#   SSH_PORT=22               SSH 端口
#   ARTEX_TASK_URL=...        写入远端 .env 的 ARTEX_TASK_URL
set -euo pipefail

cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "用法: $0 <user@host> [amd64|arm64]" >&2
  exit 1
fi

REMOTE_HOST="$1"
ARCH="${2:-amd64}"
REMOTE_DIR="${REMOTE_DIR:-artex}"
SSH_PORT="${SSH_PORT:-22}"
PACKAGE="artex-deploy.tar.gz"
REMOTE_TMP="/tmp/artex-deploy-${RANDOM}"

case "$ARCH" in
  amd64|arm64) ;;
  *) die "目标架构仅支持 amd64 或 arm64" ;;
esac

case "$REMOTE_DIR" in
  *[[:space:]]*|*[\'\"]*) die "REMOTE_DIR 不能包含空格或引号" ;;
  *) ;;
esac

command -v ssh >/dev/null 2>&1 || die "未检测到 ssh"
command -v scp >/dev/null 2>&1 || die "未检测到 scp"

info "生成本地部署包（Linux/${ARCH}）"
ARTEX_TASK_URL="${ARTEX_TASK_URL:-}" ./pack.sh "$ARCH"

info "上传部署包到 ${REMOTE_HOST}:${REMOTE_DIR}"
scp -P "$SSH_PORT" "$PACKAGE" "${REMOTE_HOST}:${REMOTE_TMP}.tar.gz"

info "远端构建镜像并启动"
ssh -p "$SSH_PORT" "$REMOTE_HOST" "
set -eu
tmp='${REMOTE_TMP}'
trap 'rm -rf \"\$tmp\" \"\$tmp.tar.gz\"' EXIT
mkdir -p \"\$tmp\" '${REMOTE_DIR}'
tar -xzf \"\$tmp.tar.gz\" -C \"\$tmp\"
cp -a \"\$tmp/artex-deploy/.\" '${REMOTE_DIR}/'
cd '${REMOTE_DIR}'
./build-local.sh
"

ok "远程部署完成：${REMOTE_HOST}:${REMOTE_DIR}"
