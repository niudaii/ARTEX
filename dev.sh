#!/usr/bin/env bash
# 开发模式：后端(:8787) + 流量代理(:8788) 与 前端 next dev(:5173) 一起跑。
# 前端 /api 反代到后端；Ctrl-C 一并退出。
#
# 单二进制（前端内嵌）方式见 README「单二进制」一节，不走这个脚本。
set -euo pipefail
cd "$(dirname "$0")"

# 本地开发时加载 .env 里的环境变量（POPO 凭据等）
set -a; [ -f .env ] && source .env; set +a

# 启动前清理上次残留进程：先按端口找（8787/8788/5173），再按进程名兜底
# （go run 父进程、已释放端口但 graceful shutdown 没退完的 artex 子进程）。
collect_pids() {
  for port in 8787 8788 5173; do
    lsof -ti :"$port" 2>/dev/null || true
  done
  pgrep -f 'go run ./cmd/artex' 2>/dev/null || true
  pgrep -f 'exe/artex' 2>/dev/null || true
  pgrep -x artex 2>/dev/null || true
}

pids=$(collect_pids | sort -u | tr '\n' ' ')
if [ -n "${pids// /}" ]; then
  echo "[dev] 清理上次残留进程: $pids"
  kill $pids 2>/dev/null || true
  sleep 0.5
  still=$(collect_pids | sort -u | tr '\n' ' ')
  if [ -n "${still// /}" ]; then
    echo "[dev] 强杀未退出进程: $still"
    kill -9 $still 2>/dev/null || true
  fi
fi

# 退出时结束本进程组内的所有子进程（后端 + 前端）。
# kill 0 后等待 1s，再按端口/进程名兜底强杀残留（go run 编译的 artex 子进程
# 卡在 graceful shutdown 时会脱离进程组，kill 0 杀不到）。
cleanup() {
  kill 0 2>/dev/null || true
  sleep 1
  pids=$(collect_pids | sort -u | tr '
' ' ')
  if [ -n "${pids// /}" ]; then
    echo "[dev] 退出时强杀残留进程: $pids"
    kill -9 $pids 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

# 后端（普通 go run，不内嵌前端）；并发 work agent 数在「系统设置」里配置。
go run ./cmd/artex -addr :8787 -proxy :8788 &

# 前端热更新（Vite/Next dev server，/api 反代到 :8787）。
( cd web && npm run dev -- -p 5173 ) &

echo "[dev] 后端 :8787 / 代理 :8788 / 前端 http://localhost:5173  (Ctrl-C 退出)"
wait
