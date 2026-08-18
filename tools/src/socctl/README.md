# socctl — SOC 漏洞平台 CLI

SOC 漏洞平台（soc.netease.com）命令行工具：漏洞详情/搜索/待审核/待修复列表、
判重/忽略标记、处理人管理、预警邮件等。被 `skills/vuln-retest` 引用。

## 源码位置（未 vendor，模块较大且为多工具仓库）

- 本机仓库：`~/workspace/code/golang/clis`（module `clis`）
  - 入口：`cmd/socctl/main.go`
  - API 封装：`internal/socapi/`
  - 同仓库还有 `artexctl` / `secctl` / `tsecctl`，如需内置参照本流程
- 上游为内部仓库，无公开地址

## 重新编译

```bash
cd ~/workspace/code/golang/clis
for arch in amd64 arm64; do
  GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
    -o ~/workspace/code/reference/ARTEX/tools/bin/$arch/socctl ./cmd/socctl
done
# macOS 本地（bin/ 里已有 darwin 产物则跳过）：
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/socctl ./cmd/socctl
```

## 注意

- `go.mod` 要求 **Go 1.26**，无法按 jwtcrack 那样固定 1.23 编译；
  在 arm64 Mac 上以 qemu 模拟运行 linux/amd64 镜像时可能触发 Go 1.24+ runtime
  的已知 SIGSEGV（真机 amd64 / arm64 原生不受影响）。
- 运行时通过环境变量鉴权（见 socctl skill），容器内使用需在 compose env 注入相应凭据。
