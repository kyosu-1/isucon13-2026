---
name: tuning-isucon13
description: Use when tuning ISUCON13 (ISUPipe) for a benchmark score - running make bench, reading measurements/, choosing the next bottleneck, changing DNS (PowerDNS) / icon / statistics / livecomment logic, or deciding whether to keep or revert a change.
---

# ISUCON13 (ISUPipe) 計測改善ループ

## 大原則

**計測なき改善は禁止。1改善 = 1コミット = 1ベンチ。**

ベンチは初期化 15s + 整合性チェック 30s + 負荷 60s で**約110秒**。まとめて入れる理由はない。
まとめた瞬間に、下がったときどれが原因か分からなくなる。

## 1サイクル

```sh
make bench NOTE="何を確かめたいか"      # measurements/<ts>/ と scores/log.md に記録
# measurements/<ts>/score.json → alp.txt → slow.txt → cpu-*.txt の順に読む
# 実装 → git commit（本文に「根拠: measurements/<ts>/xxx の数字」を書く）
make deploy && make bench
git add measurements scores docs && git commit -m "bench: <score> <何の計測か>"
```

## ISUPipe のスコアの構造を忘れない

スコアは**投げ銭(tip)の合計**。tip はライブコメント投稿(`POST /api/livestream/:id/livecomment`)に付く。

- 視聴者シナリオが多く回るほど tip が入る。視聴者は配信を見に来て、コメント・リアクション・
  統計取得・アイコン取得をする。どれかが遅い／エラーだとシナリオが失敗し、視聴者が離脱する。
- **DNS**: ベンチは配信者ごとのサブドメイン(`<name>.u.isucon.local`)を毎回名前解決する。
  さらに水責め攻撃（ランダムサブドメイン）が来る。PowerDNS は MySQL バックエンドでキャッシュ無効
  (`cache-ttl=0` 等)なので、攻撃がそのまま MySQL の負荷になる。
  `score.json` の `dns.resolved` はさくら賞の指標でもあり、名前解決が速いほど HTTPS の負荷が伸びる。
- **ユーザー登録は `pdnsutil add-record` を exec** している（1回数十ms + プロセス起動）。
  登録が遅いと新規視聴者・配信者が増えない。
- `score.json` の `scenarios` でシナリオ別の成功/失敗回数を見る。失敗が増えたら理由は `errors`。

## 判断を間違えやすい点

### 1. スコアのブレを先に測る

同一コードでも数%ぶれる。改善の効果がブレ幅より小さいときは、**同じコードでもう1回回して**から判断する。

### 2. エラーの種類を見る

- `[一般エラー] ... expected:400 actual:201`（モデレート済みスパム）はマニュアルで減点対象外と明記。
- `[リクエストタイムアウト]` は遅いエンドポイント。statistics 系（全ユーザー・全配信のランキング計算）が典型。
- `[一般エラー] POST .../report expected:201 actual:404` は、モデレーションで消えたコメントへの報告。
  初期実装でも出るので、増減だけ見る。

### 3. 反映遅延（2秒）と 304

- アイコン更新から `icon_hash` と `GET /api/user/:name/icon` への反映は2秒以内。
  キャッシュするなら `POST /api/icon` で即時に更新する。
- `If-None-Match` に一致したら 304 を返してよい。**条件付きでない GET に 304 を返してはいけない。**

### 4. SQLを変えたら EXPLAIN ANALYZE を見る

```sh
make explain SQL="SELECT ..."
```

### 5. プロファイルの割合はマシン全体の割合ではない

先に `cpu-<host>.txt`（pidstat）でプロセス別CPUを見る。c5.large は 200% が上限。
mysqld が支配しているのにアプリのコードを磨いても効かない。pdns の CPU も見る（isudns への SELECT が slow.txt に出る）。

### 6. 状態をメモリに持つなら、再起動と initialize で復元できること

追試は「3台を再起動 → 負荷走行」で、**再現スコアが最終の75%以下だと fail**。
アプリ内キャッシュは**起動時と `POST /api/initialize` で DB から作り直す**。

### 7. initialize は 42 秒以内

`init.sh` は mysql コマンドで初期データを流し込む＋`init_zone.sh`（pdnsutil load-zone）。
DB を別ノードに出したら init.sh の接続先も変わる（env.sh 経由）。
スキーマ変更（インデックス追加）は `webapp/sql/initdb.d/10_schema.sql` ではなく
**`init.sh` が流す SQL（`init.sql` 等）に入れる**。initialize のたびにテーブルが作り直されるか確認すること。

### 8. サーバーの状態を変えたら

- ミドルウェアを再起動したらアプリも再起動する。`make deploy` がやる。
- pdns は MySQL(isudns) に依存。MySQL を別ノードに出すなら `gmysql-host` も変える。

## ISUPipe で実測して分かったこと（ベンチを観測して得たもの）

| 観測 | 意味 | 根拠 |
| --- | --- | --- |
| 素の初期状態: 3545 点、名前解決 2311 回、水責め並列数 2 | | measurements/20260922-081630-raw-baseline |

（改善のたびに追記する）

## ベンチマーカーはブラックボックス

ソースを読んで挙動を先回りしない。判断材料は外から観測できるものだけ
（スコア、エラー、シナリオ回数、DNS 成功数、alp、自分のサーバーのログ）。

## Red Flags — 手を止めて計測に戻る

- 「N+1が見えているから計測は飛ばす」→ 次の一手が見えなくなる
- 「時間が無いから複数まとめてベンチする」→ ベンチは110秒
- 「このクエリは明らかに速いはず」→ EXPLAIN ANALYZE を見ていないなら分かっていない
- 「スコアは下がったが理論的には良いはず」→ ブレ幅を確認して、それでも下がっていれば revert
- 「エラーは数件だから許容」→ 負荷が増えると比例して増える
- 「アプリが速くなったのにスコアが伸びない」→ DNS の成功数とベンチ機のCPU（`vmstat-isucon13-4.txt`）を見る

## 締め

1. `make restart-test` — 3台再起動後もベンチが通るか（追試と同じ）
2. `make measure-off` — `long_query_time=0` のまま最終計測しない
3. 新規の大きい変更をしない
