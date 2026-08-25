# 画面に出る内容

goft は2種類の出力を持ちます。記録としての JSON Lines ログと、見ている人のための人間可読な出力（標準出力）です。
このページは後者を扱います。以下のブロックはすべて [cmd/logsample_test.go](../cmd/logsample_test.go)
のテストを実際に走らせて採取したもので、書き換えたのはパス・ホスト名・ポートだけです。

単発実行（`send`・`recv`）は既定で出力し、`serve` は出力しません。常駐プロセスは通常ログとともに放っておかれるからです。
`--console` / `--no-console` でどちらにも切り替えられます。

## 何かを動かす前に — `goft test`

`goft test` は設定を検証し、接続設定がどう解決されたかを表示し、接続して、両方向を確認します。

```console
config        /etc/goft/invoice-upload.yaml                      OK
local         /data/out/invoice                                  OK  readable and writable
resolve       host = invoice-sftp                                (yaml)
resolve       port = 22                                          (yaml)
resolve       user = uploader                                    (yaml)
resolve       password = REDACTED                                (yaml)
resolve       private_key = /home/svc-transfer/.ssh/id_ed25519   (default)
resolve       known_hosts = /home/svc-transfer/.ssh/known_hosts  (yaml)
connect       sftp://invoice-sftp:22/upload/invoice              OK
recv (list)   0 entries                                          OK
send (write)  writable                                           OK
```

ジョブが意図しない場所に繋がっているときに効くのが `resolve` の行です。実際に使われる値と、その出所
（`yaml` / `ssh_config` / `netrc` / `credential_manager` / `default`）が並びます。
秘密情報は「設定されている」ことだけが示され、値は表示されません。

両方向を別々に確認して報告します。読み取り専用のアカウントで `recv` だけ運用するのは正当な構成だからです。
終了コードが 2 になるのは両方向とも失敗したときだけです。転送先のディレクトリがまだ無い場合もエラーにはならず、
「最初の転送時に作成されます」と報告し、書き込み可否は親ディレクトリで確認します。
`goft test` を実行しただけでサーバー側に何も残りません。

## 何が送られるか — `--dry-run`

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (dry-run)
  2026-08/invoice_202608_03.csv                  17 B
  archive.dat                                 2.0 MiB
  invoice_202608_01.csv                     830.1 KiB
  invoice_202608_02.csv                     625.0 KiB
4 files, 3.4 MiB would be transferred (nothing was sent)
```

dry-run は送り側を読んで終わります。転送先には一切触れません（すでにファイルがあるかどうかも尋ねません）。
そのため `ok` や `skipped` は判定できず、判定したふりもしません。見つけたものと合計だけを並べます。
dry-run では結果を確認することが目的なので、コンソール出力は常に有効になります。

## 転送

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (4 files, 3.4 MiB)
[2/4] archive.dat                                 2.0 MiB  skipped     0.0s  size_limit
[1/4] 2026-08/invoice_202608_03.csv                  17 B  ok          0.0s
[3/4] invoice_202608_01.csv                     830.1 KiB  ok          0.1s
[4/4] invoice_202608_02.csv                     625.0 KiB  ok          0.1s
4 files: 3 transferred (1.4 MiB), 1 skipped, 0 failed  in 3.1s
```

ヘッダーにはジョブ名・両端・作業量が入ります。矢印は `send` が `->`、`recv` が `<-` です。
続いてファイルごとに1行、最後にサマリです。

`[2/4]` の数字は一覧上の位置で、完了順ではありません。`workers: 2` なので、行はワーカーが終わった順に出ます。
`archive.dat` は `max_file_size_mb` を超えており、失敗ではなく理由付きのスキップとして扱われます。

すべて届いている状態でもう一度実行すると、こうなります。

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (4 files, 3.4 MiB)
[1/4] 2026-08/invoice_202608_03.csv                  17 B  skipped     0.0s  already_exists
[3/4] invoice_202608_01.csv                     830.1 KiB  skipped     0.0s  already_exists
[4/4] invoice_202608_02.csv                     625.0 KiB  skipped     0.0s  already_exists
[2/4] archive.dat                                 2.0 MiB  skipped     0.0s  size_limit
4 files: 0 transferred (0 B), 4 skipped, 0 failed  in 3.0s
```

何も転送されず、各行にその理由が入ります。`on_exists: skip` のジョブが正常で、やることが無いときの姿です。
だからこそ `serve` はこうした周期について黙っています。数秒ごとにこれを出力し続けても意味がないためです。

## `serve` の場合

`--console` を付けた `serve` は、周期が終わってから、しかも実際に何かが起きたときだけ報告します。
全ファイルがスキップされた周期は何も出しません。周期そのものが実行できなかった場合（サーバーに繋がらない等）は必ず報告します。
つまり沈黙は「やることが無い」であって、「異常が起きている」ではありません。

## 色・パイプ・2つのストリーム

`ok` / `skipped` / `failed` に色が付くのは、標準出力が端末のときだけです。
パイプやリダイレクトではエスケープシーケンスを含まないプレーンテキストになります。

`log.path` を設定せずコンソール出力が有効な場合、JSON ログは標準エラーに回ります。両者が混ざらないようにするためです。

```bash
goft send 2>/dev/null    # 人間可読な出力だけ
goft send >/dev/null     # JSON ログだけ
```

どちらの出力も UTF-8 です。ログは JSON の仕様として、コンソールはファイル名を保存されているとおりに出すためです。
Windows のコンソールを地域コードページのままにしていると ASCII 以外が化けます（`chcp 65001`、Windows Terminal、
PowerShell 7 のいずれかで正しく表示されます）。書き出されるバイト列はどちらでも同じなので、リダイレクトしたログには影響しません。

## 詳細度

コンソール専用の設定はありません。`log.level` が両方の出力を同時に制御します。

| level | コンソール出力 |
|---|---|
| `error` | 失敗とサマリ |
| `warn` | + サイズ上限などの注意行 |
| `info`（既定） | + ファイルごとの1行 |
| `debug` | + 各行にハッシュと転送レート、ステップ内訳をインデントして表示 |

## 関連

- [ログを1行ずつ読む](log-example-ja.md) — 同じ実行をログ側から見たもの
- [何が転送対象になるか](file-selection-ja.md)
- [1ファイルが辿る道](transfer-lifecycle-ja.md)
