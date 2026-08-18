# jwtcrack 预编译二进制

JWT HMAC 字典爆破工具，供 jwt-testing skill 使用。Docker 构建时安装到 `/usr/local/bin/jwtcrack`；
本地 Mac 部署也建议 `cp jwtcrack /usr/local/bin/`，skill 直接按 PATH 调用。

- 上游源码：https://github.com/vk-rv/JWT-Brute-Force-Tool
- 本目录 `src/` 为 **ARTEX 定制补丁版**源码（`jwt.Parse` 增加 `jwt.WithoutClaimsValidation()`，
  过期 token 也能破解；上游默认校验 `exp`/`nbf` 会导致过期 token 永远破解失败）
- 子命令：`gen` / `genWithSecret` / `crack -token <jwt> -dict <file>` / `brute -token <jwt> -minLen N -maxLen N -charset <set> -threads N`
- 静态编译（CGO_ENABLED=0），无运行时依赖；仅对 HMAC（HS*）算法有效

## 重新编译（从 src/ 构建，勿用未打补丁的上游源码）

```bash
cd tools/jwtcrack/src
for arch in amd64 arm64; do
  GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ../$arch/jwtcrack .
done
# macOS 本地（可选）：
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /tmp/jwtcrack . \
  && sudo cp /tmp/jwtcrack /usr/local/bin/jwtcrack
```
