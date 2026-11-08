# 複数のジョブをサービスとして動かす

[English](service.md)

1つの goft プロセスが設定にあるすべてのジョブを動かし、それぞれが自分のディレクトリを監視しながら、止めるまで動き続けます。ログ転送エージェントがパイプラインを動かすのと同じ形です。同じ設定から、ジョブを名前で指定して1回だけ実行することもできます。

```bash
goft -c /etc/goft/goft.yaml                              # 有効なジョブをすべて、止めるまで
goft -c /etc/goft/goft.yaml --run-once invoice-upload    # 1つのジョブを1回だけ
goft -c /etc/goft/goft.yaml jobs                         # 設定されているジョブ
goft -c /etc/goft/goft.yaml test                         # 有効なジョブをすべて確認
```

## 設定

メインの設定ファイルには3つのセクションがあります。どのジョブでもなくプロセスに属するものを書く `service`、ジョブの一覧である `jobs`、そして別のファイルのジョブを読み込む `job_files` です。

```yaml
# /etc/goft/goft.yaml
service:
  log:
    path: /var/log/goft/goft.log
job_files:
  - jobs.d/*.yaml
jobs:
  - name: invoice-upload
    direction: send
    local:
      path: /data/out/invoice
    remote:
      protocol: sftp
      host: invoice-sftp
      path: /upload/invoice
    post_action: move
    move_to: /data/done/invoice
    log:
      path: /var/log/goft/invoice-upload.log
  - name: report-download
    direction: recv
    local:
      path: /data/in/report
    remote:
      protocol: ftp
      host: report-ftp
      path: /outgoing
    poll_interval: 1m
    log:
      path: /var/log/goft/report-download.log
```

```yaml
# /etc/goft/jobs.d/archive.yaml
jobs:
  - name: invoice-archive
    direction: send
    enabled: false
    local:
      path: /data/done/invoice
    remote:
      protocol: local
      path: /backup/invoice
    log:
      path: /var/log/goft/invoice-archive.log
```

ジョブには、以前のバージョンのジョブファイルに書けた設定（`local`、`remote`、`include`、`verify`、`post_action`、`log` など。[goft.example.yaml](../goft.example.yaml) に説明があります）をすべて書けます。加えて、ジョブ固有の設定が3つあります。

| 設定 | |
|---|---|
| `name` | 必須。すべてのログレコードに記録され、`--run-once`・`goft jobs`・`goft test` でジョブを指す名前になります。大文字小文字だけが違う名前は同じとみなします。 |
| `direction` | 必須。`send`（ローカル → リモート）か `recv`（リモート → ローカル）。 |
| `enabled` | 既定は `true`。`false` にするとサービスはそのジョブを動かしません。名前を指定すれば実行できます。 |

**ジョブファイル**には `jobs:` の一覧だけを書きます。`job_files` にはファイル名とパターンを書き、メインファイルのジョブの後に、書いた順に読み込みます。パターンは何にも一致しなくて構いません（空の `jobs.d` はごく普通の状態です）が、名前をそのまま書いたファイルは存在している必要があります。

**相対パスは、そのパスが書かれているファイルのディレクトリを基準に解決します。** goft を起動したディレクトリではありません。サービスの場合、それは誰も選ばないからです。たとえば `/etc/goft/jobs.d/report.yaml` に書いた `path: in` は `/etc/goft/jobs.d/in` を指します。`~` で始まるパスは書かれたまま残し、それを理解する設定（`private_key`、`known_hosts` など ssh 関連のファイル）が展開します。`${VAR}` はどのファイルでも環境変数から展開されます。

**起動時にはすべてのジョブを単独で、またジョブ同士で検査し、1つでも誤りがあれば goft はまったく起動しません。** ジョブが1つ欠けた状態で起動したサービスは、本来動くべきファイルが動いていないのに正常に見えてしまうためです。ジョブ同士では次のものを拒否します。

- 同じ名前のジョブ（大文字小文字は区別しません）
- 同じディレクトリ（または一方が他方の中にあるディレクトリ）からファイルを取る2つの有効なジョブで、どちらかが送ったファイルを削除・移動するもの。もう一方がファイルを取りこぼすか、途中まで消えたファイルを送ることになります
- ファイルを循環させる有効なジョブの組み合わせ。各ジョブの転送先が別のジョブの転送元になり、ファイルが元の場所に戻ってくるものです。中継（中継用ディレクトリに `recv` し、そこから `send` のジョブが取る）は循環ではないので問題ありません
- 1つのログファイルに、ローテーション設定の異なる2つのジョブが書くこと

サーバーは設定に書かれた名前で識別するので、ssh_config のエイリアスとアドレスのように2つの名前で指したサーバーは別のサーバーとみなします。

## コマンド

