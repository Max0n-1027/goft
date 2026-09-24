# 何が転送対象になるか

各周期は送り側の一覧取得から始まり、どのファイルが対象になるかを決めます。
このページはその判断を、goft が実際に行う順番で説明します。
引用しているのは [cmd/logsample_test.go](../cmd/logsample_test.go) のテストを走らせた実際の出力です。

```yaml
recursive: true
include: ["*.csv", "*.dat"]
exclude: ["*.tmp", "*.part", ".*"]
max_file_size_mb: 1
stable_duration: 3s
```

## 1. 通常ファイルのみ

ディレクトリ・シンボリックリンク・ソケット・デバイスは対象になりません。
この規則は「除外する種別を列挙する」のではなく「プロトコルが通常ファイルだと報告したものだけを対象にする」
という許可リストの形で書かれています。FTP や SMB はリンク的なものをローカルディスクとは違う形で表現するため、
除外リスト方式だと列挙し損ねたものが通ってしまうからです。

## 2. 名前

`include` と `exclude` はパスではなく**ベース名**に対する glob パターンです。
`include` のいずれかに一致し、かつ `exclude` のいずれにも一致しないファイルが対象になります。
`include` が空ならすべてに一致します。`exclude` の既定値は `["*.tmp", "*.part", ".*"]` です。

`exclude` はディレクトリにも適用され、一致したディレクトリは枝刈りされます。その配下は一覧すら取得しません。
`include` はファイルにだけ適用されるので、途中のディレクトリを通すためのパターンを書く必要はありません。

goft 自身の転送中ファイル（`*.goft.tmp`）は設定に関わらず常に除外されます。
既定の `exclude` に任せていると、それを上書きした瞬間に壊れますし、
自分の一時ファイルを拾うジョブは書きかけのデータを転送してしまいます。

既定の `recursive: false` ではルートディレクトリだけを一覧し、サブディレクトリは一切見ません。

## 3. 安定化

ファイルは、サイズと更新時刻が `stable_duration` のあいだ変化しなくなるまで対象になりません。
待ち時間は周期をまたいで累積し、ポーリングのたびにリセットされることはありません。変化があればそこから測り直します。

更新時刻だけで判定する方式（`now - mtime >= stable_duration`）は簡単ですが誤りです。
`cp -p` や `rsync --times` のようにタイムスタンプを保持するツールは、コピー中でも mtime を元ファイルの古い値のままにするため、
書きかけのファイルが「安定している」ように見えてしまいます。

単発の `send` / `recv` には比較対象になる前周期がありません。そのため `stable_duration` の間隔をあけて2回読み取り、
その間に変化しなかったものを転送します。何も無いディレクトリに対する単発実行でも数秒かかるのはこのためです。

`serve` では、新しいファイルが拾われるまでの最短時間は `max(poll_interval, stable_duration)` になります。

**FTP の更新時刻は粗いことがあります。** FTP には stat 相当のコマンドがなく、更新時刻はサーバーが対応していれば `MLST`、
そうでなければディレクトリ一覧から取得します。後者は分単位の精度しかないことがあります。
FTP サーバーから `recv` する場合は `stable_duration` に余裕を持たせてください。

デバッグレベルでは、走査で何件見つかり、そのうち何件が安定していたかが記録されます。

```jsonl
{"time":"2026-08-25T21:35:54.7474434+09:00","level":"DEBUG","msg":"scan complete","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"scan","found":4,"settled":4}
```

## 4. サイズ上限

`max_file_size_mb` は走査時ではなく安定化判定の後に適用されます。
上限を超えたファイルはスキップされ、その周期の他のファイルはそのまま処理されます。

```jsonl
{"time":"2026-08-25T21:35:36.554360166+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
```

警告はプロセスごとにファイル1つにつき1回だけ書かれ、以降の周期では同じ内容が debug に記録されます。
ファイルが変化すれば再び警告が出ます。これができるのは、判定を安定化の後に行っているからです。
走査時に落としてしまうと、記憶している側にそのファイルが渡りません。

上限超過の報告には転送先が要らないので、候補が上限超過のファイルだけの周期では転送先に一切接続しません。
そうしないと、何週間も送り側に置かれたままのファイルのために、常駐ジョブがポーリングのたびに接続することになります。

`max_file_size_mb: 0`（または未指定）で無制限になります。

## 4段階を通った結果

dry-run には、4つの段階を通り抜けたファイルがそのまま並びます。

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (dry-run)
  2026-08/invoice_202608_03.csv                  17 B
  archive.dat                                 2.0 MiB
  invoice_202608_01.csv                     830.1 KiB
  invoice_202608_02.csv                     625.0 KiB
4 files, 3.4 MiB would be transferred (nothing was sent)
```

ここで `archive.dat` が一覧に出ているのは、dry-run が「走査で見つかったもの」を報告するからです。
サイズ上限は転送時の判断で、実際に転送すればスキップとして記録されます。

## 毎周期見続けられるファイル

`on_exists: skip` は「転送しなかった」という意味なので `post_action` も実行されず、
ファイルは送り側に残って毎周期調べられることになります。
コストはファイル1つあたり1往復ではなくディレクトリ1つあたり1回の一覧取得ですが、
増える一方の送信ディレクトリはいずれ各周期を遅くします。
そこにファイルを溜めない運用であれば `post_action: move` か `delete` を使ってください。

## 関連

- [1ファイルが辿る道](transfer-lifecycle-ja.md) — 対象に選ばれた後の処理
- [ログを1行ずつ読む](log-example-ja.md)
- [画面に出る内容](console-output-ja.md)
