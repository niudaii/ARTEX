#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# ARTEX 公网部署打包脚本（本地二进制 bind-mount 模式）
#
# 用法：./pack.sh [部署URL] [目标架构]
#   部署URL：如 http://1.2.3.4:8787（写入 .env 的 ARTEX_TASK_URL）
#   目标架构：amd64（默认）/ arm64
#
# 产出 artex-deploy/，内含交叉编译的 Linux 二进制 + 一键启动脚本
# 更新二进制：重新编译 → scp 覆盖 → ./restart.sh
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

# ── 构建前端静态产物 ──
info "构建前端静态产物…"
command -v npm >/dev/null 2>&1 || die "未检测到 npm，请先安装 Node.js"
( cd web && npm ci && npm run build:static )
mkdir -p server/webui && rm -rf server/webui/dist && cp -r web/out server/webui/dist
ok "前端静态产物已就绪"

# ── 交叉编译 Linux 二进制 ──
info "交叉编译 Linux/${ARCH} 二进制…"
rm -rf "$DEST" && mkdir -p "$DEST"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -tags embedui -trimpath -o "$DEST/artex" ./cmd/artex
chmod +x "$DEST/artex"
ok "已编译 $DEST/artex（Linux/${ARCH}, $(du -h "$DEST/artex" | awk '{print $1}')）"

# ── 容器内置工具（tools/bin → 镜像 /usr/local/bin，约定见 tools/README.md）──
# tools/bin 不进 git，打包时在此现编；产物同时回填本地 tools/bin/$ARCH 供本机 docker build
REPO_ROOT="$(pwd)"
mkdir -p "$DEST/tools-bin" "tools/bin/$ARCH"

# jwtcrack：固定 Go 1.23 编译（Go 1.24+ runtime 在 qemu amd64 模拟下可能 SIGSEGV）
info "编译 jwtcrack（Linux/${ARCH}）…"
if ( cd tools/src/jwtcrack && GOSUMDB=sum.golang.google.cn GOTOOLCHAIN=go1.23.12 GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
      go build -trimpath -ldflags "-s -w" -o "$REPO_ROOT/tools/bin/$ARCH/jwtcrack" . ); then
    ok "jwtcrack 已编译（Go 1.23 工具链）"
elif ( cd tools/src/jwtcrack && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
      go build -trimpath -ldflags "-s -w" -o "$REPO_ROOT/tools/bin/$ARCH/jwtcrack" . ); then
    warn "Go 1.23 工具链不可用，jwtcrack 用当前 Go 编译（arm64 Mac 模拟 amd64 时有崩溃风险）"
else
    die "jwtcrack 编译失败（源码：tools/src/jwtcrack）"
fi

# socctl：源码在外部 clis 仓库（go.mod 要求 Go 1.26），可用 CLIS_REPO 环境变量覆盖路径
CLIS_REPO="${CLIS_REPO:-$HOME/workspace/code/golang/clis}"
info "编译 socctl（Linux/${ARCH}）…"
if [ -d "$CLIS_REPO" ]; then
    ( cd "$CLIS_REPO" && GOOS=linux GOARCH="$ARCH" CGO_ENABLED=0 \
      go build -trimpath -ldflags "-s -w" -o "$REPO_ROOT/tools/bin/$ARCH/socctl" ./cmd/socctl )
    ok "socctl 已编译（源码：${CLIS_REPO}）"
elif [ -f "tools/bin/$ARCH/socctl" ]; then
    warn "未找到 clis 仓库，沿用已有 tools/bin/$ARCH/socctl（可用 CLIS_REPO 指定源码路径）"
else
    warn "socctl 无源码且无现有二进制，镜像将不含 socctl（vuln-retest 的 SOC 来源不可用）"
fi

