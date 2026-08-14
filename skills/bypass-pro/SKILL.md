---
name: bypass-pro
description: >
  综合绕过治理 skill。覆盖 WAF 绕过、认证绕过、授权绕过、速率限制绕过、
  输入过滤绕过和内容安全策略绕过。提供绕过分类决策树、手法选择优先级、
  差异分析方法和组合攻击策略。当目标存在安全控制机制（WAF/认证/授权/限流/过滤）
  且需要测试是否可绕过时调用。本 skill 不堆砌 payload 字典——
  模型自带绕过知识，本 skill 只负责让模型按正确路径分类测试，
  避免盲目尝试和遗漏绕过向量。具体 payload 参考 references/payloads.md。
---

# 综合绕过治理

## 原则

模型已具备丰富的绕过知识（编码变换、协议滥用、解析差异、逻辑漏洞等）。
本 skill 不堆砌 payload，只提供**绕过分类决策树**和**手法选择优先级**——
让模型先识别"要绕过什么"，再选择"用什么手法"，系统化覆盖所有向量。
具体 payload 分类、编码变体和 CVE 模板见 **[payloads.md](references/payloads.md)**。

---

## PoC 边界

| 禁止 | 允许 |
|------|------|
| 绕过安全控制后执行破坏性操作 | 证明绕过成功即可 |
| 绕过后大规模遍历/导出数据 | 读取 1 条记录证明可访问 |
| 绕过后持久化访问 | 验证后丢弃绕过路径 |
| 对非目标范围内的安全控制发起绕过 | 仅测试任务指定目标 |

---

## 第一步：识别安全控制类型

发起绕过前，必须先判断目标部署了哪类安全控制：

| 控制类型 | 识别信号 | 对应路径 |
|---------|---------|---------|
| **WAF / IPS** | 请求被拦截（403 + WAF 页面）、关键词过滤、payload 被清洗 | 路径 A |
| **认证** | 401、登录页面、WWW-Authenticate 头 | 路径 B |
| **授权** | 403、权限不足提示、数据按角色过滤 | 路径 C |
| **速率限制** | 429、Retry-After 头、"too many requests" | 路径 D |
| **输入过滤** | 参数被清洗、特殊字符被移除/转义、报错提示非法字符 | 路径 E |
| **CSP / XSS 过滤** | Content-Security-Policy 头、反射内容被编码 | 路径 F |

可同时存在多种控制——按优先级逐一测试。

---

## 路径 A：WAF / IPS 绕过

### 手法优先级

| 优先级 | 手法 | 原理 |
|--------|------|------|
| 1 | **编码变换** | URL 编码、双重编码、Unicode 编码、Hex 编码、HTML 实体编码 |
| 2 | **大小写混淆** | `union select` → `UnIoN SeLeCt`、`<script>` → `<ScRiPt>` |
| 3 | **分隔符注入** | 空格 → `/**/`、`%0a`、`%09`、`+`、`%20` |
| 4 | **关键字拆分** | `union select` → `un/**/ion se/**/lect` |
| 5 | **协议层差异** | HTTP/2 降级、chunked 编码、pipelining |
| 6 | **Content-Type 切换** | `application/x-www-form-urlencoded` → `application/json`、`text/xml` |
| 7 | **请求方法变更** | `GET` → `POST`、`PUT`、`PATCH` |
| 8 | **分块传输** | Transfer-Encoding: chunked 拆分 payload |
| 9 | **Body 字符集编码** | UTF-16/32、IBM037(EBCDIC)——WAF 无法解析但服务器能解析 |
| 10 | **Body 压缩** | Gzip 压缩请求体 |
| 11 | **Content-Type 欺骗** | 伪装为 form/multipart/text-plain，绕过 JSON/XML 解析规则 |
| 12 | **Ghost Bits** | Java `char→byte` 截断，用低 8 位相同的 Unicode 字符绕过 WAF |

### 决策规则

- 先用**最小 payload**（如 `'`、`<`、`union`）探测 WAF 触发条件
- 识别 WAF 规则后，**只针对被拦截的部分做编码/变换**，不对整个 payload 盲目编码
- 编码变换逐层升级：单层 URL 编码 → 双重编码 → Unicode → Hex
- 每次变换后对比响应差异，确认 WAF 是否放行
- **Body 编码变换**仅对有 Body 的请求（POST/PUT/PATCH）生效，参见 [payloads.md](references/payloads.md) 第 2 节

### Ghost Bits 专项

