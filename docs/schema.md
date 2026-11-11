# Checking the configuration with JSON Schema

[日本語](schema-ja.md)

goft describes its configuration file in a [JSON Schema](https://json-schema.org/)
(draft-07). goft checks every file against it before it does anything else, and
an editor that understands JSON Schema can check the same file against the same
schema as you write it: a misspelt setting, a value of the wrong type or a
choice that does not exist is marked where it is written, long before the
service is restarted.

## Getting the schema

The schema comes in three places, all the same file:

- `goft schema` prints the one built into the goft you run, so it always matches
  it:

  ```bash
  goft schema > /etc/goft/goft.schema.json
  ```

- A release archive has it as `schema/goft.schema.json`, beside
  `goft.example.yaml`.
- The repository has it as
  [schema/goft.schema.json](../schema/goft.schema.json). An editor can read it
  from GitHub directly; use the tag of the version you run (`goft version`), so
  that the editor and goft agree:

  ```
  https://raw.githubusercontent.com/Max0n-1027/goft/vX.Y.Z/schema/goft.schema.json
  ```

## Using it in an editor

The same schema serves the main file and the job files `job_files` names: a job
file is a configuration with only a `jobs:` list in it.

**VS Code**, with Red Hat's YAML extension, and any other editor that uses
yaml-language-server (Neovim, Helix, Zed and others): a comment on the first
line of a file names the schema for that file. A relative path is relative to
the file:

```yaml
# yaml-language-server: $schema=goft.schema.json
service:
  log:
    path: /var/log/goft/goft.log
```

To cover every file at once instead, map the schema to them in `settings.json`:

```json
"yaml.schemas": {
  "/etc/goft/goft.schema.json": ["goft.yaml", "jobs.d/*.yaml"]
}
```

**JetBrains IDEs**: in *Settings | Languages & Frameworks | Schemas and DTDs |
JSON Schema Mappings*, add the schema file, choose JSON Schema version 7, and
add the files or patterns it applies to.

The editor then completes setting names and the values of choices such as
`verify` and `post_action`, shows each setting's description and default, and
marks what goft would refuse.

## What goft checks, and when

Every command that reads the configuration — `goft` itself, `--run-once`,
`--dry-run`, `goft jobs`, `goft test` and the Windows service — checks the main
file and then each job file against the schema first. A file that breaks it is
reported whole, every problem on its line, and goft exits with status 2 without
starting anything. `goft jobs` is the quickest way to check a configuration
after editing it.

For this file:

```yaml
# goft.yaml, with five mistakes
jobs:
  - name: invoice-upload
    direction: send
    local:
      path: /data/out/invoice
    remote:
      protocol: sftp
      host: invoice-sftp
      path: /upload/invoice
      port: "22"
    include: "*.csv"
    poll_interval: 5
    post_acton: move
    move_to: /data/done/invoice
    log:
      level: INFO
```

goft says:

```console
$ goft -c /etc/goft/goft.yaml jobs
goft: invalid configuration /etc/goft/goft.yaml:
line 11: job invoice-upload: remote.port must be an integer, not a string
line 12: job invoice-upload: include must be a list, not a string
line 13: job invoice-upload: poll_interval must be a duration such as 30s, 5m or 1h30m, not 5
line 14: job invoice-upload: post_acton is not a setting (did you mean post_action?)
line 17: job invoice-upload: log.level "INFO" is invalid (debug | info | warn | warning | error)
```

The schema decides:

- which settings there are, in every section. A setting it does not know —
  misspelt, or written in capitals — is refused, with the setting it probably
  meant when one is close;
- the type of each value: text, a whole number, a number, `true` or `false`, a
  list or a section;
- the values a choice allows, and the range of a number;
- what a duration looks like: a number with a unit, `ns`, `us`, `ms`, `s`, `m`
  or `h`, such as `30s`, `5m` or `1h30m`. `0` needs no unit;
- what is required: `name`, `direction`, `local.path`, `remote.protocol` and
  `remote.path`; `remote.host` for ftp, sftp and smb; `remote.share` for smb;
  `move_to` for `post_action: move`;
- what a copy between two local directories leaves out: `host`, `port`, `user`,
  `password` and `share`.

What a schema cannot see is checked once the files have passed it, together for
every job, as before:

- a value written as `${NAME}`, which is checked once it is replaced;
- whether `local.path` exists and is a directory, and whether two paths are
  inside each other;
- the jobs against each other: names used twice, two jobs taking from one
  directory, loops, one log file rotated two ways;
- combinations of values: `post_action: move` on a `recv` job,
  `remove_empty_dirs` without `delete` or `move`.

## Writing values the schema accepts

- **Settings are written in lower case**, as in
  [goft.example.yaml](../goft.example.yaml).
- **Text that looks like a number, or like `true`, goes in quotes.** YAML reads
  `password: 1234` as a number and `user: true` as true or false, and goft wants
  text there: `password: "1234"`.
- **A list is a list**, even of one: `include: ["*.csv"]`, not
  `include: "*.csv"`.
- **A duration has a unit**: `poll_interval: 5s`, not `poll_interval: 5`.
- **`${NAME}` may stand for the value of any setting**, a number, `true` or a
  list included: `port: ${SFTP_PORT}` is accepted as written, and the value is
  checked once the variable is replaced.
- **A section may be left empty** — `service:`, `log:` or `retry:` with every
  setting under it commented out — but **a setting may not**: `port:` with
  nothing after it is refused rather than read as left out.

## Upgrading from v0.3

Until v0.3, goft read its configuration leniently. What it now refuses, it
used to accept like this:

| Written as | goft v0.3 | Now |
|---|---|---|
| a setting goft does not have, such as `post_acton: delete` | ignored without a word, so nothing was deleted | refused |
| `Name:`, `Remote:` or another setting in capitals | read as the lower-case setting | refused |
| `poll_interval: 5` and other durations without a unit | read as 5 nanoseconds | refused |
| `include: "*.csv,*.txt"`, also `exclude` and `log.fields` | split at the commas into a list | refused; write a list |
| `workers: "4"`, `enabled: "true"` | converted to a number, to true | refused; leave out the quotes |
| `password: 1234`, `user: true` | converted to text, `true` to `1` | refused; add quotes |
| `log.level: INFO`, `log.fields: [SRC]` | read in lower case | refused; write them in lower case |
| `port:` and other settings left empty | read as left out | refused; delete the line or give a value |

Check the configuration with the new goft before the service is restarted with
it:

```bash
goft -c /etc/goft/goft.yaml jobs
```
