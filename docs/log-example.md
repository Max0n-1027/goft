# A log, line by line

This is a real log, not an illustration. It comes from one run of the test in
[cmd/logsample_test.go](https://github.com/Max0n-1027/goft/blob/main/cmd/logsample_test.go), against a real sftp server
the test starts, and it is quoted here exactly as goft wrote it. Only the paths,
the host, the port and the user name have been rewritten, so that a throwaway
temporary directory reads as a job someone might actually run.

The job sends four files. Two are ordinary, one is larger than the size cap, and
one sits in a directory the account cannot write to:

```yaml
name: invoice-upload
local:
  path: /data/out/invoice
remote:
  protocol: sftp
  host: invoice-sftp
  path: /upload/invoice
  user: uploader
  password: ${GOFT_SFTP_PASSWORD}
recursive: true
include: ["*.csv", "*.dat"]
max_file_size_mb: 1
stable_duration: 3s
workers: 2
verify: hash
on_exists: skip
log:
  path: /var/log/goft/invoice-upload.log
  level: info
```

## The first pass

Every run opens with the settings it is about to use, defaults included, so the
log answers "what moved this file, and how" without the configuration file
having to be found and dated:

```json
{
  "time": "2026-10-29T01:58:19.553460649+09:00",
  "level": "INFO",
  "msg": "starting",
  "job": "invoice-upload",
  "direction": "send",
  "event": "lifecycle",
  "config": {
    "file": "/etc/goft/invoice-upload.yaml",
    "name": "invoice-upload",
    "local": {
      "path": "/data/out/invoice"
    },
    "remote": {
      "protocol": "sftp",
      "host": "invoice-sftp",
      "path": "/upload/invoice",
      "port": 22,
      "user": "uploader",
      "password": "REDACTED",
      "known_hosts": "/home/svc-transfer/.ssh/known_hosts",
      "use_ssh_config": false,
      "connect_timeout": "30s",
      "io_timeout": "5m0s"
    },
    "recursive": true,
    "include": [
      "*.csv",
      "*.dat"
    ],
    "exclude": [
      "*.tmp",
      "*.part",
      ".*"
    ],
    "max_file_size_mb": 1,
    "poll_interval": "5s",
    "stable_duration": "3s",
    "workers": 2,
    "verify": "hash",
    "on_exists": "skip",
    "post_action": "none",
    "retry": {
      "max_attempts": 3,
      "interval": "2s",
      "backoff": 2
    },
    "log": {
      "path": "/var/log/goft/invoice-upload.log",
      "level": "info",
      "rotation": "size",
      "max_size_mb": 100,
      "max_backups": 7,
      "max_age_days": 30,
      "compress": true
    }
  }
}
```

Then the files, one record each, and the cycle they belonged to:

```jsonl
{"time":"2026-10-29T01:58:22.556042322+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
{"time":"2026-10-29T01:58:22.562050042+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"success","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
{"time":"2026-10-29T01:58:22.561992625+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"transfer","src":"/data/out/invoice/invoice_202608_02.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_02.csv","protocol":"sftp","bytes":16,"duration_ms":0,"verify":"hash","result":"success","hash_src":"d5f6162fd32742a1","hash_dst":"d5f6162fd32742a1"}
{"time":"2026-10-29T01:58:22.562222425+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"transfer","src":"/data/out/invoice/restricted/invoice_202608_03.csv","dst":"sftp://invoice-sftp/upload/invoice/restricted/invoice_202608_03.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"failed","error":"permission denied"}
{"time":"2026-10-29T01:58:22.562234098+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"summary","files":4,"succeeded":2,"skipped":1,"failed":1,"bytes":33,"duration_ms":3008}
```

Reading that: `archive.dat` was too large to send, which is a warning rather
than a failure and leaves the other files alone. Both CSV files arrived, and
`hash_src` equals `hash_dst`, which is the evidence that what was read is what
landed. `invoice_202608_03.csv` failed — the directory it belongs in is not
writable by this account — and a failure is an `ERROR` record with the reason in
`error`.

With `workers: 2` the order of the records follows whichever worker finished
first, so it varies between runs; `cycle_id` and `time` are what put them back
together, not their position in the file.

Every record from this pass carries the same `cycle_id`, and the summary shares
it, so one pass can be lifted out of a log a watcher has been appending to for
weeks.

## The second pass, with everything already delivered

```jsonl
{"time":"2026-10-29T01:58:25.564905414+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"2ff33251-593a-4fd2-a1c0-9d423885f3b7","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
{"time":"2026-10-29T01:58:25.568108976+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"2ff33251-593a-4fd2-a1c0-9d423885f3b7","event":"transfer","src":"/data/out/invoice/invoice_202608_02.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_02.csv","protocol":"sftp","bytes":16,"duration_ms":0,"verify":"hash","result":"skipped","reason":"already_exists"}
{"time":"2026-10-29T01:58:25.568094141+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"2ff33251-593a-4fd2-a1c0-9d423885f3b7","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"skipped","reason":"already_exists"}
{"time":"2026-10-29T01:58:25.568346532+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"2ff33251-593a-4fd2-a1c0-9d423885f3b7","event":"transfer","src":"/data/out/invoice/restricted/invoice_202608_03.csv","dst":"sftp://invoice-sftp/upload/invoice/restricted/invoice_202608_03.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"failed","error":"permission denied"}
{"time":"2026-10-29T01:58:25.568358707+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"2ff33251-593a-4fd2-a1c0-9d423885f3b7","event":"summary","files":4,"succeeded":0,"skipped":3,"failed":1,"bytes":0,"duration_ms":3005}
```

`on_exists: skip` does exactly what it says: the file is not sent, and nothing
is read to decide that — the destination listing taken at the start of the cycle
was enough. `duration_ms` is 0 because nothing happened. The oversized file and
the failure both repeat, since nothing about either has changed and this is a
new process (see [the size cap](#the-size-cap)).

## Comparing before overwriting

With `on_exists: overwrite` the destination is not clobbered blindly. The two
sides are compared first, and a file that is already identical is left alone:

```jsonl
{"time":"2026-10-29T01:58:28.571390046+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"43138471-aae1-48ff-b8fd-362ae1df10c7","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
{"time":"2026-10-29T01:58:28.575556337+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"43138471-aae1-48ff-b8fd-362ae1df10c7","event":"transfer","src":"/data/out/invoice/invoice_202608_01.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_01.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"skipped","reason":"identical","hash_src":"e2d74924f2f51afd","hash_dst":"e2d74924f2f51afd"}
{"time":"2026-10-29T01:58:28.575522231+09:00","level":"INFO","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"43138471-aae1-48ff-b8fd-362ae1df10c7","event":"transfer","src":"/data/out/invoice/invoice_202608_02.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_02.csv","protocol":"sftp","bytes":16,"duration_ms":0,"verify":"hash","result":"skipped","reason":"identical","hash_src":"d5f6162fd32742a1","hash_dst":"d5f6162fd32742a1"}
{"time":"2026-10-29T01:58:28.575763174+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"43138471-aae1-48ff-b8fd-362ae1df10c7","event":"transfer","src":"/data/out/invoice/restricted/invoice_202608_03.csv","dst":"sftp://invoice-sftp/upload/invoice/restricted/invoice_202608_03.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"failed","error":"permission denied"}
{"time":"2026-10-29T01:58:28.575775+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"43138471-aae1-48ff-b8fd-362ae1df10c7","event":"summary","files":4,"succeeded":0,"skipped":3,"failed":1,"bytes":0,"duration_ms":3006}
```

`reason` is `identical` rather than `already_exists`, and the hashes are
recorded: not sending needs its evidence as much as sending does.

## Retries, and a failure that stuck

Here the destination name could not be replaced: a directory is in the way of
the file. The transfer itself worked — the hashes are there — but the rename
into place kept failing. This pass used `on_exists: overwrite` and
`retry.interval: 1s`:

```jsonl
{"time":"2026-10-29T01:58:34.585508388+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"30070be4-5a3c-49f5-8f11-813a373a8f65","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":2,"of":3,"retry_in_ms":1000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE) (could not clear the destination first: remove /upload/invoice/invoice_202608_05.csv: sftp: \"remove /upload/invoice/invoice_202608_05.csv: directory not empty\" (SSH_FX_FAILURE))"}
{"time":"2026-10-29T01:58:35.588028566+09:00","level":"WARN","msg":"retrying transfer","job":"invoice-upload","direction":"send","cycle_id":"30070be4-5a3c-49f5-8f11-813a373a8f65","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","attempt":3,"of":3,"retry_in_ms":2000,"error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.2.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE) (could not clear the destination first: remove /upload/invoice/invoice_202608_05.csv: sftp: \"remove /upload/invoice/invoice_202608_05.csv: directory not empty\" (SSH_FX_FAILURE))"}
{"time":"2026-10-29T01:58:37.590781209+09:00","level":"ERROR","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"30070be4-5a3c-49f5-8f11-813a373a8f65","event":"transfer","src":"/data/out/invoice/invoice_202608_05.csv","dst":"sftp://invoice-sftp/upload/invoice/invoice_202608_05.csv","protocol":"sftp","bytes":17,"duration_ms":0,"verify":"hash","result":"failed","error":"sftp: \"rename /upload/invoice/invoice_202608_05.csv.3.goft.tmp /upload/invoice/invoice_202608_05.csv: file exists\" (SSH_FX_FAILURE) (could not clear the destination first: remove /upload/invoice/invoice_202608_05.csv: sftp: \"remove /upload/invoice/invoice_202608_05.csv: directory not empty\" (SSH_FX_FAILURE))","hash_src":"b2d06a1f4fa13583","hash_dst":"b2d06a1f4fa13583","attempt":3}
{"time":"2026-10-29T01:58:37.590805908+09:00","level":"INFO","msg":"cycle complete","job":"invoice-upload","direction":"send","cycle_id":"30070be4-5a3c-49f5-8f11-813a373a8f65","event":"summary","files":1,"succeeded":0,"skipped":0,"failed":1,"bytes":0,"duration_ms":6010}
```

The retry records name the attempt (`attempt` of `of`) and how long the next one
waits (`retry_in_ms`), which grows by `retry.backoff`: 1s, then 2s. When the
attempts run out the result is a single `failed` record carrying `attempt`, so
the log shows both that it was retried and how many times. The temporary file
was removed each time; the destination was left as it was found.

Each error carries two reasons. The server would not rename onto a name that
was taken, so goft tried to clear the name first, and the server refused that
too: `could not clear the destination first:` introduces the second refusal.
Each attempt also wrote under a temporary name of its own — `.goft.tmp`, then
`.2.goft.tmp`, then `.3.goft.tmp` — as [The life of one
file](transfer-lifecycle.md#3-written-under-a-temporary-name) explains.

The server answered with SFTP's generic `SSH_FX_FAILURE`, which says nothing
about whether trying again could help, so goft tried again.

Not every failure is retried. A dropped connection or a failed verification is;
a missing file, a permission error or a 5xx reply is not, because the next
attempt would fail the same way. That is why the permission failure above has no
`attempt` field.

## A server that was not there

After the settings record, which is the same as the one above:

```jsonl
{"time":"2026-10-29T01:58:31.579118232+09:00","level":"ERROR","msg":"run failed","job":"invoice-upload","direction":"send","event":"lifecycle","error":"dial invoice-sftp:22: dial tcp 10.0.4.12:22: connect: connection refused"}
```

An unreachable server ends the run before any file is attempted, so there are no
transfer records at all — just the settings and the reason. `goft send` exits 2
here, which is how a monitor tells "could not start" apart from "ran, and some
files failed" (exit 1).

## Level debug

`log.level: debug` adds three things. The connection parameters and where each
one was resolved from:

```jsonl
{"time":"2026-10-29T01:58:37.591500593+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"host","value":"invoice-sftp","source":"yaml"}
{"time":"2026-10-29T01:58:37.59150647+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"port","value":"22","source":"yaml"}
{"time":"2026-10-29T01:58:37.591509101+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"user","value":"uploader","source":"yaml"}
{"time":"2026-10-29T01:58:37.591511153+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"password","value":"REDACTED","source":"yaml"}
{"time":"2026-10-29T01:58:37.591513114+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"private_key","value":"/home/svc-transfer/.ssh/id_ed25519","source":"default"}
{"time":"2026-10-29T01:58:37.591515199+09:00","level":"DEBUG","msg":"resolved connection setting","job":"invoice-upload","direction":"send","event":"connect","field":"known_hosts","value":"/home/svc-transfer/.ssh/known_hosts","source":"yaml"}
```

What the scan found, and how much of it had settled:

```jsonl
{"time":"2026-10-29T01:58:40.592979021+09:00","level":"DEBUG","msg":"scan complete","job":"invoice-upload","direction":"send","cycle_id":"92731096-67a4-42c8-b935-29f994a8ad1d","event":"scan","found":4,"settled":4}
```

And a record per step of each transfer, with its own `duration_ms`, so a slow
run can be attributed to the part that is actually slow:

```jsonl
{"time":"2026-10-29T01:58:40.597366462+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"92731096-67a4-42c8-b935-29f994a8ad1d","event":"transfer","step":"mkdir","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":0}
{"time":"2026-10-29T01:58:40.597579447+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"92731096-67a4-42c8-b935-29f994a8ad1d","event":"transfer","step":"compare","src":"/data/out/invoice/invoice_202608_01.csv","duration_ms":0}
{"time":"2026-10-29T01:58:40.5976686+09:00","level":"DEBUG","msg":"step","job":"invoice-upload","direction":"send","cycle_id":"92731096-67a4-42c8-b935-29f994a8ad1d","event":"transfer","step":"write","src":"/data/out/invoice/restricted/invoice_202608_03.csv","duration_ms":0,"error":"permission denied"}
```

A successful transfer also gains `rate_mibs` at this level. Skipped and failed
files do not carry it: their `bytes` is the size of a file that did not move, so
a rate computed from it would describe nothing.

<a id="the-size-cap"></a>

## The size cap

A file over `max_file_size_mb` is skipped with `reason: size_limit`, at warn
level:

```jsonl
{"time":"2026-10-29T01:58:22.556042322+09:00","level":"WARN","msg":"transfer","job":"invoice-upload","direction":"send","cycle_id":"ea701700-3c50-48d9-8cd1-f1b6ecabf70e","event":"transfer","src":"/data/out/invoice/archive.dat","dst":"sftp://invoice-sftp/upload/invoice/archive.dat","protocol":"sftp","bytes":2097152,"duration_ms":0,"verify":"hash","result":"skipped","reason":"size_limit"}
```

The warning is written once per file per process, and later cycles record the
same condition at debug. A file that will never be sent should be visible in the
log without repeating the same warning on every poll. If the file changes — it
grew, or was rewritten — it is news again and the warning returns, since it is
no longer the file that was reported.

The cap is applied after settling rather than during the scan, so that the file
is a known one by the time the decision is made. Each of the three passes above
warns, because each was a separate `goft send`: a new process has nothing to
remember. Under `serve`, only the first cycle does.

## Reading it

The log is JSON Lines, so `jq` reads it a record at a time:

```bash
# everything one cycle did
jq -c 'select(.cycle_id == "ea701700-3c50-48d9-8cd1-f1b6ecabf70e")' invoice-upload.log

# what failed, and why
jq -r 'select(.result == "failed") | "\(.time) \(.src) \(.error)"' invoice-upload.log

# bytes moved per cycle
jq -r 'select(.event == "summary") | "\(.time) \(.bytes)"' invoice-upload.log

# proof that a particular file arrived intact
jq -c 'select(.src | endswith("invoice_202608_01.csv")) | {time, result, hash_src, hash_dst}' invoice-upload.log
```

`log.fields` decides which of these fields a transfer record carries;
`time`, `level`, `msg`, `job`, `direction`, `event` and `cycle_id` are always
written. The full list is in [goft.example.yaml](../goft.example.yaml), and
[README.md](../README.md#output) explains the two outputs.

## Regenerating this

```bash
GOFT_LOG_SAMPLE=/tmp/goft-sample go test ./cmd -run TestGenerateLogSample
```

## See also

- [What gets transferred](file-selection.md)
- [The life of one file](transfer-lifecycle.md)
- [What a run looks like on screen](console-output.md)
