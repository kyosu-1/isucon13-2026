#!/usr/bin/env bash
# ベンチマーカー専用ノードから競技用サービスを止める。
#
# AMIは競技サーバーと同じものなので、何もしないとベンチ機でも
# nginx / mysql / pdns / isupipe-go が動いていてCPUを取られる。
# 負荷をかける側が詰まるとスコアがアプリではなくベンチ機の性能で決まってしまう。
#
# usage: ./tools/setup/bench-node.sh [bench_host]
set -euo pipefail
ssh() { command ssh -o LogLevel=ERROR "$@"; }

HOST="${1:-isucon13-4}"
echo "==> $HOST : 競技用サービスを停止"
ssh "$HOST" 'sudo systemctl disable --now isupipe-go nginx mysql pdns >/dev/null 2>&1 || true
  command -v sar >/dev/null 2>&1 || (sudo apt-get update -qq >/dev/null 2>&1; sudo apt-get install -y -qq sysstat >/dev/null 2>&1)
  sudo systemctl disable --now unattended-upgrades apt-daily.timer apt-daily-upgrade.timer >/dev/null 2>&1 || true
  for s in isupipe-go nginx mysql pdns; do printf "  %-12s %s\n" $s "$(systemctl is-active $s)"; done'
