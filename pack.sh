#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# ARTEX 公网部署打包脚本（本地二进制 bind-mount 模式）
#
# 用法：./pack.sh [部署URL] [目标架构]
#   部署URL：如 http://1.2.3.4:8787（写入 .env 的 ARTEX_TASK_URL）
#   目标架构：amd64（默认）/ arm64
#
# 产出 artex-deploy/，内含交叉编译的 Linux 二进制 + 一键启动脚本
# 更新二进制：重新编译 → scp 覆盖 → docker compose restart
# ─────────────────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

DEST="artex-deploy"
DEPLOY_URL="${1:-}"
ARCH="${2:-amd64}"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

# ── 检查 Go ──
command -v go >/dev/null 2>&1 || die "未检测到 Go，请先安装 Go >= 1.26"
ok "Go: $(go version)"

# ── 检查前端静态产物 ──
if [ ! -d server/webui/dist ] || [ -z "$(ls -A server/webui/dist 2>/dev/null)" ]; then
    info "前端静态产物不存在，开始构建…"
    command -v npm >/dev/null 2>&1 || die "未检测到 npm，请先安装 Node.js"
    ( cd web && npm ci && npm run build:static )
    mkdir -p server/webui && rm -rf server/webui/dist && cp -r web/out server/webui/dist
    ok "前端静态产物已就绪"
else
    ok "前端静态产物已存在（server/webui/dist/）"
fi

# ── 交叉编译 Linux 二进制 ──
info "交叉编译 Linux/${ARCH} 二进制…"
rm -rf "$DEST" && mkdir -p "$DEST"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -tags embedui -trimpath -o "$DEST/artex" ./cmd/artex
chmod +x "$DEST/artex"
ok "已编译 $DEST/artex（Linux/${ARCH}, $(du -h "$DEST/artex" | awk '{print $1}')）"

# ── docker-compose.yml（bind-mount 二进制覆盖镜像内置的）────────
cat > "$DEST/docker-compose.yml" << 'YML'
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_USER: ${POSTGRES_USER:-artex}
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?请在 .env 设置 POSTGRES_PASSWORD}
      POSTGRES_DB: ${POSTGRES_DB:-artex}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER:-artex} -d ${POSTGRES_DB:-artex}"]
      interval: 5s
      timeout: 3s
      retries: 10
    restart: unless-stopped

  artex:
    image: autumn27/artex:${ARTEX_TAG:-latest}
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      ARTEX_PG_DSN: postgres://${POSTGRES_USER:-artex}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB:-artex}?sslmode=disable
      ANTHROPIC_API_KEY: ${ANTHROPIC_API_KEY:-}
      OPENAI_API_KEY: ${OPENAI_API_KEY:-}
      ARTEX_LLM_PROVIDER: ${ARTEX_LLM_PROVIDER:-}
      ARTEX_LLM_MODEL: ${ARTEX_LLM_MODEL:-}
      ARTEX_LLM_BASE_URL: ${ARTEX_LLM_BASE_URL:-}
      ARTEX_LLM_PROXY: ${ARTEX_LLM_PROXY:-}
      ARTEX_POPO_APP_KEY: ${ARTEX_POPO_APP_KEY:-}
      ARTEX_POPO_APP_SECRET: ${ARTEX_POPO_APP_SECRET:-}
      ARTEX_POPO_NOTIFY_TO: ${ARTEX_POPO_NOTIFY_TO:-}
      ARTEX_TASK_URL: ${ARTEX_TASK_URL:-}
    ports:
      - "8787:8787"
      - "8788:8788"
    volumes:
      # bind-mount 本地二进制，覆盖镜像内置的 /app/artex
      # 更新二进制：替换 ./artex 后 docker compose restart artex
      - ./artex:/app/artex
      - ./data:/app/data
      - ./skills:/app/skills
    restart: unless-stopped

volumes:
  pgdata:
YML
ok "已生成 docker-compose.yml（bind-mount 二进制模式）"

# ── .env ─────────────────────────────────────────────────
if [ -f .env ]; then
    cp .env "$DEST/.env"
    if [ -n "$DEPLOY_URL" ]; then
        if grep -q "^ARTEX_TASK_URL=" "$DEST/.env"; then
            sed -i.bak "s|^ARTEX_TASK_URL=.*|ARTEX_TASK_URL=${DEPLOY_URL}|" "$DEST/.env"
            rm -f "$DEST/.env.bak"
        else
            echo "ARTEX_TASK_URL=${DEPLOY_URL}" >> "$DEST/.env"
        fi
        ok "已设置 ARTEX_TASK_URL=${DEPLOY_URL}"
    fi
    info "已复制 .env（含当前配置）"
else
    cp .env.example "$DEST/.env"
    warn ".env 不存在，已从模板生成，请编辑后启动"
fi

# ── skills/ ──────────────────────────────────────────────
if [ -d skills ]; then
    cp -R skills "$DEST/"
    info "已复制 skills/（$(ls "$DEST/skills" | wc -l | tr -d ' ') 个 skill）"
