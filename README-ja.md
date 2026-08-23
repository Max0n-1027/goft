# goft

[English](README.md)

ディレクトリを監視して FTP / SFTP / SMB でファイルを転送します。転送したファイルは必ず検証します。

```bash
goft send -c invoice-upload.yaml        # ローカル → リモート、1回だけ
goft recv -c report-download.yaml       # リモート → ローカル、1回だけ
goft serve send -c invoice-upload.yaml  # ローカル → リモート、常駐監視
goft test -c invoice-upload.yaml        # 設定と接続の確認
```

`send` / `recv` / `serve` に `--dry-run` を付けると、転送先に一切触れずに転送対象の一覧だけを出します。

## 何をするか

送り側で見つけたファイルごとに、goft は次の順で処理します。

1. ファイルが変化しなくなるまで待つ
2. 受け側へ一時的な名前で書き込む
3. 検証する（既定では読み戻して xxHash を比較）
4. 検証を通ったときにだけ本来の名前へリネームする
5. 送り側に対して設定された転送後処理を行う

受け側に本来の名前でファイルが現れるのは、それが完全かつ検証済みのときだけです。転送の途中でプロセスが落ちても、残るのは `.goft.tmp` ファイルだけで、次回の実行がそれを上書きします。

## 設定

1つの設定ファイルが1つのジョブを表し、1つのジョブは1つのプロセスで動きます。注釈つきの完全版は [goft.example.yaml](goft.example.yaml) にあります。

```yaml
name: invoice-upload
local:
  path: /data/out/invoice
remote:
  protocol: sftp
  host: invoice-sftp        # ~/.ssh/config の Host エイリアスが使えます
  path: /upload/invoice
include: ["*.csv"]
verify: hash
on_exists: skip
post_action: move
move_to: /data/done/invoice
log:
  path: /var/log/goft/invoice-upload.log
  level: info
  rotation: daily
```

転送の方向は設定ファイルではなくコマンドで決まります。つまり同じファイルを `send` と `recv` の両方で使えてしまいます。用途ごとにファイルを分けてください。とくに `post_action: delete` と組み合わせると、2つのジョブが同じファイルを延々とやり取りすることになります。

### 資格情報

パスワードを平文で書く必要はありません。

- ファイル中のどこでも `${VAR}` は環境変数から展開されます
- **sftp** は書かなかった項目を `~/.ssh/config` から補います（`HostName`、`Port`、`User`、`IdentityFile`、`UserKnownHostsFile`）
- **ftp** は `user` と `password` を `~/.netrc`（Windows は `_netrc`）から補います
- **smb** は既定のファイルを読みません。`user`、`password`、`share` を設定に書いてください

設定ファイルに書いた値は常に既定ファイルより優先されます。何がどこから解決されたかは `goft test` で確認できます。

ssh_config の機能のうち2つは未対応で、黙って無視せず警告として報告します。`Match` ブロックと `ProxyJump` / `ProxyCommand` です。必要な設定はジョブファイルに直接書いてください。ssh-agent は使いません。鍵は `private_key` で、パスフレーズつきなら `private_key_passphrase` で渡してください。

### 複数ジョブの運用

goft は複数ジョブを管理しません。設定ファイルごとにプロセスを分けて起動してください。Linux では systemd のテンプレートユニットが便利です。

```ini
# /etc/systemd/system/goft@.service
[Service]
ExecStart=/usr/local/bin/goft serve send -c /etc/goft/%i.yaml
Restart=always
```

```bash
systemctl enable --now goft@invoice-upload
```

すべてのログレコードに `job` と `direction` が入るので、複数プロセスの出力を1箇所に集約しても区別できます。

## 知っておくとよい挙動

**検知は即時ではありません。** `serve` は1周期ずつ順番に実行し、その間にスリープします。そのため最短の検知時間は `max(poll_interval, stable_duration)` です。周期を直列に回すことで、転送中のファイルを次の周期が二重に拾うことが原理的に起きなくなります。

**ファイルは変化しなくなってから転送されます。** goft は更新時刻だけを信用せず、サイズと更新時刻を周期をまたいで比較します。タイムスタンプを保持してコピーするツール（`cp -p`、`rsync --times`）は、コピー中もファイルの更新時刻を元ファイルの古い値のままにするためです。

**FTP のタイムスタンプは粗いです。** FTP に stat 相当のコマンドはありません。更新時刻はサーバが対応していれば `MLST` から、そうでなければディレクトリ一覧から取得しますが、後者は分単位の精度しかないことがあります。FTP サーバから `recv` する場合は `stable_duration` を長めに取ってください。

**`on_exists: skip` は送り側のファイルをそのまま残します。** スキップは「転送しなかった」ということなので `post_action` も実行されず、そのファイルは毎周期あらためて調べられます。送り側のディレクトリを増やし続けたくない場合は `post_action`（または `on_exists: overwrite`）を使ってください。

**転送したファイルはモード 0644 で作成されます**（プロセスの umask が適用されます）。次にそのファイルを読む側が読めるようにするためです。Windows ではモードは無視されます。

**失敗したファイルは再送されますが、効果が見込める場合だけです。** 同じ周期の中で `retry.max_attempts` 回まで（既定3回）試します。接続断や検証失敗は再送しますが、ファイルが存在しない・権限がない・サーバが 5xx を返した場合は再送しません。次も同じ結果になるためです。再送の前には接続を張り直します。切れた接続のまま試しても同じように失敗するからです。`retry.max_attempts: 1` で無効にできます。

