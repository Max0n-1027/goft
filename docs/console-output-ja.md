# 画面に出る内容

goft は2種類の出力を持ちます。記録としての JSON Lines ログと、見ている人のための人間可読な出力（標準出力）です。
このページは後者を扱います。以下のブロックは、「途中で止めた場合」の例を除いて、
[cmd/logsample_test.go](https://github.com/Max0n-1027/goft/blob/main/cmd/logsample_test.go) のテストを実際に走らせて採取したもので、
書き換えたのはパス・ホスト名・ポート・ユーザー名だけです。「途中で止めた場合」の例は手で止めた実行のもので、
その結果を goft のコンソール出力で日本語にして載せています。
このページの例は日本語で表示したときのものです。英語の例は [英語版](console-output.md) にあります。

単発実行（`send`・`recv`）は既定で出力し、`serve` は出力しません。常駐プロセスは通常ログとともに放っておかれるからです。
`--console` / `--no-console` でどちらにも切り替えられます。

## 表示する言語

画面に出す内容（この出力、`goft test`、`--help`、エラーメッセージ）は英語か日本語で表示します。次のうち最初に指定があるものに従います。

1. `--lang en` または `--lang ja`
2. 環境変数 `GOFT_LANG`（`en` か `ja`）
3. ロケール。`LANGUAGE`・`LC_ALL`・`LC_MESSAGES`・`LANG` を gettext と同じ規則で読みます。`LANG=ja_JP.UTF-8` なら日本語、`LANG=C` なら英語です
4. Windows では、ユーザーアカウントの表示言語

どれにも当てはまらなければ英語です。`--lang` と `GOFT_LANG` は goft のために指定するものなので、`en` と `ja` 以外を書くとエラーになります。
ロケールが他の言語を指している場合は、単に英語で表示します。

ログは表示言語にかかわらず常に英語です。ログは検索や集計に使うもので、同じ事象は常に同じ文言で記録されるべきだからです。
設定項目の名前（`remote.host` など）や、取得元（`yaml`・`env` など）のように設定ファイルに書く語は、どちらの言語でもそのまま表示します。
OS やライブラリが返すメッセージ（`permission denied` など）は、goft の説明文の中にそのまま入ります。

## 何かを動かす前に — `goft test`

`goft test` は設定を検証し、接続設定がどう解決されたかを表示し、接続して、両方向を確認します。

```console
設定          /etc/goft/invoice-upload.yaml                      OK
ローカル      /data/out/invoice                                  OK  読み書き可能
解決          host = invoice-sftp                                (yaml)
解決          port = 22                                          (yaml)
解決          user = uploader                                    (yaml)
解決          password = REDACTED                                (yaml)
解決          private_key = /home/svc-transfer/.ssh/id_ed25519   (default)
解決          known_hosts = /home/svc-transfer/.ssh/known_hosts  (yaml)
接続          sftp://invoice-sftp:22/upload/invoice              OK
受信（一覧）  0 件                                               OK
送信（書込）  書き込み可能                                       OK
```

ジョブが意図しない場所に繋がっているときに効くのが「解決」の行です。実際に使われる値と、その出所
（`yaml` / `ssh_config` / `netrc` / `credential_manager` / `env` / `default`）が並びます。
秘密情報は「設定されている」ことだけが示され、値は表示されません。`env` は `SSH_AUTH_SOCK` で
見つけた ssh-agent です。その行については [ssh-agent で認証する](ssh-agent-ja.md) で説明しています。

両方向を別々に確認して報告します。読み取り専用のアカウントで `recv` だけ運用するのは正当な構成だからです。
終了コードが 2 になるのは両方向とも失敗したときだけです。転送先のディレクトリがまだ無い場合もエラーにはならず、
「最初の転送時に作成されます」と報告し、書き込み可否は親ディレクトリで確認します。
`goft test` を実行しただけでサーバー側に何も残りません。

## 何が送られるか — `--dry-run`

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  （ドライラン）
  2026-08/invoice_202608_03.csv                  17 B
  archive.dat                                 2.0 MiB
  invoice_202608_01.csv                     830.1 KiB
  invoice_202608_02.csv                     625.0 KiB
4 件、3.4 MiB を転送する予定です（何も送っていません）
```

dry-run は送り側を読んで終わります。転送先には一切触れません（すでにファイルがあるかどうかも尋ねません）。
そのため「完了」や「スキップ」は判定できず、判定したふりもしません。見つけたものと合計だけを並べます。
dry-run では結果を確認することが目的なので、コンソール出力は常に有効になります。

## 転送

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  （4 件、3.4 MiB）
[2/4] archive.dat                                 2.0 MiB  スキップ    0.0s  サイズ上限を超過
[1/4] 2026-08/invoice_202608_03.csv                  17 B  完了        0.0s
[4/4] invoice_202608_02.csv                     625.0 KiB  完了        0.0s
[3/4] invoice_202608_01.csv                     830.1 KiB  完了        0.0s
4 件: 転送 3（1.4 MiB）、スキップ 1、失敗 0  3.0 秒
```

ヘッダーにはジョブ名・両端・作業量が入ります。矢印は `send` が `->`、`recv` が `<-` です。
続いてファイルごとに1行、最後にサマリです。

`[2/4]` の数字は一覧上の位置で、完了順ではありません。`workers: 2` なので、行はワーカーが終わった順に出ます。
`archive.dat` は `max_file_size_mb` を超えており、失敗ではなく理由付きのスキップとして扱われます。
スキップの理由は、ログの `reason` 項目では `size_limit`（サイズ上限を超過）・`already_exists`（転送先に既にある）・`identical`（内容が同じ）と記録されます。

すべて届いている状態でもう一度実行すると、こうなります。

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  （4 件、3.4 MiB）
[2/4] archive.dat                                 2.0 MiB  スキップ    0.0s  サイズ上限を超過
[1/4] 2026-08/invoice_202608_03.csv                  17 B  スキップ    0.0s  転送先に既にある
[4/4] invoice_202608_02.csv                     625.0 KiB  スキップ    0.0s  転送先に既にある
[3/4] invoice_202608_01.csv                     830.1 KiB  スキップ    0.0s  転送先に既にある
4 件: 転送 0（0 B）、スキップ 4、失敗 0  3.0 秒
```

何も転送されず、各行にその理由が入ります。`on_exists: skip` のジョブが正常で、やることが無いときの姿です。
だからこそ `serve` はこうした周期について黙っています。数秒ごとにこれを出力し続けても意味がないためです。

## 途中で止めた場合

Ctrl+C を受けると、転送中のファイルは最後まで終わらせ、次のファイルには手を付けません。
次の例は、64 MiB のファイル3つのうち1つ目の転送が始まって1秒足らずで止めたものです。

```console
goft statement-upload  /data/out/statement -> sftp://invoice-sftp/upload/statement  （3 件、192.0 MiB）
[1/3] part-1.dat                                 64.0 MiB  完了        6.4s
3 件: 転送 1（64.0 MiB）、スキップ 0、失敗 0、未着手 2  6.4 秒で中断
goft: 実行が終わる前に中断されました
```

送っていたファイルは完全に届き、残りの2つは次の実行に回されます。この実行は完了していないので、終了コードは 2 です。

## `serve` の場合

`--console` を付けた `serve` は、周期が終わってから、しかも実際に何かが起きたときだけ報告します。
全ファイルがスキップされた周期は何も出しません。周期そのものが実行できなかった場合（サーバーに繋がらない等）は必ず報告します。
つまり沈黙は「やることが無い」であって、「異常が起きている」ではありません。

## 色・パイプ・2つのストリーム

「完了」「スキップ」「失敗」に色が付くのは、標準出力が端末のときだけです。
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
| `debug` | + 各行に転送先のハッシュ（`verify: hash` のとき）と、実際に送ったファイルの転送レート |

## 関連

- [ログを1行ずつ読む](log-example-ja.md) — 同じ実行をログ側から見たもの
- [何が転送対象になるか](file-selection-ja.md)
- [1ファイルが辿る道](transfer-lifecycle-ja.md)
