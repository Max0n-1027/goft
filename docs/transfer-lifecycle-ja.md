# 1ファイルが辿る道

対象に選ばれた後（[何が転送対象になるか](file-selection-ja.md)を参照）、ファイルには次の処理が行われます。
engine はどちらの端がローカルかを知りません。`send` はローカルを送り側・リモートを受け側として渡し、
`recv` はその逆にするだけで、以下の内容は両方向で同じです。

## 1. 転送先のディレクトリ

その周期の一覧で存在しないと分かっていた場合に作成します。ファイルごとではなくディレクトリごとに1回だけです。
転送先そのものも対象で、`send` の `remote.path` は最初の転送の前に存在している必要はありません。
足りない階層はどのプロトコルでも作成します（`local.path` は起動時に検証するため、`recv` でも存在している必要があります）。親が存在すれば1回の要求で作り、存在しない場合にだけ足りない親をさかのぼって作ります。

## 2. すでに存在するか

周期の開始時に、転送先の各ディレクトリを1回ずつ一覧して結果を保持します。
ファイルごとにサーバーへ問い合わせると1ファイルにつき1往復かかります。
`on_exists: skip` で1万個のファイルが送り側に残っていれば、毎周期1万往復です。

名前はまず完全一致で照合し、転送先が `FOO.CSV` と `foo.csv` を区別できない場合（Windows・macOS のディスク、SMB 共有）
は小文字化した名前でも照合します。そうしないと、すでに存在するファイルを毎周期転送し直すことになります。

こうした転送先には、大文字小文字だけが違う2つのファイルを置けません（Linux の送り側では置けます）。
1つの周期で `A.csv` と `a.csv`（あるいは `Invoices/a.csv` と `invoices/a.csv`）が見つかった場合は、どちらも送らず、
相手の名前を示して両方を失敗として記録します。両方送ると後から着いた方だけが残り、両方とも「届いた」と記録され、
`post_action: delete` なら元ファイルも両方消えてしまうからです。

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

再送は、直前の試行が使った名前ではなく自分専用の名前に書きます
（`<name>.2.goft.tmp`、次は `<name>.3.goft.tmp`）。goft が固まった接続を見限っても、
サーバー側がその接続を失ったことに気づいているとは限らず、気づくまでその試行が
書いていたファイルを掴んだままです。1つの名前に2つの書き手がいる状態は、
届いたバイト列がどちらの試行のものでもなくなり得る唯一の経路であり、
さらに Windows ではサーバーのハンドルのせいでその名前へのリネーム自体ができず、
どの試行も最初と同じ場所で失敗してしまいます。これらの名前はいずれも `.goft.tmp` で
終わるので、転送対象のファイルとして拾われることはありません。

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

検証は「読んだもの」と「書いたもの」を比べるだけなので、それだけでは送り側の書き込みがまだ続いていたことに気づけません。
両側が同じバイト列を持っていても、それがファイルの全体とは限らないからです。そこで、読み取ったバイト数を安定化判定の時点のサイズとも比べます。
両者が違えば一時ファイルを破棄し、本名には何も出さず、失敗として記録します。

```
the source changed while it was being transferred: 1048576 bytes when it settled, 1310720 read
```

すぐには再送しません。書き込み側がまだ作業中である可能性が高いからです。
いつ転送できる状態になるかは、次の周期の安定化判定が決めます。

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

`delete` と `move` は送り側のファイルを取り去るので、実行の前に送り側をもう一度確認します。
読み取った直後に1回、処理の直前にもう1回見て、その間にサイズか更新時刻が変わっていれば
（検証や rename の最中に書き込み側が追記した場合）、元のファイルはそのまま残し、失敗として記録します。

```
the source changed while it was being transferred: it was modified after being sent, so it was left in place (1048576 bytes then, 1310720 now)
```

送った分は届いていますが、ここで元ファイルを消すと、転送先に届いていないデータまで失われます。
`on_exists: overwrite` で同一と判定したファイルにも、比較した時点のサイズを基準に同じ確認を行います。
この確認で1ファイルあたり2回の問い合わせが増えますが、元ファイルを削除・移動する設定のときだけです。

## 7. 空になったディレクトリ

`remove_empty_dirs` を有効にすると、転送によって最後のファイルが取り出されたサブディレクトリを削除し、
その結果として親も空になれば親も削除します。

```jsonl
{"time":"2026-08-30T19:34:24.849405311+09:00","level":"INFO","msg":"removed empty directory","job":"invoice-archive","direction":"send","cycle_id":"83471e30-674e-44fe-8571-fbe34e98caab","event":"postaction","src":"/data/out/invoice/2026-08/day-01"}
{"time":"2026-08-30T19:34:24.849418141+09:00","level":"INFO","msg":"removed empty directory","job":"invoice-archive","direction":"send","cycle_id":"83471e30-674e-44fe-8571-fbe34e98caab","event":"postaction","src":"/data/out/invoice/2026-08"}
{"time":"2026-08-30T19:34:24.849427315+09:00","level":"INFO","msg":"cycle complete","job":"invoice-archive","direction":"send","cycle_id":"83471e30-674e-44fe-8571-fbe34e98caab","event":"summary","files":2,"succeeded":2,"skipped":0,"failed":0,"bytes":34,"duration_ms":1001,"dirs_removed":2}
```

どちらのレコードもディレクトリを省略なしで示します。削除した数はサマリの `dirs_removed` に入りますが、
このフィールドは実際に削除があったときにだけ書かれます。

行わないことが4つあります。

- **送り側のルートは決して削除しません。** 監視しているディレクトリ自体が消えたジョブには、監視する対象が無くなります
- **goft が空にしたのではないディレクトリはそのまま残します。** 周期の前からすでに空だったもの、途中で誰かが書き込んだものは対象外です
- **削除の時点で空でなければ、そのまま残します。** 削除を試して相手に拒否させるのではなく、先に一覧を取って確認します。すべてのプロトコルが拒否してくれるとは限らないためです
- **転送を失敗にはしません。** 削除できなかったディレクトリは警告どまりです。ファイルは届いており、それが仕事です。片付けは次の周期でまた試されます

このオプションには `post_action: delete` か `move` が必要です。`none` では送り側のファイルが残るため、
ディレクトリが空になることがないからです。これは「黙って何もしない設定」ではなく設定エラーとして扱います。
`recursive` が無効な場合もすることが無いので、こちらは警告として報告します。

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
転送中に送り側が変化した場合も再送しません。書き込み側がまだ作業中である可能性が高く、次の周期の安定化判定のほうが適切に判断できるからです。
判定は除外方式で、恒久的だと分かっているもの以外は「もう一度試す価値がある」と扱います。

通信が止まった接続も同じように扱います。何かが接続を待っている間に `remote.io_timeout`（既定5分）データが一切動かなければ接続を切ります。
待っていた呼び出しはその旨のメッセージで失敗し、そのファイルは新しい接続で再試行されます。

```
the connection stalled: nothing moved for 5m0s, so the connection was dropped (...)
```

測るのは経過時間ではなく止まっている時間なので、動き続けている転送はどれだけ長くかかっても打ち切られません。
接続の確立には別の上限 `remote.connect_timeout`（既定30秒）があり、TCP 接続だけでなくハンドシェイクとログイン、sftp では ssh-agent への問い合わせまでを含みます。

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
