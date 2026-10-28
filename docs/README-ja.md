# goft ドキュメント

- [何が転送対象になるか](file-selection-ja.md) — 名前・安定化・サイズ上限で、周期がどのファイルを拾うか
- [1ファイルが辿る道](transfer-lifecycle-ja.md) — 一時名・検証・本名への rename・転送後処理・再送
- [ログを1行ずつ読む](log-example-ja.md) — 実際のログを、info と debug の両方でレコードごとに解説
- [ローカルのディレクトリ同士でコピーする](local-copy-ja.md) — `protocol: local`、起動前の検証、`cp` との使い分け
- [画面に出る内容](console-output-ja.md) — `goft test`・`--dry-run`・転送、表示する言語、そして2つの出力の分け方
- [ssh-agent で認証する](ssh-agent-ja.md) — エージェントの探し方、出す鍵とその順番、サービスとして動かすジョブ

これらのページの例は、[cmd/logsample_test.go](https://github.com/Max0n-1027/goft/blob/main/cmd/logsample_test.go) のテストが実際に実行した結果を採取したものです。
テストは本物の sftp サーバーを起動して相手にします。書き換えているのはパス・ホスト名・ポート・ユーザー名だけです。
例外は [画面に出る内容](console-output-ja.md#途中で止めた場合) の途中で止めた実行で、これは手で止めたものです。
画面出力の例は日本語で表示したときのもので、ログの例は英語です（ログは表示言語にかかわらず英語で記録します）。

そのほか、導入と設定は [README-ja.md](../README-ja.md)、設定項目と既定値の一覧は
[goft.example.yaml](../goft.example.yaml)、コマンドの詳細は `go doc goft` にあります。

The English version is at [README.md](README.md).
