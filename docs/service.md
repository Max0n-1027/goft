# Running several jobs as a service

[日本語](service-ja.md)

One goft process runs every job in its configuration, each watching its own
directory, until it is stopped — the way a log shipper runs its pipelines. A
job can also be run once, by name, from the same configuration.

```bash
goft -c /etc/goft/goft.yaml                              # every enabled job, until stopped
goft -c /etc/goft/goft.yaml --run-once invoice-upload    # one job, once
goft -c /etc/goft/goft.yaml jobs                         # what is configured
goft -c /etc/goft/goft.yaml test                         # check every enabled job
```

## The configuration

The main file has three sections: `service`, for what belongs to the process
rather than to any one job; `jobs`, a list of jobs; and `job_files`, naming
more files of jobs.

```yaml
# /etc/goft/goft.yaml
service:
  log:
    path: /var/log/goft/goft.log
job_files:
  - jobs.d/*.yaml
jobs:
  - name: invoice-upload
    direction: send
    local:
      path: /data/out/invoice
    remote:
      protocol: sftp
      host: invoice-sftp
      path: /upload/invoice
    post_action: move
    move_to: /data/done/invoice
    log:
      path: /var/log/goft/invoice-upload.log
  - name: report-download
    direction: recv
    local:
      path: /data/in/report
    remote:
      protocol: ftp
      host: report-ftp
      path: /outgoing
    poll_interval: 1m
    log:
      path: /var/log/goft/report-download.log
```

```yaml
# /etc/goft/jobs.d/archive.yaml
jobs:
  - name: invoice-archive
    direction: send
    enabled: false
    local:
      path: /data/done/invoice
    remote:
      protocol: local
      path: /backup/invoice
    log:
      path: /var/log/goft/invoice-archive.log
```

A job takes every setting a job file of earlier versions took — `local`,
`remote`, `include`, `verify`, `post_action`, `log` and the rest, described in
[goft.example.yaml](../goft.example.yaml) — and three of its own:

| Setting | |
|---|---|
| `name` | Required. Shown in every log record, and how `--run-once`, `goft jobs` and `goft test` name the job. Names that differ only in case count as the same. |
| `direction` | Required. `send` (local to remote) or `recv` (remote to local). |
| `enabled` | `true` by default. `false` keeps the service from running the job; it can still be run by name. |

**Job files** hold a `jobs:` list and nothing else. `job_files` takes file
names and patterns, read in the order given, after the jobs of the main file. A
pattern may match nothing — an empty `jobs.d` is an ordinary state to be in —
but a name written out in full must exist.

**A relative path is resolved against the directory of the file it is written
in**, not the directory goft was started from: for a service, nobody chooses
that. So `path: in` in `/etc/goft/jobs.d/report.yaml` means
`/etc/goft/jobs.d/in`. A path starting with `~` is left as written, for the
settings that understand it — `private_key`, `known_hosts` and the other files
of an ssh setup — to expand. `${VAR}` is replaced from the environment in every
file.

**Every job is checked at startup, alone and against the others, and a single
mistake stops goft starting at all.** A service that came up without one of its
jobs would look healthy while files that should be moving were not. Between
jobs, goft refuses:

- two jobs with the same name, ignoring case;
- two enabled jobs taking files from the same directory, or one inside the
  other, where either of them deletes or moves what it sends — the other would
  miss files, or send them half gone;
- enabled jobs passing files round in a loop, each one's destination another's
  source until the files come back where they started. A relay — `recv` into a
  staging directory that a `send` job takes from — is not a loop and is fine;
- one log file written by two jobs with different rotation settings.

A server is known by the name the configuration gives it, so one reached under
two names — an ssh_config alias and its address — counts as two.

Before any of this, every file is checked against the configuration's JSON
Schema: a setting goft does not have, a value of the wrong type or a choice that
does not exist is reported on its line. An editor can check the files against
the same schema as they are written; see
[Checking the configuration with JSON Schema](schema.md).

## Commands

