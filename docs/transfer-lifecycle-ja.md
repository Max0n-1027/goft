# 1ファイルが辿る道

対象に選ばれた後（[何が転送対象になるか](file-selection-ja.md)を参照）、ファイルには次の処理が行われます。
engine はどちらの端がローカルかを知りません。`send` はローカルを送り側・リモートを受け側として渡し、
`recv` はその逆にするだけで、以下の内容は両方向で同じです。

## 1. 転送先のディレクトリ

その周期の一覧で存在しないと分かっていた場合に作成します。ファイルごとではなくディレクトリごとに1回だけです。

## 2. すでに存在するか

周期の開始時に、転送先の各ディレクトリを1回ずつ一覧して結果を保持します。
ファイルごとにサーバーへ問い合わせると1ファイルにつき1往復かかります。
`on_exists: skip` で1万個のファイルが送り側に残っていれば、毎周期1万往復です。

名前はまず完全一致で照合し、転送先が `FOO.CSV` と `foo.csv` を区別できない場合（Windows・macOS のディスク、SMB 共有）
は小文字化した名前でも照合します。そうしないと、すでに存在するファイルを毎周期転送し直すことになります。

その後の動作は `on_exists` で決まります。

- **`skip`**（既定）— 転送せず、転送後処理も実行しません。スキップは「何も起きなかった」という意味なので、元のファイルもそのままです。
- **`overwrite`** — 先に `verify` で指定した方法で両側を比較し、同一であればそのままにします。

```jsonl
{"time":"2026-08-25T21:35:42.649116785+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":3,"verify":"hash","result":"skipped","reason":"identical","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
```

  ハッシュの前にサイズを比較するので、長さが違うファイルは両側を読まずに決着します。
  `verify: none` では比較する手段がないため、常に転送します。

## 3. 一時名で書き込む

バイト列は転送先ディレクトリの `<name>.goft.tmp` に書かれ、流れていく途中でハッシュが計算されます。
転送中に転送先を一覧すると、実際にこう見えます。

```console
$ ls -1 /upload/invoice        # while the transfer is running
2026-08
invoice_202608_01.csv
invoice_202608_02.csv
statement_202608.dat.goft.tmp

$ ls -1 /upload/invoice        # once it has finished
2026-08
invoice_202608_01.csv
invoice_202608_02.csv
statement_202608.dat
```

本名のファイルが常に完全な状態でしか現れないのは、この仕組みのためです。
転送中にプロセスを落としても、残るのは `.goft.tmp` だけで、次回の実行が上書きします。
後続のシステムが監視している名前が、途中まで書かれたファイルになることはありません。

## 4. 検証

- `hash`（既定）— 一時ファイルを転送先から読み戻し、送信中に計算した xxHash と比較します。
- `length` — サイズを比較します。
- `none` — エラーが出なければ成功とみなします。

手順2の比較で計算したハッシュは、ここでは再利用しません。
再利用すると「比較のために読んだバイト列」を検証することになり、その間に元ファイルが書き換わったケースを検出できなくなります。
転送時はどのみちストリームを読むので、流しながらハッシュを取る追加コストはありません。

```jsonl
{"time":"2026-08-25T21:35:36.561189297+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":6,"verify":"hash","result":"success","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
```

`hash_src` と `hash_dst` は info レベルで記録されるので、ファイルが壊れずに届いたことはログだけで確認できます。

## 5. 本名へ rename

検証を通った後にだけ行います。まず rename を試みます。転送先を原子的に置き換えられるサーバーにはそうさせるべきだからです。
既存の名前への rename を拒否するサーバーの場合は、対象を削除してから rename し直します。

その直前に、転送先をもう一度だけ確認します。手順2の一覧は周期開始時のスナップショットなので、
`on_exists: skip` では、その間に現れたファイルを上書きしてはいけません。
その場合は一時ファイルを削除し、結果を `already_exists` として記録します。
この追加の問い合わせは1回で、しかも実際に転送したファイルにしか発生しません。

