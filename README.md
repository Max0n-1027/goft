# goft

[日本語](README-ja.md)

Watches a directory and moves files over FTP, SFTP or SMB — or between two
directories on the same machine — verifying every file it transfers.

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

`protocol: local` names a second directory on this machine instead of a server,
and the job is otherwise unchanged — same scan, same verification, same log:

```yaml
remote:
  protocol: local
  path: /backup/invoice
```

The two directories must be separate; goft refuses a configuration where either
contains the other. See
[Copying between two local directories](docs/local-copy.md).

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
- **smb** has no file of its own; give it `user`, `password` and `share`.
- On **Windows**, a generic Credential Manager entry registered for goft fills
  in `user` and `password` for any protocol, including ftp. The entry is named
  `goft:<protocol>://<host>`, so one host can hold a separate credential per
  protocol:

  ```
  cmdkey /generic:goft:ftp://invoice-ftp /user:uploader /pass:secret
  cmdkey /generic:goft:smb://fileserver /user:svc-transfer /pass:secret
  ```

  It is read before `~/.netrc` and `~/.ssh/config`, so an ftp job with an entry
  registered needs no netrc at all, and an sftp job authenticating by password
  needs neither a password in the file nor a `User` in ssh_config. Only the user
  name and the password come from the store; `private_key_passphrase` unlocks a
  file rather than authenticating to a host, so it stays in the job file or in
  `${VAR}`. `credential_target` reads an entry under some other name, and
  `use_credential_manager: false` skips the store.

  The credentials Windows keeps for network shares are a different kind, and
  the platform reserves them for its own authentication packages, so a share
  that Explorer or `net use` remembered cannot be reused here.

What the configuration file states always wins over a default file. Run
`goft test` to see the values that were resolved and where each one came from.

Two ssh_config features are not supported, and are reported as warnings rather
than applied silently: `Match` blocks, and `ProxyJump`/`ProxyCommand`. Put the
settings you need directly in the job file instead. ssh-agent is not used;
supply the key with `private_key`, and its passphrase with
`private_key_passphrase` if it has one.

Keys goft finds for itself — `IdentityFile` in ssh_config, or
`~/.ssh/id_ed25519` and `~/.ssh/id_rsa` — are offered only when they can be
used as they stand. One that is passphrase protected while no
`private_key_passphrase` is set is skipped with a warning, so the passphrase on
a person's own key does not stand in the way of a job that authenticates by
password. A key named by `private_key` is always offered, and failing to unlock
it is an error.

`StrictHostKeyChecking` in ssh_config is honoured, with a warning either way.
`no` turns host key verification off. `accept-new` behaves as it does for
OpenSSH: the key of a host that known_hosts does not list yet is accepted and
recorded there — creating the file if need be — while a host that is listed
with a different key is refused, since that is what a man in the middle looks
like.

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

**A file that changes during its transfer is neither published nor removed.**
Settling can be fooled by a writer that pauses for longer than
`stable_duration` and then carries on. So the bytes read are also checked
against the size the file settled at, and nothing is published under the real
name if they differ. When `post_action` would delete or move the source, the
source is looked at once more just before, and one that has changed since it
was sent is left where it is. Either way the file is recorded as failed, is not
retried at once, and the next cycle's settling decides when to try again.

**FTP timestamps are coarse.** FTP has no stat command; the modification time
comes from `MLST` where the server supports it and from a directory listing
otherwise, which can be accurate only to the minute. Allow for that in
`stable_duration` when downloading from an FTP server.

**`on_exists: skip` leaves the source in place.** Skipping means no transfer
happened, so `post_action` does not run either, and the file will be examined
again on every cycle. Use `post_action` (or `on_exists: overwrite`) if the
sending directory should not keep growing.

**Empty directories are only removed if you ask.** With `remove_empty_dirs`, a
subdirectory this cycle took the last file out of is removed, and its parent too
if that leaves it empty. The sending root is never removed, and neither is a
directory goft did not empty — one that was already empty, or that something
wrote to in the meantime, is left alone. It needs `post_action: delete` or
`move`, since with `none` the source files stay where they are. See
[The life of one file](docs/transfer-lifecycle.md).

**Who sets the mode of a transferred file depends on the destination.** Writing
into a directory on this machine — `protocol: local` included — or onto an SMB
share, goft creates the file 0644, subject to the process umask, so that
whatever consumes it next can read it. Over sftp and ftp it sends no mode at
all and the server decides: OpenSSH's sftp-server uses 0666 for a file the
client gave no mode for. Windows has no modes to act on either way: a file
written there, locally or by a Windows SFTP server, takes the permissions of
the directory it lands in. Verified against OpenSSH 9.5p2 for Windows on
Windows 11 — a file transferred into a directory carrying an inheritable grant
came out with that grant, marked inherited, exactly like a subdirectory goft
created alongside it.

