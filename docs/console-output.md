# What a run looks like on screen

goft writes two things: a JSON Lines log, which is the record, and a human
readable account on stdout for whoever is watching. This page is the second one.
Every block below is captured from a real run of the test in
[cmd/logsample_test.go](../cmd/logsample_test.go); only paths, the host and the
port have been rewritten.

Single runs (`send`, `recv`) print it by default. `serve` does not, since a
watcher is usually left alone with its log. `--console` and `--no-console`
override either way.

## Before anything moves: `goft test`

`goft test` validates the configuration, shows how each connection setting was
resolved, connects, and checks both directions:

```console
config        /etc/goft/invoice-upload.yaml                      OK
local         /data/out/invoice                                  OK  readable and writable
resolve       host = invoice-sftp                                (yaml)
resolve       port = 22                                          (yaml)
resolve       user = uploader                                    (yaml)
resolve       password = REDACTED                                (yaml)
resolve       private_key = /home/svc-transfer/.ssh/id_ed25519   (default)
resolve       known_hosts = /home/svc-transfer/.ssh/known_hosts  (yaml)
connect       sftp://invoice-sftp:22/upload/invoice              OK
recv (list)   0 entries                                          OK
send (write)  writable                                           OK
```

The `resolve` lines are the useful part when a job connects to the wrong place:
each names the value that will be used and where it came from — `yaml`,
`ssh_config`, `netrc`, `credential_manager`, `env` or `default`. Secrets are
reported as set, never printed. `env` is the ssh-agent found through
`SSH_AUTH_SOCK`; [Authenticating with ssh-agent](ssh-agent.md) covers that line.

Both directions are checked and reported separately, because a read-only account
that can only `recv` is a legitimate setup: the exit status is 2 only when
neither direction works. A destination directory that does not exist yet is not
an error either — it is reported as one that will be created on the first
transfer, and the write is probed on its parent, so running `goft test` leaves
nothing behind on the server.

## What would be sent: `--dry-run`

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (dry-run)
  2026-08/invoice_202608_03.csv                  17 B
  archive.dat                                 2.0 MiB
  invoice_202608_01.csv                     830.1 KiB
  invoice_202608_02.csv                     625.0 KiB
4 files, 3.4 MiB would be transferred (nothing was sent)
```

A dry run reads the sending side and stops. It never touches the destination —
not even to ask whether a file is already there — so it cannot say `ok` or
`skipped`, and it does not pretend to: it lists what it found and what that
comes to. Console output is always on for a dry run, since inspecting the
result is the whole point.

## A transfer

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (4 files, 3.4 MiB)
[2/4] archive.dat                                 2.0 MiB  skipped     0.0s  size_limit
[1/4] 2026-08/invoice_202608_03.csv                  17 B  ok          0.0s
[3/4] invoice_202608_01.csv                     830.1 KiB  ok          0.1s
[4/4] invoice_202608_02.csv                     625.0 KiB  ok          0.1s
4 files: 3 transferred (1.4 MiB), 1 skipped, 0 failed  in 3.1s
```

The header names the job, the two ends and the size of the work; the arrow is
`->` for `send` and `<-` for `recv`. Then one line per file, and a summary.

The counter (`[2/4]`) is the position in the list, not the order of completion:
with `workers: 2` the lines appear as each worker finishes. `archive.dat` was
over `max_file_size_mb`, which is a skip with its reason, not a failure.

Run it again with everything already delivered:

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (4 files, 3.4 MiB)
[1/4] 2026-08/invoice_202608_03.csv                  17 B  skipped     0.0s  already_exists
[3/4] invoice_202608_01.csv                     830.1 KiB  skipped     0.0s  already_exists
[4/4] invoice_202608_02.csv                     625.0 KiB  skipped     0.0s  already_exists
[2/4] archive.dat                                 2.0 MiB  skipped     0.0s  size_limit
4 files: 0 transferred (0 B), 4 skipped, 0 failed  in 3.0s
```

Nothing was transferred, and each line says why. This is what a healthy
`on_exists: skip` job looks like when there is nothing new to do — which is
also why `serve` keeps quiet about such cycles rather than printing this every
few seconds.

## Stopping part way

Ctrl+C lets the file under way finish and starts no other. Here it came less
than a second into the first of three 64 MiB files:

```console
goft statement-upload  /data/out/statement -> sftp://invoice-sftp/upload/statement  (3 files, 192.0 MiB)
[1/3] part-1.dat                                 64.0 MiB  ok          6.4s
3 files: 1 transferred (64.0 MiB), 0 skipped, 0 failed, 2 not started  interrupted after 6.4s
goft: interrupted before the run was complete
```

The file that was being sent arrived whole; the other two were left for the
next run, and the exit status is 2 because this one did not finish.

## `serve`

With `--console`, `serve` reports a cycle once it is over, and only when
something actually happened: a cycle where every file was skipped prints
nothing. A cycle that could not run at all — an unreachable server — is always
reported, so silence means "nothing to do" and never "something is wrong".

## Colour, pipes and the two streams

`ok`, `skipped` and `failed` are coloured only when stdout is a terminal. Piped
or redirected, the output is plain text with no escape sequences in it.

When no `log.path` is configured and console output is on, the JSON log goes to
stderr, so the two never mix:

```bash
goft send 2>/dev/null    # just the readable output
goft send >/dev/null     # just the JSON log
```

Both streams are UTF-8: the log because JSON is defined that way, the console
because a file name is printed as it is stored. A Windows console left on its
regional code page will garble anything outside ASCII — `chcp 65001`, Windows
Terminal or PowerShell 7 shows it properly. The bytes written are the same
either way, so a redirected log is unaffected.

## Verbosity

There is no console-specific setting: `log.level` governs both outputs at once.

| level | console |
|---|---|
| `error` | failures and the summary |
| `warn` | + notes such as a size cap skip |
| `info` (default) | + one line per file |
| `debug` | + the destination digest on each line when `verify: hash`, and the rate for files that were sent |

## See also

- [A log, line by line](log-example.md) — the same run as it appears in the log
- [What gets transferred](file-selection.md) — which files are picked up
- [The life of one file](transfer-lifecycle.md) — what happens between the two ends
