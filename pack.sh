#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────
# ARTEX 公网部署打包脚本
# 把部署所需文件收集到 artex-deploy/，内含一键启动命令
# 用法：./pack.sh [部署URL，如 http://1.2.3.4:8787]
# ─────────────────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

DEST="artex-deploy"
DEPLOY_URL="${1:-}"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }

# ── 清理旧目录 ──────────────────────────────────────────
rm -rf "$DEST"
mkdir -p "$DEST"

# ── docker-compose.yml ──────────────────────────────────
cp docker-compose.yml "$DEST/"
info "已复制 docker-compose.yml"

# ── .env ─────────────────────────────────────────────────
if [ -f .env ]; then
    cp .env "$DEST/.env"
    # 如果传了部署 URL，更新 ARTEX_TASK_URL
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

# ── start.sh（一键启动）──────────────────────────────────
cat > "$DEST/start.sh" << 'START'
#!/usr/bin/env bash
# ──────────────────────────────────────────────
# ARTEX 一键启动
# ──────────────────────────────────────────────
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
rand(){ head -c 24 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# ── Docker 检测 ──
if ! command -v docker >/dev/null 2>&1; then
    die "未检测到 Docker，请先安装：curl -fsSL https://get.docker.com | sh"
fi
if ! docker compose version >/dev/null 2>&1; then
    die "未检测到 docker compose，请升级 Docker 或安装 compose 插件"
fi
ok "Docker $(docker --version | awk '{print $3}' | tr -d ',')"

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
    warn "请编辑 .env 填入 ANTHROPIC_API_KEY 或 OPENAI_API_KEY 后重新运行"
    warn "否则系统启动后无法执行渗透任务（但 Web 界面可正常访问）"
    read -rp "已编辑好 .env？按回车继续，Ctrl-C 退出编辑…"
fi

# ── 拉取镜像 + 启动 ──
info "拉取镜像…"
docker compose pull

info "启动容器…"
docker compose up -d

# ── 等待就绪 ──
info "等待服务就绪…"
for i in $(seq 1 30); do
    if curl -sf http://localhost:8787/api/health >/dev/null 2>&1; then
        ok "ARTEX 已就绪！"
        break
    fi
    sleep 2
    [ "$i" -eq 30 ] && warn "30s 未就绪，请检查日志：docker compose logs -f artex"
done

# ── 输出访问信息 ──
HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "localhost")
echo ""
echo "═══════════════════════════════════════════════"
echo "  ARTEX 已启动"
echo ""
echo "  访问地址：  http://${HOST_IP}:8787"
echo "  首次进入：  http://${HOST_IP}:8787/setup （设置管理员密码）"
echo ""
echo "  查看日志：  docker compose logs -f artex"
echo "  停止服务：  docker compose down"
echo "  重启服务：  docker compose restart"
echo "═══════════════════════════════════════════════"
START
chmod +x "$DEST/start.sh"
ok "已生成 start.sh（一键启动）"

# ── README.md ───────────────────────────────────────────
cat > "$DEST/README.md" << 'README'
# ARTEX 部署包

## 快速启动

```bash
./start.sh
```

脚本会自动：检测 Docker → 初始化 .env（首次）→ 拉取镜像 → 启动 → 等待就绪 → 输出访问地址。

首次访问 `http://<IP>:8787/setup` 设置管理员密码。

## 前置要求

- Docker + Docker Compose（`start.sh` 会自动检测）
- 至少一个 LLM API Key（在 `.env` 里填 `ANTHROPIC_API_KEY` 或 `OPENAI_API_KEY`）

## 文件说明

| 文件 | 用途 |
|------|------|
| `docker-compose.yml` | 容器编排（ARTEX + PostgreSQL） |
| `.env` | 环境变量配置（密码、API Key 等） |
| `skills/` | Skill 定义目录，绑定挂载进容器 |
| `start.sh` | 一键启动脚本 |

## .env 配置项

| 变量 | 说明 |
|------|------|
| `POSTGRES_PASSWORD` | 数据库密码（首次随机生成） |
| `ANTHROPIC_API_KEY` | Anthropic API Key（与 OPENAI 二选一） |
| `OPENAI_API_KEY` | OpenAI API Key |
| `ARTEX_LLM_PROVIDER` | 自定义 LLM Provider（可选） |
| `ARTEX_LLM_MODEL` | 自定义模型名（可选） |
| `ARTEX_LLM_BASE_URL` | 自定义 API 地址（可选，用于第三方接口） |
| `ARTEX_TASK_URL` | 服务器访问地址（用于 POPO 通知里的链接） |

## 常用命令

```bash
./start.sh              # 启动
docker compose logs -f artex   # 查看日志
docker compose down     # 停止
docker compose restart  # 重启
docker compose pull && docker compose up -d  # 更新到最新镜像
```

## 端口说明

| 端口 | 用途 |
|------|------|
| 8787 | Web UI + API（主要访问端口） |
| 8788 | 流量录制代理（内部使用，可不在白名单放行） |

## 数据持久化

- PostgreSQL 数据：Docker 卷 `pgdata`（容器重建不丢失）
- `./data/`：JWT 密钥、SQLite、MITM CA 证书（绑定挂载，宿主机可直接备份）
- `./skills/`：Skill 定义（绑定挂载，改了重启即生效）
README
ok "已生成 README.md"

# ── 打包 tar.gz（方便传输到服务器）─────────────────────
tar czf "${DEST}.tar.gz" "$DEST"
ok "已打包 ${DEST}.tar.gz（传输到服务器后解压运行）"

echo ""
echo "═══════════════════════════════════════════════"
echo "  打包完成！"
echo ""
echo "  方式一：直接在本机部署"
echo "    cd ${DEST} && ./start.sh"
echo ""
echo "  方式二：传到服务器部署"
echo "    scp ${DEST}.tar.gz user@server:~/"
echo "    ssh user@server 'tar xzf ${DEST}.tar.gz && cd ${DEST} && ./start.sh'"
echo "═══════════════════════════════════════════════"
