# goft

[English](README.md)

ディレクトリを監視して FTP / SFTP / SMB でファイルを転送します。同じマシン上のディレクトリ同士のコピーもできます。転送したファイルは必ず検証します。

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

`protocol: local` を指定すると、サーバーではなく同じマシン上のもう1つのディレクトリを指定できます。それ以外はまったく同じジョブで、走査も検証もログも変わりません。

```yaml
remote:
  protocol: local
  path: /backup/invoice
```

2つのディレクトリは別々である必要があり、一方が他方を含む設定は拒否されます。詳しくは [ローカルのディレクトリ同士でコピーする](docs/local-copy-ja.md) を参照してください。

転送の方向は設定ファイルではなくコマンドで決まります。つまり同じファイルを `send` と `recv` の両方で使えてしまいます。用途ごとにファイルを分けてください。とくに `post_action: delete` と組み合わせると、2つのジョブが同じファイルを延々とやり取りすることになります。

### 資格情報

パスワードを平文で書く必要はありません。

- 値の中の `${VAR}` は環境変数から展開されます。設定されていない変数は空文字にせずエラーにするので、export し忘れは後でログイン失敗として現れるのではなく、起動時に止まります。認識するのは `${...}` の形だけです。単独の `$` は普通の文字として扱うのでパスワードに含めても構いません。コメントの中の変数は無視します
- **sftp** は書かなかった項目を `~/.ssh/config` から補います（`HostName`、`Port`、`User`、`IdentityFile`、`UserKnownHostsFile`）
- **ftp** は `user` と `password` を `~/.netrc`（Windows は `%USERPROFILE%\.netrc`）から補います
- **smb** には専用のファイルがありません。`user`、`password`、`share` を設定に書いてください
- **Windows** では、goft 用に登録した汎用資格情報から `user` と `password` を補えます（ftp を含め、プロトコルを問いません）。エントリ名は `goft:<protocol>://<host>` なので、同じホストでもプロトコルごとに別の資格情報を持てます:

  ```
  cmdkey /generic:goft:ftp://invoice-ftp /user:uploader /pass:secret
  cmdkey /generic:goft:smb://fileserver /user:svc-transfer /pass:secret
  ```

  資格情報マネージャーは `~/.netrc` や `~/.ssh/config` より先に参照されるため、エントリを登録しておけば ftp のジョブに netrc は不要ですし、パスワード認証の sftp のジョブも設定ファイルにパスワードを書かず ssh_config に `User` を書かずに済みます。取得できるのはユーザー名とパスワードだけです。`private_key_passphrase` はホストへの認証ではなく鍵ファイルの復号に使うものなので、設定ファイルか `${VAR}` に置いたままになります。別名で登録済みのエントリを読むには `credential_target`、資格情報マネージャーを一切読まないようにするには `use_credential_manager: false` を指定します

  Windows が共有フォルダ用に保存する資格情報はこれとは別の種類で、プラットフォームが認証パッケージ専用としているため、エクスプローラーや `net use` が記憶したものは再利用できません

設定ファイルに書いた値は常に既定ファイルより優先されます。何がどこから解決されたかは `goft test` で確認できます。

ssh_config の機能のうち2つは未対応で、黙って無視せず警告として報告します。`Match` ブロックと `ProxyJump` / `ProxyCommand` です。必要な設定はジョブファイルに直接書いてください。ssh-agent は使いません。鍵は `private_key` で、パスフレーズつきなら `private_key_passphrase` で渡してください。

goft が自分で見つける鍵（ssh_config の `IdentityFile`、または `~/.ssh/id_ed25519` と `~/.ssh/id_rsa`）は、そのまま使える場合にだけ使います。パスフレーズで保護されていて `private_key_passphrase` が設定されていない鍵は、警告を出して候補から外します。個人の鍵に付けたパスフレーズが、パスワード認証のジョブの邪魔をしないようにするためです。`private_key` で指定した鍵は常に使い、復号できなければエラーにします。

ssh_config の `StrictHostKeyChecking` はどちらの値でも警告を出したうえで従います。`no` ではホスト鍵の検証を行いません。`accept-new` は OpenSSH と同じ動作で、known_hosts にまだ載っていないホストの鍵を受け入れてそこに記録し（ファイルが無ければ作成します）、載っているのに鍵が違うホストは拒否します。中間者攻撃はまさにそう見えるからです。

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