else
    warn "skills/ 不存在，容器将使用镜像内置的 skills"
fi

# ── build.sh（远程/本地交叉编译新二进制）──────────────────
cat > "$DEST/build.sh" << 'BUILD'
#!/usr/bin/env bash
# ──────────────────────────────────────────────
# 交叉编译 Linux 二进制（在有 Go + 前端产物的环境运行）
# 用法：./build.sh [amd64|arm64]
# ──────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"
ARCH="${1:-amd64}"

echo "[*] 交叉编译 Linux/${ARCH}…"
# 需要在项目根目录有前端产物 server/webui/dist
# 如果没有，先在项目根执行：cd web && npm ci && npm run build:static && cp -r web/out server/webui/dist
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -tags embedui -trimpath -o artex.new ./cmd/artex
chmod +x artex.new
echo "[+] 编译完成 → artex.new ($(du -h artex.new | awk '{print $1}'))"
echo "[*] 替换旧二进制…"
mv artex.new artex
echo "[+] 完成。重启容器生效：docker compose restart artex"
BUILD
chmod +x "$DEST/build.sh"
ok "已生成 build.sh（交叉编译脚本）"

# ── start.sh（一键启动）──────────────────────────────────
cat > "$DEST/start.sh" << 'START'
#!/usr/bin/env bash
# ──────────────────────────────────────────────
# ARTEX 一键启动（本地二进制 bind-mount 模式）
# ──────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
rand(){ head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# ── Docker 检测 ──
if ! command -v docker >/dev/null 2>&1; then
    die "未检测到 Docker，请先安装：curl -fsSL https://get.docker.com | sh"
fi
# 自动检测 docker compose V2 或 docker-compose V1
DC=""
if docker compose version >/dev/null 2>&1; then
    DC="docker compose"
elif command -v docker-compose >/dev/null 2>&1 && docker-compose version >/dev/null 2>&1; then
    DC="docker-compose"
else
    die "未检测到 docker compose，请安装：sudo apt-get install -y docker-compose-plugin"
fi
ok "Docker $(docker --version | awk '{print $3}' | tr -d ',') · $DC"

# ── 二进制检测 ──
if [ ! -f artex ]; then
    die "artex 二进制不存在。请在项目根目录用 pack.sh 打包，或在有 Go 的环境运行 ./build.sh"
fi
if file artex | grep -q "ELF"; then
    ok "artex 二进制就绪（Linux ELF, $(du -h artex | awk '{print $1}')）"
else
    warn "artex 二进制格式非 ELF，可能架构不匹配——继续尝试启动"
fi

# ── .env 初始化 ──
if [ ! -f .env ]; then
    warn ".env 不存在，正在生成…"
    cat > .env << ENV
ARTEX_TAG=latest
POSTGRES_USER=artex
POSTGRES_PASSWORD=$(rand)
POSTGRES_DB=artex
ANTHROPIC_API_KEY=
OPENAI_API_KEY=
ARTEX_LLM_PROVIDER=
ARTEX_LLM_MODEL=
ARTEX_LLM_BASE_URL=
ARTEX_LLM_PROXY=
ARTEX_TASK_URL=
ENV
    ok "已生成 .env（Postgres 密码已随机生成）"
    warn "请编辑 .env 填入 ANTHROPIC_API_KEY 或 OPENAI_API_KEY"
    read -rp "已编辑好 .env？按回车继续，Ctrl-C 退出…"
fi

# ── 拉取镜像（仅运行时环境，二进制用本地的）──
info "拉取运行时镜像（autumn27/artex）…"
$DC pull

# ── 启动 ──
info "启动容器…"
$DC up -d

# ── 等待就绪 ──
info "等待服务就绪…"
for i in $(seq 1 30); do
    if curl -sf http://localhost:8787/api/health >/dev/null 2>&1; then
        ok "ARTEX 已就绪！"
        break
    fi
    sleep 2
    [ "$i" -eq 30 ] && warn "30s 未就绪，检查日志：$DC logs -f artex"
done

HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "localhost")
echo ""
echo "═══════════════════════════════════════════════"
echo "  ARTEX 已启动（本地二进制 bind-mount 模式）"
echo ""
echo "  访问地址：  http://${HOST_IP}:8787"
echo "  首次进入：  http://${HOST_IP}:8787/setup"
echo ""
echo "  更新二进制：替换 ./artex → $DC restart artex"
echo "  查看日志：  $DC logs -f artex"
echo "  停止服务：  $DC down"
echo "═══════════════════════════════════════════════"
START

# ── build-local.sh（服务器上本地构建镜像，不依赖 Docker Hub）──────────
cat > "$DEST/build-local.sh" << 'BUILDLOCAL'
#!/usr/bin/env bash
# ──────────────────────────────────────────────
# 在服务器上本地构建 artex:local 镜像（不拉 Docker Hub）
# 用法：./build-local.sh
# ──────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

echo "[1/5] 准备构建上下文…"
mkdir -p dist/amd64 && cp artex dist/amd64/artex

