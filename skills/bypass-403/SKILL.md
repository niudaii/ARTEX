---
name: bypass-403
description: >
  403/401 访问拒绝绕过治理 skill。触发：目标返回 403/401，且需要系统化测试
  是否存在访问控制缺陷时调用。覆盖路径重写、请求头伪造、方法覆盖与差异分析；
  payload 分类参考 bypass-pro/references/payloads.md 第 1 节。
---

# 403/401 绕过治理

## 原则

模型已具备丰富的 HTTP 绕过知识。本 skill 不堆砌 payload 字典，
只提供**绕过路径决策矩阵**和**差异分析方法**——让模型系统化覆盖所有绕过向量，
而非随机猜测。具体 payload（suffix/prefix/boundary_insert/headers 分类）
参考 **bypass-pro/references/payloads.md** 第 1 节。

---

## PoC 边界

| 禁止 | 允许 |
|------|------|
| 越权读取他人数据 | 证明能访问受限页面/接口即可 |
| 大规模遍历受限资源 | 读取 1 条记录证明可访问 |
| 破坏性操作 | 只读验证 |

---

## 绕过路径决策矩阵

 按以下顺序逐一测试。**每条路径至少尝试 3 种不同变形**后仍无信号再转向下一条；若某路径出现弱信号（如 body 大小变化、响应头差异），可追加同路径变体而非立即放弃：

### 路径 1：URL 重写与编码

四类变形手法，每类有明确的目标场景：

| 手法 | 示例 | 原理 |
|------|------|------|
| **Suffix（后缀注入）** | `/admin` → `/admin.js`、`/admin;.css`、`/admin/`、`/admin/.`、`/admin%20`、`/admin?`、`/admin/images/..;` | 静态资源伪装 / 分隔符混淆 / 伪目录回退 / 矩阵参数 |
| **Prefix（前缀注入）** | `/admin` → `//admin`、`;/admin`、`./admin`、`.;/admin`、`%2e/admin`、`%252e/admin`、`images;/../admin` | 目录层级混淆 / 编码绕过 / 伪目录组合 |
| **Boundary Insert（边界插入）** | `a/b/c` → `a;/b/c`、`a/b;/c`、`a.;/b/c`、`a..;/b/c`、`a%00/b/c` | 矩阵参数 / 点分号回退 / 编码截断 |
| **编码变换** | `/admin` → `/%61dmin`、`/%2561dmin`（双重编码）、全角 `%２ｅ` | 编码后组件解析差异 |

> 完整 payload 列表见 [bypass-pro/references/payloads.md](../bypass-pro/references/payloads.md) 第 1.1-1.3 节。

### 路径 2：请求头伪造（IP 信任绕过）

当目标使用反向代理/CDN 且后端直接信任代理头时：

| Header | 优先级 | 适用场景 |
|--------|--------|---------|
| `X-Forwarded-For` | 最高 | 最通用，几乎所有反向代理 |
| `X-Real-IP` | 高 | Nginx realip 模块 |
| `X-Client-IP` | 高 | Apache mod_remoteip |
| `X-Custom-IP-Authorization` | 高 | 部分自定义鉴权 |
| `CF-Connecting-IP` | 高 | Cloudflare 后端 |
| `True-Client-IP` | 中 | Akamai / Cloudflare 企业版 |
| `X-Originating-IP` | 低 | IIS / Exchange |
| `X-Remote-Addr` | 低 | 部分老代理 |
| `X-Host` | 低 | Host 头变体 |
| `Forwarded` | 中 | RFC 7239 标准（`for=127.0.0.1`） |
| `Referer` | 低 | 部分应用检查来源 |
| `HTTP-Version` | 低 | 降级到 HTTP/1.0 绕过某些过滤 |

**IP 值矩阵**（每个 header 逐一测试）：
`127.0.0.1`、`localhost`、`::1`、`0.0.0.0`、`10.0.0.1`、`192.168.1.1`、`172.16.0.1`

**组合头攻击**：同时投递多个代理头（BypassPro 默认组合 6 个头一次性发送）。

