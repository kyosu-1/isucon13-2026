# isucon13 2026

[ISUCON13](https://github.com/isucon/isucon13)（ISUPipe）を、レギュレーションを守った上でどこまでスコアを伸ばせるか検証するリポジトリ。
環境は [isuenv](https://github.com/kyosu-1/isuenv) で AWS 上に作り（競技 c5.large × 3 + ベンチ専用 c5.2xlarge × 1）、
計測 → 改善のループは Claude Code が回した。

## 到達点

| | スコア |
| --- | ---: |
| ベースライン（Go / 無改善、計測ON） | 3,211 |
| **最高記録** | **708,197**（計測OFF。同一コードで 64〜71万の 2峰性のブレあり） |

- 再起動試験（3台同時 reboot → 全サービス自動起動 → ベンチ）に合格
- ベンチマーカーは本番（ECS 8 vCPU / 8 GB）相当に、ベンチプロセスを cgroup で 8 GB に制限して実行
- 改善の経緯は `docs/journal.md`、全計測は `scores/log.md` と `measurements/`

### 改善の推移（抜粋。全件は [scores/log.md](scores/log.md)）

| 時刻 | スコア | 変更 |
| --- | ---: | --- |
| 17:24 | 3,211 | ベースライン（計測ON） |
| 17:29 | 7,288 | インデックス追加 |
| 17:31 | 11,202 | interpolateParams・接続プール・デバッグログ停止 |
| 17:41 | 15,546 | statistics のランキング N+1 を集約クエリに |
| 17:44 | 25,624 | ユーザー・テーマ・アイコンをメモリキャッシュ、icon の 304 |
| 17:47 | 64,122 | タグをメモリキャッシュ |
| 17:50 | 115,960 | 配信をメモリキャッシュ |
| 17:53 | 100,938 | moderate を一括 DELETE（モデレーションが回るようになった） |
| 18:00 | 149,875 | MySQL を isu2 に分離 |
| 18:05 | 154,773 | DNS をアプリ内サーバーに（PowerDNS 廃止、pdnsutil 廃止） |
| 18:08 | 260,266 | nginx を isu3 に分離 |
| 18:11 | 308,889 | 予約枠の範囲・N+1 |
| 18:15 | 347,060 | ランキングをメモリ集計 |
| 18:20〜18:27 | 385,724 | 読み取り・単文書き込みのトランザクション廃止、NG ワードキャッシュ |
| 18:35 | 430,512 | nginx keepalive_requests・TLS セッションキャッシュ |
| 18:51 | 419,505 | セッション Cookie のメモ化 |
| 19:15 | 499,869 | goccy/go-json |
| 19:25 | 532,161 | コメント・リアクションをメモリキャッシュ |
| 19:34 | 658,757 | nginx を 3 台に分散（DNS で振り分け） |
| 19:41 | 697,114 | 再起動試験後 |
| 19:56 | **708,197** | 計測OFF |

## 構成

| ノード | 役割 |
| --- | --- |
| `isucon13-1` (c5.large) | Go アプリ（:8080、状態をメモリに持つので 1 プロセス）+ アプリ内 DNS（:53、PowerDNS の代わり）+ nginx |
| `isucon13-2` (c5.large) | MySQL + nginx |
| `isucon13-3` (c5.large) | nginx（`pipe.u.isucon.local` 宛）。配信者サブドメインは DNS が名前のハッシュで 3 台の nginx に振り分ける |
| `isucon13-4` (c5.2xlarge) | ベンチマーカー専用（競技サーバーではない） |

役割は `etc/isuN/services` が正。改善の経緯は `docs/journal.md`、スコアは `scores/log.md`。

## 使い方

```sh
aws login --profile personal
AWS_PROFILE=personal isuenv up isucon13 --nodes 3 --bench-instance-type c5.2xlarge --ttl 12h
make setup
make bench
```