Java 生态中 `char` 是 16 位，但 `(byte) ch` 或 `ch & 0xFF` 截断为低 8 位。用低 8 位相同的 Unicode 字符替换 ASCII payload，WAF 看到 Unicode 不拦截，后端截断后看到原始 ASCII。

**适用场景**：目标是 Java 中间件（Spring/Tomcat/Jetty/Fastjson/Jackson/BCEL）且 WAF 拦截了关键字符。

**使用流程**：
1. 确定后端最终要看到的 ASCII payload
2. 为每个字符查找低 8 位相同的 Unicode 字符（参见 [payloads.md](references/payloads.md) 第 3 节 atoms 表）
3. 某些场景需要 Raw Socket 发送（绕过 HTTP 库的规范化）
4. **禁止对 Unicode 数字类漏洞换组**——JDK URLDecoder、Fastjson Unicode Digit 依赖字符的真实数字属性

**CVE 模板**：Spring 路径穿越(CVE-2025-41242)、Jetty 松散 Hex(CVE-2023-32315)、Angus Mail CRLF(CVE-2025-7962) 等，详见 [payloads.md](references/payloads.md) 第 3.4 节。

---

## 路径 B：认证绕过

| 手法 | 适用场景 |
|------|---------|
| 默认/弱凭据 | 设备/服务初始密码（admin/admin、root/root） |
| 认证绕过参数 | `?admin=true`、`?debug=1`、`?bypass=1`、`?auth=0` |
| 路径直接访问 | 认证校验仅在入口页，API/后台路径无校验 |
| 请求头伪造 | `X-Forwarded-For: 127.0.0.1`（见 `bypass-403` skill） |
| Token 缺陷 | JWT alg:none、弱密钥（见 `jwt-testing` skill） |
| 会话固定 | 登录前后 session ID 不变 |
| 密码重置滥用 | 重置 token 可预测/复用 |
| OAuth 缺陷 | state 参数缺失、redirect_uri 绕过 |

---

## 路径 C：授权绕过

| 手法 | 适用场景 |
|------|---------|
| ID 枚举 | `/api/user/1` → `/api/user/2`（数字 ID 可遍历） |
| ID 替换 | 请求体中 `userId` 参数替换为其他用户 |
| 角色提升 | 修改请求中的角色/权限标识 |
| 功能访问 | 普通用户访问管理员 API 路径 |
| 越权数据 | 查询接口返回非授权范围的数据 |
| 多步骤跳过 | 跳过校验步骤直接访问后续步骤 |

### 授权测试顺序

1. **水平越权**：用 A 用户的凭据访问 B 用户的数据
2. **垂直越权**：用普通用户凭据访问管理员功能
3. **上下文越权**：跳过业务流程步骤直接操作

---

## 路径 D：速率限制绕过

| 手法 | 原理 |
|------|------|
| IP 轮换 | `X-Forwarded-For` 伪造不同 IP（见 `bypass-403` skill） |
| Header 轮换 | 变换 User-Agent、Accept-Language 等 |
| 大小写变体 | `admin` / `Admin` / `ADMIN`（若限流基于精确匹配） |
| 路径变体 | `/login` / `/login/` / `/./login`（若限流基于路径匹配） |
| 并发请求 | 并行发送突破串行限流 |
| 分布式入口 | 通过不同子域名/CDN 节点访问同一后端 |
| HTTP/2 多路复用 | 单连接多流绕过连接级限流 |

---

## 路径 E：输入过滤绕过

| 手法 | 适用场景 |
|------|---------|
| 编码绕过 | URL 编码、双重编码、Base64、Hex、Unicode |
| 替代语法 | SQL: `||` 替代 `OR`；XSS: `<img onerror>` 替代 `<script>` |
| 字符替换 | 过滤 `union` → `uniunionon`（过滤后重组） |
| 大小写混淆 | 关键字大小写混合 |
| Content-Type 切换 | URL-encoded → JSON / XML（解析器差异） |
| 参数污染 | 同名参数多次传递（HPP） |
| 编码嵌套 | URL 编码内嵌 Base64 内嵌 Hex |
| Ghost Bits | Java char→byte 截断（见路径 A Ghost Bits 专项） |

### 决策规则

- 先发送**原始 payload** 探测过滤行为
- 识别被过滤的字符/关键字后，**只针对被过滤部分做变换**
- 变换后验证 payload 语义是否保持完整（编码不应破坏攻击逻辑）
- 逐层升级：单层编码 → 双层编码 → 替代语法 → 组合变换

---

## 路径 F：CSP / XSS 过滤绕过

