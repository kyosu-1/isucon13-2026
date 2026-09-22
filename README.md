# isucon13 2026

[ISUCON13](https://github.com/isucon/isucon13)（ISUPipe）を、レギュレーションを守った上でどこまでスコアを伸ばせるか検証するリポジトリ。
環境は [isuenv](https://github.com/kyosu-1/isuenv) で AWS 上に作り（競技 c5.large × 3 + ベンチ専用 c5.2xlarge × 1）、
計測 → 改善のループは Claude Code が回した。

## 到達点

| | スコア |
| --- | ---: |
| ベースライン（Go / 無改善、計測ON） | 3,211 |
| **現在** | **約 500,000**（42〜50万。同一コードで 2峰性のブレあり） |

- 再起動試験（3台同時 reboot → 全サービス自動起動 → ベンチ）に合格
- ベンチマーカーは本番（ECS 8 vCPU / 8 GB）相当に、ベンチプロセスを cgroup で 8 GB に制限して実行
- 改善の経緯は `docs/journal.md`、全計測は `scores/log.md` と `measurements/`

## 構成

| ノード | 役割 |
| --- | --- |
| `isucon13-1` (c5.large) | Go アプリ（:8080、状態をメモリに持つので 1 プロセス）+ アプリ内 DNS（:53、PowerDNS の代わり） |
| `isucon13-2` (c5.large) | MySQL |
| `isucon13-3` (c5.large) | nginx（TLS 終端・HTTP/2・静的ファイル・アイコンのキャッシュ）。DNS の A レコードはここを指す |
| `isucon13-4` (c5.2xlarge) | ベンチマーカー専用（競技サーバーではない） |

役割は `etc/isuN/services` が正。改善の経緯は `docs/journal.md`、スコアは `scores/log.md`。

## 使い方

```sh
aws login --profile personal
AWS_PROFILE=personal isuenv up isucon13 --nodes 3 --bench-instance-type c5.2xlarge --ttl 12h
make setup
make bench
```