転送後処理だけは例外です。ファイルは届いたのに `post_action` が失敗した場合、やり直すのは後処理だけです。既に届いているファイルを送り直しても意味がないためです。

**`on_exists: overwrite` は無駄な転送を避けます。** 上書きの前に、設定された `verify` の方法で既存ファイルを比較し、内容が一致していれば転送をスキップします。`verify: none` は比較する手段がないため、常に転送し直します。

## 状態

3つのプロトコルすべてを実サーバで検証しています（OpenSSH 10.2、vsftpd 3.0.5、Samba 4.23）。双方向に転送し、チェックサムを goft の外で突き合わせて確認しました。SFTP はプロセス内に SSH サーバを立てるテストでも網羅しているので、`go test ./...` は外部サービスなしで通ります。

手元のサーバに向けてライブスイートを実行するには次のようにします。

```bash
GOFT_LIVE_PROTOCOL=sftp GOFT_LIVE_HOST=192.168.0.10 \
GOFT_LIVE_USER=user GOFT_LIVE_PASSWORD=secret \
GOFT_LIVE_PATH=/home/user/goft-test \
GOFT_LIVE_KNOWN_HOSTS=/path/to/known_hosts \
go test -count=1 ./internal/fsys/ -run Live -v
```

同じ変数が `ftp` と `smb` でも使えます（smb は `GOFT_LIVE_SHARE` を追加してください）。`-count=1` は必須です。Go はテスト結果をキャッシュしますが、サーバ側が変わったことを知る術がないためです。

## 終了コード

| コード | 意味 |
|---|---|
| 0 | 正常終了（`serve` がシグナルで停止した場合を含む） |
| 1 | 実行はできたが、1件以上のファイルが失敗した |
| 2 | 実行を完了できなかった（設定の誤り、または接続失敗） |

`goft test` だけは例外で、上り下りの両方を報告し、**どちらも使えないときだけ**失敗になります。読み取り専用のアカウントは `recv` の構成としては何もおかしくないためです。

## 出力

記録として残すのは JSON Lines のログで、標準出力の人間可読な出力は作業を見ている人のためのものです。単発実行では既定で表示され、`serve` では表示されません。`--console` / `--no-console` でどちらにも切り替えられます。

`serve` は周期が終わってからまとめて報告し、何も転送されなかった周期については何も出しません。そのため、正常に動いていて対象がない監視は完全に無言になります。周期そのものが失敗した場合（サーバに到達できないなど）は必ず報告します。

ログファイルを設定しておらず、かつコンソール出力が有効な場合は、両者が混ざらないよう JSON ログが標準エラーに回ります。

```bash
goft send 2>/dev/null    # 人間可読な出力だけ
goft send >/dev/null     # JSON ログだけ
```

検証を通った転送では `hash_src` と `hash_dst` が info レベルで記録されるので、ログだけでファイルが無傷で届いたことを確認できます。パスは両端ともフルパスで記録されます（`/data/out/invoice/a.csv` と `sftp://host/upload/invoice/a.csv`）。読み手が知らないルートからの相対パスでは、どのファイルか特定できないためです。

実行のたびに、使用した設定が既定値も含めて記録されます。監査用にログを保管しておけば、後から「どの設定でこのファイルを転送したか」がログだけで分かります。

```json
{"msg":"starting","event":"lifecycle","config":{"file":"/etc/goft/invoice.yaml",
 "local":{"path":"/data/out/invoice"},
 "remote":{"protocol":"sftp","host":"invoice-sftp","path":"/upload/invoice",
           "user":"uploader","password":"REDACTED"},
 "verify":"hash","on_exists":"skip","post_action":"move", ...}}
```

パスワードとパスフレーズは「設定されている」ことだけが記録され、値は出力されません。

**どのレコードを書くかはレベルが、各転送レコードに何を書くかは `log.fields` が決めます。**

```yaml
log:
  fields: [src, dst, bytes, result, error, hash_src, hash_dst]
```

キーごと省略すると既定のセットになります。`time` / `level` / `msg` / `job` / `direction` / `event` は常に書かれます。これらを欠いたレコードは解釈できないためです。指定できる項目名は [goft.example.yaml](goft.example.yaml) に一覧があり、未知の名前は黙って無視せず設定エラーになります。

詳細度の設定は `log.level` の1つだけで、両方の出力を制御します。各レベルで何が出るかは [goft.example.yaml](goft.example.yaml) 末尾の表を参照してください。

## ドキュメント

コマンドと各パッケージの説明は `go doc` で読めます。

```bash
go doc .                    # 使い方・設定・フラグ・終了コード
go doc ./internal/engine    # 転送1周期の仕組み
go doc ./internal/fsys      # プロトコル実装が満たすべき契約
```

## ビルド

```bash
./build.sh
```

依存はすべて純粋な Go なので、クロスコンパイル用のツールチェーンは不要です。生成物は `build/<os>/<arch>/` の下に置かれます。

```
build/linux/amd64/goft
build/windows/amd64/goft.exe
build/darwin/arm64/goft
```

対象を絞るにはプラットフォームを引数で渡します。リリース版のバージョンは `VERSION` で埋め込みます。

```bash
VERSION=v1.0.0 ./build.sh linux/amd64 windows/amd64
```

埋め込まれた内容は `goft version` で確認できます。`SOURCE_DATE_EPOCH` を設定すると再現可能なビルドになります。手元で1つ作るだけなら `go build .` でも構いません。
