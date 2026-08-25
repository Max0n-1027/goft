# ログを1行ずつ読む

これは説明用に書き起こしたものではなく、実際のログです。[cmd/logsample_test.go](../cmd/logsample_test.go)
のテストを実サーバー（sftp）に対して1回走らせた出力を、goft が書いたままの形で引用しています。
書き換えたのはパス・ホスト名・ポートだけで、使い捨ての一時ディレクトリが実運用のジョブらしく読めるようにしてあります。

ジョブは4つのファイルを送ります。2つは普通のファイル、1つはサイズ上限を超えたファイル、
残る1つはアカウントに書き込み権限のないディレクトリにあります。

```yaml
name: invoice-upload
local:
  path: /data/out/invoice
remote:
  protocol: sftp
  host: invoice-sftp
  path: /upload/invoice
  user: uploader
  password: ${GOFT_SFTP_PASSWORD}
recursive: true
include: ["*.csv", "*.dat"]
max_file_size_mb: 1
stable_duration: 3s
workers: 2
verify: hash
on_exists: skip
log:
  path: /var/log/goft/invoice-upload.log
  level: info
```

## 1回目の実行

すべての実行は、これから使う設定（既定値を含む）の記録から始まります。
設定ファイルを後から探して日付を突き合わせなくても、「このファイルは何がどう動かしたのか」にログだけで答えられるようにするためです。

```json
{
  "time": "2026-08-25T21:35:33.517911157+09:00",
  "level": "INFO",
  "msg": "starting",
  "job": "invoice-upload",
  "direction": "send",
  "event": "lifecycle",
  "config": {
    "file": "/etc/goft/invoice-upload.yaml",
    "name": "invoice-upload",
    "local": {
      "path": "/data/out/invoice"
    },
    "remote": {
      "protocol": "sftp",
      "host": "invoice-sftp",
      "path": "/upload/invoice",
      "port": 22,
      "user": "uploader",
      "password": "REDACTED",
      "known_hosts": "/home/svc-transfer/.ssh/known_hosts",
      "use_ssh_config": false
    },
    "recursive": true,
    "include": [
      "*.csv",
      "*.dat"
    ],
    "exclude": [
      "*.tmp",
      "*.part",
      ".*"
    ],
    "max_file_size_mb": 1,
    "poll_interval": "5s",
    "stable_duration": "3s",
    "workers": 2,
    "verify": "hash",
    "on_exists": "skip",
    "post_action": "none",
    "retry": {
      "max_attempts": 3,
      "interval": "2s",
      "backoff": 2
    },
    "log": {
      "path": "/var/log/goft/invoice-upload.log",
      "level": "info",
      "rotation": "size",
      "max_size_mb": 100,
      "max_backups": 7,
      "max_age_days": 30,
      "compress": true
    }
  }
}
```

続いてファイルごとに1レコード、最後にその周期のサマリです。

```jsonl
{"time":"2026-08-25T21:35:36.554360166+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
{"time":"2026-08-25T21:35:36.561189297+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":6,"verify":"hash","result":"success","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
{"time":"2026-08-25T21:35:36.561935166+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/invoice_202608_02.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_02.csv","protocol":"sftp","bytes":16,"duration_ms":7,"verify":"hash","result":"success","hash_src":"d5f6162fd32742a1","hash_dst":"d5f6162fd32742a1"}
{"time":"2026-08-25T21:35:36.563337239+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/restricted/invoice_202608_03.csv","dst":"sftp://invoice-sftp/upload/invoice/restricted/invoice_202608_03.csv","protocol":"sftp","bytes":17,"duration_ms":2,"verify":"hash","result":"failed","error":"permission denied"}
{"time":"2026-08-25T21:35:36.563447555+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"summary","files":4,"succeeded":2,"skipped":1,"failed":1,"bytes":33,"duration_ms":3044}
```

読み方はこうです。`archive.dat` はサイズが大きすぎて送られませんでした。これは失敗ではなく警告で、他のファイルには影響しません。
CSV は2つとも届いていて、`hash_src` と `hash_dst` が一致しています。これが「読んだものがそのまま着いた」ことの証拠になります。
`invoice_202608_03.csv` は失敗しました。置き先のディレクトリにこのアカウントの書き込み権限がないためで、
失敗は `ERROR` レコードになり理由が `error` に入ります。

`workers: 2` なので、レコードの並びは先に終わったワーカー順になり、実行のたびに前後します。
順序ではなく `cycle_id` と `time` が並べ直すための手がかりです。

この周期のレコードはすべて同じ `cycle_id` を持ち、サマリも同じ値を共有します。
何週間も追記され続けたログからでも、1周期ぶんだけを抜き出せます。

## 2回目の実行 — すでに届いている場合

