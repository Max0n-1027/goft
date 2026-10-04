# ssh-agent で認証する

sftp は `ssh` と同じように ssh-agent から鍵を受け取ります。`ssh-add` でエージェントに
読み込んだ鍵は、パスフレーズをジョブファイルにも環境変数にも、goft から見える場所の
どこにも置かずにサインインに使えます。署名はエージェントが行い、秘密鍵がエージェントの
外に出ることはありません。

設定は何も要りません。エージェントが動いていて、サーバーが受け付ける鍵を持っていれば、
`password` も `private_key` もないジョブで接続できます。

```yaml
remote:
  protocol: sftp
  host: invoice-sftp
  user: uploader
  path: /upload/invoice
```

`goft test` は、見つけたエージェントと、それをどこで見つけたか、何本の鍵を持っているかを
表示します。

```console
config        /etc/goft/invoice-upload.yaml                             OK
local         /data/out/invoice                                         OK  readable and writable
resolve       host = invoice-sftp                                       (yaml)
resolve       port = 22                                                 (yaml)
resolve       user = uploader                                           (yaml)
resolve       ssh_agent = /run/user/1000/ssh-agent.sock (holds 2 keys)  (env)
resolve       known_hosts = /home/svc-transfer/.ssh/known_hosts         (yaml)
connect       sftp://invoice-sftp:22/upload/invoice                     OK
recv (list)   0 entries                                                 OK
send (write)  writable                                                  OK
```

同じ内容は debug レベルのログにも、`field` が `ssh_agent` の
`resolved connection setting` レコードとして残ります。

## エージェントを探す順番

上から順に見て、最初に何かを指定しているものに従います。

| 順 | 設定 | `goft test` に出る取得元 |
|---|---|---|
| 1 | `use_ssh_agent: false` でエージェントを使わない | `yaml` |
| 2 | ジョブの `ssh_agent` でソケット（Windows では名前付きパイプ）を指定する | `yaml` |
| 3 | ssh_config の `IdentityAgent` | `ssh_config` |
| 4 | 環境変数 `SSH_AUTH_SOCK` | `env` |
| 5 | Windows のみ、`\\.\pipe\openssh-ssh-agent` | `default` |

`IdentityAgent` は OpenSSH と同じように解釈します。パス（`~`・`%h`・`%u`・`%d` を展開）、
その変数を意味する `SSH_AUTH_SOCK` という語、変数 `NAME` を意味する `$NAME`、
そのホストではエージェントを使わない `none` のいずれかです。ジョブの `ssh_agent` も
`~` と同じトークンを展開します。

最後の項目は、Windows に付属する OpenSSH Authentication Agent サービスのパイプで、
`ssh.exe` が探す場所でもあります。このサービスは誰かが起動するまで無効になっているので、
そこに何もないのは普通のことであり、goft は何も言いません。一方、ジョブ・ssh_config・
`SSH_AUTH_SOCK` のいずれかが指しているのに接続できないエージェントは話が別です。起動時の
ログと `goft test` に警告として出し、ジョブはエージェントなしで続けます。

```console
warning       the ssh-agent at /run/user/1000/ssh-agent.sock cannot be used, so none of its keys are offered: dial unix /run/user/1000/ssh-agent.sock: connect: no such file or directory
```

エージェントへの問い合わせは接続の確立の一部なので、`connect_timeout` の対象です。
問い合わせを受け付けたまま答えないエージェントは、その時間が尽きた時点で見限ります。
時間を使い切っているのでその接続の試行も失敗となり、他の失敗と同じように再試行します。

Unix ソケットか Windows の名前付きパイプで ssh-agent プロトコルを話すエージェントであれば、
どれでも使えます。たとえば Pageant は、昔からのウィンドウメッセージによる方式に加えて
名前付きパイプでも待ち受けており、`pageant --openssh-config <ファイル>` でそのパイプを指す
`IdentityAgent` 行を書き出せます。

### Windows での扱い

パイプは `ssh.exe` が自身のパイプを開くのと同じ方法で開きます。単に到達できること
以上に、これには2つの意味があります。

**エージェントにジョブの実行アカウントとして振る舞うことを許しません。** 名前付きパイプの
サーバー側は通常、接続してきたクライアントになりすませます。そして
`\\.\pipe\openssh-ssh-agent` という名前は予約されていません。OpenSSH Authentication
Agent サービスが動いていないマシン（出荷時の状態）では、**どのプロセスでも先にその
パイプを作ってエージェントとして扱われ得ます**。goft は識別レベルで接続を要求するため、
向こう側のサーバーは「どのアカウントが尋ねているか」を確認できるだけで、それ以上は
できません。トークンを使って何かを開くことはできません。名前を先取りしたプロセスには
どの鍵が求められているかとどのホストへの署名かは分かりますが、鍵なしに署名はできない
ままなので、残るのは実害のない部分だけです。

**全インスタンスが使用中のパイプは空くまで待ちます。** 名前付きパイプは1インスタンスに
つき1クライアントを扱い、エージェントは直前のインスタンスが取られてから次を作ります。
そのため、その隙間に来たクライアントは待たされるのではなく「全インスタンスが使用中」と
返されます。goft は解決時に1回、ワーカーごとに1回エージェントを開くので、まさにこの隙間に
当たる集中が起きます。一瞬の競合で1周期を失うのは割に合いません。待ち時間は他の処理と
同じく `connect_timeout` で上限が決まるため、インスタンスを永遠に空けないエージェントでも
失うのは1回の接続試行だけです。

