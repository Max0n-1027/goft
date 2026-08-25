# goft ドキュメント

- [何が転送対象になるか](file-selection-ja.md) — 名前・安定化・サイズ上限で、周期がどのファイルを拾うか
- [1ファイルが辿る道](transfer-lifecycle-ja.md) — 一時名・検証・本名への rename・転送後処理・再送
- [ログを1行ずつ読む](log-example-ja.md) — 実際のログを、info と debug の両方でレコードごとに解説
- [画面に出る内容](console-output-ja.md) — `goft test`・`--dry-run`・転送、そして2つの出力の分け方

これらのページの例はすべて、[cmd/logsample_test.go](../cmd/logsample_test.go) のテストが
実サーバー（sftp）に対して実行した結果を採取したものです。書き換えているのはパス・ホスト名・ポートだけです。

そのほか、導入と設定は [README-ja.md](../README-ja.md)、設定項目と既定値の一覧は
[goft.example.yaml](../goft.example.yaml)、コマンドの詳細は `go doc goft` にあります。

The English version is at [README.md](README.md).
