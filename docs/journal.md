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

### 17:24 ベースライン（計測ON）3211

`make bench` の初回。素の 3545 より低いのは slow log（long_query_time=0）と LTSV ログの分。
mysqld 106% / isupipe 21%。slow の 1位は `SELECT * FROM livestream_tags WHERE livestream_id=?`（全件走査、DB 時間の 34%）。
DNS のタイムアウト（`read udp ... i/o timeout`）がすでに出ている。

### 17:29 インデックス追加 → 7288（+127%）

`webapp/sql/indexes.sql`（isupipe 13本）と `indexes_dns.sql`（isudns.records(name,type)）を `init.sh` から `mysql -f` で流す。
init.sql は TRUNCATE なのでインデックスは残る。重複エラーは無視。

### 17:31 DB 接続設定・ログ停止 → 11202（+54%）

interpolateParams=true（ADMIN PREPARE が DB 時間の 10%）、MaxOpenConns 10→64、echo の Debug と Logger ミドルウェアを停止。

### 17:35 icon の upsert → 11235（±0、500 が 24→0）

`DELETE FROM icons WHERE user_id=?` + INSERT が、追加した user_id の二次索引のギャップロックでデッドロックしていた。
user_id を UNIQUE にして `INSERT ... ON DUPLICATE KEY UPDATE` に。

### 17:37 MySQL 設定 → 11130（±0）

binlog 停止、`innodb_flush_log_at_trx_commit=2`、buffer_pool 1G、max_connections 1024。
COMMIT（DB 時間の 14%）は消えたがスコアは変わらず。**initialize が 15s → 2.6s** になった（初期データ投入の fsync）。

### 17:41 statistics の N+1 → 15546（+40%）

ユーザー統計のランキングが「全ユーザー × 2クエリ」（1リクエスト約 1000 クエリ、13秒、20秒タイムアウト 4件）。
集約 2本の 1クエリにして Go で同じ順序（score, name 昇順）にソート。配信統計も同様。

### 17:44 ユーザー・テーマ・アイコンのメモリキャッシュ → 25624（+65%）

`fillUserResponse` が毎回 themes / icons(LONGBLOB) を引いて sha256 していた。`webapp/go/cache.go` に users キャッシュ。
`GET /api/user/:name/icon` は `If-None-Match` が一致したら 304（14527 回中 14504 が 304 に）。
更新は DB コミット後に反映、起動時と initialize で DB から作り直す。

### 17:47 タグのメモリキャッシュ → 64122（+150%）

`SELECT tags WHERE id=?` が 207777 回（DB 時間の 32%）。tags と livestream_tags を起動時/initialize に読み、予約はコミット後に追加。

### 17:50 配信のメモリキャッシュ → 115960（+81%）

`SELECT * FROM livestreams WHERE id=?` が 193267 回（31%）。ライブコメント/リアクション一覧の要素ごとに引いていた。

### 17:53 moderate の一括 DELETE → 100938（-13%、ただし採用）

元実装は「全ライブコメントを取得 → 1件ずつ DELETE（LIKE 判定）」で、1回 8秒・20秒タイムアウト。
`DELETE ... WHERE livestream_id=? AND comment LIKE CONCAT('%',?,'%')` に。
モデレーションが 20回 → 947回に増え、消えたコメントの報告一覧 `GET /api/livestream/:id/report` が
fill で no rows → 500（132 件）。`expected:400 actual:201`（モデレート済みスパムの投稿）も増えたが、
これはマニュアルで「ベンチ側の問題、減点対象外」と明記されている。

### 17:56 報告一覧の 500 修正 → 104305

報告一覧をコメントが残っているものだけに（`INNER JOIN livecomments`）。

### 18:00 MySQL を isucon13-2 に分離 → 149875（+44%）

isu1 が CPU 93%（mysqld 67 / isupipe 45 / nginx 32 / pdns 9）。
`etc/isu2/mysql`（bind-address 0.0.0.0）、`etc/isu1/home/env.sh` と `powerdns/pdns.d/gmysql-host.conf` は `__ISU2_IP__`。
踏んだ罠:
- AMI の isucon / isudns ユーザーは localhost 限定 → `tools/setup/mysql-remote-users.sh` で `'%'` を作る（`make setup` に組み込み）
- `isupipe-go.service` の `Requires=mysql.service` → mysql を disable したノードでアプリが起動しない。外した。

