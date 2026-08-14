---
name: jwt-testing
description: >
  JWT 安全测试治理 skill。提供算法攻击决策树、密钥破解策略、声明篡改测试顺序、
  header 注入检测和差异验证方法。当目标使用 JWT（Bearer token / cookie / 参数）
  且需要测试 JWT 安全性时调用。本 skill 不提供 payload 或工具命令——
  模型自带 JWT 攻击知识，本 skill 只负责让模型按正确路径系统化测试，
  避免遗漏关键攻击向量或对非 HMAC 算法跑字典爆破。
---

# JWT 测试治理

## 原则

模型已具备 JWT 攻击知识（alg:none、密钥混淆、kid 注入、JWKS 欺骗等）。
本 skill 不提供工具命令或 payload，只提供**攻击决策树**和**硬边界**——
让模型按正确顺序测试，避免对非 HMAC 算法跑字典爆破、避免无前提条件的盲目利用。

---

## PoC 边界

| 禁止 | 允许 |
|------|------|
| 伪造 token 越权操作他人数据 | 伪造 token 证明能被接受即可 |
| 大规模遍历其他用户数据 | 读取自己数据的越权版本证明提权 |
| 持久化伪造 token 的访问权限 | 验证后丢弃伪造 token |

---

## 资源

| 资源 | 路径 | 说明 |
|------|------|------|
| JWT 密钥字典 | `/Users/zp857/workspace/tools/jwt/keys.txt` | 12.6 万条常见弱密钥，HMAC 爆破专用 |

---

## 第一步：离线分析（必须先做）

在发起任何 live 请求前，先离线解码 token：

1. **解码三段结构**：`header.payload.signature`，各段 base64url 解码
2. **识别算法**：`alg` 字段决定后续攻击路径
3. **检查 header 敏感参数**：`kid`、`jku`、`jwk`、`x5u`、`x5c`
4. **检查 payload 高价值声明**：`role`、`sub`、`userId`、`isAdmin`、`permissions`、`scope`、`tenant`、`exp`、`nbf`

**算法分类决定攻击路径**：

| 算法 | 类型 | 可用攻击 |
|------|------|---------|
| `HS256` / `HS384` / `HS512` | HMAC（对称） | 字典爆破、alg:none、声明篡改 |
| `RS256` / `RS384` / `RS512` | RSA（非对称） | alg:none、密钥混淆（RS→HS）、JWKS 欺骗 |
| `ES256` / `ES384` | ECDSA（非对称） | alg:none、JWKS 欺骗 |
| `none` / `None` / `NONE` | 无签名 | 直接伪造（若后端接受） |
| `PS256` 等 | RSA-PSS | 同 RSA 路径 |

---

## 攻击决策树

按以下顺序测试，**前一步无果再进入下一步**：

### 攻击 1：alg:none（最快验证）

**前提假设**：后端可能接受无签名 token。

修改 header `alg` 为 `none`，移除 signature 段，篡改 payload 中的高价值声明（如 `role: admin`），发送请求验证是否被接受。

**判定**：伪造的无签名 token 返回 200 且包含越权数据 → alg:none 绕过成立。
**无果**：后端严格验签 → 进入攻击 2。

### 攻击 2：HMAC 密钥爆破（仅限 HS* 算法）

**前提**：`alg` 为 HS256/HS384/HS512。

**三阶段爆破策略**：

| 阶段 | 字典来源 | 超时 | 说明 |
|------|---------|------|------|
| 1. 已知弱密钥 | 手工列表 | 10s | `secret`、`key`、`123456`、`password`、项目名、`jwt_secret`、`SECRET_KEY`、`your-256-bit-secret` |
| 2. 小字典快速验证 | `keys.txt` 前 1000 行 | 2min | `head -1000 /Users/zp857/workspace/tools/jwt/keys.txt > /tmp/jwt_small.dict` |
| 3. 全量字典 | `/Users/zp857/workspace/tools/jwt/keys.txt`（12.6 万条） | 5min | 超时无命中 → 结论为"弱密钥未命中，可能为强密钥" |

**编码变换**（阶段 2-3 无命中时追加）：
- md5 编码：`secret = md5(dict_value)`
- 16 位 md5 截断：`secret = md5(dict_value)[8:24]`
- base64 编码：`secret = base64(dict_value)`

**爆破工具选择**：
- `hashcat -m 16500`：GPU 加速，适合全量字典
- `john --format=HMAC-SHA256`：CPU 多核，适合中等字典
- Python `pyjwt` + 多线程：灵活，适合带编码变换的场景

**拿到密钥后**：立即伪造高权限 token（篡改 `role`/`userId`/`isAdmin`），用真实请求验证是否被接受。

### 攻击 3：RS→HS 密钥混淆（仅限 RS*/PS* 算法）

**前提**：`alg` 为 RS256 等非对称算法，且能获取到公钥。

