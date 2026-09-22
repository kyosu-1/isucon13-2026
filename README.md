# isucon13 2026

[ISUCON13](https://github.com/isucon/isucon13)（ISUPipe）を、レギュレーションを守った上でどこまでスコアを伸ばせるか検証するリポジトリ。
環境は [isuenv](https://github.com/kyosu-1/isuenv) で AWS 上に作り（競技 c5.large × 3 + ベンチ専用 c5.2xlarge × 1）、
計測 → 改善のループは Claude Code が回した。

## 構成

| ノード | 役割 |
| --- | --- |
| `isucon13-1` (c5.large) | ベンチの入口。nginx / Go アプリ / MySQL / PowerDNS（初期状態） |
| `isucon13-2` (c5.large) | （未使用） |
| `isucon13-3` (c5.large) | （未使用） |
| `isucon13-4` (c5.2xlarge) | ベンチマーカー専用（競技サーバーではない） |

役割は `etc/isuN/services` が正。改善の経緯は `docs/journal.md`、スコアは `scores/log.md`。

## 使い方

```sh
aws login --profile personal
AWS_PROFILE=personal isuenv up isucon13 --nodes 3 --bench-instance-type c5.2xlarge --ttl 12h
make setup
make bench
```
