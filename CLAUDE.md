# CLAUDE.md — ISUCON13 (ISUPipe) 攻略の運用ルール

このリポジトリは **ISUCON13 の問題（ISUPipe）で、レギュレーションを守った上で最高スコアを取る**ためのもの。
AIエージェント（Claude Code）が主体で「計測 → 改善」のループを回す前提で書いてある。
実装言語は **Go**（`webapp/go/`）。

---

## 0. まずこれを読む

| 知りたいこと | 見る場所 |
| --- | --- |
| 何をやったか（再現手順） | `docs/journal.md` |
| スコアの推移 | `scores/log.md` |
| 計測の生データ | `measurements/<timestamp>/` |
| 当日マニュアル（レギュレーション・スコア計算・DNS） | `docs/reference/manual.md` |
| アプリケーション仕様（猶予時間・アイコン・bcrypt） | `docs/reference/app_manual.md` |
| 公開レギュレーション | `docs/reference/regulation.md` |
| 計測改善ループの判断基準 | `.claude/skills/tuning-isucon13/SKILL.md` |

---

## 1. レギュレーション（違反したら全部無意味）

`docs/reference/manual.md` と `docs/reference/app_manual.md` が原典。要点:

### 構成

- 競技サーバーは **c5.large（2 vCPU / 4GB）× 3台**。**この3台だけ**で処理する。
  - isuenv では `isucon13-1..3` が競技サーバー、`isucon13-4`（c5.2xlarge）は**ベンチマーカー専用**。
    ベンチ機にアプリの処理を載せるのは禁止（外部リソースの利用にあたる）。
  - 本番のベンチマーカーは **ECS の 8 vCPU / 8 GB**。ベンチ機 c5.2xlarge は 8 vCPU / 16 GB なので、
    `make bench` はベンチプロセスを cgroup で **MemoryMax=8G** に制限して回す（`tools/bench/run.sh`）。
    ベンチ機のインスタンスタイプは途中で変えない（前後のスコアが比べられなくなる）。
  - 役割分担は `etc/isuN/services` が正（`make deploy` がそのとおりに enable/disable する）。
- ベンチマーカーは **`isucon13-1` の DNS(53/UDP) に名前解決**し、得られたIPに HTTPS(443) でアクセスする。
  名前解決の結果は**3台のIPのどれか**でなければならない（`--webapp` で渡している）。
  ドメインはこの AMI では `*.u.isucon.local`（本番は `*.u.isucon.dev`）。

### スコア

```
スコア = 負荷走行中に投稿された投げ銭(tip)の合計 ISUCOIN
```

- 視聴者が配信にライブコメント（tip付き）を投稿できた分だけ点が入る。
- 視聴者は「配信を最後まで視聴できた」かで増える。スパム（NGワード）への対処、
  配信者のモデレーションが回ると配信品質が上がり視聴者が増える。
- **DNS 水責め攻撃**が来る。名前解決が失敗する（タイムアウト・応答なし）と HTTPS アクセスに進めず負荷が伸びない。
  `score.json` の `dns.resolved / dns.failed` を毎回見る。

### FAIL（スコア無効）になる

- `POST /api/initialize` が **42秒**以内に終わらない
- 整合性チェック（走行前 20秒 / 最終 10秒 のタイムアウト）の失敗
- 名前解決の結果が3台以外のIP、または DNS が応答しない

### 猶予時間・仕様（app_manual）

- アイコン更新 → `icon_hash` と `GET /api/user/:username/icon` への反映: **2秒以内**
- `GET /api/user/:username/icon` は `If-None-Match: "<icon_hash>"` に **304 を返してよい**（条件付きでない場合は返してはいけない）
- **bcrypt のコストやアルゴリズムを変えてはいけない**（`bcrypt.MinCost` = 4 のまま）
- 「モデレート済みスパムの投稿で expected:400 actual:201」の一般エラーは減点対象ではない（マニュアル明記）

### 変更してはいけない

- アクセス先の URI・レスポンスの JSON 構造（空白の増減は可）
- `webapp/public/` の静的ファイル（JS/CSS/画像）の内容
- `POST /api/initialize` のレスポンス `lang`（空だと初期化失敗扱い。`golang` のまま）
- SSH(22) / HTTPS(443) / **DNS(53/UDP)** のセキュリティグループ
- `envcheck` / `aws-env-isucon-subdomain-address` 関連ファイル、`isuadmin` ユーザ

### やっていい

- DBスキーマの変更、インデックス、キャッシュ、ミドルウェアの入れ替え・設定変更
- 3台の役割変更（DB分離、DNS分離など）
- PowerDNS の設定変更・入れ替え（自前の DNS サーバーに置き換えるのも可。ただし 53/UDP で A レコードを返すこと）

### 守らないといけない性質

- **追試**: 3台を再起動 → 負荷走行。**再現スコアが最終スコアの75%以下だと fail**。
  → 手で起動したプロセス、`enable` していないサービス、起動順序に依存した構成は全部アウト。
  → 終盤に `make restart-test` を必ず通す。
- 負荷走行中に書き込まれたデータは再起動後も取得できること（上位チームに対して確認される）。
  アプリ内キャッシュは `POST /api/initialize` と起動時に DB から作り直す。

---

## 2. 大原則

