# goft documentation

- [What gets transferred](file-selection.md) — which files a cycle picks up:
  names, settling, the size cap.
- [The life of one file](transfer-lifecycle.md) — the temporary name,
  verification, the rename into place, post-transfer actions and retries.
- [A log, line by line](log-example.md) — a real log, explained record by
  record, at info and at debug level.
- [Copying between two local directories](local-copy.md) — `protocol: local`,
  what it checks before it runs, and when it beats `cp`.
- [What a run looks like on screen](console-output.md) — `goft test`,
  `--dry-run`, a transfer, the language it is shown in, and how the two output
  streams are kept apart.
- [Authenticating with ssh-agent](ssh-agent.md) — where goft finds the agent,
  which keys it offers and in what order, and jobs run as a service.

The examples on these pages are captured from real runs by the tests in
[cmd/logsample_test.go](https://github.com/Max0n-1027/goft/blob/main/cmd/logsample_test.go), which start a real sftp
server for them to talk to; only paths, the host, the port and the user name are
rewritten. The one exception is the run stopped part way in
[What a run looks like on screen](console-output.md#stopping-part-way), which
was stopped by hand.

Elsewhere: [README.md](../README.md) for installing and configuring goft,
[goft.example.yaml](../goft.example.yaml) for every setting with its default,
and `go doc goft` for the command reference.

日本語版は [README-ja.md](README-ja.md) にあります。