echo "[2/5] 生成 Dockerfile…"
cat > Dockerfile << 'EOF'
FROM python:3.12-slim-bookworm
ARG TARGETARCH=amd64
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
RUN npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY dist/${TARGETARCH}/artex /app/artex
RUN chmod +x /app/artex
COPY skills/ /app/skills/
VOLUME ["/app/data"]
EXPOSE 8787 8788
ENTRYPOINT ["/app/artex"]
CMD ["-addr", ":8787", "-proxy", ":8788"]
EOF

echo "[3/5] 本地构建镜像 artex:local（约 3-5 分钟）…"
docker build -t artex:local .

echo "[4/5] 改 docker-compose.yml 用本地镜像…"
if grep -q "autumn27/artex" docker-compose.yml; then
    sed -i 's|image: autumn27/artex:${ARTEX_TAG:-latest}|image: artex:local|' docker-compose.yml
    echo "[+] docker-compose.yml 已改用 artex:local"
else
    echo "[*] docker-compose.yml 已是本地镜像"
fi

echo "[5/5] 启动…"
# 检测 docker compose V2 或 V1
DC="docker compose"
if ! $DC version >/dev/null 2>&1; then
    DC="docker-compose"
fi
$DC up -d

echo "等待就绪…"
for i in $(seq 1 60); do
    if curl -sf http://localhost:8787/api/health >/dev/null 2>&1; then
        echo "[+] ARTEX 已就绪！"
        break
    fi
    sleep 2
    [ "$i" -eq 60 ] && echo "[!] 检查日志：$DC logs -f artex"
done

HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "localhost")
echo ""
echo "═══════════════════════════════════════════════"
echo "  ARTEX 已启动（本地构建镜像）"
echo "  访问：http://${HOST_IP}:8787"
echo "  首次：http://${HOST_IP}:8787/setup"
echo "═══════════════════════════════════════════════"
BUILDLOCAL
chmod +x "$DEST/build-local.sh"
ok "已生成 build-local.sh（服务器上本地构建镜像，Docker Hub 拉不到时用）"
chmod +x "$DEST/start.sh"
ok "已生成 start.sh（一键启动）"

# ── README.md ───────────────────────────────────────────
cat > "$DEST/README.md" << 'README'
# ARTEX 部署包（本地二进制 bind-mount 模式）

## 原理

Docker 镜像 `autumn27/artex` 提供运行时环境（Python / Node / Playwright / nmap 等），
`./artex` 二进制通过 bind-mount 覆盖镜像内置的 `/app/artex`。
**更新二进制不需要重新拉镜像**，替换文件 + restart 即可。

## 快速启动

```bash
./start.sh
```

## 更新二进制

在有 Go + 前端产物的环境（项目根目录）：

```bash
# 方式一：用 pack.sh 重新打包
./pack.sh

# 方式二：只编译新二进制（在部署包目录里，需要 Go + 前端产物）
./build.sh           # 默认 amd64
./build.sh arm64     # arm64 服务器
```

编译完成后，传到服务器替换：

```bash
scp artex user@server:~/artex-deploy/
ssh user@server 'cd artex-deploy && docker compose restart artex'
```

## 文件说明

| 文件 | 用途 |
|------|------|
| `artex` | 交叉编译的 Linux 二进制（bind-mount 覆盖镜像内置的） |
| `docker-compose.yml` | 容器编排（bind-mount 二进制 + skills + data） |
| `.env` | 环境变量配置 |
| `skills/` | Skill 定义目录 |
| `start.sh` | 一键启动 |
| `build.sh` | 交叉编译新二进制 |
| `build-local.sh` | 服务器上本地构建镜像（Docker Hub 拉不到时用） |

## 常用命令

```bash
./start.sh                         # 启动（需能拉 Docker Hub）
./build-local.sh                   # Docker Hub 拉不到时，本地构建镜像后启动
docker compose restart artex      # 更新二进制后重启
docker compose logs -f artex      # 查看日志
docker compose down               # 停止
docker compose pull && docker compose up -d  # 更新运行时镜像（非二进制）
```
README
ok "已生成 README.md"

# ── 打包 tar.gz ──
COPYFILE_DISABLE=1 tar --no-xattrs -czf "${DEST}.tar.gz" "$DEST"
ok "已打包 ${DEST}.tar.gz ($(du -h "${DEST}.tar.gz" | awk '{print $1}'))"

echo ""
echo "═══════════════════════════════════════════════"
echo "  打包完成！（本地二进制 bind-mount 模式）"
echo ""
echo "  传到服务器："
echo "    scp ${DEST}.tar.gz user@server:~/"
echo "    ssh user@server 'tar xzf ${DEST}.tar.gz && cd ${DEST} && ./start.sh'"
echo ""
echo "  更新二进制（日常）："
echo "    ./pack.sh                                    # 本地重新打包"
echo "    scp ${DEST}/artex user@server:~/artex-deploy/ # 只传二进制"
echo "    ssh user@server 'cd artex-deploy && docker compose restart artex'"
echo "═══════════════════════════════════════════════"