削除も拒否された場合は、両方の理由を記録します。rename のエラーに続けて
`could not clear the destination first:` と、対象を削除できなかった理由が並びます。
ジョブが権限を失った転送先では、この2つは同じ文言をまとった別々の失敗であり、
最初の1つだけを報告すると、読む側が見当違いの権限を調べることになるためです。
どちらか一方でも恒久的な失敗なら、その試行全体を恒久的な失敗として扱います。
次の周期も同じところで拒否されるためです。

手順3〜5のどこかで失敗した場合は一時ファイルを削除し、転送先は見つけたときのまま残します。

## 6. 転送後処理

送り側に対して実行します。

- `none`（既定）— 何もしません。
- `delete` — 元のファイルを削除します。
- `move` — 相対パスを保ったまま `move_to` の下へ移動します。`send` のみです。
  `move_to` は送信ディレクトリの外である必要があり（起動時に検証します）、そうでないと移動したファイルを次の周期で拾い直します。
  `move_to` に同名のファイルがある場合は、自動でリネームせず失敗として扱います。

転送後処理が失敗した場合は、接続を張り直して単独で再試行します。
ファイルの再転送は行いません。ファイルはすでに届いており、二度送るほうが元を残すより悪いからです。

## 失敗したとき

1周期のなかで、ファイルは `retry.max_attempts` 回（既定3回）まで試行されます。
待ち時間は毎回倍になり、その間に接続は張り直されます。

```jsonl
{"time":"2026-08-25T21:35:48.694181956+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":2,"of":3,"retry_in_ms":1000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)"}
{"time":"2026-08-25T21:35:49.718238957+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":3,"of":3,"retry_in_ms":2000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)"}
{"time":"2026-08-25T21:35:51.740558027+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_05.csv","protocol":"sftp","bytes":17,"duration_ms":8,"verify":"hash","result":"failed","error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)","hash_src":"b2d06a1f4fa13583","hash_dst":"b2d06a1f4fa13583","attempt":3}
{"time":"2026-08-25T21:35:51.740695447+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"summary","files":1,"succeeded":0,"skipped":0,"failed":1,"bytes":0,"duration_ms":6077}
```

再送するのは、次に試せば成功しうる失敗だけです。接続の切断・タイムアウト・検証の不一致は再送します。
ファイルが無い・権限が無い・すでに存在する・FTP サーバーが 5xx を返した、といった失敗は再送しません。次も同じ結果になるからです。
判定は除外方式で、恒久的だと分かっているもの以外は「もう一度試す価値がある」と扱います。

1ファイルの失敗で周期は止まりません。他のファイルはそのまま処理され、サマリに失敗件数が入り、
`send` / `recv` は終了コード 1 で終わります。終了コード 2 は「そもそも実行を開始できなかった」場合です。

`serve` で周期そのものが失敗し続けた場合（繋がらないサーバーなど）、次の周期までの待ち時間が倍々になり、最大5分で頭打ちになります。
1周期でも成功すれば `poll_interval` に戻ります。1秒間隔のジョブが完全に停止したサーバーを叩き続ければ、
そうしないと同じエラーを1日に数万行書くことになります。

## 途中経過を見る

デバッグレベルではステップごとに独立したレコードが出て、それぞれ所要時間が付きます。
遅い転送のどこが遅いのかを切り分けられます。

```jsonl
{"time":"2026-08-25T21:35:54.781854799+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"mkdir","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":0}
{"time":"2026-08-25T21:35:54.784956443+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"compare","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":2}
{"time":"2026-08-25T21:35:54.785978073+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"write","src":"/data/out/invoice/restricted/invoice_202608_03.csv","duration_ms":0,"error":"permission denied"}
```

## 関連

- [何が転送対象になるか](file-selection-ja.md)
- [ログを1行ずつ読む](log-example-ja.md)
- [画面に出る内容](console-output-ja.md)