```jsonl
{"time":"2026-08-25T21:35:39.602116798+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"377bafc7-4a65-49f9-b0cb-6d516e67b37e","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
{"time":"2026-08-25T21:35:39.602367285+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"377bafc7-4a65-49f9-b0cb-6d516e67b37e","event":"transfer","src":"/data/out/invoice/invoice_202608_02.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_02.csv","protocol":"sftp","bytes":16,"duration_ms":0,"verify":"hash","result":"skipped","reason":"already_exists"}
{"time":"2026-08-25T21:35:39.60247795+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"377bafc7-4a65-49f9-b0cb-6d516e67b37e","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"skipped","reason":"already_exists"}
{"time":"2026-08-25T21:35:39.605613666+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"377bafc7-4a65-49f9-b0cb-6d516e67b37e","event":"transfer","src":"/data/out/invoice/restricted/invoice_202608_03.csv","dst":"sftp://invoice-sftp/upload/invoice/restricted/invoice_202608_03.csv","protocol":"sftp","bytes":17,"duration_ms":3,"verify":"hash","result":"failed","error":"permission denied"}
{"time":"2026-08-25T21:35:39.605796156+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"377bafc7-4a65-49f9-b0cb-6d516e67b37e","event":"summary","files":4,"succeeded":0,"skipped":3,"failed":1,"bytes":0,"duration_ms":3037}
```

