#!/usr/bin/env bash
# 計測ループの本体。1回叩けば「ログ初期化 → ベンチ → 解析 → 記録」まで終わる。
#
# 出力は measurements/<timestamp>/ に全部入る（gitにコミットする）:
#   score.json               ベンチ結果の要約（score / pass / メッセージ / エラー内訳 / DNS）
#   bench.log                ベンチの標準出力（staff-logger の生ログ）
#   result.json              ベンチが書く結果 JSON（--result-path）
#   contestant.log           選手向けログ（エラーの理由はここ）
#   alp.txt                  エンドポイント別集計（SUM降順）
#   slow.txt                 クエリ別集計（pt-query-digest）
#   cpu-<host>.txt           プロセス別CPU（pidstat の平均）
#   vmstat-<host>.txt        マシン全体のCPU/IO（ベンチ機含む）
#   app-errors-<host>.txt    アプリのエラーログ（種類別件数）
#   meta.txt                 コミット・ホスト・条件
#
# usage: ./tools/bench/run.sh ["メモ"]
set -euo pipefail
ssh() { command ssh -o LogLevel=ERROR "$@"; }
scp() { command scp -o LogLevel=ERROR "$@"; }
export RSYNC_RSH="ssh -o LogLevel=ERROR"

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

NOTE="${1:-}"
ENTRY="${ENTRY:-isucon13-1}"          # nginx（アクセスログ）が動くノード
DB_HOST="${DB_HOST:-isucon13-1}"
DNS_HOST="${DNS_HOST:-$ENTRY}"        # ベンチが名前解決に使うノード
read -r -a APP_HOSTS_ARR <<< "${APP_HOSTS:-isucon13-1 isucon13-2 isucon13-3}"
BENCH="${BENCH:?BENCH が未設定です。make 経由で実行してください}"

ip_of() { ssh -o ConnectTimeout=5 "$1" 'hostname -I | awk "{print \$1}"'; }
n="${DNS_HOST##*-}"; v="ISU${n}_IP"
TARGET_IP="${!v:-$(ip_of "$DNS_HOST")}"
BENCH_IP="${BENCH_IP:-$(ip_of "$BENCH")}"
# 名前解決の結果として許容する競技サーバーのIP（nameserver 自身は自動で含まれる）
WEBAPP_FLAGS=""
for h in "${APP_HOSTS_ARR[@]}"; do
  m="${h##*-}"; w="ISU${m}_IP"; ip="${!w:-}"
  [ -n "$ip" ] && [ "$ip" != "$TARGET_IP" ] && WEBAPP_FLAGS="$WEBAPP_FLAGS --webapp $ip"
done

TS="$(date +%Y%m%d-%H%M%S)"
OUT="measurements/$TS"
mkdir -p "$OUT"

COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo '-')"
SUBJECT="$(git log -1 --format=%s 2>/dev/null || echo '-')"
{
  echo "timestamp : $TS"
  echo "commit    : $COMMIT  $SUBJECT"
  echo "entry     : $ENTRY (nginx)"
  echo "dns_host  : $DNS_HOST ($TARGET_IP)"
  echo "app_hosts : ${APP_HOSTS_ARR[*]}"
  echo "db_host   : $DB_HOST"
  echo "bench     : $BENCH ($BENCH_IP) MemoryMax=${BENCH_MEMORY_MAX:-8G}"
  echo "note      : $NOTE"
  echo "dirty     : $(git status --porcelain -- webapp etc | wc -l | tr -d ' ') files uncommitted (webapp/ etc/)"
} > "$OUT/meta.txt"

echo "==> [1/5] ログ初期化・リソース記録開始"
START_EPOCH="$(date +%s)"
# nginx は複数ノードで動きうるので全ノードのログを消す（alp は ENTRY のものだけ集計する）
for h in "${APP_HOSTS_ARR[@]}"; do
  ssh "$h" "sudo truncate -s 0 /var/log/nginx/access.log /var/log/nginx/error.log 2>/dev/null || true" &
done
wait
ssh "$DB_HOST" "sudo truncate -s 0 /var/log/mysql/slow.log 2>/dev/null || true"
./tools/analyze/mysql-status.sh "$DB_HOST" snapshot > "$OUT/raw-mysql-status-before.txt" 2>/dev/null || true
for h in "${APP_HOSTS_ARR[@]}"; do
  ssh "$h" "nohup sudo sh -c 'vmstat -t 1 150 > /tmp/vmstat.txt 2>&1' >/dev/null 2>&1 &
            nohup sudo sh -c 'LC_ALL=C pidstat -u 1 140 > /tmp/pidstat.txt 2>&1' >/dev/null 2>&1 &
            sleep 0.1" &
done
ssh "$BENCH" "nohup sudo sh -c 'vmstat -t 1 150 > /tmp/vmstat.txt 2>&1' >/dev/null 2>&1 & sleep 0.1" &
wait

echo "==> [2/5] 疎通確認"
code="$(ssh "$ENTRY" "curl -sk -o /dev/null -w '%{http_code}' --resolve pipe.u.isucon.local:443:127.0.0.1 'https://pipe.u.isucon.local/api/tag'")"
dns="$(ssh "$BENCH" "dig +short +time=2 +tries=1 pipe.u.isucon.local @$TARGET_IP | head -1" || true)"
echo "    GET /api/tag -> $code (200 なら正常) / DNS pipe.u.isucon.local -> ${dns:-none}"
if [ "$code" != "200" ]; then
  echo "!! アプリに届いていません。ベンチを中止します。" >&2
  exit 1