Git Bash・MSYS・Cygwin のシェルが設定した `SSH_AUTH_SOCK` は、goft でも `ssh.exe` でも
使えません。これらのエージェントは通常のファイルの中でエミュレートしたソケットで待ち受けており、
接続方法を知っているのはそれら自身のライブラリだけです。Windows はこれを「ソケットがそもそも
存在しない」場合と同じ文言で報告するため、goft は使えるエージェントの居場所を補って示します。

```console
warning  the ssh-agent at C:/…/ssh-AbC123/agent.4711 cannot be used, so none of its keys are offered: dial unix C:/…/agent.4711: connect: A socket operation encountered a dead network. (the agent of an MSYS or Cygwin shell listens on a socket Windows cannot connect to; the OpenSSH agent for Windows listens on \\.\pipe\openssh-ssh-agent)
```

`SSH_AUTH_SOCK` を消すか、`ssh_agent` でパイプを指定すれば、OpenSSH のエージェントが
使われます。

## どの鍵をどの順番で出すか

鍵は OpenSSH と同じ順番で出し、パスワードはすべての鍵の後に試します。

1. ジョブが使う鍵ファイルのうち、エージェントが持っているもの（エージェント側のものを使う）。
   鍵ファイルとは `private_key`、または ssh_config の `IdentityFile`、それもなければ
   `~/.ssh/id_ed25519` と `~/.ssh/id_rsa` です
2. エージェントのその他の鍵
3. エージェントが持っていない鍵ファイル

鍵ファイルの鍵をエージェントから使うことで、パスフレーズつきの鍵をパスフレーズなしで
使えるようになります。両者は公開鍵で突き合わせます。OpenSSH 形式の鍵は公開鍵を暗号化せずに
持っており、古い PEM 形式の鍵は隣にある `.pub` ファイルから読みます。

### エージェントの鍵を指定したものだけに絞る

サーバーは提示された鍵をすべて試行回数に数え（OpenSSH の `MaxAuthTries` は既定で 6）、
上限に達すると `Too many authentication failures` で打ち切ります。エージェントが多くの鍵を
持っていると、正しい鍵やパスワードを試す前に上限に達することがあります。次の2つの設定で、
ジョブが指定していない鍵をエージェントが出さないようにできます。

- **ジョブの `private_key`**。鍵を指定したジョブは使う鍵を決めているので、その鍵だけを
  出します。エージェントがその鍵を持っていればエージェントが署名し、パスフレーズは要りません。
  持っていなければファイルを使い、ロックを解除できなければエラーです。
- **ssh_config の `IdentitiesOnly yes`**。`ssh` と同じく、`IdentityFile` に一致する鍵だけを
  エージェントから出します。

エージェントが持っている鍵より出す鍵が少ないときは、`goft test` がそう表示します。

```console
resolve       private_key = /home/svc-transfer/.ssh/id_invoice                             (yaml)
resolve       ssh_agent = /run/user/1000/ssh-agent.sock (offers 1 of the 2 keys it holds)  (env)
```

エージェントをまったく使わないときは、`use_ssh_agent: false` か、ssh_config で
`IdentityAgent none` を指定します。

### goft が自分で見つける鍵

`IdentityFile` や既定の場所にある、パスフレーズつきの鍵は、エージェントが持っていれば
使い、持っていなければその旨の警告を出して読み飛ばします。個人の鍵にパスフレーズが
かかっていても、パスワードで認証するジョブの邪魔にはなりません。

```
skipping the key /home/uploader/.ssh/id_ed25519: it is passphrase protected, private_key_passphrase is not set, and the ssh-agent does not hold it
```

## エージェントの準備

Linux や macOS のデスクトップセッションでは、たいていエージェントがすでに動いています。
動いていなければ次のようにします。

```bash
eval "$(ssh-agent)"
ssh-add ~/.ssh/id_ed25519
```

Windows では、管理者の PowerShell で OpenSSH Authentication Agent サービスを一度起動し、
goft を実行するアカウントで鍵を追加します。

```powershell
Get-Service ssh-agent | Set-Service -StartupType Automatic
Start-Service ssh-agent
ssh-add $env:USERPROFILE\.ssh\id_ed25519
```

Windows のエージェントは鍵をアカウントごとに保持します。サービスアカウントで動くジョブに
見えるのはそのアカウントが追加した鍵だけで、インストールした人の鍵は見えません。

### サービスとして動かすジョブ

systemd・cron・Windows のサービスマネージャーから起動したジョブは、ログインセッションの
`SSH_AUTH_SOCK` を引き継がないので、Linux では場所を教えない限りエージェントを見つけません。
`ssh_agent` でソケットを指定するか（エージェントは goft と同じユーザーで動き、ログイン
セッションが終わっても残っている必要があります）、`private_key` でジョブ専用の鍵ファイルを
渡し、パスフレーズを `private_key_passphrase: ${VAR}` で与えてください。

## 関連

- [README の「資格情報」](../README-ja.md#資格情報) — ssh_config・netrc・資格情報マネージャーから
  補うその他の値
- [画面に出る内容](console-output-ja.md) — `goft test` のその他の項目