**転送中に変化したファイルは、公開も削除もしません。** 安定化判定は、`stable_duration` より長く止まってから書き込みを再開するプロセスにはだまされます。そこで、読み取ったバイト数を安定化時点のサイズとも比べ、違っていれば本名では公開しません。`post_action` が元ファイルを削除・移動する設定では、その直前にもう一度確認し、送った後に変化していれば元ファイルをそのまま残します。どちらの場合も失敗として記録し、すぐには再送せず、次の周期の安定化判定に任せます。

**FTP のタイムスタンプは粗いです。** FTP に stat 相当のコマンドはありません。更新時刻はサーバが対応していれば `MLST` から、そうでなければディレクトリ一覧から取得しますが、後者は分単位の精度しかないことがあります。FTP サーバから `recv` する場合は `stable_duration` を長めに取ってください。

**`on_exists: skip` は送り側のファイルをそのまま残します。** スキップは「転送しなかった」ということなので `post_action` も実行されず、そのファイルは毎周期あらためて調べられます。送り側のディレクトリを増やし続けたくない場合は `post_action`（または `on_exists: overwrite`）を使ってください。

**空になったディレクトリの削除は、指定したときだけ行います。** `remove_empty_dirs` を有効にすると、その周期で最後のファイルを取り出したサブディレクトリを削除し、その結果として親も空になれば親も削除します。送り側のルートは決して削除しません。goft が空にしたのではないディレクトリ（もともと空だったもの、途中で誰かが書き込んだもの）もそのまま残します。`post_action` は `delete` か `move` である必要があります。`none` では送り側のファイルが残り、ディレクトリが空になることがないからです。詳しくは [1ファイルが辿る道](docs/transfer-lifecycle-ja.md) を参照してください。

**転送したファイルのモードを誰が決めるかは宛先によって違います。** このマシン上のディレクトリ（`protocol: local` を含む）または SMB 共有に書く場合、goft はモード 0644 で作成します（プロセスの umask が適用されます）。次にそのファイルを読む側が読めるようにするためです。sftp と ftp ではモードを一切送らず、サーバが決めます（OpenSSH の sftp-server は、クライアントがモードを送らなかったファイルに 0666 を使います）。Windows にはそもそも作用させるモードがなく、ローカルに書く場合も Windows の SFTP サーバが書く場合も、書き込み先ディレクトリの権限を引き継ぎます。Windows 11 上の OpenSSH 9.5p2 for Windows で確認しました。継承可能な許可を持つディレクトリへ転送したファイルには、その許可が継承済みとして付き、goft が同時に作成したサブディレクトリとまったく同じ状態になりました。

**Windows の転送先では、権限をフォルダだけでなくファイルにも継承させる必要があります。** `(CI)`（コンテナ継承）だけを持つアクセス許可はサブディレクトリには継承されますが、その中のファイルには継承されません。`icacls upload /grant "グループ:(CI)(M)"` と書くとこの状態になります。goft が作成するディレクトリは正しく継承する一方、書き込んだファイルは何も継承しません。外から見たときの症状がまさにこれです。転送は中身を置くところまで進んでからその後で失敗します。ファイルの作成はディレクトリに対して権限チェックされ、それ以降はファイルに対してチェックされるためです。`verify: hash` ならファイルの読み戻しで失敗し、`verify: length` や `none` なら rename まで進んで、一時ファイルの削除権限が必要なそこで失敗します。素の `sftp put` は読み戻しも rename もしないため、goft を向けるまでこの設定のディレクトリは問題なく見えます。

一時ファイルの削除も拒否されるため（`could not remove temporary file` として記録されます）`.goft.tmp` が残り、次の周期からは write でも失敗するようになります。自分で作成したファイルを開き直せなくなるためです。`(CI)` と併せて `(OI)` を付与し、既存の中身にも押し下げたうえで、残骸を削除してください。

```
icacls C:\upload /grant "グループ:(OI)(CI)(M)" /T
del /s C:\upload\*.goft.tmp
```

同じ条件で確認しました。同じサーバー、同じ非管理者アカウント、同じジョブで、`(OI)(CI)` なら2件とも転送でき、`(CI)` だけなら2件とも失敗します。失敗する箇所は `verify` の設定に応じて検証か rename です。上の2つのコマンドを失敗した状態に対して実行すると、転送できる状態に戻ることも確認しました。

