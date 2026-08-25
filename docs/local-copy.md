# Copying between two local directories

`protocol: local` names a second directory on this machine instead of a server.
Everything else is the same job: the same scan, the same settling, the same
verification, the same temporary name and the same log.

```yaml
name: invoice-archive
local:
  path: /data/out/invoice
remote:
  protocol: local
  path: /backup/invoice
recursive: true
include: ["*.csv"]
verify: hash
on_exists: skip
```

`send` copies from `local.path` to `remote.path`, `recv` the other way round —
the direction is the command, exactly as it is for sftp. `serve send` watches
the first directory and copies as files arrive.

## What it looks like

```console
goft invoice-archive  /data/out/invoice -> /backup/invoice  (2 files, 830.1 KiB)
[1/2] 2026-08/invoice_202608_02.csv                  16 B  ok          0.0s
[2/2] invoice_202608_01.csv                     830.1 KiB  ok          0.0s
2 files: 2 transferred (830.1 KiB), 0 skipped, 0 failed  in 3.0s
```

The header names two plain paths rather than a URL, and so does the log:

```jsonl
{"time":"2026-08-26T04:49:53.025347889+09:00","level":"INFO","msg":"transfer","job":"invoice-archive","direction":"send","cycle_id":"7837a662-4f12-40ad-9cd7-163a8753ff83","event":"transfer","src":"/data/out/invoice/2026-08/invoice_202608_02.csv","dst":"/backup/invoice/2026-08/invoice_202608_02.csv","protocol":"local","bytes":16,"duration_ms":0,"verify":"hash","result":"success","hash_src":"d5f6162fd32742a1","hash_dst":"d5f6162fd32742a1"}
```

`protocol` is recorded as `local`, and `src` and `dst` are the two file names in
full.

## `goft test`

```console
config        /etc/goft/invoice-archive.yaml  OK
local         /data/out/invoice               OK  readable and writable
resolve       path = /backup/invoice          (yaml)
remote        /backup/invoice                 OK
recv (list)   0 entries                       OK
send (write)  writable                        OK
```

There is no `connect` line, because nothing is dialled: the check is called
`remote` and shows the directory that will be used. The `resolve` section has a
single entry for the same reason — there is no host, no port and no credential
to look up.

## What is checked before it runs

The two directories must be separate. goft refuses a configuration where either
contains the other, or where both name the same directory:

```
remote.path "/data/out/invoice/backup" is inside local.path "/data/out/invoice":
the two sides of a transfer must be separate directories
```

Copying into the directory being scanned would transfer the copies as well, and
with `recursive: true` that does not stop. This is checked at startup, so the
job fails with exit status 2 before anything is written.

Settings that only mean something over a network — `host`, `port`, `user`,
`password`, `share` — are rejected rather than ignored. A job that names a
password for a directory on its own disk is not the job its author thought they
were writing.

## What it is and is not

This is a verified copy, not a synchronisation: files move in one direction, and
nothing is deleted at the destination because it is missing at the source. It
is worth using instead of `cp` when the copy has to be checked, recorded, and
repeatable — an archive that must be provably identical, a hand-off directory
another program watches, a mount that is remote in everything but name (NFS,
SMB mounted by the operating system, a USB disk).

For a mounted share, letting the operating system do the mounting and using
`protocol: local` is often simpler than `protocol: smb`: the credentials are the
mount's problem, and goft sees an ordinary directory. The trade-off is that a
mount that has gone away looks like an empty or missing directory rather than a
connection error.

`post_action: move` still applies to the sending side only, and `move_to` must
be outside `local.path` as it always must.

## See also

- [What gets transferred](file-selection.md)
- [The life of one file](transfer-lifecycle.md)
- [A log, line by line](log-example.md)
