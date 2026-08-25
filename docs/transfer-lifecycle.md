# The life of one file

Once a file has been chosen — see [What gets transferred](file-selection.md) —
this is what happens to it. The engine does not know which end is local: `send`
gives it the local side as the source and the remote as the destination, `recv`
the other way round, and everything below is the same in both directions.

## 1. The destination directory

The parent directory is created if the cycle's listing showed it missing, once
per directory rather than once per file.

## 2. Is it already there?

At the start of the cycle goft lists each destination directory once and keeps
the result. Asking the server about each file separately would cost one
round trip per file — with `on_exists: skip` and ten thousand files left in
place, ten thousand round trips every poll.

Names are matched exactly first, then case-insensitively when the destination
cannot tell `FOO.CSV` from `foo.csv` (a Windows or macOS disk, or an SMB share).
Otherwise a file that is already there would be transferred again on every
cycle.

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

## 5. Renamed into place

Only after verification passes. goft renames first, since a server that replaces
the target atomically should be allowed to; if the server refuses to rename onto
an existing name, the target is removed and the rename retried.

The destination is checked once more immediately before this. The listing from
step 2 is a snapshot of the start of the cycle, and under `on_exists: skip` a
file that appeared in the meantime must not be overwritten — the temporary file
is removed instead and the result recorded as `already_exists`. This extra check
costs one request, and only for files that were actually transferred.

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
the next attempt would fail identically. The classification is by exclusion:
anything not known to be permanent is treated as worth another try.

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