cp tools/bin/"$ARCH"/* "$DEST/tools-bin/" 2>/dev/null || true
ok "内置工具已就绪：$(ls "$DEST/tools-bin" | tr '\n' ' ')"

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
      # 更新二进制：替换 ./artex 后 ./restart.sh
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

# ── restart.sh（更新二进制后重启）──────────────────────
cat > "$DEST/restart.sh" << 'RESTART'
#!/usr/bin/env bash
# ──────────────────────────────────────────────
# ARTEX 重启（更新二进制后执行）
# ──────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

DC="docker compose"
if ! $DC version >/dev/null 2>&1; then DC="docker-compose"; fi

# 容器不存在时自动创建，已存在则 restart
if $DC ps artex | grep -q "artex"; then
    echo "[*] 重启 artex…"
    $DC restart artex
else
    echo "[*] 容器不存在，启动…"
    $DC up -d
fi

echo "[*] 等待就绪…"
for i in $(seq 1 30); do
    curl -sf http://localhost:8787/api/health >/dev/null 2>&1 && { echo "[+] ARTEX 已就绪"; break; }
    sleep 2
    [ "$i" -eq 30 ] && echo "[!] 检查日志：$DC logs -f artex"
done
RESTART
chmod +x "$DEST/restart.sh"
ok "已生成 restart.sh（更新二进制后重启）"

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
# 容器内置工具（jwtcrack/socctl 等）→ tools/bin/amd64，Dockerfile 会 COPY 进 /usr/local/bin
if [ -d tools-bin ] && ls tools-bin/* >/dev/null 2>&1; then
    mkdir -p tools/bin/amd64 && cp tools-bin/* tools/bin/amd64/
    echo "[+] 内置工具已放入构建上下文：$(ls tools-bin | tr '\n' ' ')"
fi

echo "[2/5] 生成 Dockerfile…"
cat > Dockerfile << 'EOF'
FROM python:3.12-slim-bookworm
ARG TARGETARCH=amd64
# 国内镜像加速
RUN sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources 2>/dev/null; \
    sed -i 's|deb.debian.org|mirrors.aliyun.com|g' /etc/apt/sources.list 2>/dev/null; \
    apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates ripgrep curl wget vim git jq unzip \
      dnsutils iputils-ping netcat-openbsd inetutils-telnet whois nmap \
    && curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*
RUN npm config set registry https://registry.npmmirror.com \
    && npm install -g @playwright/mcp@latest @playwright/cli@latest playwright@latest \
    && playwright-cli --help \
    && playwright install --with-deps chromium \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY dist/${TARGETARCH}/artex /app/artex
# 内置工具二进制 → /usr/local/bin（约定见仓库 tools/README.md）
COPY tools/bin/${TARGETARCH}/ /usr/local/bin/
RUN chmod +x /app/artex && find /usr/local/bin -maxdepth 1 -type f -exec chmod +x {} +
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

# ── README.md ───────────────────────────────────────────
cat > "$DEST/README.md" << 'README'
# ARTEX 部署包（本地二进制 bind-mount 模式）

## 原理

Docker 镜像 `artex:local`（本地构建）提供运行时环境（Python / Node / Playwright / nmap 等），
`./artex` 二进制通过 bind-mount 覆盖镜像内置的 `/app/artex`。
**更新二进制不需要重新构建镜像**，替换文件 + `./restart.sh` 即可。

内置工具（jwtcrack / socctl 等，供 agent 的 skill 调用）在构建时烘焙进镜像
`/usr/local/bin/`，更新它们需要重跑 `./build-local.sh` 重建镜像。

## 首次部署

```bash
./build-local.sh    # 本地构建镜像 + 启动
```

## 更新二进制（日常）

本地重新打包后，传新二进制到服务器，然后重启：

```bash
scp artex-deploy/artex user@server:~/artex-deploy/
ssh user@server 'cd artex-deploy && ./restart.sh'
```

## 文件说明

| 文件 | 用途 |
|------|------|
| `artex` | 交叉编译的 Linux 二进制（bind-mount 覆盖镜像内置的） |
| `tools-bin/` | 内置工具二进制，构建镜像时 COPY 进 `/usr/local/bin/` |
| `docker-compose.yml` | 容器编排（bind-mount 二进制 + skills + data） |
| `.env` | 环境变量配置 |
| `skills/` | Skill 定义目录 |
| `build-local.sh` | 首次部署：本地构建镜像 + 启动 |
| `restart.sh` | 更新二进制后重启 |

## 常用命令

```bash
./build-local.sh                  # 首次部署（构建镜像 + 启动）
./restart.sh                      # 更新二进制后重启
docker compose logs -f artex      # 查看日志
docker compose down               # 停止
docker compose up -d              # 启动（已构建镜像后）
```

## 更新内置工具（jwtcrack / socctl）

工具烘焙在镜像里，不能像 `./artex` 那样热替换：

```bash
# 本地（macOS）重新打包（pack.sh 会自动重编工具）→ 整包上传 → 重建镜像
./pack.sh
scp artex-deploy.tar.gz user@server:~/
ssh user@server 'tar xzf artex-deploy.tar.gz && cd artex-deploy && ./build-local.sh'
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
echo "  首次部署："
echo "    scp ${DEST}.tar.gz user@server:~/"
echo "    ssh user@server 'tar xzf ${DEST}.tar.gz && cd ${DEST} && ./build-local.sh'"
echo ""
echo "  更新二进制（日常）："
echo "    ./pack.sh                                    # 本地重新打包"
echo "    scp ${DEST}/artex user@server:~/artex-deploy/ # 只传二进制"
echo "    ssh user@server 'cd artex-deploy && ./restart.sh'"
echo ""
echo "  更新内置工具（jwtcrack/socctl）："
echo "    ./pack.sh && scp ${DEST}.tar.gz user@server:~/"
echo "    ssh user@server 'tar xzf ${DEST}.tar.gz && cd ${DEST} && ./build-local.sh' # 需重建镜像"
echo "═══════════════════════════════════════════════"
