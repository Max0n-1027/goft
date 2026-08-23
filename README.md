# goft

[日本語](README-ja.md)

Watches a directory and moves files over FTP, SFTP or SMB, verifying every file
it transfers.

```bash
goft send -c invoice-upload.yaml        # local -> remote, once
goft recv -c report-download.yaml       # remote -> local, once
goft serve send -c invoice-upload.yaml  # local -> remote, continuously
goft test -c invoice-upload.yaml        # check the configuration and connection
```

Add `--dry-run` to `send`, `recv` or `serve` to list what would be transferred
without touching the destination.

## What it does

For every file it finds on the sending side, goft:

1. waits until the file has stopped changing,
2. writes it to the receiving side under a temporary name,
3. verifies it, by default by reading it back and comparing xxHash digests,
4. renames it onto its final name only once verification passed,
5. applies the configured post-transfer action to the source.

A file only ever appears under its real name on the receiving side once it is
complete and verified. If the process is killed mid-transfer, all that is left
behind is a `.goft.tmp` file, which the next run overwrites.

## Configuration

One file describes one job, and one job runs in one process. See
[goft.example.yaml](goft.example.yaml) for the annotated version.

```yaml
name: invoice-upload
local:
  path: /data/out/invoice
remote:
  protocol: sftp
  host: invoice-sftp        # a ~/.ssh/config Host alias works here
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

The direction is chosen by the command, not by the file. That means the same
file could be used for both `send` and `recv`; keep one file per purpose,
especially with `post_action: delete`, or a pair of jobs will pass the same
files back and forth.

### Credentials

Nothing needs to be written in plain text:

- `${VAR}` anywhere in the file is replaced from the environment.
- **sftp** fills in anything you leave out from `~/.ssh/config`, including
  `HostName`, `Port`, `User`, `IdentityFile` and `UserKnownHostsFile`.
- **ftp** fills in `user` and `password` from `~/.netrc`, or
  `%USERPROFILE%\.netrc` on Windows.
- **smb** reads no default file; give it `user`, `password` and `share`.

What the configuration file states always wins over a default file. Run
`goft test` to see the values that were resolved and where each one came from.

Two ssh_config features are not supported, and are reported as warnings rather
than applied silently: `Match` blocks, and `ProxyJump`/`ProxyCommand`. Put the
settings you need directly in the job file instead. ssh-agent is not used;
supply the key with `private_key`, and its passphrase with
`private_key_passphrase` if it has one.

### Running several jobs

goft does not manage multiple jobs; run one process per configuration file. On
Linux a systemd template unit does this neatly:

```ini
# /etc/systemd/system/goft@.service
[Service]
ExecStart=/usr/local/bin/goft serve send -c /etc/goft/%i.yaml
Restart=always
```

```bash
systemctl enable --now goft@invoice-upload
```

Every log record carries `job` and `direction`, so the output of several
processes can be collected in one place and still be told apart.

## Behaviour worth knowing

**Detection is not instant.** `serve` runs one cycle at a time and sleeps
between them, so the shortest delay before a file is picked up is
`max(poll_interval, stable_duration)`. Running cycles serially is what makes it
impossible for a file to be picked up twice while it is still being sent.

**A file is only transferred once it stops changing.** goft compares size and
modification time across cycles rather than trusting the timestamp alone,
because tools that preserve timestamps (`cp -p`, `rsync --times`) leave the
modification time at the source file's old value while the copy is still
running.

**FTP timestamps are coarse.** FTP has no stat command; the modification time
comes from `MLST` where the server supports it and from a directory listing
otherwise, which can be accurate only to the minute. Allow for that in
`stable_duration` when downloading from an FTP server.

**`on_exists: skip` leaves the source in place.** Skipping means no transfer
happened, so `post_action` does not run either, and the file will be examined
again on every cycle. Use `post_action` (or `on_exists: overwrite`) if the
sending directory should not keep growing.

**Transferred files are created mode 0644**, subject to the process umask, so
that whatever consumes them next can read them. Windows ignores the mode.

**A watching job that keeps failing goes quiet rather than loud.** When a whole
cycle cannot run — an unreachable server, say — the pause before the next one
doubles, up to five minutes, and returns to `poll_interval` as soon as a cycle
succeeds. A job polling every second against a server that has gone for good
would otherwise write tens of thousands of identical errors a day.

**A failed file is retried, but only when that could help.** Within a cycle a
file is attempted up to `retry.max_attempts` times (3 by default). A dropped
connection or a failed verification is retried; a missing file, a permission
error or a 5xx reply from the server is not, because the next attempt would
fail the same way. The connection is rebuilt before each retry, since retrying
over one that has just died would fail identically. `retry.max_attempts: 1`
turns it off.

Post-processing is the exception: if the file arrived but `post_action` failed,
only the post-processing is tried again. Re-sending a file that is already
there would achieve nothing.

**`on_exists: overwrite` avoids pointless transfers.** Before overwriting,
goft compares the existing file using the configured `verify` method and skips
the transfer when the two already match. With `verify: none` there is nothing
to compare, so the file is always sent again.

## Status

All three protocols have been exercised against real servers: OpenSSH 10.2,
vsftpd 3.0.5 and Samba 4.23, transferring in both directions and comparing
checksums outside of goft.

`go test ./...` needs nothing external: it runs an SSH server and an FTP server
in process. SMB has no such server available, so its transfer methods are only
covered by the live suite below. Two FTP behaviours are the same story — a
missing path answered with an empty listing, and a directory that DELE will not
remove — because the in-process server does neither, while vsftpd does both.

To point the live suite at a server of your own:

```bash
GOFT_LIVE_PROTOCOL=sftp GOFT_LIVE_HOST=192.168.0.10 \
GOFT_LIVE_USER=user GOFT_LIVE_PASSWORD=secret \
GOFT_LIVE_PATH=/home/user/goft-test \
GOFT_LIVE_KNOWN_HOSTS=/path/to/known_hosts \
go test -count=1 ./internal/fsys/ -run Live -v
```

The same variables work for `ftp` and `smb` (add `GOFT_LIVE_SHARE` for the
latter). Pass `-count=1`: Go caches test results and cannot tell that the server
changed underneath it.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Finished normally, including `serve` stopping on a signal |
| 1 | The run completed but at least one file failed |
| 2 | The run could not be completed: bad configuration, or the connection failed |

`goft test` is the exception to the last row: it reports both directions and
only fails when neither of them works, because a read-only account is a perfectly
good setup for `recv`.

## Output

The JSON Lines log is the record; the human readable output on stdout is for
whoever is watching. Single runs print it by default, `serve` does not, and
`--console` / `--no-console` override either way.

`serve` reports a cycle once it is over, and stays silent about cycles where
nothing moved, so a working watcher with nothing to do prints nothing at all.
A cycle that could not run — an unreachable server, say — is always reported.

When no log file is configured and the console output is on, the JSON log goes
to stderr so the two never mix:

```bash
goft send 2>/dev/null    # just the readable output
goft send >/dev/null     # just the JSON log
```

Every verified transfer records `hash_src` and `hash_dst` at info level, so the
log is enough on its own to show that a file arrived intact. A file skipped
because the destination already held the same content records them too: not
sending needs its evidence as much as sending does. Both ends are
recorded in full — `/data/out/invoice/a.csv` and
`sftp://host/upload/invoice/a.csv` — because a path relative to a root the
reader cannot see identifies nothing.

