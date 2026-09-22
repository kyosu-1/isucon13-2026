#!/usr/bin/env python3
"""ISUCON13 ベンチマーカーの出力を要約して score.json にする。

入力は measurements/<ts>/ ディレクトリ。次の2つを読む:
  result.json   ベンチが --result-path に書く {"pass","score","messages"}
  bench.log     標準出力（staff-logger）。シナリオ成功/失敗数、名前解決成功数、走行時間など

スコアは投げ銭(tip)の合計。それ以外に改善の判断に効く情報:
  - messages: 「[一般エラー] ...」等。同種をまとめて件数にする（IDなどの数字は N に潰す）
  - scenarios: シナリオ別の成功/失敗回数（負荷がどれだけ回ったか）
  - dns: 水責め攻撃の並列数、名前解決の成功/失敗数（さくら賞の指標。失敗が出ていたら pdns が詰まっている）
  - viewers_completed: 配信を最後まで視聴できた視聴者数
  - phases: 初期化・整合性チェック・負荷走行にかかった秒数（initialize は 42 秒でタイムアウト）

usage: parse.py <measurement_dir> > score.json
"""
import json
import os
import re
import sys
from collections import Counter
from datetime import datetime

d = sys.argv[1]

out = {
    "pass": None,
    "score": 0,
    "error_count": 0,
    "errors": [],
    "messages": [],
    "scenarios": {},
    "dns": {},
    "viewers_completed": None,
    "phases": {},
}

try:
    r = json.load(open(os.path.join(d, "result.json"), encoding="utf-8"))
except Exception:
    r = {}
out["pass"] = r.get("pass")
out["score"] = r.get("score", 0) or 0
msgs = r.get("messages") or []
# 「売上: N」「名前解決成功数 N」は情報。エラーは "[一般エラー]" のように [ で始まる
info = [m for m in msgs if not m.startswith("[")]
msgs = [m for m in msgs if m.startswith("[")]
out["error_count"] = len(msgs)
out["info"] = info
c = Counter()
for m in msgs:
    key = re.sub(r"/api/user/[^/\s\"]+", "/api/user/<name>", m)
    key = re.sub(r"\d+", "N", key)
    c[key] += 1
out["errors"] = [{"count": n, "msg": m[:300]} for m, n in c.most_common(20)]
out["messages"] = msgs[:5]

def ts(line):
    m = re.match(r"(\S+)\t", line)
    try:
        return datetime.fromisoformat(m.group(1).replace("Z", "+00:00")).timestamp()
    except Exception:
        return None

marks = {}
try:
    for ln in open(os.path.join(d, "bench.log"), encoding="utf-8", errors="replace"):
        ln = ln.rstrip("\n")
        m = re.search(r"\[(失敗シナリオ|シナリオ) ([\w-]+)\] (?:(\d+) 回成功)?(?:, )?(?:(\d+) 回失敗)?", ln)
        if m:
            out["scenarios"][m.group(2)] = {"success": int(m.group(3) or 0), "fail": int(m.group(4) or 0)}
            continue
        m = re.search(r"DNSAttacker並列数: (\d+)", ln)
        if m:
            out["dns"]["attackers"] = int(m.group(1)); continue
        m = re.search(r"名前解決成功数: (\d+)", ln)
        if m:
            out["dns"]["resolved"] = int(m.group(1)); continue
        m = re.search(r"名前解決失敗数: (\d+)", ln)
        if m:
            out["dns"]["failed"] = int(m.group(1)); continue
        m = re.search(r"配信を最後まで視聴できた視聴者数.*\"viewers\": (\d+)", ln)
        if m:
            out["viewers_completed"] = int(m.group(1)); continue
        m = re.search(r"スコア: (\d+)", ln)
        if m and not out["score"]:
            out["score"] = int(m.group(1))
        for key, pat in (("init_start", "webappの初期化を行います"),
                         ("pretest_start", "データ整合性チェックを行います"),
                         ("load_start", "ベンチマーク走行を開始します"),
                         ("load_end", "ベンチマーク走行を停止します")):
            if pat in ln and key not in marks:
                t = ts(ln)
                if t: marks[key] = t
except FileNotFoundError:
    pass

if "init_start" in marks and "pretest_start" in marks:
    out["phases"]["initialize_sec"] = round(marks["pretest_start"] - marks["init_start"], 1)
if "pretest_start" in marks and "load_start" in marks:
    out["phases"]["pretest_sec"] = round(marks["load_start"] - marks["pretest_start"], 1)
if "load_start" in marks and "load_end" in marks:
    out["phases"]["load_sec"] = round(marks["load_end"] - marks["load_start"], 1)

out["dns_resolved"] = out["dns"].get("resolved")
out["scenario_success"] = sum(v["success"] for v in out["scenarios"].values())
out["scenario_fail"] = sum(v["fail"] for v in out["scenarios"].values())

json.dump(out, sys.stdout, ensure_ascii=False, indent=2)
print()