### 18:05 DNS をアプリ内サーバーに置き換え → 154773（+3%、名前解決 26822 → 172493 回）

pdns 29% + journald 4%（全問い合わせをログ）、DB 時間の 36% が DNS 由来、登録の `pdnsutil` exec が 150ms。
`webapp/go/dns.go`（miekg/dns）: ゾーンファイルの静的名 + users キャッシュ → A、それ以外 NXDOMAIN。TTL 120。
systemd に `AmbientCapabilities=CAP_NET_BIND_SERVICE`。init.sh の `init_zone.sh` 呼び出しは外した。
1回目は **FAIL**: ユーザー名 `wkGQKEWut2` に大文字が含まれ、小文字化した qname で引けなかった（pdnsutil は小文字で登録していた）。
users キャッシュに小文字名のインデックスを追加して解決。

### 18:08 nginx を isucon13-3 に分離 → 260266（+68%）

isu1 が nginx 87% + isupipe 84% で飽和。DNS の A レコードを isu3 に向け、isu3 の nginx が /api を isu1:8080 へ
（upstream keepalive）。ベンチの `--nameserver` は isu1 のまま（`DNS_HOST`）。`ENTRY` は isu3。
構成: **isu1 = アプリ + DNS / isu2 = MySQL / isu3 = nginx**。

### 18:11 予約枠の範囲・N+1 → 308889（+19%）

`start_at >= ?` だけだと右側が無制限で Rows examine 4.7k。`start_at < end` を足して rows=13。
残数の再 SELECT（同じ行）も削除。

### 18:15 ランキングをメモリ集計 → 347060（+12%）

集約クエリが 1回 150〜230ms（DB 時間の 33%）。配信ごとのスコアをメモリで数え、ランクはソートだけ。
リアクション/コメント投稿とモデレーション削除でコミット後に増減。SQL と rank を突き合わせて一致を確認。

### 18:20 読み取り専用ハンドラの tx 廃止・search キャッシュ → 359467（+4%）

COMMIT 134246回（DB 時間の 23%）。15 ハンドラで BEGIN/COMMIT をやめ、fill 系は `sqlx.QueryerContext` を受ける形に。

### 18:24 NG ワードのメモリキャッシュ・Go でスパム判定 → 371642（+3%）

