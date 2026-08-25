# goft documentation

- [What gets transferred](file-selection.md) — which files a cycle picks up:
  names, settling, the size cap.
- [The life of one file](transfer-lifecycle.md) — the temporary name,
  verification, the rename into place, post-transfer actions and retries.
- [A log, line by line](log-example.md) — a real log, explained record by
  record, at info and at debug level.
- [What a run looks like on screen](console-output.md) — `goft test`,
  `--dry-run`, a transfer, and how the two output streams are kept apart.

Every example on these pages is captured from a real run against a live sftp
server, by the test in [cmd/logsample_test.go](../cmd/logsample_test.go). Only
paths, the host and the port are rewritten.

Elsewhere: [README.md](../README.md) for installing and configuring goft,
[goft.example.yaml](../goft.example.yaml) for every setting with its default,
and `go doc goft` for the command reference.

日本語版は [README-ja.md](README-ja.md) にあります。
