# 設定を JSON Schema で検証する

[English](schema.md)

goft は設定ファイルの形を [JSON Schema](https://json-schema.org/)（draft-07）で定めています。goft は何よりも先にすべての設定ファイルをこのスキーマで検証します。JSON Schema に対応したエディタなら、同じファイルを同じスキーマで、書いている最中に検証できます。設定名の書き間違い、型の違う値、存在しない選択肢は、サービスを再起動するずっと前に、書いたその場所で指摘されます。

## スキーマの入手

スキーマは次の3か所にあり、どれも同じファイルです。

- `goft schema` は、実行している goft に組み込まれたスキーマを出力します。そのため、常にその goft と一致します。

  ```bash
  goft schema > /etc/goft/goft.schema.json
  ```

- リリースのアーカイブには、`goft.example.yaml` と並んで `schema/goft.schema.json` が入っています。
- リポジトリでは [schema/goft.schema.json](../schema/goft.schema.json) です。エディタから GitHub 上のファイルを直接読むこともできます。エディタと goft の判定がずれないよう、実行しているバージョン（`goft version`）のタグを使ってください。

  ```
  https://raw.githubusercontent.com/Max0n-1027/goft/vX.Y.Z/schema/goft.schema.json
  ```

## エディタで使う

メインのファイルにも、`job_files` で読み込むジョブファイルにも、同じスキーマを使います。ジョブファイルは、`jobs:` の一覧だけを書いた設定ファイルだからです。

**VS Code**（Red Hat の YAML 拡張機能）や、yaml-language-server を使うほかのエディタ（Neovim、Helix、Zed など）では、ファイルの1行目にコメントを書くと、そのファイルのスキーマを指定できます。相対パスはそのファイルからの相対です。

```yaml
# yaml-language-server: $schema=goft.schema.json
service:
  log:
    path: /var/log/goft/goft.log
```

すべてのファイルにまとめて指定するには、`settings.json` でスキーマとファイルを対応付けます。

```json
"yaml.schemas": {
  "/etc/goft/goft.schema.json": ["goft.yaml", "jobs.d/*.yaml"]
}
```

**JetBrains の IDE** では、*Settings | Languages & Frameworks | Schemas and DTDs | JSON Schema Mappings* でスキーマファイルを追加し、JSON Schema のバージョンに 7 を選び、対象のファイルやパターンを追加します。

これでエディタが設定名や、`verify`・`post_action` などの選択肢の値を補完し、各設定の説明と既定値を表示し、goft が受け付けない箇所に印を付けるようになります。

## goft がいつ何を検証するか

設定を読むコマンド（`goft` そのもの、`--run-once`、`--dry-run`、`goft jobs`、`goft test`、Windows サービス）はすべて、最初にメインのファイル、次に各ジョブファイルをスキーマで検証します。スキーマに合わないファイルがあると、そのファイルの問題をすべて行番号付きで報告し、何も始めずに終了コード 2 で終了します。設定を編集した後に確かめるには `goft jobs` が手軽です。

次のファイルに対して、

```yaml
# goft.yaml（誤りが5つあります）
jobs:
  - name: invoice-upload
    direction: send
    local:
      path: /data/out/invoice
    remote:
      protocol: sftp
      host: invoice-sftp
      path: /upload/invoice
      port: "22"
    include: "*.csv"
    poll_interval: 5
    post_acton: move
    move_to: /data/done/invoice
    log:
      level: INFO
```

goft は次のように報告します。

```console
$ goft -c /etc/goft/goft.yaml jobs
goft: 設定 /etc/goft/goft.yaml に誤りがあります:
11 行目: ジョブ invoice-upload: remote.port は整数で書いてください（文字列になっています）
12 行目: ジョブ invoice-upload: include はリストで書いてください（文字列になっています）
13 行目: ジョブ invoice-upload: poll_interval は 30s、5m、1h30m のような時間で書いてください（5 になっています）
14 行目: ジョブ invoice-upload: post_acton という設定はありません（post_action の書き間違いではありませんか？）
17 行目: ジョブ invoice-upload: log.level "INFO" は無効です (debug | info | warn | warning | error)
```

スキーマが決めているのは次のことです。

- どのセクションにどの設定があるか。スキーマにない設定（書き間違いや、大文字で書いたもの）は受け付けません。近い名前の設定があれば、それを示します。
- 各値の型。文字列、整数、数値、`true` か `false`、リスト、セクションのいずれかです。
- 選択肢として許される値と、数値の範囲。
- 時間の書き方。`ns`・`us`・`ms`・`s`・`m`・`h` の単位を付けた数で、`30s`、`5m`、`1h30m` のように書きます。`0` だけは単位が要りません。
- 必須の設定。`name`、`direction`、`local.path`、`remote.protocol`、`remote.path` は常に必須です。ftp・sftp・smb では `remote.host`、smb では `remote.share`、`post_action: move` では `move_to` も必須です。
- ローカルのディレクトリ同士のコピーで使わない設定。`host`、`port`、`user`、`password`、`share` です。

スキーマでは分からないことは、すべてのファイルがスキーマの検証を通った後に、これまでどおり全ジョブまとめて検査します。

- `${NAME}` で書いた値。置き換えた後の値を検査します。
- `local.path` が存在してディレクトリであるか、2つのパスが互いの中にないか。
- ジョブ同士の関係。名前の重複、2つのジョブが同じディレクトリから取ること、ファイルの循環、1つのログファイルを2通りにローテーションすることです。
- 値の組み合わせ。`recv` のジョブでの `post_action: move`、`delete` か `move` のない `remove_empty_dirs` などです。

## スキーマに合う値の書き方

- **設定名は小文字で書きます。** [goft.example.yaml](../goft.example.yaml) のとおりです。
- **数字や `true` のように見える文字列は引用符で囲みます。** YAML は `password: 1234` を数値として、`user: true` を真偽値として読みますが、goft がそこで受け取るのは文字列です。`password: "1234"` と書いてください。
- **リストは、要素が1つでもリストで書きます。** `include: "*.csv"` ではなく `include: ["*.csv"]` です。
- **時間には単位を付けます。** `poll_interval: 5` ではなく `poll_interval: 5s` です。
- **`${NAME}` はどの設定の値の代わりにも書けます。** 数値、`true`、リストの代わりにも書けます。`port: ${SFTP_PORT}` はそのまま受け付け、変数を置き換えた後の値を検査します。
- **セクションは空にできますが、設定は空にできません。** `service:`、`log:`、`retry:` の下をすべてコメントアウトしても構いません。一方、何も書かない `port:` は、書かなかったものとしては扱わず、誤りとして報告します。

## v0.3 からの移行

v0.3 までの goft は、設定を寛容に読んでいました。今は受け付けない書き方を、以前は次のように扱っていました。

| 書き方 | goft v0.3 | 現在 |
|---|---|---|
| `post_acton: delete` のような、goft にない設定 | 何も言わずに無視したため、何も削除されなかった | 受け付けない |
| `Name:`、`Remote:` など大文字の設定名 | 小文字の設定として読んだ | 受け付けない |
| `poll_interval: 5` など単位のない時間 | 5 ナノ秒として読んだ | 受け付けない |
| `include: "*.csv,*.txt"`（`exclude`、`log.fields` も同じ） | カンマで区切ってリストにした | 受け付けない。リストで書く |
| `workers: "4"`、`enabled: "true"` | 数値や真偽値に変換した | 受け付けない。引用符を外す |
| `password: 1234`、`user: true` | 文字列に変換した（`true` は `1`） | 受け付けない。引用符で囲む |
| `log.level: INFO`、`log.fields: [SRC]` | 小文字として読んだ | 受け付けない。小文字で書く |
| `port:` など値を書いていない設定 | 書かなかったものとして読んだ | 受け付けない。行を消すか値を書く |

サービスを新しい goft で再起動する前に、新しい goft で設定を確かめてください。

```bash
goft -c /etc/goft/goft.yaml jobs
```