hitSpam の `SELECT (? LIKE ...)` 54026 回。utf8mb4_bin なので `strings.Contains` と一致。`% _ \` を含む語だけ SQL。
手動確認: 事前投稿のスパムがモデレーションで消え、以後のスパム投稿は 400、通常投稿は 201、統計の max_tip に反映。

### 18:27 単文書き込みハンドラの tx 廃止・pprof → 385724（+4%）

reaction / livecomment / icon / enter / exit / report は 1 文しか書かない。`127.0.0.1:6060` に pprof。
`PPROF_HOST=isucon13-1 make bench` で負荷走行 55 秒後から 30 秒のプロファイルを `measurements/<ts>/pprof.txt` に。

### 18:33 nginx(isu3) チューニング → 375963（-2.5%、5xx あり）→ 18:35 rlimit 修正 → 430512（+14.5%）

`keepalive_requests 100000`（1.18 の既定 100 で接続が切れ TLS フルハンドシェイクをやり直していた）、
TLS セッションキャッシュ、`worker_connections 8192`、gzip off。
1回目は `worker_rlimit_nofile` を上げておらず `accept4() failed (24: Too many open files)` で 5xx が 57 件。修正して +14.5%。

### 18:38 アイコン画像をローカルファイルに → 415094（-3.6%、ブレの範囲）

`INSERT ... ON DUPLICATE KEY UPDATE icons`（LONGBLOB）が DB 時間の 31%。`webapp/icons/<user_id>` に tmp→rename で書き、
起動時に読み直し、initialize で全消去。**isu2 の busy が 78% → 33% に落ちた**がスコアは動かず。
アプリ再起動後もアイコンが返ることを確認。

### 18:40 同一コード再計測 → 407991（ブレ -1.7%）

この時点でどのノードも CPU 飽和していない（isu1 72% / isu2 35% / isu3 66% / ベンチ 45%）。

ベンチの出力（staff-logger）を 5 秒刻みで数えると:
- 視聴者シナリオの開始は最初の 10 秒が 200/5秒、**予約が全部 400 になる 10 秒以降は 150/5秒** に落ちる
- 予約失敗（`expected:201 actual:400`）は 60 秒で 4622 回。初期データの予約枠は 1 時間 5 枠、
  ベンチは人気の時間帯（2024/03/31 の 19 時間など）に集中して予約するので 10 秒で使い切る。
  枠は初期データで決まる仕様（リファレンス実装でも同じ）なので、負荷の上限はベンチ側で決まっている。
- `expected:400 actual:201`（モデレート済みスパムの投稿）は 1 回あたり 130〜180 件。access log で追うと、その配信への
  **最初の moderate より前に**スパム投稿が来ている（別配信で登録した語？）。マニュアルで減点対象外と明記。
  スコア/視聴者数は 194〜195 で一定なので、これがスコアを下げている形跡はない。

### 18:44 nginx http2 → 404394（-0.9%、ブレの範囲。そのまま採用）

### 18:51 セッション Cookie のデコードをメモ化 → 419505（+3.7%）

pprof で `encoding/gob.(*Decoder).compileDec` が 8%（gorilla/sessions が毎リクエスト gob デコード）。
Cookie の値を鍵にメモ。isupipe の CPU が 117% → 71%。

### 18:55 トラフィックの内訳

isu3 の access log: `pipe.u.isucon.local` 宛が 69%（うち GET icon が 270682 回、ほぼ全部 304）、
配信者サブドメイン宛が 31%。nginx を 2 台に分けても pipe の名前解決結果が固定される限り偏る。

### 18:56 再起動試験 → 479001（pass）

1回目は「アプリが isu2 の MySQL より先に起動 → exit → 5 秒後に再起動」の間に疎通確認が走って失敗判定。
アプリに DB 接続の 60 秒リトライ、`restart-test.sh` に API の応答待ちを入れて再試験 → 3台とも自動起動、pass。

### 18:58〜19:10 47.9万 と 42.5万 の2峰性の追跡（結論: ブレ）

再起動直後 479001 → 次 425458 → アプリ再起動 428672 → MySQL 再起動 479375 → 次 426837 → ANALYZE TABLE 424947
→ MySQL 再起動 424337。「MySQL 再起動で速くなる」は 2 回目で再現せず、**ベンチ側の乱数（予約する時間帯など）による
2峰性**と判断。速い回は視聴者数が 2426〜2438、遅い回は 2185〜2204 で、他の指標（DNS、予約成功数 1398）は同じ。
`run.sh` に MySQL の GLOBAL STATUS 増分（`mysql-status.txt`）を残すようにした。

### 19:12 ベンチ機のメモリを 8 GB に制限（本番相当）

ユーザーの指示: 本番のベンチマーカーは ECS の 8 vCPU / 8 GB。ベンチ機 c5.2xlarge は 8 vCPU / 16 GB。
EC2 に 8 vCPU / 8 GB のタイプは無いので、`systemd-run --scope -p MemoryMax=8G -p MemorySwapMax=0` で
ベンチプロセスだけ制限。ピーク RSS は 525 MB（`/usr/bin/time` で bench.log に記録）なので結果への影響なし（421775）。

### 19:15 goccy/go-json → 493778 → 再計測 499869（+17%）

pprof で encoding/json のエンコードが約 20%。echo の JSONSerializer を差し替え。出力は同じ。

### 19:20 GET icon を nginx でキャッシュ → 502859（±0）

app が `ETag` を返し、isu3 の nginx が `proxy_cache`（有効 1 秒）で If-None-Match を判定して 304。
1回目は `proxy_cache_path /var/cache/nginx/...` の親ディレクトリが無く `nginx -t` が落ちて、
**deploy が黙って旧設定のまま**動いていた。`deploy.sh` は nginx -t の失敗を表示して止めるようにした。
スコアは変わらない（icon の往復は律速ではなかった）が、app の負荷は減るので採用。