Each run opens with the settings it is about to use, defaults included, so a
log kept for auditing answers "what moved this file, and how" on its own:

```json
{"msg":"starting","event":"lifecycle","config":{"file":"/etc/goft/invoice.yaml",
 "local":{"path":"/data/out/invoice"},
 "remote":{"protocol":"sftp","host":"invoice-sftp","path":"/upload/invoice",
           "user":"uploader","password":"REDACTED"},
 "verify":"hash","on_exists":"skip","post_action":"move", ...}}
```

Passwords and passphrases are reported as set, never as their value.

The level decides which records are written; `log.fields` decides what each
transfer record carries:

```yaml
log:
  fields: [src, dst, bytes, result, error, hash_src, hash_dst]
```

Omit the key for the full set. `time`, `level`, `msg`, `job`, `direction`,
`event` and `cycle_id` are always written, since a record missing those cannot
be placed.

`cycle_id` is a fresh UUID for each pass over the sending side, shared by every
record that pass produced — the transfers, their retries and the summary — so
one cycle can be picked out of a log a watcher has been appending to for days:

```bash
jq -r 'select(.cycle_id == "6482c7d7-...")' goft.log
```

Startup and shutdown records carry no `cycle_id`, because they belong to the
process rather than to any one pass. The
field names are listed in [goft.example.yaml](goft.example.yaml), and an
unknown one is a configuration error rather than something silently ignored.

`log.level` is the only verbosity setting, and it governs both outputs. See the
table at the end of [goft.example.yaml](goft.example.yaml).

## Documentation

`go doc` covers the command and every package:

```bash
go doc .                    # usage, configuration, flags, exit status
go doc ./internal/engine    # how a transfer cycle works
go doc ./internal/fsys      # what a protocol implementation has to provide
```

## Building

```bash
./build.sh
```

Every dependency is pure Go, so no cross toolchain is involved. Binaries land
under `build/<os>/<arch>/`:

```
build/linux/amd64/goft
build/windows/amd64/goft.exe
build/darwin/arm64/goft
```

Pass platforms to narrow it down, and `VERSION` to stamp a release:

```bash
VERSION=v1.0.0 ./build.sh linux/amd64 windows/amd64
```

`goft version` reports what was stamped. Setting `SOURCE_DATE_EPOCH` makes the
build reproducible. For a plain local build, `go build .` still works.
