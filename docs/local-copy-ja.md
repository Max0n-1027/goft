# ローカルのディレクトリ同士でコピーする

`protocol: local` は、サーバーではなくこのマシン上のもう1つのディレクトリを指定します。
それ以外はまったく同じジョブです。走査も、安定化判定も、検証も、一時名も、ログも変わりません。

```yaml
name: invoice-archive
local:
  path: /data/out/invoice
remote:
  protocol: local
  path: /backup/invoice
recursive: true
include: ["*.csv"]
verify: hash
on_exists: skip
```

`send` は `local.path` から `remote.path` へ、`recv` はその逆にコピーします。
方向を決めるのはコマンドで、sftp のときとまったく同じです。`serve send` は前者を監視し、ファイルが届くたびにコピーします。

## 実行するとこうなります

```console
goft invoice-archive  /data/out/invoice -> /backup/invoice  （2 件、830.1 KiB）
[1/2] 2026-08/invoice_202608_02.csv                  16 B  完了        0.0s
[2/2] invoice_202608_01.csv                     830.1 KiB  完了        0.0s
2 件: 転送 2（830.1 KiB）、スキップ 0、失敗 0  3.0 秒
```

ヘッダーには URL ではなく2つのパスがそのまま並びます。ログでも同じです。

```jsonl
{"time":"2026-08-26T04:49:53.025347889+09:00","level":"INFO","msg":"transfer","job":"invoice-archive","direction":"send","cycle_id":"7837a662-4f12-40ad-9cd7-163a8753ff83","event":"transfer","src":"/data/out/invoice/2026-08/invoice_202608_02.csv","dst":"/backup/invoice/2026-08/invoice_202608_02.csv","protocol":"local","bytes":16,"duration_ms":0,"verify":"hash","result":"success","hash_src":"d5f6162fd32742a1","hash_dst":"d5f6162fd32742a1"}
```

`protocol` は `local` として記録され、`src` と `dst` には両方のファイル名が省略なしで入ります。

## `goft test`

```console
設定          /etc/goft/invoice-archive.yaml  OK
ローカル      /data/out/invoice               OK  読み書き可能
解決          path = /backup/invoice          (yaml)
リモート      /backup/invoice                 OK
受信（一覧）  0 件                            OK
送信（書込）  書き込み可能                    OK
```

「接続」の行はありません。接続する相手がいないからです。項目名は「リモート」になり、使用するディレクトリを表示します。
「解決」が1行しかないのも同じ理由です。解決すべきホストもポートも資格情報もありません。

## 実行前に検証されること

2つのディレクトリは別々でなければなりません。一方が他方を含む場合や、同じディレクトリを指定した場合は設定エラーになります。

```
remote.path "/data/out/invoice/backup" is inside local.path "/data/out/invoice":
the two sides of a transfer must be separate directories
```

走査しているディレクトリの中へコピーすると、そのコピー自体も転送対象になり、`recursive: true` では止まらなくなります。
この検証は起動時に行われるので、何かが書き込まれる前に終了コード 2 で停止します。

ネットワークでしか意味を持たない設定（`host`・`port`・`user`・`password`・`share`）は、無視せずエラーにします。
自分のディスク上のディレクトリに対してパスワードを書いているジョブは、書いた人が意図したジョブではないからです。

## 何ではあり、何ではないか

これは検証付きのコピーであって、同期ではありません。ファイルは一方向にしか動かず、
送り側に無いからといって受け側のファイルが消されることもありません。
`cp` の代わりにこれを使う価値があるのは、コピーを検証し、記録し、繰り返し実行したい場合です。
同一であることを証明する必要があるアーカイブ、別のプログラムが監視している受け渡しディレクトリ、
名前以外はリモートであるマウント（NFS、OS がマウントした SMB、USB ディスク）などが該当します。

マウント済みの共有に対しては、OS にマウントさせて `protocol: local` を使うほうが `protocol: smb` より簡単なことがよくあります。
資格情報はマウント側の問題になり、goft からは普通のディレクトリに見えます。
引き換えに、マウントが外れた状態は接続エラーではなく「空のディレクトリ」または「存在しないディレクトリ」として現れます。

`post_action: move` は従来どおり送り側にのみ適用され、`move_to` は `local.path` の外である必要があります。

## 関連

- [何が転送対象になるか](file-selection-ja.md)
- [1ファイルが辿る道](transfer-lifecycle-ja.md)
- [ログを1行ずつ読む](log-example-ja.md)