| コマンド | |
|---|---|
| `goft` | 有効なジョブをすべて動かし、止めるまで監視し続けます。 |
| `goft --run-once NAME` | ジョブを1回だけ実行して終了します。繰り返し指定すると複数のジョブを順番に実行し、前のジョブが失敗しても次のジョブは実行します。無効なジョブもこの方法なら実行できます。 |
| `goft --dry-run` | 何も転送せずに、転送対象の一覧を出します。対象は `--run-once` で指定したジョブ、指定がなければ有効なジョブすべてです。 |
| `goft jobs` | サービスと同じように設定を検査したうえで、ジョブの一覧を出します。 |
| `goft test [NAME...]` | 設定と各ジョブの接続を、ジョブが実際に動く方向で確認します。名前を指定しなければ有効なジョブすべてです。 |
| `goft service install` / `uninstall` | Windows のみ。goft を Windows サービスとして登録、または削除します。[後述](#windows)。 |

`goft jobs` は、各ジョブの方向、サービスが動かすかどうか、ファイルを探す間隔、どこからどこへ転送するかを表示します。

```
名前             方向  状態  間隔  転送元 -> 転送先
invoice-upload   send  有効  5s    /data/out/invoice -> sftp://invoice-sftp/upload/invoice
report-download  recv  有効  1m0s  ftp://report-ftp/outgoing -> /data/in/report
invoice-archive  send  無効  5s    /data/done/invoice -> /backup/invoice
```

`--run-once` を複数指定した場合の終了コードは、最も悪い結果になります。実行できなかったジョブがあれば 2、ファイルが失敗したジョブがあれば 1、すべて成功したときだけ 0 です。設定にない名前を指定すると、どのジョブも実行する前にコマンドが止まります。

## サービスが書くもの

サービス自身のログ `service.log` にはプロセスのことを記録します。どの設定を読み、どのジョブを起動し、いつ止まったかです。各ジョブが何をしたかは、これまでどおりそのジョブのログに記録されます。

```json
{"time":"2026-11-07T08:33:06.651120375+09:00","level":"INFO","msg":"service starting","event":"service","config":"/etc/goft/goft.yaml","job_files":["/etc/goft/jobs.d/archive.yaml"],"jobs":["invoice-upload","report-download"],"lock_dir":"/tmp/goft-0"}
{"time":"2026-11-07T08:33:07.650239099+09:00","level":"INFO","msg":"service stopping","event":"service"}
{"time":"2026-11-07T08:33:07.650283971+09:00","level":"INFO","msg":"service stopped","event":"service"}
```

起動できないジョブがある場合（別のプロセスがロックを持っている、接続設定を解決できないなど）は、理由とともに `service could not start` として記録され、どのジョブも動きません。サービスのログに `path` を書かなければ標準出力に出力され、systemd がそれをジャーナルに渡します。

ローテーション方法が同じなら、複数のジョブが1つのログファイルを共有できます。どのレコードにも `job` と `direction` が入るので区別できます。

```bash
jq -c 'select(.job == "report-download")' /var/log/goft/goft-jobs.log
```

誰も見ていないので、人間可読なコンソール出力はサービスでは表示しません。`--console` で表示できます。その場合も各ジョブの周期の報告はひとまとまりで書き出すので、2つのジョブの行が混ざることはありません。

サービスは、各ジョブの周期をこれまでどおり1つずつ順番に実行し、ジョブ同士は並行して動かします。遅いサーバーが足を引っ張るのは、そのサーバーのジョブだけです。

**停止する**（Ctrl+C、SIGTERM、`systemctl stop`、Windows のサービス マネージャー）と、どのジョブも新しいファイルには手を付けず、進行中のファイルは最後まで終わらせます。固まっている転送は `remote.io_timeout` で打ち切られるので、停止にかかる時間には上限があります。設定は起動時に一度だけ読み込みます。変更を反映するにはサービスを再起動してください。

## 1つのジョブは同時に1つのプロセスだけ

各ジョブは実行中ロックを持ちます。同じジョブを別の goft で実行しようとすると（たとえばサービスが動かしている最中に `--run-once` する）、拒否されて終了コード 2 で終わります。

```
goft: ジョブ "invoice-upload": 別の goft プロセスが実行中です（pid 4242）
```

ジョブのどれか1つでもロックされていれば、サービスはまったく起動しません。ロックはファイルに対して OS が持つものなので、プロセスが落ちれば解放され、後片付けは不要です。

ジョブは「それを定義しているファイルの中の名前」で区別します。別の設定にある同じ名前のジョブ（後述の `--name` で登録した2つ目のサービスなど）は別のジョブなので、互いを妨げません。一方、2つの設定が同じジョブファイルを読み込んでいれば同じジョブなので、同時に実行できるのはどちらか一方だけです。

ロックは `service.lock_dir` に置きます。既定は次のとおりです。

- Windows では `C:\ProgramData\goft\locks`。どのアカウントでも同じディレクトリなので、LocalSystem で動くサービスと、`--run-once` を実行する管理者が互いを認識できます
- それ以外では `/tmp/goft-<uid>`。そのアカウント専用のディレクトリです。ディレクトリであり、そのアカウントのものであり、他のアカウントに閉じていなければ使いません。他のアカウントが書き込めるディレクトリには、goft に書き込ませるためのリンクを置かれるおそれがあるためです

したがって、同じアカウントで2回目の実行をすることは防げます。サービスを手動で動かすときも、普通は同じアカウントで実行します（`sudo -u goft goft --run-once ...`）。サービスと人とで同じディレクトリが見えない場合（別のアカウント、systemd の `PrivateTmp` など）は、両方が使えるディレクトリを `service.lock_dir` に設定してください。

## Linux: systemd

1つのユニットですべてのジョブを動かします。

```ini
# /etc/systemd/system/goft.service
[Unit]
Description=goft file transfer
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/usr/local/bin/goft -c /etc/goft/goft.yaml
Restart=always
RestartSec=60
# A stop finishes the files under way; give a stalled one until
# remote.io_timeout (5 minutes by default) to be dropped.
TimeoutStopSec=330

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable --now goft
systemctl restart goft      # 設定を変更した後
journalctl -u goft          # サービスのログに path がない場合
```

## Windows

goft はネイティブの Windows サービスとして動きます。管理者のプロンプトから次を実行します。

```
goft service install -c C:\ProgramData\goft\goft.yaml
sc.exe start goft
```

`install` は `goft` という名前のサービスを次のように登録します。

- この `goft.exe` を、指定した設定（絶対パスにしたもの）で実行します。`-c` がなければ goft が見つける設定を使います。Windows ではその候補に `C:\ProgramData\goft\goft.yaml` が含まれます
- LocalSystem で実行します
- Windows の起動時に開始します
- エラーで止まった場合は 1 分後に再起動します。設定を読み込めなかった場合も含みます。1日失敗がなければ回数はリセットされます

**ログはすべてファイルに出す必要があります。** サービスにはコンソールがなく、標準出力に残したログは何の知らせもなく失われるため、`service.log.path` と、有効なすべてのジョブの `log.path` が必須です。`install` は登録する前に、これを含めて設定全体を検査します。サービスも開始するたびに改めて検査します。

**開始と停止はイベント ログに記録されます。** Application ログに、サービス名をソースとして書きます。すべてのジョブが開始したときは ID 1（ジョブ名を含む）、停止したときは ID 2、開始できなかったときは理由とともに ID 3 です。`sc.exe start` は成功したのにサービスがまた止まってしまう場合は、まずここを確認してください。

```
sc.exe stop goft
goft service uninstall          # 実行中なら先に止めます
```

`--name` を指定すると別の名前でサービスを登録・削除できます。同じマシンで2つ目の設定を動かす場合に使います。

```
goft service install --name goft-test -c C:\ProgramData\goft\test.yaml
```

**LocalSystem には専用のホーム**（`C:\Windows\System32\config\systemprofile`）があるので、そこから見える `~\.ssh`、`%USERPROFILE%\.netrc`、資格情報マネージャーのエントリは管理者のものではありません。鍵や known_hosts のファイルは絶対パスで指定するか、サービスを別のアカウントで動かしてください。`services.msc` の［ログオン］タブ、または `sc.exe config goft obj= .\svc-transfer password= ...` で変更できます。エージェントについては [ssh-agent で認証する](ssh-agent-ja.md#サービスとして動かすジョブ) を参照してください。

## 1ファイル1ジョブの形式からの移行

以前のバージョンは1つのプロセスで1つのジョブを動かし、転送の方向はコマンドで決めていました。その形式のファイルは、どうすればよいかを示すメッセージとともに拒否されます。設定を `jobs:` の下に、`name` と `direction` を持つ1つの項目として移してください。

```yaml
# 変更前: /etc/goft/invoice-upload.yaml
name: invoice-upload
local:
  path: /data/out/invoice
remote:
  protocol: sftp
  host: invoice-sftp
  path: /upload/invoice
```

```yaml
# 変更後: /etc/goft/jobs.d/invoice-upload.yaml
jobs:
  - name: invoice-upload
    direction: send
    local:
      path: /data/out/invoice
    remote:
      protocol: sftp
      host: invoice-sftp
      path: /upload/invoice
```

古いファイルはそれぞれ、このようにジョブファイルにできます。メインのファイルにはサービスの設定だけを書き、`job_files` でそれらを読み込みます。

| 以前 | 現在 |
|---|---|
| `goft send -c job.yaml` | `goft --run-once job` |
| `goft recv -c job.yaml` | `goft --run-once job`（ジョブに `direction: recv` を書く） |
| `goft serve send -c job.yaml` | `goft`（有効なジョブをすべて動かす） |
| `goft test -c job.yaml` | `goft test job`（ジョブが動く方向を確認する） |
| `--log-file` | ジョブの `log.path`、または `service.log.path` |
| ジョブごとの `goft@.service` ユニット | 1つの `goft.service` |
| 作業ディレクトリを基準にした相対パス | ファイルのディレクトリを基準にした相対パス |
