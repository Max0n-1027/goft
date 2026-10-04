# The life of one file

Once a file has been chosen — see [What gets transferred](file-selection.md) —
this is what happens to it. The engine does not know which end is local: `send`
gives it the local side as the source and the remote as the destination, `recv`
the other way round, and everything below is the same in both directions.

## 1. The destination directory

The parent directory is created if the cycle's listing showed it missing, once
per directory rather than once per file. That includes the destination itself:
for `send`, `remote.path` need not exist before the first transfer, and whatever
levels of it are missing are created, on every protocol. (`local.path` is
checked at startup and has to exist, for `recv` as well.)
A directory is created in one request when its parent exists, and the missing
parents are only worked out when it does not.

## 2. Is it already there?

At the start of the cycle goft lists each destination directory once and keeps
the result. Asking the server about each file separately would cost one
round trip per file — with `on_exists: skip` and ten thousand files left in
place, ten thousand round trips every poll.

Names are matched exactly first, then case-insensitively when the destination
cannot tell `FOO.CSV` from `foo.csv` (a Windows or macOS disk, or an SMB share).
Otherwise a file that is already there would be transferred again on every
cycle.

The same destinations cannot hold two files whose names differ only in case,
which a Linux source can. When one cycle finds `A.csv` and `a.csv` — or
`Invoices/a.csv` and `invoices/a.csv` — neither is sent, and both are recorded
as failed, naming the other. Sending both would leave whichever arrived last,
report both as delivered, and with `post_action: delete` remove both sources.

What happens next depends on `on_exists`:

- **`skip`** (default) — no transfer, and no post-transfer action either:
  skipping means nothing happened, so the source stays where it is.
- **`overwrite`** — the two sides are compared with the configured `verify`
  method first, and an identical file is left alone:

```jsonl
{"time":"2026-08-25T21:35:42.649116785+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"dc0db4b0-a4de-41f8-a30b-1521a2a56724","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":3,"verify":"hash","result":"skipped","reason":"identical","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
```

  Sizes are compared before hashes, so a file that differs in length is settled
  without either side being read in full. With `verify: none` there is nothing
  to compare with, so the file is always sent.

## 3. Written under a temporary name

The bytes go to `<name>.goft.tmp` in the destination directory, hashed as they
stream past. Listing the destination while a transfer is in progress shows it:

```console
$ ls -1 /upload/invoice        # while the transfer is running
2026-08
invoice_202608_01.csv
invoice_202608_02.csv
statement_202608.dat.goft.tmp

$ ls -1 /upload/invoice        # once it has finished
2026-08
invoice_202608_01.csv
invoice_202608_02.csv
statement_202608.dat
```

This is the whole reason the real name only ever appears complete. Kill the
process mid-transfer and what is left behind is a `.goft.tmp` file, which the
next run overwrites; the name a downstream system watches for is never a partial
file.

A retry writes to a name of its own — `<name>.2.goft.tmp`, then
`<name>.3.goft.tmp` — rather than the one the attempt before it used. goft gives
up on a stalled connection without the server having necessarily noticed losing
it, and until it does, it still holds the file that attempt was writing. Two
writers on one name is the one way the bytes that arrive could be neither
attempt's, and on Windows the server's handle stops the name being renamed at
all, so every attempt would fail exactly where the first one did. All of these
names end in `.goft.tmp`, so none of them is ever picked up as a file to
transfer.

## 4. Verified

- `hash` (default) — the temporary file is read back from the destination and
  its xxHash compared with the one computed while sending.
- `length` — the sizes are compared.
- `none` — anything that did not error is accepted.

The digest computed during the comparison in step 2 is never reused here. Doing
so would mean verifying the bytes that were read for the comparison rather than
the ones that were sent, and a source file rewritten in between would go
unnoticed. Hashing while streaming costs no extra I/O.

```jsonl
{"time":"2026-08-25T21:35:36.561189297+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"46466581-ab9f-4d57-bb5f-1c63aab9f97b","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":6,"verify":"hash","result":"success","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
```

