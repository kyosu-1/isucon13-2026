#!/usr/bin/env bash
# MySQL の SHOW GLOBAL STATUS を取る（before / after の差分を見るため）。
#
# usage: ./tools/analyze/mysql-status.sh <db_host> snapshot > file
#        ./tools/analyze/mysql-status.sh diff <before_file> <after_file>
set -euo pipefail
ssh() { command ssh -o LogLevel=ERROR "$@"; }

if [ "${1:-}" = "diff" ]; then
  # ベンチ中の増分が大きい/意味のあるカウンタだけ
  awk 'NR==FNR {a[$1]=$2; next} ($1 in a) && $2 ~ /^[0-9]+$/ && a[$1] ~ /^[0-9]+$/ {d=$2-a[$1]; if (d != 0) printf "%-45s %14d\n", $1, d}' "$2" "$3" \
    | grep -E 'Innodb_(log_waits|log_write_requests|os_log_fsyncs|os_log_written|data_fsyncs|data_writes|data_reads|buffer_pool_wait_free|buffer_pool_pages_flushed|buffer_pool_read_requests|buffer_pool_reads|rows_(read|inserted|updated|deleted)|row_lock_waits|row_lock_time)|Threads_created|Connections|Created_tmp|Handler_read_rnd_next|Select_scan|Select_full_join|Slow_queries|Questions|Table_open_cache_misses|Opened_tables' || true
  exit 0
fi

HOST="${1:?usage: mysql-status.sh <db_host> snapshot | diff <before> <after>}"
ssh "$HOST" 'sudo mysql -N -e "SHOW GLOBAL STATUS" 2>/dev/null'