fi
if [ -z "$dns" ]; then
  echo "!! DNS が応答していません。ベンチを中止します。" >&2
  exit 1
fi

echo "==> [3/5] ベンチ実行 -> nameserver $TARGET_IP (webapp:$WEBAPP_FLAGS)"
# PPROF_HOST=isucon13-1 make bench なら、負荷走行の途中（開始 55 秒後）から 30 秒の CPU プロファイルを取る
PPROF_PID=""
if [ -n "${PPROF_HOST:-}" ]; then
  ( sleep 55; ./tools/analyze/pprof.sh "$PPROF_HOST" 30 "$OUT/cpu.pprof" > "$OUT/pprof.txt" 2>&1 ) &
  PPROF_PID=$!
fi
set +e
# 本番のベンチマーカーは ECS（8 vCPU / 8 GB）。ベンチ機 c5.2xlarge は 8 vCPU / 16 GB なので、
# メモリだけ cgroup で 8 GB に制限して本番相当にする（swap なし）。ピーク RSS は /usr/bin/time で bench.log に残す
ssh "$BENCH" "cd /home/isucon && sudo rm -f /tmp/result.json /tmp/staff.log /tmp/contestant.log && \
  sudo systemd-run --scope -q -p MemoryMax=${BENCH_MEMORY_MAX:-8G} -p MemorySwapMax=0 \
  sudo -u isucon /usr/bin/time -f 'bench peak RSS: %M KB, elapsed %e s' ./bench run --enable-ssl \
  --nameserver $TARGET_IP $WEBAPP_FLAGS --target https://pipe.u.isucon.local \
  --result-path /tmp/result.json --staff-log-path /tmp/staff.log --contestant-log-path /tmp/contestant.log" \
  > "$OUT/bench.log" 2>&1
BENCH_RC=$?
set -e
scp -q "$BENCH:/tmp/result.json" "$OUT/result.json" 2>/dev/null || echo '{}' > "$OUT/result.json"
scp -q "$BENCH:/tmp/contestant.log" "$OUT/contestant.log" 2>/dev/null || true
python3 tools/bench/parse.py "$OUT" > "$OUT/score.json"

[ -n "$PPROF_PID" ] && wait "$PPROF_PID" || true

echo "==> [4/5] 解析"
for h in "${APP_HOSTS_ARR[@]}"; do
  (
    ssh "$h" "cat /tmp/pidstat.txt" > "$OUT/raw-pidstat-$h.txt" 2>/dev/null || true
    ./tools/analyze/cpu-by-process.sh "$OUT/raw-pidstat-$h.txt" > "$OUT/cpu-$h.txt" 2>/dev/null || true
    rm -f "$OUT/raw-pidstat-$h.txt"
    ssh "$h" "cat /tmp/vmstat.txt" > "$OUT/vmstat-$h.txt" 2>/dev/null || true
    ssh "$h" "sudo journalctl -u isupipe-go --since @$START_EPOCH --no-pager -o cat 2>/dev/null \
      | grep -iE 'level=ERROR|\"level\":\"ERROR\"|panic|error at ' | sed -E 's/[0-9]+/N/g' \
      | cut -c1-200 | sort | uniq -c | sort -rn | head -30" > "$OUT/app-errors-$h.txt" 2>/dev/null || true
    [ -s "$OUT/app-errors-$h.txt" ] || rm -f "$OUT/app-errors-$h.txt"
  ) &
done
ssh "$BENCH" "cat /tmp/vmstat.txt" > "$OUT/vmstat-$BENCH.txt" 2>/dev/null &
( ./tools/analyze/mysql-status.sh "$DB_HOST" snapshot > "$OUT/raw-mysql-status-after.txt" 2>/dev/null \
  && ./tools/analyze/mysql-status.sh diff "$OUT/raw-mysql-status-before.txt" "$OUT/raw-mysql-status-after.txt" > "$OUT/mysql-status.txt"; \
  rm -f "$OUT/raw-mysql-status-before.txt" "$OUT/raw-mysql-status-after.txt" ) &
./tools/analyze/alp.sh "$ENTRY" > "$OUT/alp.txt" 2>&1 &
./tools/analyze/slow.sh "$DB_HOST" > "$OUT/slow.txt" 2>&1 &
wait

echo "==> [5/5] 記録"
./tools/bench/record.sh "$OUT" "$NOTE"

echo
echo "==> measurements: $OUT"
python3 - "$OUT/score.json" <<'PY'
import json, sys
s = json.load(open(sys.argv[1]))
print(f"  score={s['score']} pass={s['pass']} errors={s['error_count']} dns_resolved={s.get('dns_resolved')}")
for m in s.get("messages", [])[:8]:
    print(f"  msg: {m[:200]}")
for e in s.get("errors", [])[:8]:
    print(f"  ERROR x{e['count']}: {e['msg'][:180]}")
PY
for h in "${APP_HOSTS_ARR[@]}" "$BENCH"; do
  busy="$(awk '$15 ~ /^[0-9]+$/ && $15 < 90 {n++; b += 100 - $15} END {if (n) printf "%.0f%% (%ds)", b/n, n; else print "-"}' "$OUT/vmstat-$h.txt" 2>/dev/null)"
  printf '  %-11s busy=%-10s ' "$h" "$busy"; { sed -n '2,5p' "$OUT/cpu-$h.txt" 2>/dev/null || true; } | awk '{printf "%s=%s%% ", $1, $2}'; echo
done
echo "--- alp (上位) ---"; head -16 "$OUT/alp.txt" || true
[ "$BENCH_RC" = 0 ] || echo "!! bench exit code = $BENCH_RC" >&2
