# スコア推移

`make bench` が自動で1行ずつ追記する。判断（採用/revert）はメモ列に追記する。

- **スコア** = 負荷走行中に投稿された投げ銭(tip)の合計。
- **エラー** = ベンチの messages 件数（`[一般エラー]` など。詳細は `measurements/<ts>/score.json`）。
- **DNS** = 名前解決の成功数/失敗数（水責め攻撃を含む）。失敗が出ていたら pdns が詰まっている。
- **init** = `POST /api/initialize` にかかった秒数（42秒でタイムアウト → FAIL）。
- `pass=false` のスコアは無効。

| 時刻 | スコア | 前回比 | pass | エラー | DNS 成功/失敗 | 視聴完了 | init秒 | 変更 | commit | 計測 | メモ |
| --- | ---: | ---: | --- | ---: | --- | ---: | ---: | --- | --- | --- | --- |
| 09-22 17:27 | 3211 | - | ok | 7 | 2632/3 | 11 | 15.2 | fix(run.sh): ベンチ機の /tmp/result.json は isucon 所有なので sudo で消す（消せず && で中断していた） | `a9da39c` | 20260922-172455 | ベースライン（計測ON） |
| 09-22 17:31 | 7288 | +127.0% | ok | 6 | 12216/5 | 34 | 15.8 | インデックス追加（isupipe 13本 + isudns.records(name,type)） | `1c935fb` | 20260922-172908 | インデックス追加 |
| 09-22 17:33 | 11202 | +53.7% | ok | 3 | 8304/0 | 54 | 15.3 | DB接続: interpolateParams / MaxOpenConns 64、echo のデバッグ・リクエストログを止める | `26e2997` | 20260922-173149 | DB接続設定・ログ停止 |
| 09-22 17:37 | 11235 | +0.3% | ok | 1 | 8019/1 | 54 | 15.5 | POST /api/icon: DELETE+INSERT を UNIQUE(user_id) の upsert に（デッドロックで 500 が 24 件） | `9f79cc1` | 20260922-173522 | icon upsert（デッドロック修正） |
| 09-22 17:39 | 11130 | -0.9% | ok | 6 | 5753/0 | 52 | 2.6 | MySQL: binlog 停止、flush_log_at_trx_commit=2、buffer_pool 1G、max_connections 1024 | `519a48e` | 20260922-173749 | MySQL 設定 |
