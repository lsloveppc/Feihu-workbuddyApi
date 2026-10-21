#!/usr/bin/env python3
"""chanprobe.py — 上游请求形态诊断工具。

用途
----
同一账号、同一请求体，逐项改变请求形态（TLS 指纹 / User-Agent / 请求头组 /
端点路径 / 设备令牌），记录上游响应，用于排查某类请求失败的具体成因。
每次只改一个变量，结果才有对照意义；定位清楚之前不建议先改网关代码，
否则多个变量一起动，就分不清是哪一项起作用。

可调维度
--------
  TLS      客户端 TLS 指纹（curl 默认 / Chrome）
  UA       User-Agent 取值
  Headers  请求头组（完整 / 最小）
  Path     端点路径（/v2 与 /console）
  Device   X-Device-Token 是否携带

用法
----
    # 只看将跑哪些变体（不需要 token）
    python scripts/chanprobe.py --dry-run

    # 真实探测（会消耗账号积分，用最小 max_tokens）
    python scripts/chanprobe.py --auth auths/workbuddy-<uid>.json --model deepseek-v4.1

    # 只跑某一组
    python scripts/chanprobe.py --auth auths/x.json --only baseline,tls_chrome

输出
----
    <out>/chanprobe-<ts>.json   完整记录（含每变体的响应原文前 400 字）
    stdout                      矩阵表

注意
----
  * 默认每变体只发 1 次；`--repeat N` 才重复（减少对服务的打扰）。
  * 变体之间 sleep（默认 1.5s），避免把限流响应误读成其他原因。
  * 若同一轮所有变体返回相同错误，通常是账号或凭证层面的问题，而非请求形态。
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import sys
import time
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

try:
    from curl_cffi import requests as cffi_requests
except ImportError:  # pragma: no cover - 环境缺依赖时给出可执行提示
    print("缺少 curl_cffi：python -m pip install curl_cffi", file=sys.stderr)
    raise SystemExit(2)


# ---------------------------------------------------------------------------
# 上游常量（与 internal/upstream 保持一致，改动须同步）
# ---------------------------------------------------------------------------

BASES = {
    "cn": "https://copilot.tencent.com",
    "global": "https://www.workbuddy.ai",
}
ORIGINS = {
    "cn": "https://www.codebuddy.cn",
    "global": "https://www.workbuddy.ai",
}
CHAT_PATHS = {
    "v2": "/v2/chat/completions",
    "console": "/console/chat/completions",
}
# headers.go:19-22 的内置默认版本段
CLIENT_VERSION = "5.5.4"
CLI_VERSION = "2.137.1"
# desktop.go:43 抓包实测的桌面端 UA（5.5.6 内嵌 CLI 2.137.1）
DESKTOP_UA = "WorkBuddy/5.5.6 WorkBuddy/5.5.6 CLI/2.137.1"


def official_ua(realm: str) -> str:
    platform = "WorkBuddy AI" if realm == "global" else "WorkBuddy"
    return f"WorkBuddy/{CLIENT_VERSION} {platform}/{CLIENT_VERSION} CLI/{CLI_VERSION}"


def derive_uid_id(uid: str, purpose: str) -> str:
    """headers.go:131-134 的 X-Machine-ID / X-Session-ID 派生（36 hex）。"""
    return hashlib.sha256(f"wb2a:{purpose}:{uid}".encode()).hexdigest()[:36]


# ---------------------------------------------------------------------------
# 响应分类（照 internal/upstream/client.go Classify 的口径）
# ---------------------------------------------------------------------------

def classify(status: int, body: str) -> str:
    low = body.lower()
    has_envelope = ('"code"' in body) or ('"msg"' in body)

    if status == 200:
        if '"code"' in body and not re.search(r'"code"\s*:\s*0\b', body):
            if "11-128" in body:
                return "CHANNEL_REFUSED_200"
        return "OK"
    # 业务码优先
    if "11-128" in body:
        return "CHANNEL_REFUSED"
    if "11140" in body and "request illegal" in low:
        return "ACCOUNT_RESTRICTED"
    if "11102" in body or "11158" in body:
        return "MODEL_BLOCKED"
    if '"code":6021' in body or "queue.full" in low:
        return "UPSTREAM_BUSY"
    if "11115" in body or "prompt is too long" in low:
        return "PROMPT_TOO_LONG"
    if status == 402:
        return "CREDIT_EXHAUSTED"
    if status == 429:
        return "SOFT_RATE"
    if status == 403 and not has_envelope:
        return "WAF_BLOCK"
    if status == 404:
        return "NOT_FOUND"
    if status >= 500:
        return "SERVER"
    if 400 <= status < 500:
        return "CLIENT"
    return f"UNKNOWN_{status}"


# ---------------------------------------------------------------------------
# 变体
# ---------------------------------------------------------------------------

@dataclass
class Variant:
    name: str
    desc: str
    impersonate: str | None = None   # curl_cffi 的 TLS 指纹；None = curl 默认
    path_key: str = "v2"
    headers: dict[str, str] = field(default_factory=dict)
    note: str = ""


def build_variants(auth: dict[str, Any], model: str) -> list[Variant]:
    """构造变体矩阵：每个变体相对 baseline 只多/少一组要素。"""
    realm = auth.get("realm") or "cn"
    uid = auth.get("uid") or ""
    token = auth.get("access_token") or ""
    device_token = auth.get("device_token") or ""
    origin = ORIGINS[realm]

    bearer = {"Authorization": f"Bearer {token}"}
    # 最小必需头：只带认证 + 上游认的 UA，其余一律不加
    minimal = dict(bearer)
    # 完整官方头族（headers.go / desktop.go 实测形状）
    full = dict(bearer)
    full.update({
        "User-Agent": official_ua(realm),
        "Origin": origin,
        "Referer": origin + "/",
        "X-Requested-With": "XMLHttpRequest",
        "X-Domain": re.sub(r"^https?://", "", BASES[realm]),
        "X-Product": "SaaS",
        "X-User-Id": uid,
        "X-IDE-Version": CLIENT_VERSION,
        "X-IDE-Name": "WorkBuddy",
        "X-IDE-Type": "WorkBuddy",
    })
    if uid:
        full["X-Machine-ID"] = derive_uid_id(uid, "machine")
        full["X-Session-ID"] = derive_uid_id(uid, "session")
    if device_token:
        full["X-Device-Token"] = device_token

    desktop_headers = dict(full)
    desktop_headers["User-Agent"] = DESKTOP_UA

    vs = [
        Variant("baseline",
                "完整官方头族 + curl 默认 TLS（Go 状）→ v2",
                None, "v2", full),
        Variant("tls_chrome",
                "完整官方头族 + Chrome TLS 指纹 → v2（检验 L2 TLS 是否被校验）",
                "chrome", "v2", full),
        Variant("tls_chrome_desktop_ua",
                "Chrome TLS + 桌面实测 UA 5.5.6 → v2",
                "chrome", "v2", desktop_headers),
        Variant("minimal_headers",
                "只带 Authorization + 默认 TLS → v2（检验是否根本不需要头族）",
                None, "v2", minimal),
        Variant("minimal_chrome",
                "只带 Authorization + Chrome TLS → v2（分离 TLS 与头族的贡献）",
                "chrome", "v2", minimal),
        Variant("console_path",
                "完整头族 + Chrome TLS → /console（检验路径级 WAF/校验差异）",
                "chrome", "console", full),
        Variant("no_origin",
                "完整头族去掉 Origin/Referer + Chrome TLS → v2",
                "chrome", "v2", {k: v for k, v in full.items()
                                 if k not in ("Origin", "Referer")}),
        Variant("no_device_token",
                "完整头族去掉 X-Device-Token + Chrome TLS → v2",
                "chrome", "v2", {k: v for k, v in full.items()
                                 if k != "X-Device-Token"}),
    ]
    return vs


def payload(model: str, stream: bool) -> bytes:
    # payload.go:1-4 —— 上游拒绝非流式，探测必须带 stream:true
    return json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": "probe"}],
        "stream": stream,
        "max_tokens": 16,
    }, ensure_ascii=False).encode()


# ---------------------------------------------------------------------------
# 执行
# ---------------------------------------------------------------------------

def run_variant(v: Variant, auth: dict[str, Any], model: str, timeout: int) -> dict[str, Any]:
    realm = auth.get("realm") or "cn"
    url = BASES[realm] + CHAT_PATHS[v.path_key]
    headers = dict(v.headers)
    headers.setdefault("Content-Type", "application/json")
    kwargs: dict[str, Any] = {"headers": headers, "timeout": timeout}
    if v.impersonate:
        kwargs["impersonate"] = v.impersonate

    started = time.time()
    rec: dict[str, Any] = {
        "variant": v.name, "desc": v.desc, "url": url,
        "tls": v.impersonate or "curl-default",
        "header_keys": sorted(headers.keys()),
    }
    try:
        resp = cffi_requests.post(url, data=payload(model, True), **kwargs)
        body = resp.text or ""
        rec.update({
            "status": resp.status_code,
            "kind": classify(resp.status_code, body),
            "elapsed_ms": int((time.time() - started) * 1000),
            "body_head": body[:400],
        })
    except Exception as exc:  # 网络/TLS 失败本身也是证据
        rec.update({
            "status": 0,
            "kind": f"TRANSPORT::{type(exc).__name__}",
            "elapsed_ms": int((time.time() - started) * 1000),
            "body_head": str(exc)[:400],
        })
    return rec


def summarize(records: list[dict[str, Any]]) -> None:
    print()
    print(f"{'variant':<24} {'tls':<12} {'st':>4} {'kind':<22} {'ms':>6}")
    print("-" * 74)
    for r in records:
        print(f"{r['variant']:<24} {r['tls']:<12} {r['status']:>4} "
              f"{r['kind']:<22} {r['elapsed_ms']:>6}")

    kinds = {r["kind"] for r in records}
    print()
    if "CHANNEL_REFUSED" in kinds or "CHANNEL_REFUSED_200" in kinds:
        hit = [r["variant"] for r in records
               if r["kind"].startswith("CHANNEL_REFUSED")]
        miss = [r["variant"] for r in records
                if not r["kind"].startswith("CHANNEL_REFUSED")]
        print(f"返回拒绝的变体: {', '.join(hit)}")
        print(f"正常通过的变体: {', '.join(miss) if miss else '（无）'}")
        if miss and hit:
            print("→ 结论方向：对比命中/未命中变体的差异项，差异项即被校验的维度。")
    elif kinds == {"OK"}:
        print("全部变体通过 —— 当前账号此刻未出现拒绝。"
              "此时探测无信息量，需在问题复现时重跑。")
    else:
        print(f"未出现请求拒绝；出现其它分类：{', '.join(sorted(kinds))}")


def main() -> int:
    ap = argparse.ArgumentParser(description="上游请求形态诊断工具")
    ap.add_argument("--auth", help="auths/workbuddy-<uid>.json 路径")
    ap.add_argument("--model", default="deepseek-v4.1")
    ap.add_argument("--stream", action="store_true", default=True)
    ap.add_argument("--timeout", type=int, default=60)
    ap.add_argument("--sleep", type=float, default=1.5)
    ap.add_argument("--repeat", type=int, default=1)
    ap.add_argument("--only", help="逗号分隔的变体名子集")
    ap.add_argument("--out", default="data/chanprobe")
    ap.add_argument("--dry-run", action="store_true")
    args = ap.parse_args()

    auth: dict[str, Any] = {"realm": "cn", "uid": "000000",
                            "access_token": "<TOKEN>", "device_token": ""}
    if args.auth:
        with open(args.auth, "r", encoding="utf-8") as fh:
            raw = json.load(fh)
        # auth 文件有两种形态：扁平形（顶层字段）与插件嵌套形（auth.access_token）
        auth = {
            "access_token": raw.get("access_token") or (raw.get("auth") or {}).get("access_token", ""),
            "uid": raw.get("uid") or (raw.get("auth") or {}).get("uid", ""),
            "realm": raw.get("realm") or (raw.get("auth") or {}).get("realm", "cn"),
            "device_token": raw.get("device_token") or (raw.get("auth") or {}).get("device_token", ""),
        }
        if not auth["access_token"]:
            print(f"{args.auth} 里没有 access_token", file=sys.stderr)
            return 2

    variants = build_variants(auth, args.model)
    if args.only:
        want = {s.strip() for s in args.only.split(",") if s.strip()}
        variants = [v for v in variants if v.name in want]
        if not variants:
            print(f"--only 没匹配到任何变体", file=sys.stderr)
            return 2

    if args.dry_run:
        print(f"账号 realm={auth['realm']} uid={auth['uid']} "
              f"device_token={'有' if auth['device_token'] else '无'}")
        print(f"model={args.model} base={BASES[auth['realm']]}\n")
        for v in variants:
            print(f"- {v.name:<24} tls={v.impersonate or 'curl-default':<13} "
                  f"path={CHAT_PATHS[v.path_key]}")
            print(f"  {v.desc}")
        print(f"\n共 {len(variants)} 个变体，每变体 {args.repeat} 次，"
              f"间隔 {args.sleep}s")
        return 0

    records: list[dict[str, Any]] = []
    for v in variants:
        for i in range(args.repeat):
            rec = run_variant(v, auth, args.model, args.timeout)
            rec["round"] = i
            rec["at"] = datetime.now(timezone.utc).isoformat()
            records.append(rec)
            print(f"[{len(records)}] {v.name:<24} -> {rec['status']} {rec['kind']}")
            time.sleep(args.sleep)

    os.makedirs(args.out, exist_ok=True)
    ts = datetime.now().strftime("%Y%m%d-%H%M%S")
    path = os.path.join(args.out, f"chanprobe-{ts}.json")
    with open(path, "w", encoding="utf-8") as fh:
        json.dump({"model": args.model, "records": records},
                  fh, ensure_ascii=False, indent=2)

    summarize(records)
    print(f"\n记录写入 {path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
