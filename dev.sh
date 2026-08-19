#!/usr/bin/env bash
# 开发模式：后端(:8787) + 流量代理(:8788) 与 前端 next dev(:5173) 一起跑。
# 前端 /api 反代到后端；Ctrl-C 一并退出。
#
# 单二进制（前端内嵌）方式见 README「单二进制」一节，不走这个脚本。
set -euo pipefail
cd "$(dirname "$0")"

# 本地开发时加载 .env 里的环境变量（POPO 凭据等）
set -a; [ -f .env ] && source .env; set +a

# 收集残留进程 PID：按端口（只匹配 LISTEN 状态，避免误杀浏览器等
# 客户端连接）和进程名兜底（go run 父进程、artex 子进程、next dev 前端）。
collect_pids() {
  for port in 8787 8788 5173; do
    lsof -ti :"$port" -sTCP:LISTEN 2>/dev/null || true
  done
  pgrep -f 'go run ./cmd/artex' 2>/dev/null || true
  pgrep -f 'artex -addr' 2>/dev/null || true
  pgrep -x artex 2>/dev/null || true
  pgrep -f 'next dev' 2>/dev/null || true
}

# 等待端口完全释放（kill 后端口可能需要短暂时间才可用）
wait_ports_free() {
  for _ in $(seq 1 30); do
    local busy=0
    for port in 8787 8788 5173; do
      lsof -ti :"$port" -sTCP:LISTEN >/dev/null 2>&1 && busy=1 || true
    done
    [ "$busy" -eq 0 ] && return 0
    sleep 0.3
  done
  return 1
}

# ── 启动前清理上次残留 ──
pids=$(collect_pids | sort -u | tr '\n' ' ')
if [ -n "${pids// /}" ]; then
  echo "[dev] 清理上次残留进程: $pids"
  kill $pids 2>/dev/null || true
  sleep 1
  still=$(collect_pids | sort -u | tr '\n' ' ')
  if [ -n "${still// /}" ]; then
    echo "[dev] 强杀未退出进程: $still"
    kill -9 $still 2>/dev/null || true
  fi
fi
# 确保端口完全释放后再启动，避免 bind: address already in use
wait_ports_free || echo "[dev] 警告: 部分端口仍被占用，可能影响启动"

# ── 退出时清理 ──
BACKEND_PID=0
FRONTEND_PID=0
_CLEANING=false

cleanup() {
  # 防止递归：信号可能同时触发 EXIT 和 INT/TERM trap
  $_CLEANING && return 0
  _CLEANING=true

  # 先 SIGTERM 已知的直接子进程（不使用 kill 0，避免信号回传自身）
  [ "$BACKEND_PID" -gt 0 ] 2>/dev/null && kill "$BACKEND_PID" 2>/dev/null || true
  [ "$FRONTEND_PID" -gt 0 ] 2>/dev/null && kill "$FRONTEND_PID" 2>/dev/null || true
  sleep 1

  # 兜底：按端口/进程名强杀残留（go run 编译的 artex 子进程可能
  # 脱离进程组，npm 子进程 next dev 也可能被孤儿化）
  pids=$(collect_pids | sort -u | tr '\n' ' ')
  if [ -n "${pids// /}" ]; then
    echo "[dev] 退出时强杀残留进程: $pids"
    kill -9 $pids 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

# 后端（普通 go run，不内嵌前端）；并发 work agent 数在「系统设置」里配置。
go run ./cmd/artex -addr :8787 -proxy :8788 &
BACKEND_PID=$!

# 前端热更新（Vite/Next dev server，/api 反代到 :8787）。
( cd web && npm run dev -- -p 5173 ) &
FRONTEND_PID=$!

echo "[dev] 后端 :8787 / 代理 :8788 / 前端 http://localhost:5173  (Ctrl-C 退出)"

# set -e 下 wait 返回非零（子进程退出码非 0）会触发提前退出，
# 用 || true 吞掉退出码。wait 无参数会阻塞直到所有后台作业结束。
wait || true
