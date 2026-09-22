#!/usr/bin/env bash
# ベンチ結果を scores/log.md に1行追記し、前回比を出す。
#
# usage: ./tools/bench/record.sh <measurement_dir> [note]
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

DIR="${1:?usage: record.sh <measurement_dir> [note]}"
NOTE="${2:-}"
LOG=scores/log.md

if [ ! -f "$LOG" ]; then
  cat > "$LOG" <<'HDR'
# スコア推移

`make bench` が自動で1行ずつ追記する。判断（採用/revert）はメモ列に追記する。

- **スコア** = 負荷走行中に投稿された投げ銭(tip)の合計。
- **エラー** = ベンチの messages 件数（`[一般エラー]` など。詳細は `measurements/<ts>/score.json`）。
- **DNS** = 名前解決の成功数/失敗数（水責め攻撃を含む）。失敗が出ていたら pdns が詰まっている。
- **init** = `POST /api/initialize` にかかった秒数（42秒でタイムアウト → FAIL）。
- `pass=false` のスコアは無効。

| 時刻 | スコア | 前回比 | pass | エラー | DNS 成功/失敗 | 視聴完了 | init秒 | 変更 | commit | 計測 | メモ |
| --- | ---: | ---: | --- | ---: | --- | ---: | ---: | --- | --- | --- | --- |
HDR
fi

read -r SCORE PASS ERR DNS VIEW INIT < <(python3 - "$DIR/score.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
d = s.get("dns") or {}
print(s["score"], s["pass"], s["error_count"], f"{d.get('resolved','-')}/{d.get('failed','-')}",
      s.get("viewers_completed", "-"), (s.get("phases") or {}).get("initialize_sec", "-"))
PY
)

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo '-')"
SUBJECT="$(git log -1 --format=%s 2>/dev/null || echo '-')"
NOW="$(date '+%m-%d %H:%M')"

PREV="$(grep -E '^\| [0-9-]+ [0-9:]+ \|' "$LOG" | tail -1 | awk -F'|' '{gsub(/ /,"",$3); print $3}' || true)"
DELTA="-"
if [[ "$PREV" =~ ^[0-9]+$ ]] && [ "$PREV" -gt 0 ]; then
  DELTA="$(awk -v a="$SCORE" -v b="$PREV" 'BEGIN{printf "%+.1f%%", (a-b)*100.0/b}')"
fi

PASSMARK="$PASS"
[ "$PASS" = "True" ] && PASSMARK="ok"
[ "$PASS" = "False" ] && PASSMARK="**FAIL**"

printf '| %s | %s | %s | %s | %s | %s | %s | %s | %s | `%s` | %s | %s |\n' \
  "$NOW" "$SCORE" "$DELTA" "$PASSMARK" "$ERR" "$DNS" "$VIEW" "$INIT" "${SUBJECT//|/\\|}" "$COMMIT" "${DIR#measurements/}" "$NOTE" >> "$LOG"

echo "recorded: score=$SCORE (prev=${PREV:-none}, $DELTA) pass=$PASS -> $LOG"