1. **計測なき改善は禁止。** 変更前に必ず根拠（alp / スロークエリ / pidstat / ベンチのエラー）を
   `measurements/` から示す。
2. **1改善 = 1コミット = 1ベンチ。** 複数の変更を混ぜない。
3. **スコアが下がったら revert。** ただしスコアのブレ（同一コードでの差）を先に把握しておくこと。
4. **サーバー上で直接編集しない。** コードも `/etc` もローカルの git が正。反映は `make deploy` だけ。
   ssh越しの**読み取り**（ログ、EXPLAIN、top）は自由。
5. **やったことは全部 `docs/journal.md` に残す。** 失敗した試行も消さない。
6. **計測の記録はすべてコミットする。** `make bench` の出力 `measurements/<ts>/` と
   `scores/log.md` は、ベンチのたびに `git commit` する（コードの変更とは別コミット）。
7. **終盤は守りに入る。** 再起動試験・計測OFF・最終確認を優先する。

---

## 3. 改善ループ

```
make bench          # ログ初期化 → ベンチ → alp/slow/pidstat/vmstat収集 → scores/log.md に記録
  ↓
measurements/<ts>/ を読む（score.json のエラー・DNS・シナリオ回数、alp.txt、slow.txt、cpu-*.txt）
  ↓
支配的なボトルネックを1つだけ特定（根拠を添えて）
  ↓
最小の手を選んでローカルで実装 → git commit（根拠を本文に書く）
  ↓
make deploy && make bench     # 上がれば継続、下がれば git revert
  ↓
git add measurements scores && git commit -m "bench: ..."
```

### ボトルネックの読み方

| 症状 | 見るもの | 疑うこと |
| --- | --- | --- |
| `dns.failed` が出る／`dns.resolved` が低い | `score.json` | pdns が MySQL を叩いて詰まっている。DNS のキャッシュ・軽量化・分離 |
| 特定URIの `SUM` が突出 | `alp.txt` | そのエンドポイントの実装（N+1、全件取得） |
| 同じクエリの Calls が異常 | `slow.txt` | N+1（fillXxxResponse の連鎖） |
| `Rows examine` >> `Rows sent` | `slow.txt` | インデックス欠如 |
| mysqld が CPU を支配 | `cpu-*.txt` | DB。インデックス／クエリ／DB分離 |
| isupipe が CPU を支配 | `cpu-*.txt` | アプリ。JSON・ログ出力・bcrypt |
| pdns が CPU を食う | `cpu-*.txt` | 水責め攻撃の NXDOMAIN 応答が MySQL 経由。ネガティブキャッシュ |
| `[リクエストタイムアウト]` | `score.json` の errors | 遅いエンドポイント（statistics 系の全件ランキング） |
| init秒 が 30 を超える | `scores/log.md` | initialize が遅い（42秒で FAIL） |

---

## 4. 自律で進めてよい範囲

**承認なしで進めてよい:**

- 計測、解析、ログの読み取り
- ローカルでのコード・設定の変更、コミット
- `make deploy` / `make bench` / `make restart-test`（＝改善ループを回し続けること）
- スコアが下がった変更の revert
- 3台の役割分担の変更（DB分離、DNS分離など。レギュレーション内）
- 自分のリポジトリへの push

**必ず先に確認する:**

- `make down`（環境の破棄）、`isuenv nuke`、環境の作り直し
- ベンチ機のインスタンスタイプの変更（前後の点数が比べられなくなる）
- リポジトリの公開設定の変更
- レギュレーションの解釈が割れる変更

---

## 5. やってはいけないこと

- **ベンチマーカーのソースを読んで挙動を先回りする。** ベンチマーカーはブラックボックスとして扱う。
  判断材料は外から観測できるもの（スコア、エラー、シナリオ回数、DNS 成功数、alp、自分のサーバーのログ）だけ。
  仮説があるなら、読むのではなく**入れて測る**。（CLI のフラグの意味を確認するのは可）
- ベンチマーカー自体に手を入れる／ベンチ機に処理を載せる
- 静的ファイルの内容を書き換える
- bcrypt のコスト変更・パスワードの平文保存
- 計測ログを取らないままの「ついで修正」
- 再起動で消える状態に依存する
- `long_query_time=0` のまま最終ベンチを回す（`make measure-off` を忘れない）

---

## 6. 環境

```sh
# AWSセッション（切れていたら人間がブラウザでサインイン）
aws login --profile personal

# 作成（競技3台 c5.large + ベンチ1台 c5.2xlarge）
AWS_PROFILE=personal isuenv up isucon13 --nodes 3 --bench-instance-type c5.2xlarge --ttl 12h
make setup      # hosts生成 → ベンチ機のサービス停止 → 計測ツール導入 → deploy → 計測ON
make bench

# 終わったら
make down
```

- グローバルIPが変わってSSHが通らなくなったら `AWS_PROFILE=personal isuenv ssh isucon13` で貼り直す。
- ベンチは約 **110秒**（初期化 15s + 整合性チェック 30s + 負荷 60s）。
- DNS の A レコードの向き先（`ISUCON13_POWERDNS_SUBDOMAIN_ADDRESS`）は `etc/isuN/home/env.sh` の
  `__ISU1_IP__` プレースホルダで持ち、`make deploy` が入口ノードの private IP に置換する。