`hash_src` and `hash_dst` are recorded at info level, so the log alone shows
that a file arrived intact.

Verification compares what was read with what was written, so on its own it
cannot tell that the source's writer was still at work: both sides hold the same
bytes, just not the whole file. The number of bytes read is therefore also
checked against the size the file settled at. If they differ, the temporary file
is discarded, nothing appears under the real name, and the file is recorded as
failed:

```
the source changed while it was being transferred: 1048576 bytes when it settled, 1310720 read
```

It is not retried at once, since the writer would most likely still be at it.
The next cycle's settling decides when the file is ready.

## 5. Renamed into place

Only after verification passes. goft renames first, since a server that replaces
the target atomically should be allowed to; if the server refuses to rename onto
an existing name, the target is removed and the rename retried.

The destination is checked once more immediately before this. The listing from
step 2 is a snapshot of the start of the cycle, and under `on_exists: skip` a
file that appeared in the meantime must not be overwritten — the temporary file
is removed instead and the result recorded as `already_exists`. This extra check
costs one request, and only for files that were actually transferred.

When the removal is refused as well, both reasons are recorded — the rename
error and, after it, `could not clear the destination first:` and the reason the
target could not be removed. On a destination the job has lost the rights to,
the two are different failures wearing the same words, and reporting only the
first sends the reader looking at the wrong permission. The attempt counts as
permanent if either of them is, since the next cycle would be refused in the
same place.

If anything in steps 3 to 5 fails, the temporary file is removed and the
destination is left exactly as it was found.

## 6. The post-transfer action

Applied to the sending side:

- `none` (default) — nothing.
- `delete` — the source file is removed.
- `move` — the source is moved under `move_to`, keeping its relative path.
  `send` only; `move_to` must be outside the sending directory, which is
  checked at startup, or the moved files would be picked up again next cycle.
  A name already taken under `move_to` is a failure, not an automatic rename.

A post-transfer action that fails is retried on its own, with a fresh
connection. It does not cause the file to be transferred again: the file
arrived, and sending it a second time would be worse than leaving the source in
place.

`delete` and `move` take the source away, so before either of them the source
is looked at again. It is looked at once right after it was read and once more
just before the action, and if its size or timestamp moved in between — a writer
appended while the file was being verified and published — the source is left
where it is and the file is recorded as failed:

```
the source changed while it was being transferred: it was modified after being sent, so it was left in place (1048576 bytes then, 1310720 now)
```

What was sent did arrive, but deleting the source now would throw away data
that never reached the destination. The same check guards a file that
`on_exists: overwrite` found identical, against the size it was compared at.
These looks cost two extra requests per file, and only when the action deletes
or moves the source.

## 7. Empty directories

With `remove_empty_dirs`, a subdirectory that the transfer has just taken the
last file out of is removed, and its parent too if that leaves it empty:

```jsonl
{"time":"2026-08-30T19:34:24.849405311+09:00","level":"INFO","msg":"removed empty directory","job":"invoice-archive","direction":"send","cycle_id":"83471e30-674e-44fe-8571-fbe34e98caab","event":"postaction","src":"/data/out/invoice/2026-08/day-01"}
{"time":"2026-08-30T19:34:24.849418141+09:00","level":"INFO","msg":"removed empty directory","job":"invoice-archive","direction":"send","cycle_id":"83471e30-674e-44fe-8571-fbe34e98caab","event":"postaction","src":"/data/out/invoice/2026-08"}
{"time":"2026-08-30T19:34:24.849427315+09:00","level":"INFO","msg":"cycle complete","job":"invoice-archive","direction":"send","cycle_id":"83471e30-674e-44fe-8571-fbe34e98caab","event":"summary","files":2,"succeeded":2,"skipped":0,"failed":0,"bytes":34,"duration_ms":1001,"dirs_removed":2}
```

