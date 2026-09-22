#!/usr/bin/env bash
# nginxアクセスログ(LTSV)をalpで集計する。SUM（合計時間）降順 = 潰す価値が高い順。
#
# ISUPipe のパスパラメータ（livestream_id・username）を持つエンドポイントは -m でまとめないとばらけて読めない。
# クエリ文字列（search の tag・limit など）は alp が既定で落とすのでそのままでよい。
#
# usage: ./tools/analyze/alp.sh [host]
set -euo pipefail
# isuenv の ssh 設定は known_hosts を持たないので、毎回出る "Permanently added" 警告を黙らせる
ssh() { command ssh -o LogLevel=ERROR "$@"; }
scp() { command scp -o LogLevel=ERROR "$@"; }
export RSYNC_RSH="ssh -o LogLevel=ERROR"

HOST="${1:-isucon13-1}"
LOG="${ACCESS_LOG:-/var/log/nginx/access.log}"

MATCH='^/api/livestream/[0-9]+/livecomment/[0-9]+/report,^/api/livestream/[0-9]+/livecomment,^/api/livestream/[0-9]+/reaction,^/api/livestream/[0-9]+/statistics,^/api/livestream/[0-9]+/report,^/api/livestream/[0-9]+/ngwords,^/api/livestream/[0-9]+/moderate,^/api/livestream/[0-9]+/enter,^/api/livestream/[0-9]+/exit,^/api/livestream/[0-9]+$,^/api/user/[^/]+/icon,^/api/user/[^/]+/theme,^/api/user/[^/]+/statistics,^/api/user/[^/]+/livestream,^/api/user/[^/]+$,^/assets/.+'

ssh "$HOST" "sudo alp ltsv --file '$LOG' \
  --sort sum -r \
  -m '$MATCH' \
  -o count,method,uri,min,avg,max,sum,p99,2xx,3xx,4xx,5xx"