**Windows は、名前が変わってしまうファイルを受け取りません。** サーバ上で `2026:01.csv` という名前のファイルは、その名前のまま Windows のディスクには書けません。コロンは NTFS の代替データストリームを開くため、中身は `2026` という空のファイルの中に隠れてしまい、エクスプローラからも `dir` からもバックアップからも、goft 自身の次の周期からも見えなくなります。しかも読み戻せば同じ解釈が適用されて中身は取り出せるので、検証は通り、転送は成功したように見えます。末尾のドットや空白も同じです。Windows がそれを落とすため、サーバが使っていた名前とは別の名前で届きます。予約デバイス名（`con`、`nul`、`aux`、`com1` など）も同様で、通常の Win32 パス解決では後から到達できなくなります。いずれも事後には検出できないため、Windows への `recv` は書き込む前にその名前を拒否し、そのファイルを失敗として記録し、名前のどこが問題だったかを示します。再試行はしません。次も同じように拒否されるからです。周期の残りはそのまま続き、Windows がその名前のまま保持できるファイルはこれまでどおり転送されます。

**常駐ジョブは失敗が続くと静かになります。** 周期そのものが実行できない場合（サーバに到達できないなど）、次の周期までの間隔が倍々に伸び、上限は5分です。周期が1回成功すれば `poll_interval` に戻ります。1秒間隔のジョブがサーバの恒久障害に当たると、そのままでは同じエラーを1日に数万行書き続けることになります。

**失敗したファイルは再送されますが、効果が見込める場合だけです。** 同じ周期の中で `retry.max_attempts` 回まで（既定3回）試します。接続断や検証失敗は再送しますが、ファイルが存在しない・権限がない・サーバが 5xx を返した場合は再送しません。次も同じ結果になるためです。再送の前には接続を張り直します。切れた接続のまま試しても同じように失敗するからです。`retry.max_attempts: 1` で無効にできます。

転送後処理だけは例外です。ファイルは届いたのに `post_action` が失敗した場合、やり直すのは後処理だけです。既に届いているファイルを送り直しても意味がないためです。

**`on_exists: overwrite` は無駄な転送を避けます。** 上書きの前に、設定された `verify` の方法で既存ファイルを比較し、内容が一致していれば転送をスキップします。`verify: none` は比較する手段がないため、常に転送し直します。

## 状態

3つのプロトコルすべてを実サーバで検証しています（OpenSSH 10.2、vsftpd 3.0.5、Samba 4.23）。双方向に転送し、チェックサムを goft の外で突き合わせて確認しました。

`go test ./...` は外部サービスなしで通ります。SSH サーバと FTP サーバをプロセス内に立てるためです。SMB には利用できるサーバ実装がないため、転送メソッドの検証は下記のライブスイートのみになります。FTP でも2つの挙動が同様です（存在しないパスに空の一覧が返ること、DELE でディレクトリを消せないこと）。プロセス内サーバはどちらもそう振る舞わず、vsftpd は両方そう振る舞うためです。

手元のサーバに向けてライブスイートを実行するには次のようにします。

```bash
GOFT_LIVE_PROTOCOL=sftp GOFT_LIVE_HOST=192.168.0.10 \
GOFT_LIVE_USER=user GOFT_LIVE_PASSWORD=secret \
GOFT_LIVE_PATH=/home/user/goft-test \
GOFT_LIVE_KNOWN_HOSTS=/path/to/known_hosts \
go test -count=1 ./internal/fsys/ -run Live -v
```

同じ変数が `ftp` と `smb` でも使えます（smb は `GOFT_LIVE_SHARE` を追加してください）。`-count=1` は必須です。Go はテスト結果をキャッシュしますが、サーバ側が変わったことを知る術がないためです。

### Windows

`go test ./...` は Windows でも他の環境と同じように通ります。Windows にしか存在しない挙動（ロックされたファイル、Windows が保持できない名前、`%USERPROFILE%`、`MAX_PATH` を超えるパス）は `_windows_test.go` のファイルが担当し、これらは Windows でのみビルドされます。逆に、POSIX のファイルシステムを前提とするテストは `requirePOSIX` を呼んで理由を明示しているため、Windows では失敗ではなくスキップとして報告されます。そうしたテストにはそれぞれ、同じコードが Windows で何をすべきかを検証する対の Windows 側テストがあります。

1つだけ明示的に有効化するテストがあります。実行した人の資格情報ストアに書き込むためです。どのジョブも使わないホスト名でエントリを登録し、実行後に削除します。