| Command | |
|---|---|
| `goft` | Runs every enabled job and keeps watching until stopped. |
| `goft --run-once NAME` | Runs the job once and exits. Repeat it to run several, one after another; each runs even if one before it failed. A disabled job can be run this way. |
| `goft --dry-run` | Lists what would be transferred without transferring anything: for the jobs named by `--run-once`, or every enabled job. |
| `goft jobs` | Lists the jobs, after checking the configuration as the service would. |
| `goft test [NAME...]` | Checks the configuration and each job's connection, in the direction the job runs. With no name, every enabled job. |
| `goft service install` / `uninstall` | Windows only: registers goft as a Windows service, or removes it. See [below](#windows). |
| `goft schema` | Prints the JSON Schema of the configuration, for an editor to check the files with. See [Checking the configuration with JSON Schema](schema.md). |

`goft jobs` shows each job's direction, whether the service runs it, how often
it looks for files, and where it moves them:

```
NAME             DIRECTION  STATE     POLL  FROM -> TO
invoice-upload   send       enabled   5s    /data/out/invoice -> sftp://invoice-sftp/upload/invoice
report-download  recv       enabled   1m0s  ftp://report-ftp/outgoing -> /data/in/report
invoice-archive  send       disabled  5s    /data/done/invoice -> /backup/invoice
```

With several `--run-once`, the exit status is the worst of them: 2 if any job
could not run, 1 if any had a file fail, and 0 only if every one succeeded. A
name that is not configured stops the command before any job runs.

## What the service writes

The service's own log, `service.log`, records the process: which configuration
it read, which jobs it started, and when it stopped. What each job does goes to
that job's log, as it always has.

```json
{"time":"2026-11-07T08:33:06.651120375+09:00","level":"INFO","msg":"service starting","event":"service","config":"/etc/goft/goft.yaml","job_files":["/etc/goft/jobs.d/archive.yaml"],"jobs":["invoice-upload","report-download"],"lock_dir":"/tmp/goft-0"}
{"time":"2026-11-07T08:33:07.650239099+09:00","level":"INFO","msg":"service stopping","event":"service"}
{"time":"2026-11-07T08:33:07.650283971+09:00","level":"INFO","msg":"service stopped","event":"service"}
```

A job that cannot start — its lock held by another process, a connection
setting that cannot be resolved — is recorded as `service could not start`
with the reason, and none of the jobs run. Left without a `path`, the service
log goes to stdout, which systemd hands to the journal.

Jobs may share a log file, provided they rotate it the same way; every record
carries `job` and `direction`, so they can still be told apart:

```bash
jq -c 'select(.job == "report-download")' /var/log/goft/goft-jobs.log
```

The human readable console is off for the service, since nobody is watching
it; `--console` turns it on. Each job's report of a cycle is then written in
one piece, so the lines of two jobs never interleave.

The service runs cycles of each job one at a time, as a watching job always
has, and the jobs side by side: a slow server holds up only its own job.

**Stopping** — Ctrl+C, SIGTERM, `systemctl stop`, or the Windows service
manager — stops every job from starting anything new, and the files already
under way are finished. One that has stalled is dropped after
`remote.io_timeout`, which bounds how long a stop can take. The configuration
is read once, at startup; restart the service to apply a change.

## One job, one process at a time

Every job holds a lock while it runs. A second goft asked to run the same job —
`--run-once` while the service is running it, say — is refused, and exits 2:

```
goft: job "invoice-upload": another goft process is running it (pid 4242)
```

The service refuses to start at all when any of its jobs is locked. The lock is
the operating system's, held on a file, so a process that dies releases it with
nothing to clean up.

A job is its name in the file that defines it. A job of the same name in
another configuration — the second service installed with `--name` below, say
— is another job, and the two do not stop each other; one job file named by two
configurations is the same job, and only one of them runs it at a time.

Locks are kept in `service.lock_dir`. By default that is:

- on Windows, `C:\ProgramData\goft\locks`, the same directory for every
  account, so that the service, running as LocalSystem, and an administrator
  running `--run-once` see each other;
- elsewhere, `/tmp/goft-<uid>`, a directory of the account's own. It is refused
  unless it is a directory, belongs to the account, and is closed to everyone
  else, since one that another account could write to could be filled with
  links for goft to write through.

A job is therefore protected from a second run under the same account, which is
how a service is normally run by hand as well (`sudo -u goft goft --run-once
...`). When the service and a person would not see the same directory — two
accounts, or systemd's `PrivateTmp` — set `service.lock_dir` to one both use.

## Linux: systemd

One unit runs every job:

```ini
# /etc/systemd/system/goft.service
[Unit]
Description=goft file transfer
Wants=network-online.target
After=network-online.target

[Service]
ExecStart=/usr/local/bin/goft -c /etc/goft/goft.yaml
Restart=always
RestartSec=60
# A stop finishes the files under way; give a stalled one until
# remote.io_timeout (5 minutes by default) to be dropped.
TimeoutStopSec=330

[Install]
WantedBy=multi-user.target
```

```bash
systemctl enable --now goft
systemctl restart goft      # after changing the configuration
journalctl -u goft          # the service log, when it has no path of its own
```

## Windows

goft runs as a native Windows service. From an administrator's prompt:

```
goft service install -c C:\ProgramData\goft\goft.yaml
sc.exe start goft
```

`install` registers a service called `goft` that:

- runs this `goft.exe` with the configuration given, made absolute — or the one
  goft would find without `-c`, which on Windows includes
  `C:\ProgramData\goft\goft.yaml`;
- runs as LocalSystem;
- starts with Windows;
- is restarted a minute after it stops on an error, including a configuration
  that cannot be loaded. A day without a failure resets the count.

**Every log must go to a file.** A service has no console, and a log left on
stdout would be lost without a word, so `service.log.path` and the `log.path`
of every enabled job are required. `install` checks the configuration — that,
and everything else — before registering anything, and the service checks it
again each time it starts.

**Starting and stopping are written to the event log**, under the Application
log with the service's name as the source: id 1 when every job has started,
naming them; id 2 when it stopped; id 3, with the reason, when it could not
start. That is the first place to look when `sc.exe start` succeeds but the
service stops again.

```
sc.exe stop goft
goft service uninstall          # stops it first if it is running
```

`--name` installs, or removes, a service under another name, for a second
configuration on the same machine:

```
goft service install --name goft-test -c C:\ProgramData\goft\test.yaml
```

**LocalSystem has a home of its own**, `C:\Windows\System32\config\systemprofile`,
so `~\.ssh`, `%USERPROFILE%\.netrc` and the Credential Manager entries it sees
are not an administrator's. Name key and known_hosts files by absolute path, or
run the service as another account: in `services.msc` under *Log On*, or with
`sc.exe config goft obj= .\svc-transfer password= ...`. See
[Authenticating with ssh-agent](ssh-agent.md#jobs-run-as-a-service) for the
agent.

## Moving from one job per file

Earlier versions ran one job per process, with the direction chosen by the
command. A file in that shape is refused with a message saying what to do:
move its settings under `jobs:`, as one item with a `name` and a `direction`.

```yaml
# before: /etc/goft/invoice-upload.yaml
name: invoice-upload
local:
  path: /data/out/invoice
remote:
  protocol: sftp
  host: invoice-sftp
  path: /upload/invoice
```

```yaml
# after: /etc/goft/jobs.d/invoice-upload.yaml
jobs:
  - name: invoice-upload
    direction: send
    local:
      path: /data/out/invoice
    remote:
      protocol: sftp
      host: invoice-sftp
      path: /upload/invoice
```

Each old file can become a job file like this, named by `job_files` in a main
file that holds only the service settings.

| Before | Now |
|---|---|
| `goft send -c job.yaml` | `goft --run-once job` |
| `goft recv -c job.yaml` | `goft --run-once job`, with `direction: recv` in the job |
| `goft serve send -c job.yaml` | `goft`, which runs every enabled job |
| `goft test -c job.yaml` | `goft test job`, which checks the direction the job runs in |
| `--log-file` | `log.path` in the job, or `service.log.path` |
| a `goft@.service` unit per job | one `goft.service` |
| relative paths from the working directory | relative paths from the file's directory |
