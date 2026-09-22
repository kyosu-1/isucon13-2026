#!/usr/bin/env python3
"""ベンチ 1 回分の時系列（5 秒刻み）を並べる。速い回と遅い回の比較用。

  viewer        視聴者シナリオの開始数（bench.log の staff-logger）
  reserve_fail  予約失敗（枠切れ。ベンチ側のリクエスト量の目安）
  icon          アイコン変更（配信者シナリオの目安）
  busy-<host>   vmstat の CPU 使用率（100 - idle）
  newconn-<h>   nginx の新規接続数（= TLS フルハンドシェイク数）。conns-<host>.txt があるもの

usage: timeline.py <measurement_dir> [<measurement_dir> ...]
"""
import collections, json, re, sys
from datetime import datetime, timezone

def ts(t):
    return datetime.fromisoformat(t.replace("Z", "+00:00")).timestamp()

def timeline(M):
    start = None
    b = collections.defaultdict(collections.Counter)
    for ln in open(f"{M}/bench.log", encoding="utf-8", errors="replace"):
        m = re.match(r"(\S+)\t", ln)
        if not m:
            continue
        t = m.group(1)
        if "ベンチマーク走行を開始します" in ln:
            start = ts(t)
        if start is None:
            continue
        sec = int((ts(t) - start) // 5) * 5
        if "basic viewer scenario" in ln:
            b[sec]["viewer"] += 1
        elif "reserve: failed to reserve" in ln:
            b[sec]["reserve_fail"] += 1
        elif "change icon" in ln:
            b[sec]["icon"] += 1
    s = json.load(open(f"{M}/score.json"))
    buckets = list(range(0, 60, 5))
    print(f"== {M} score={s['score']} viewers={s['viewers_completed']}")
    print("  " + " " * 14 + " ".join(f"{k:>4}" for k in buckets))
    for key in ("viewer", "reserve_fail", "icon"):
        print(f"  {key:14s}" + " ".join(f"{b[k][key]:>4}" for k in buckets))
    import glob, os
    for f in sorted(glob.glob(f"{M}/vmstat-*.txt")):
        h = os.path.basename(f)[7:-4]
        rows = []
        for ln in open(f):
            x = ln.split()
            if len(x) >= 18 and x[0].isdigit():
                try:
                    t = datetime.strptime(x[-2] + " " + x[-1], "%Y-%m-%d %H:%M:%S").replace(tzinfo=timezone.utc).timestamp()
                except ValueError:
                    continue
                rows.append((t - start, 100 - int(x[14])))
        vals = []
        for bb in buckets:
            xs = [r[1] for r in rows if bb <= r[0] < bb + 5]
            vals.append(sum(xs) / len(xs) if xs else float("nan"))
        print(f"  busy-{h:9s}" + " ".join(f"{v:4.0f}" for v in vals))
    for f in sorted(glob.glob(f"{M}/conns-*.txt")):
        h = os.path.basename(f)[6:-4]
        # conns.sh の t はアクセスログの最初の行からの秒（初期化の分だけ負荷開始より早い）
        rows = {}
        for ln in open(f):
            x = ln.split()
            if len(x) >= 3 and x[0].isdigit():
                rows[int(x[0])] = int(x[2])
        if not rows:
            continue
        # 初期化 + 整合性チェック（約 7 秒）ぶんずらして負荷開始に揃える
        off = 5
        print(f"  newconn-{h:6s}" + " ".join(f"{rows.get(k + off, 0):>4}" for k in buckets))

for M in sys.argv[1:]:
    timeline(M.rstrip("/"))