```bash
GOFT_WINCRED_TEST=1 go test -count=1 ./internal/fsys/ -run Credential -v
```

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

どちらの出力も UTF-8 です。ログは JSON がそう定められているため、コンソールはファイル名を保存されているとおりに表示するためです。したがって Windows のコンソールをその地域のコードページ（日本語環境なら cp932）のままにしていると、ASCII 以外は文字化けします。`chcp 65001` を実行するか、Windows Terminal か PowerShell 7 を使えば正しく表示されます。書き出されるバイト列自体はどちらでも同じなので、リダイレクトしたログには影響しません。

ログファイルを設定しておらず、かつコンソール出力が有効な場合は、両者が混ざらないよう JSON ログが標準エラーに回ります。

```bash
goft send 2>/dev/null    # 人間可読な出力だけ
goft send >/dev/null     # JSON ログだけ
```

検証を通った転送では `hash_src` と `hash_dst` が info レベルで記録されるので、ログだけでファイルが無傷で届いたことを確認できます。転送先に同じ内容が既にあってスキップした場合も同様に記録されます。「送らなかった」ことにも「送った」ことと同じだけ根拠が要るためです。パスは両端ともフルパスで記録されます（`/data/out/invoice/a.csv` と `sftp://host/upload/invoice/a.csv`）。読み手が知らないルートからの相対パスでは、どのファイルか特定できないためです。

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

キーごと省略すると既定のセットになります。`time` / `level` / `msg` / `job` / `direction` / `event` / `cycle_id` は常に書かれます。これらを欠いたレコードは解釈できないためです。

`cycle_id` は送り側を1周するごとに発行される UUID で、その周期が出したレコード（転送・再送・サマリ）すべてに同じ値が入ります。常駐プロセスが何日も追記し続けたログから、1周期分だけを取り出せます。

```bash
jq -r 'select(.cycle_id == "6482c7d7-...")' goft.log
```

起動・停止のレコードには `cycle_id` が付きません。どの周期にも属さず、プロセスに属するものだからです。指定できる項目名は [goft.example.yaml](goft.example.yaml) に一覧があり、未知の名前は黙って無視せず設定エラーになります。

詳細度の設定は `log.level` の1つだけで、両方の出力を制御します。各レベルで何が出るかは [goft.example.yaml](goft.example.yaml) 末尾の表を参照してください。

実際のログを1行ずつ読み解いた例が [docs/log-example-ja.md](docs/log-example-ja.md) にあります。転送とそのハッシュ、スキップ、再送とその待ち時間、失敗、そして同じ実行を debug レベルで見た場合を扱っています。

## ドキュメント

[docs/](docs/README-ja.md) 以下のページでは、実際の実行結果を使って挙動を解説しています。

- [何が転送対象になるか](docs/file-selection-ja.md) — 名前・安定化・サイズ上限
- [ローカルのディレクトリ同士でコピーする](docs/local-copy-ja.md) — `protocol: local`
- [1ファイルが辿る道](docs/transfer-lifecycle-ja.md) — 一時名・検証・rename・転送後処理・再送
- [ログを1行ずつ読む](docs/log-example-ja.md) — 実際のログをレコードごとに解説
- [画面に出る内容](docs/console-output-ja.md) — `goft test`・`--dry-run`・転送

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

### リリース

ビルド済みのバイナリは [リリースページ](https://github.com/Max0n-1027/goft/releases) で公開しています。`v*` タグごとのバージョン付きリリースと、`main` への push ごとに作られる `main-<日付>-<コミット>` という名前のプレリリースの2種類です。後者はリリースではなくビルドで、サポート対象ではなく、次のコミットで置き換わります。残すのは最新5件だけです。

各アーカイブには実行ファイル・両方の README・ライセンス・設定例が入っており、`SHA256SUMS` で照合できます。

```bash
tar xzf goft_v0.1.0_linux_amd64.tar.gz
sudo install goft_v0.1.0_linux_amd64/goft /usr/local/bin/
sha256sum -c SHA256SUMS --ignore-missing
```

[ワークフロー](.github/workflows/release.yml) は先に `gofmt`・`go vet`・`go test -race` を実行するので、これらが通らないビルドが公開されることはありません。公開しているのは `./package.sh` の出力そのもので、手元でも同じコマンドで再現できます。

```bash
VERSION=v1.0.0 ./package.sh
```
