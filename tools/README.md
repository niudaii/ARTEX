# tools/ — 容器内置工具

本目录集中管理**构建时装入镜像 `/usr/local/bin/` 的第三方工具二进制**，
使 skill 在本地 Mac 与 Docker 部署中都能按 PATH 直接调用同一套工具。

## 目录约定

```
tools/
├── README.md          # 本文件
├── bin/               # 预编译二进制（唯一被 Dockerfile COPY 的目录；gitignore，不提交）
│   ├── amd64/<tool>   #   COPY tools/bin/${TARGETARCH}/ → /usr/local/bin/
│   └── arm64/<tool>
└── src/               # 对应源码（含 ARTEX 定制补丁说明），重新编译用
    └── <tool>/
```

**`bin/` 不进 git**：二进制是构建产物（且可能含会话凭据，如 socctl 的 Cookie），
clone 后按各 `src/<tool>/README.md` 的重编译命令本地生成，再 docker build。

## 新增一个工具的步骤

1. 在 `src/<tool>/` 放入源码（README 注明上游地址与本地补丁内容）；
2. 交叉编译静态二进制（CGO_ENABLED=0）放入 `bin/amd64/`、`bin/arm64/`，文件名即命令名；
3. 无需改 Dockerfile —— `COPY tools/bin/${TARGETARCH}/ /usr/local/bin/` 会自动装入并赋可执行权限；
4. 本地 Mac 部署：`bin/` 只放 Linux 架构产物，Mac 需从 `src/<tool>/` 用
   `GOOS=darwin GOARCH=arm64` 另编译，再 `sudo cp <tool> /usr/local/bin/`；
5. 在引用它的 skill 中直接按命令名调用（不要写宿主机绝对路径）。

## 当前工具清单

| 工具 | 用途 | 源码 | 引用方 |
|------|------|------|--------|
| `jwtcrack` | JWT HMAC 字典/字符爆破（支持过期 token） | `src/jwtcrack/` | `skills/jwt-testing` |
| `socctl` | SOC 漏洞平台 CLI（详情/审核/标记/预警） | `src/socctl/`（源码在本机 clis 仓库，见其 README） | `skills/vuln-retest` |

## 编译注意

- **Go 工具链能用 1.23.x 就用 1.23.x**（`GOTOOLCHAIN=go1.23.12`）：Go 1.24+ 的新 runtime map
  在 qemu amd64 模拟下（arm64 Mac 跑 linux/amd64 容器）可能 SIGSEGV；真机不受影响。
  jwtcrack 已固定 1.23；socctl 的 go.mod 要求 1.26，无法降级，本地模拟测试时留意该风险。
- 国内网络需临时 `GOSUMDB=sum.golang.google.cn` 让 toolchain 下载通过校验。
- 始终 `CGO_ENABLED=0` + `-trimpath -ldflags "-s -w"` 产出静态二进制。