**多 IP 链伪造**（某些后端取 XFF 不同位置）：
- 取最左：`X-Forwarded-For: 127.0.0.1`
- 取最右：`X-Forwarded-For: 8.8.8.8, 127.0.0.1`
- 取非代理：`X-Forwarded-For: 127.0.0.1, 10.0.0.1`

> 完整 header 组合列表见 [bypass-pro/references/payloads.md](../bypass-pro/references/payloads.md) 第 1.4 节。

### 路径 3：HTTP 方法覆盖

| 手法 | 示例 |
|------|------|
| 方法变更 | `GET /admin` → `POST /admin`、`PUT`、`PATCH`、`DELETE`、`OPTIONS` |
| 方法覆盖头 | `X-HTTP-Method-Override: GET`（发送 POST 时） |
| `_method` 参数 | `POST /admin?_method=GET` |

### 路径 4：Host 头与虚拟主机

| 手法 | 示例 |
|------|------|
| Host 替换 | `Host: target.com` → `Host: localhost`、`Host: 127.0.0.1` |
| X-Forwarded-Host | `X-Forwarded-Host: localhost` |
| 绝对 URI | `GET https://target.com/admin HTTP/1.1`（vs `GET /admin`） |

### 路径 5：Content-Type 与 Accept 变更

| 手法 | 示例 |
|------|------|
| Content-Type 切换 | `application/json` → `text/xml`、`multipart/form-data` |
| Accept 头 | `Accept: application/json` vs `Accept: text/html` |
| 空 Content-Type | 移除 Content-Type 头 |

---

## 差异分析方法

**基线建立**（第一步，必须做）：
- 无任何伪造头发送原始请求 → 记录响应码、响应体大小、响应体内容
- 这是后续所有判断的基准

**绕过判定标准**（任一满足即可能存在绕过）：

| 信号 | 判定 |
|------|------|
| 响应码从 403/401 → 200 | ⭐ 强信号 |
| 响应码从 403/401 → 302（非登录跳转） | 需检查 Location 头目标 |
| 响应码不变但 body 大小显著变化（如 0 → >500B） | 需检查 body 内容 |
| 响应码不变但 body 包含受限内容（非错误页/登录框） | ⭐ 强信号 |

**常见误判排除**（必须检查）：

| 现象 | 真绕过 | 误判 |
|------|--------|------|
| 403 → 200，body = 登录框 | ❌ | 只是跳到了登录页 |
| 403 → 200，body = "Internal Error" | ❌ | 请求异常，非正常访问 |
| 403 → 302，Location = /login | ❌ | 重定向到登录 |
| 403 → 200，body = 管理面板内容 | ✅ | 绕过成功 |

---

## 批量测试策略

必须脚本化批量测试，**禁止逐条手动 curl**：

- 路径 1（URL 重写）：遍历 suffix × prefix × boundary_insert 的组合
- 路径 2（请求头）：header × IP 值的笛卡尔积，Python `itertools.product`
- 每条请求必须带 `--max-time 10 --connect-timeout 5`
- `allow_redirects=False`（不跟随重定向，才能看到原始 302）
- 结果按响应码分组，只输出与基线不同的条目

---

## 完成定义

**Positive exit**：
- 伪造请求访问受限资源成功：响应码从 403/401/302 变为 200，且响应体包含实际受限内容（非错误页/登录框）。
- artifact 含完整请求（curl 命令 + 响应码 + 响应体片段）供复现。

**Negative exit**：
- 5 条路径全部测试完毕（URL 重写、请求头伪造、方法覆盖、Host 头、Content-Type）。
- 请求头路径至少覆盖 10 个 header × 5 个 IP 值。
- URL 重写路径至少覆盖 suffix（含静态资源伪装、分隔符混淆、伪目录回退、矩阵参数）+ prefix + boundary_insert。
- 所有尝试的响应行为与基线一致（状态码和 body 大小无变化）。
- 不输出"确认不存在绕过"，只输出"当前测试向量下未发现绕过"。

---

## 超时铁律

| 约束 | 值 |
|------|-----|
| curl 超时 | `--max-time 10 --connect-timeout 5` |
| Python requests | `timeout=(5, 10)` |
| 单条路径测试上限 | 2 分钟 |
| 全路径测试上限 | 10 分钟 |
| HTTPS 证书 | 始终忽略（`-k` / `verify=False`） |
