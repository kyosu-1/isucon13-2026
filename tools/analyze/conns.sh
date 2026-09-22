#!/usr/bin/env bash
# nginx のアクセスログから、5 秒ごとの リクエスト数 / 新規接続数 / TLS 再開なしのハンドシェイク数 / HTTP/2 の割合 を出す。
# 「遅い回」で TLS ハンドシェイクが増えているかを見るためのもの。
# usage: ./tools/analyze/conns.sh [host]
set -euo pipefail
ssh() { command ssh -o LogLevel=ERROR "$@"; }
HOST="${1:-isucon13-3}"
ssh "$HOST" 'sudo awk -F"\t" '"'"'
  { t=""; cr=""; sr=""; pr="";
    for(i=1;i<=NF;i++){ if($i ~ /^time:/) t=substr($i,6); if($i ~ /^conn_req:/) cr=substr($i,10); if($i ~ /^ssl_reused:/) sr=substr($i,12); if($i ~ /^proto:/) pr=substr($i,7) }
    # time: 22/Sep/2026:11:16:24 +0000 -> 秒
    split(t, a, /[:\/ ]/); sec=a[4]*3600+a[5]*60+a[6]; if (first=="") first=sec; b=int((sec-first)/5)*5;
    req[b]++; if (cr=="1") { newc[b]++; if (sr!="r") hs[b]++ } if (pr=="HTTP/2.0") h2[b]++;
    if (b>maxb) maxb=b }
  END { printf "%5s %7s %7s %7s %7s\n", "t", "req", "newconn", "fullhs", "h2";
        for (b=0;b<=maxb;b+=5) printf "%5d %7d %7d %7d %7d\n", b, req[b], newc[b], hs[b], h2[b] }'"'"' /var/log/nginx/access.log'