`on_exists: skip` は文字どおりの動作です。転送しませんし、その判断のために中身を読むこともしません。
周期の開始時に取った転送先の一覧だけで足ります。何もしていないので `duration_ms` は 0 です。
サイズ超過のファイルと失敗はどちらも繰り返されます。状況が変わっておらず、しかもこれは別のプロセスだからです（[サイズ上限](#サイズ上限)を参照）。

## 上書きの前に比較する

`on_exists: overwrite` でも無条件には上書きしません。先に両側を比較し、すでに同一のファイルはそのままにします。

```jsonl
{"time":"2026-08-25T21:35:42.645140415+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
{"time":"2026-08-25T21:35:42.649116785+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":3,"verify":"hash","result":"skipped","reason":"identical","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
{"time":"2026-08-25T21:35:42.64981559+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"transfer","src":"/data/out/invoice/invoice_202608_02.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_02.csv","protocol":"sftp","bytes":16,"duration_ms":3,"verify":"hash","result":"skipped","reason":"identical","hash_src":"d5f6162fd32742a1","hash_dst":"d5f6162fd32742a1"}
{"time":"2026-08-25T21:35:42.651905931+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"transfer","src":"/data/out/invoice/restricted/invoice_202608_03.csv","dst":"sftp://invoice-sftp/upload/invoice/restricted/invoice_202608_03.csv","protocol":"sftp","bytes":17,"duration_ms":2,"verify":"hash","result":"failed","error":"permission denied"}
{"time":"2026-08-25T21:35:42.652011532+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"summary","files":4,"succeeded":0,"skipped":3,"failed":1,"bytes":0,"duration_ms":3040}
```

`reason` は `already_exists` ではなく `identical` になり、ハッシュも記録されます。
「送らなかったこと」にも、送ったときと同じだけの証拠が要るからです。

## 再送と、それでも直らなかった失敗

ここでは転送先の名前を置き換えられませんでした。転送そのものは成功していて（ハッシュが記録されています）、
本名へ移す rename が失敗し続けています。

```jsonl
{"time":"2026-08-25T21:35:48.694181956+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":2,"of":3,"retry_in_ms":1000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)"}
{"time":"2026-08-25T21:35:49.718238957+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":3,"of":3,"retry_in_ms":2000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)"}
{"time":"2026-08-25T21:35:51.740558027+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_05.csv","protocol":"sftp","bytes":17,"duration_ms":8,"verify":"hash","result":"failed","error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)","hash_src":"b2d06a1f4fa13583","hash_dst":"b2d06a1f4fa13583","attempt":3}
{"time":"2026-08-25T21:35:51.740695447+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"summary","files":1,"succeeded":0,"skipped":0,"failed":1,"bytes":0,"duration_ms":6077}
```

再送のレコードには、何回目か（`attempt` / `of`）と次の待ち時間（`retry_in_ms`）が入ります。
待ち時間は 1秒 → 2秒 と倍になります。試行を使い切ると `failed` のレコードが1つ出て、そこに `attempt` が付きます。
再送したことと、何回試したかの両方がログから分かります。一時ファイルは削除され、転送先は元のまま残ります。

すべての失敗が再送されるわけではありません。接続の切断や検証の不一致は再送しますが、
ファイルが無い・権限が無い・サーバーが 5xx を返した場合は再送しません。次に試しても同じ結果になるからです。
上の権限エラーに `attempt` が付いていないのはそのためです。

## サーバーに繋がらなかった場合

設定のレコード（内容は上と同じ）に続いて、次の1行だけが出ます。

```jsonl
{"time":"2026-08-25T21:35:45.661033784+09:00","level":"ERROR","msg":"run failed","job":"invoice-upload","direction":"send","event":"lifecycle","error":"dial invoice-sftp:22: dial tcp 10.0.4.12:22: connect: connection refused"}
```

繋がらないサーバーは、ファイルを1つも試す前に実行を終わらせます。転送のレコードは1件も出ず、設定と理由だけが残ります。
このとき `goft send` の終了コードは 2 です。監視する側は「そもそも起動できていない」と
「動いたが一部のファイルが失敗した」（終了コード 1）を区別できます。

## デバッグレベル

`log.level: debug` で増えるのは3種類です。まず、接続パラメータと、その値がどこから解決されたか。

```jsonl
{"time":"2026-08-25T21:35:51.743764459+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"host","value":"invoice-sftp","source":"yaml"}
{"time":"2026-08-25T21:35:51.744010482+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"port","value":"22","source":"yaml"}
{"time":"2026-08-25T21:35:51.744042149+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"user","value":"uploader","source":"yaml"}
{"time":"2026-08-25T21:35:51.744061866+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"password","value":"REDACTED","source":"yaml"}
{"time":"2026-08-25T21:35:51.744079129+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"private_key","value":"/home/svc-transfer/.ssh/id_ed25519","source":"default"}
{"time":"2026-08-25T21:35:51.74409743+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"known_hosts","value":"/home/svc-transfer/.ssh/known_hosts","source":"yaml"}
```

次に、走査で何件見つかり、そのうち何件が安定していたか。

```jsonl
{"time":"2026-08-25T21:35:54.7474434+09:00","level":"DEBUG","msg":"scan complete","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"scan","found":4,"settled":4}
```

そして転送のステップごとのレコードです。それぞれに `duration_ms` が付くので、遅い実行のどこが遅いのかを切り分けられます。

```jsonl
{"time":"2026-08-25T21:35:54.781854799+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"mkdir","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":0}
{"time":"2026-08-25T21:35:54.784956443+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"compare","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":2}
{"time":"2026-08-25T21:35:54.785978073+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"write","src":"/data/out/invoice/restricted/invoice_202608_03.csv","duration_ms":0,"error":"permission denied"}
```

転送が成功したレコードには、このレベルでのみ `rate_mibs` が加わります。
スキップや失敗のレコードには付きません。`bytes` は「動かなかったファイル」のサイズなので、
そこから計算した速度は何も表さないからです。

<a id="サイズ上限"></a>

## サイズ上限

`max_file_size_mb` を超えたファイルは `reason: size_limit` でスキップされ、warn レベルで記録されます。

```jsonl
{"time":"2026-08-25T21:35:36.554360166+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
```

警告はプロセスごとにファイル1つにつき1回だけ書かれ、以降の周期では同じ内容を debug に落とします。
決して送られないファイルはログから見えるべきですが、毎周期同じ警告を繰り返す必要はないからです。
ファイルが変化した場合（サイズが増えた、書き直された）は再び警告が出ます。もう「報告済みのファイル」ではないからです。

サイズ判定は走査時ではなく安定化判定の後に行います。判定の時点でファイルが既知のものになっているようにするためです。
上の3回の実行がいずれも警告を出しているのは、それぞれ別の `goft send` だからです。新しいプロセスには記憶がありません。
`serve` であれば警告は最初の周期だけです。

## ログを読む

JSON Lines なので `jq` でレコード単位に読めます。

```bash
# 1周期がしたことをすべて
jq -c 'select(.cycle_id == "46466581-ab9f-4d57-bb5f-1c63aab9f97b")' invoice-upload.log

# 何が、なぜ失敗したか
jq -r 'select(.result == "failed") | "\(.time) \(.src) \(.error)"' invoice-upload.log

# 周期ごとの転送バイト数
jq -r 'select(.event == "summary") | "\(.time) \(.bytes)"' invoice-upload.log

# 特定のファイルが壊れずに届いた証拠
jq -c 'select(.src | endswith("invoice_202608_01.csv")) | {time, result, hash_src, hash_dst}' invoice-upload.log
```

転送レコードにどのフィールドを載せるかは `log.fields` で決まります。
`time`・`level`・`msg`・`job`・`direction`・`event`・`cycle_id` は常に書かれます。
フィールドの一覧は [goft.example.yaml](../goft.example.yaml) に、2種類の出力の使い分けは
[README-ja.md](../README-ja.md#出力) にあります。

## この例を作り直す

```bash
GOFT_LOG_SAMPLE=/tmp/goft-sample go test ./cmd -run TestGenerateLogSample
```

## 関連

- [何が転送対象になるか](file-selection-ja.md)
- [1ファイルが辿る道](transfer-lifecycle-ja.md)
- [画面に出る内容](console-output-ja.md)
