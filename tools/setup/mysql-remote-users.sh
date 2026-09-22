#!/usr/bin/env bash
# DB ノードの MySQL に、他ノードから接続できるユーザーを作る（冪等）。
#
# AMI の isucon / isudns ユーザーは localhost からしか接続できない
# （ERROR 1130 Host '...' is not allowed to connect）。MySQL を別ノードに出すときに1回だけ流す。
# mysql.user に永続化されるので再起動しても消えない。
#
# usage: ./tools/setup/mysql-remote-users.sh [db_host]
set -euo pipefail
ssh() { command ssh -o LogLevel=ERROR "$@"; }

HOST="${1:-isucon13-2}"
echo "==> $HOST : MySQL のリモート接続ユーザー"
ssh "$HOST" "sudo mysql -e \"
CREATE USER IF NOT EXISTS 'isucon'@'%' IDENTIFIED BY 'isucon';
GRANT ALL PRIVILEGES ON isupipe.* TO 'isucon'@'%';
CREATE USER IF NOT EXISTS 'isudns'@'%' IDENTIFIED BY 'isudns';
GRANT ALL PRIVILEGES ON isudns.* TO 'isudns'@'%';
FLUSH PRIVILEGES;
SELECT user, host FROM mysql.user WHERE user IN ('isucon','isudns');\" 2>&1 | grep -v 'unable to resolve host'"