| 手法 | 适用场景 |
|------|---------|
| CSP 绕过 | 利用 `unsafe-inline`、`unsafe-eval`、允许的 CDN 域名 |
| 同源脚本注入 | 在 CSP 允许的域名下找到 JSONP 端点或可控 JS |
| `base-uri` 缺失 | `<base>` 标签劫持资源加载路径 |
| `object-src` 缺失 | `<object>`/`<embed>` 加载 Flash/Java |
| DOM clobbering | 利用 DOM 元素命名覆盖 JS 变量 |
| 模板注入 | CSP 不约束 SSTI（服务端执行） |
| nonce 泄露 | 从页面源码提取 CSP nonce 并复用 |

---

## 组合攻击策略

多种控制叠加时，按以下策略组合：

| 场景 | 策略 |
|------|------|
| WAF + 认证 | 先绕 WAF（编码变换），再绕认证（请求头/token） |
| 限流 + WAF | 先绕限流（IP 轮换），再绕 WAF（编码变换） |
| 输入过滤 + 授权 | 先绕输入过滤（编码），再绕授权（参数替换） |
| WAF + CSP | 先绕 WAF 传递 payload，再绕 CSP 执行（同源脚本/nonce） |

**原则**：逐层剥离，先解决最外层控制，再处理内层。每层绕过后验证 payload 是否完整到达后端。

---

## 差异分析方法

**基线建立**：
- 发送不含任何 payload 的正常请求 → 记录响应码、响应体大小、响应时间
- 发送含 payload 但不做变换的请求 → 记录 WAF/过滤行为（拦截？清洗？报错？）

**绕过判定标准**：

| 信号 | 判定 |
|------|------|
| 被 WAF 拦截（403/WAF 页面）→ 变换后返回 200 且 payload 生效 | ⭐ WAF 绕过成功 |
| 认证失败（401）→ 变换后返回 200 且有实际内容 | ⭐ 认证绕过成功 |
| 限流（429）→ 变换后返回 200 | ⭐ 限流绕过成功 |
| 过滤清洗 payload → 变换后 payload 完整到达后端 | ⭐ 过滤绕过成功 |
| 响应码不变但响应体/响应时间变化 | 需进一步验证 payload 是否生效 |

**关键**：绕过 WAF/过滤 ≠ 漏洞利用成功。payload 到达后端后，还需验证漏洞本身是否成立（如 SQL 注入是否真的执行了）。

---

## 误判排除

| 现象 | 真绕过 | 误判 |
|------|--------|------|
| WAF 403 → 200，但 body = WAF 误报页面 | ❌ | WAF 规则误触发 |
| 401 → 200，但 body = 登录页 | ❌ | 只是跳转 |
| 429 → 200，但实际限流计数器未重置 | ❌ | 临时窗口 |
| 过滤后 payload 到达后端但报错 | ❌ | payload 格式问题，非绕过 |

---

## 批量测试策略

必须脚本化批量测试，**禁止逐条手动请求**：

- 编码变换：遍历编码类型 × payload 变体，Python `itertools.product`
- 请求头伪造：header × 值的笛卡尔积
- 路径变形：suffix × prefix × boundary_insert 的组合（参见 [payloads.md](references/payloads.md) 第 1 节）
- 每条请求必须带 `--max-time 10 --connect-timeout 5`
- `allow_redirects=False`
- 结果按响应码分组，只输出与基线不同的条目

---

## 超时铁律

| 约束 | 值 |
|------|-----|
| curl 超时 | `--max-time 10 --connect-timeout 5` |
| Python requests | `timeout=(5, 10)` |
| 单条路径测试上限 | 5 分钟 |
| 全路径测试上限 | 20 分钟 |
| HTTPS 证书 | 始终忽略 |
| sleep（限流等待） | 最长 5s |

---

## 完成定义

**Positive exit**：
- 安全控制被成功绕过，且 payload/请求到达了受保护资源。
- artifact 含：控制类型识别、绕过手法、原始请求 vs 绕过请求、响应码对比、响应体片段。

**Negative exit**：
- 适用于该控制类型的所有手法路径均已测试。
- WAF 绕过：至少测试编码变换（3 层）+ 大小写 + 分隔符 + Content-Type 切换 + Body 编码（若有 Body）。
- 认证/授权绕过：至少测试路径直接访问 + 请求头伪造 + 参数替换。
- 限流绕过：至少测试 IP 轮换 + Header 轮换 + 并发。
- 输入过滤绕过：至少测试编码变换（3 层）+ 替代语法 + 字符替换。
- 不输出"确认不可绕过"，只输出"当前测试向量下未发现绕过"。