**公钥来源**：JWKS 端点（`/.well-known/jwks.json`、`/certs`）、源码泄露、HTTPS 证书提取。

**攻击**：修改 header `alg` 为 `HS256`，用公钥 PEM 内容作为 HMAC 密钥签名 → 若后端用公钥做 HMAC 验证则伪造 token 被接受。

### 攻击 4：kid 注入

**前提**：header 含 `kid` 参数。

| 注入类型 | 手法 |
|---------|------|
| SQL 注入 | `kid: "' UNION SELECT 'attacker_key'--"` → 后端用攻击者控制的密钥验签 |
| 路径遍历 | `kid: "../../../../dev/null"` → 空密钥 → 用空字符串签名 |
| 路径遍历 | `kid: "../../../../etc/hostname"` → 用 hostname 内容签名 |

**判定**：用注入后的密钥签名的 token 被接受 → kid 注入成立。

### 攻击 5：jku / x5u Header 注入

**前提**：header 含 `jku` 或 `x5u` 参数，且后端未做白名单校验。

**攻击**：
1. 生成 RSA 密钥对
2. 用私钥签名 JWT，header 中 `jku` 指向攻击者控制的 JWKS URL
3. 后端 fetch 攻击者的 JWKS → 用攻击者的公钥验签 → 接受

**判定**：后端请求了攻击者的 JWKS URL 且接受了用攻击者私钥签名的 token → jku 注入成立。

### 攻击 6：声明篡改（已知密钥或 alg:none 后）

**前提**：已通过攻击 1-5 获得签名能力（拿到密钥或可绕过签名）。

篡改高价值声明并验证：

| 声明 | 篡改目标 |
|------|---------|
| `role` | `user` → `admin` |
| `userId` / `sub` | 自己的 ID → 其他用户 ID |
| `isAdmin` | `false` → `true` |
| `permissions` / `scope` | 添加 `admin` / `write` / `delete` |
| `tenant` / `tenant_id` | 切换到其他租户 |

---

## 硬边界

| # | 禁止 | 原因 |
|---|------|------|
| 1 | 对 RS/ES/PS 算法跑 HMAC 字典爆破 | 非对称算法无对称密钥，爆破无意义 |
| 2 | 无公钥时尝试密钥混淆 | 需要公钥作为 HMAC 密钥 |
| 3 | 把 exploit 描述为"确定性攻击" | 所有 JWT 攻击都是假设验证 |
| 4 | 跳过离线解码直接发 live 请求 | 必须先识别算法和 header 参数 |
| 5 | 全量字典爆破超 5 分钟仍继续 | 缩小字典或判定为强密钥 |
| 6 | 未验证目标可达性就跑 playbook 扫描 | 先 `curl --max-time 5` 确认可达 |
| 7 | 跳过阶段 1-2 直接跑全量字典 | 先快速验证常见弱密钥，再全量 |

---

## 差异验证方法

伪造/篡改 token 后，必须与原始 token 的请求行为对比：

| 信号 | 判定 |
|------|------|
| 原始 token → 200，伪造 token → 200 且返回不同（越权）数据 | ✅ 漏洞成立 |
| 原始 token → 200，伪造 token → 401/403 | 后端验签正常，该攻击向量无效 |
| 原始 token → 200，伪造 token → 200 且返回相同数据 | 声明未被信任或后端二次校验 |
| 原始 token → 200，伪造 token → 500 | 请求异常，非正常绕过 |

**关键**：对比响应体内容，不能只看状态码。添加 canary 标记（如篡改 `userId` 后检查响应中是否返回了其他用户的数据）。

---

## 超时铁律

| 约束 | 值 |
|------|-----|
| 目标连通性检查 | `curl --max-time 5` |
| live 请求超时 | `--max-time 10 --connect-timeout 5` |
| 阶段 1（弱密钥列表） | 10s |
| 阶段 2（小字典 1000 条） | 2min |
| 阶段 3（全量 12.6 万条） | 5min，超时则判定为强密钥 |
| playbook 扫描上限 | 2min 无结果 → Ctrl+C，回退离线分析 |
| HTTPS 证书 | 始终忽略 |

---

## 完成定义

**Positive exit**：
- 伪造/篡改的 token 被后端接受，且返回了越权数据（不同用户/更高权限的内容）。
- artifact 含：原始 token、伪造 token、完整请求、响应码、响应体片段。

**Negative exit**：
- 决策树中适用于该算法的所有攻击路径均已测试。
- HMAC 算法：至少尝试弱密钥列表 + 小字典爆破（`keys.txt` 前 1000 行）+ 全量字典爆破（`keys.txt`）+ 编码变换（md5/base64）+ alg:none。
- 非对称算法：至少尝试 alg:none + 密钥混淆（若有公钥）+ jku/x5u 注入（若有相关 header）。
- 不输出"确认不存在 JWT 漏洞"，只输出"当前测试路径下未发现 JWT 安全问题"。
