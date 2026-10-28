# What gets transferred

Every cycle starts by listing the sending side and deciding which files take
part. This page describes that decision, in the order goft makes it. The
captures come from a real run of the test in
[cmd/logsample_test.go](https://github.com/Max0n-1027/goft/blob/main/cmd/logsample_test.go).

```yaml
recursive: true
include: ["*.csv", "*.dat"]
exclude: ["*.tmp", "*.part", ".*"]
max_file_size_mb: 1
stable_duration: 3s
```

## 1. Regular files only

Directories, symbolic links, sockets and devices are never candidates. The rule
is written as an allow list — a file takes part only if the protocol reports it
as a regular file — rather than as a list of things to skip. FTP and SMB
describe link-like objects differently from a local disk, and a deny list would
let whatever it failed to name through.

## 2. Names

`include` and `exclude` are glob patterns matched against the **base name**, not
the path. A file must match one of the `include` patterns and none of the
`exclude` ones. An empty `include` list matches everything; `exclude` defaults to
`["*.tmp", "*.part", ".*"]`.

`exclude` also applies to directories, and a directory that matches is pruned:
nothing beneath it is even listed. `include` applies to files only, so it does
not have to be written to allow the directories on the way.

goft's own in-flight files (`*.goft.tmp`) are excluded whatever the
configuration says. Leaving that to the default `exclude` list would break the
moment someone overrode it, and a job that picks up its own temporary files
would transfer half-written data.

With `recursive: false` — the default — only the root directory is listed and
subdirectories are ignored entirely.

## 3. Settling

A file is not a candidate until its size and modification time have stayed
unchanged for `stable_duration`. The clock accumulates across cycles rather than
restarting each poll, and a file that changes starts it again.

Judging by modification time alone (`now - mtime >= stable_duration`) would be
simpler and wrong: `cp -p`, `rsync --times` and every other tool that preserves
timestamps leaves the mtime at the source file's old value while the copy is
still running, so a half-written file looks settled.

A single `send` or `recv` has no earlier cycle to compare against, so it takes
two readings `stable_duration` apart and transfers what did not change in
between. That is why a one-shot run takes at least `stable_duration` whenever
there is a file to look at; an empty directory, or `stable_duration: 0`, needs
no second reading.

Under `serve`, scans are `poll_interval` apart, counted from the end of the
previous cycle, and a file is picked up by the first scan that has seen it
unchanged for `stable_duration`. Once a file stops changing it therefore waits
for the next scan, and then for `stable_duration` rounded up to whole poll
intervals: with the defaults, `poll_interval: 5s` and `stable_duration: 3s`,
between 5 and 10 seconds.

**FTP timestamps are coarse.** FTP has no stat command; the modification time
comes from `MLST` where the server supports it and from a directory listing
otherwise, which can be accurate only to the minute. Allow for that in
`stable_duration` when downloading from an FTP server.

At debug level the scan reports what it found and how much of it had settled:

```jsonl
{"time":"2026-10-29T01:58:40.592979021+09:00","level":"DEBUG","msg":"scan complete","job":"invoice-upload","direction":"send","cycle_id":"92731096-67a4-42c8-b935-29f994a8ad1d","event":"scan","found":4,"settled":4}
```

## 4. The size cap

`max_file_size_mb` is applied after settling, not during the scan. A file over
the cap is skipped, with the rest of the cycle carrying on:

```jsonl
{"time":"2026-10-29T01:58:22.556042322+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
```

The warning is written once per file per process; later cycles record the same
condition at debug, and a file that changes is reported again. Applying the cap
after settling is what makes that possible — a file dropped during the scan
would never be seen by the part that remembers it.

Reporting a file over the cap needs nothing from the destination, so a cycle
whose only candidates are over it does not connect there at all. A file left
in the source directory for weeks would otherwise cost a watcher a connection
on every poll.

`max_file_size_mb: 0`, or leaving it out, means no limit.

## What that adds up to

The dry run lists the files that survived the first three steps:

```console
goft invoice-upload  /data/out/invoice -> sftp://invoice-sftp/upload/invoice  (dry-run)
  2026-08/invoice_202608_03.csv                  17 B
  archive.dat                                 2.0 MiB
  invoice_202608_01.csv                     830.1 KiB
  invoice_202608_02.csv                     625.0 KiB
4 files, 3.4 MiB would be transferred (nothing was sent)
```

`archive.dat` is listed although it is over the cap: the cap is decided when the
file would be transferred, and would have shown there as a skip.

## Files that keep being examined

`on_exists: skip` means no transfer happened, so `post_action` does not run
either and the file stays on the sending side to be examined again every cycle.
The cost is one listing per directory rather than one request per file, but a
sending directory that only grows will eventually make every cycle slower. Use
`post_action: move` or `delete` if files are not meant to accumulate there.

## See also

- [The life of one file](transfer-lifecycle.md) — what happens once a file is chosen
- [A log, line by line](log-example.md)
- [What a run looks like on screen](console-output.md)
