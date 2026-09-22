# 作業ログ (ISUCON13 / ISUPipe, 2026-09-22〜)

再現性のため、**実行したコマンドと結果を時系列で記録する**。失敗した試行も消さない。

- 1エントリ = 1アクション。`### HH:MM 何をしたか` の見出しで始める（時刻は JST）。
- コマンドは**コピペで再実行できる形**で書く。
- ベンチのスコアは `scores/log.md` が正。ここには「何をしたか・なぜか」を書く。

---

## 2026-09-22

### 17:05 環境作成

```sh
aws login --profile personal
AWS_PROFILE=personal isuenv up isucon13 --nodes 3 --bench-instance-type c5.2xlarge --ttl 12h
```

| ノード | 役割 | タイプ | private |
| --- | --- | --- | --- |
| isucon13-1 | 競技（初期状態では nginx + Go + MySQL + PowerDNS 全部入り。ベンチの入口・DNS） | c5.large | 10.100.0.155 |
| isucon13-2 | 競技 | c5.large | 10.100.0.101 |
| isucon13-3 | 競技 | c5.large | 10.100.0.64 |
| isucon13-4 | ベンチマーカー専用 | c5.2xlarge | 10.100.0.186 |

AMI: `ami-006d211cb716fe8a0 (isucon13-20231128102223)`、Ubuntu 22.04、MySQL 8.0.35、nginx 1.18、PowerDNS 4.8（gmysql backend）。
ドメインは `*.u.isucon.local`（本番の `.dev` は HSTS preload のため AMI では置換されている）。

ベンチ機の競技用サービス（isupipe-go / nginx / mysql / pdns）は止めた（`tools/setup/bench-node.sh`）。

### 17:10 マニュアル・レギュレーションの確認

- `docs/reference/manual.md`（当日マニュアル）: スコア = tip 合計、initialize 42秒、DNS(53/UDP) 必須、
  名前解決結果は3台のIPのみ、水責め攻撃あり、追試は再起動後 75% 以上。
- `docs/reference/app_manual.md`: アイコン反映 2秒、If-None-Match → 304 可、bcrypt 変更不可。
- `docs/reference/regulation.md`: 公開レギュレーション。

### 17:15 DNS の向き先を private IP に

AMI の初期状態では `ISUCON13_POWERDNS_SUBDOMAIN_ADDRESS="127.0.0.1"`（ゾーンの A レコードが 127.0.0.1）。
ベンチ機から名前解決すると 127.0.0.1 が返ってベンチ機自身に繋ぎに行ってしまうので、
入口ノード isucon13-1 の private IP を向くようにした。
（`aws-env-isucon-subdomain-address.service` は disabled なので再起動で上書きされない）

`etc/isuN/home/env.sh` では `__ISU1_IP__` プレースホルダで持ち、`make deploy` が置換する。

### 17:16 素のベースライン（ツール導入前）

```sh
ssh isucon13-4 'cd /home/isucon && sudo -u isucon ./bench run --enable-ssl --nameserver 10.100.0.155 --webapp 10.100.0.101 --webapp 10.100.0.64 --target https://pipe.u.isucon.local --result-path /tmp/result.json'
```

- `pass=true スコア 3545`。名前解決成功 2311 / 失敗 0、水責め並列数 2。
- エラー 7 件: report の 404 が 4、`GET /api/user/:name/statistics` のタイムアウトが 3。
- 初期化 14.9s、整合性チェック 33.2s、負荷 60s。
- `--webapp` は名前解決の結果として許容する競技サーバーIP（nameserver 自身は自動で含まれる）。
- 結果は `--result-path` の JSON（pass / score / messages）と標準出力（シナリオ回数、DNS 成功数）に出る。
  → `tools/bench/parse.py` で `score.json` に要約する。

### 17:20 リポジトリ化・ツール移植

- `initial` コミット: サーバーの初期状態（webapp/go, sql, pdns, /etc の nginx・mysql・powerdns・systemd、env.sh）。
- isucon14-2026 の Makefile / tools を移植。`make setup` 相当（hosts 生成、ベンチ機停止、alp / pt-query-digest / sysstat 導入）。
- nginx のアクセスログを LTSV に（alp 用）。
- `make deploy` → `make measure-on` → `make bench` でツール込みのベースラインを計測。