Both records name the directory in full, and the summary counts them in
`dirs_removed`, which is written only when something was removed.

Four things it will not do:

- **The sending root is never removed.** A watching job whose own directory
  disappeared would have nothing to watch.
- **A directory goft did not empty is left alone.** One that was already empty
  before the cycle, or that something else has written to since, is not its
  business.
- **A directory that is not empty when the time comes is left alone**, and it is
  listed to find that out rather than the removal being attempted and left to
  the far side to refuse.
- **It does not fail the transfer.** A directory that could not be removed is a
  warning; the files arrived, which is the job, and the next cycle will try the
  tidying up again.

The option needs `post_action: delete` or `move` — with `none` the source files
stay where they are, so no directory ever becomes empty — and that is a
configuration error rather than a setting that quietly does nothing. Without
`recursive` it has nothing to do either, which is reported as a warning.

## When it fails

Within a cycle a file is attempted up to `retry.max_attempts` times (3 by
default), with the wait doubling each time and the connection rebuilt in
between:

```jsonl
{"time":"2026-08-25T21:35:48.694181956+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":2,"of":3,"retry_in_ms":1000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)"}
{"time":"2026-08-25T21:35:49.718238957+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":3,"of":3,"retry_in_ms":2000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)"}
{"time":"2026-08-25T21:35:51.740558027+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_05.csv","protocol":"sftp","bytes":17,"duration_ms":8,"verify":"hash","result":"failed","error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE)","hash_src":"b2d06a1f4fa13583","hash_dst":"b2d06a1f4fa13583","attempt":3}
{"time":"2026-08-25T21:35:51.740695447+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"51a62aca-7da8-40fe-8ea9-af5e1a7ce683","event":"summary","files":1,"succeeded":0,"skipped":0,"failed":1,"bytes":0,"duration_ms":6077}
```

Only failures that could plausibly succeed next time are retried. A dropped
connection, a timeout or a failed verification is; a missing file, a permission
error, an existing-file error or a 5xx reply from an FTP server is not, because
the next attempt would fail identically. Nor is a source that changed while it
was sent: its writer is most likely still at it, and settling on the next cycle
is the better judge. The classification is by exclusion: anything not known to
be permanent is treated as worth another try.

A connection that goes quiet is handled the same way. When one moves no data
for `remote.io_timeout` (5 minutes by default) while something is waiting on
it, it is dropped, the waiting call fails with a message saying so, and the
file is retried over a new connection:

```
the connection stalled: nothing moved for 5m0s, so the connection was dropped (...)
```

It measures silence rather than duration, so a transfer that keeps moving is
never cut off however long it takes. Opening a connection has its own limit,
`remote.connect_timeout` (30 seconds), covering the handshake and the login as
well as the TCP connection, and for sftp the questions put to ssh-agent.

One file failing does not stop the cycle. The others carry on, the summary
counts the failures, and `send`/`recv` exit 1 — as opposed to exit 2, which
means the run could not start at all.

When a whole cycle fails under `serve` — an unreachable server, say — the pause
before the next one doubles, up to five minutes, and returns to `poll_interval`
as soon as a cycle succeeds. A job polling every second against a server that
has gone for good would otherwise write tens of thousands of identical errors a
day.

## Watching it happen

At debug level each step is its own record with its own duration, so a slow
transfer can be attributed to the part that is actually slow:

```jsonl
{"time":"2026-08-25T21:35:54.781854799+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"mkdir","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":0}
{"time":"2026-08-25T21:35:54.784956443+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"compare","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":2}
{"time":"2026-08-25T21:35:54.785978073+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"d148da46-4096-4f54-b7ea-54edcbf60f3f","event":"transfer","step":"write","src":"/data/out/invoice/restricted/invoice_202608_03.csv","duration_ms":0,"error":"permission denied"}
```

## See also

- [What gets transferred](file-selection.md)
- [A log, line by line](log-example.md)
- [What a run looks like on screen](console-output.md)