**A Windows destination has to grant its permissions to files, not only to
folders.** An access rule carrying `(CI)` alone is inherited by subdirectories
and not by the files in them, and `icacls upload /grant "group:(CI)(M)"` is an
easy way to arrive at one. The directories goft creates then come out right
while the files it writes inherit nothing, which is the shape of the problem
seen from the outside. The transfer gets as far as putting the bytes there and
fails after: creating the file is checked against the directory, and everything
that follows against the file. With `verify: hash` it fails reading the file
back; with `verify: length` or `none` it reaches the rename, which needs delete
permission on the temporary file, and fails there. A plain `sftp put` neither
reads back nor renames, so a directory set up this way looks fine until goft is
pointed at it.

Removing the temporary file is refused as well — logged as `could not remove
temporary file` — so a `.goft.tmp` stays behind, and from the next cycle the
write fails too, because the account cannot reopen the file it created itself.
Grant `(OI)` alongside `(CI)`, push it down over what is already there, and
clear what was left:

```
icacls C:\upload /grant "group:(OI)(CI)(M)" /T
del /s C:\upload\*.goft.tmp
```

Verified the same way: same server, same non-administrator account, same job.
With `(OI)(CI)` both files transfer; with `(CI)` alone both fail, at the
verification or at the rename according to `verify`; and running those two
commands over the failed state turns it back into a working one.

**Windows refuses a name it would store as something else.** A file the server
calls `2026:01.csv` cannot be written to a Windows disk under that name: the
colon opens an NTFS alternate data stream, so the bytes end up hidden inside an
empty file called `2026`, invisible to Explorer, to `dir`, to a backup and to
goft's own next cycle. Reading the file back finds it again — the same
reinterpretation happens on the way in — so verification passes and the
transfer looks like a success. A trailing dot or space is the same story:
Windows drops it, and the file arrives under a name that is not the one the
server used. So are the reserved device names (`con`, `nul`, `aux`, `com1` and
the rest), which ordinary Win32 path resolution cannot reach afterwards.
Because none of this can be caught after the fact, `recv` onto Windows refuses
such a name before writing anything, records the file as failed and says what
about the name was the problem. It is not retried, since the name would be
refused identically next time. The rest of the cycle carries on, and every name
Windows can hold as written is transferred as before.

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

### Windows

`go test ./...` passes on Windows as it does elsewhere. Behaviour that only
exists there — locked files, the names Windows will not store, `%USERPROFILE%`,
paths past `MAX_PATH` — is covered by the `_windows_test.go` files, which are
built only on Windows. Going the other way, a test whose expectations are those
of a POSIX file system calls `requirePOSIX` and says why, so it reports as
skipped rather than failing on Windows; each of those has a Windows counterpart
asserting what the same code does there instead.

One test is opt-in, because it writes to the credential store of whoever runs
it. It registers an entry under a host no job would use and removes it again:

```bash
GOFT_WINCRED_TEST=1 go test -count=1 ./internal/fsys/ -run Credential -v
```

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

Both outputs are UTF-8, the log because JSON is defined that way and the console
because a file name is printed as it is stored. A Windows console left on its
regional code page will therefore garble anything outside ASCII; `chcp 65001`,
Windows Terminal or PowerShell 7 shows it properly. The bytes written are the
same either way, so a redirected log is unaffected.

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

[docs/log-example.md](docs/log-example.md) walks through a real log line by
line: a transfer and its hashes, a skip, a retry and its backoff, a failure, and
what the same run looks like at debug level.

## Documentation

The pages under [docs/](docs/README.md) walk through the behaviour with output
from real runs:

- [What gets transferred](docs/file-selection.md) — names, settling, the size cap
- [Copying between two local directories](docs/local-copy.md) — `protocol: local`
- [The life of one file](docs/transfer-lifecycle.md) — temporary name, verification, rename, post-transfer actions, retries
- [A log, line by line](docs/log-example.md) — a real log explained record by record
- [What a run looks like on screen](docs/console-output.md) — `goft test`, `--dry-run`, a transfer

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

### Releases

Binaries are published on the [releases page](https://github.com/Max0n-1027/goft/releases):
a versioned release for each `v*` tag, and a prerelease named
`main-<date>-<commit>` for every push to `main`. The latter are builds rather
than releases — unsupported, superseded by the next commit — and only the five
most recent are kept.

Each archive holds the binary, both READMEs, the licence and the example
configuration, and `SHA256SUMS` covers them all:

```bash
tar xzf goft_v0.1.0_linux_amd64.tar.gz
sudo install goft_v0.1.0_linux_amd64/goft /usr/local/bin/
sha256sum -c SHA256SUMS --ignore-missing
```

[The workflow](.github/workflows/release.yml) runs `gofmt`, `go vet` and
`go test -race` first, so a build that fails them is never published. It
publishes what `./package.sh` produces, which is the same command to run by
hand:

```bash
VERSION=v1.0.0 ./package.sh
```
