# Authenticating with ssh-agent

sftp takes keys from ssh-agent the way `ssh` does. A key loaded into the agent
with `ssh-add` signs in without its passphrase appearing in the job file, in an
environment variable, or anywhere else goft can see — the agent does the
signing, and the private key never leaves it.

Nothing needs to be configured. When the agent is running and holds a key the
server accepts, a job with no `password` and no `private_key` connects:

```yaml
remote:
  protocol: sftp
  host: invoice-sftp
  user: uploader
  path: /upload/invoice
```

`goft test` shows which agent was found, where it was found, and how many keys
it holds:

```console
config        /etc/goft/invoice-upload.yaml                             OK
local         /data/out/invoice                                         OK  readable and writable
resolve       host = invoice-sftp                                       (yaml)
resolve       port = 22                                                 (yaml)
resolve       user = uploader                                           (yaml)
resolve       ssh_agent = /run/user/1000/ssh-agent.sock (holds 2 keys)  (env)
resolve       known_hosts = /home/svc-transfer/.ssh/known_hosts         (yaml)
connect       sftp://invoice-sftp:22/upload/invoice                     OK
recv (list)   0 entries                                                 OK
send (write)  writable                                                  OK
```

The same lines go into the log at debug level, as `resolved connection setting`
records with `field` set to `ssh_agent`.

## Where goft looks for the agent

The first of these that says anything decides:

| Order | Setting | Source shown by `goft test` |
|---|---|---|
| 1 | `use_ssh_agent: false` turns the agent off | `yaml` |
| 2 | `ssh_agent` in the job names a socket, or on Windows a named pipe | `yaml` |
| 3 | `IdentityAgent` in ssh_config | `ssh_config` |
| 4 | the `SSH_AUTH_SOCK` environment variable | `env` |
| 5 | on Windows only, `\\.\pipe\openssh-ssh-agent` | `default` |

`IdentityAgent` is read as OpenSSH reads it: a path, with `~`, `%h`, `%u` and
`%d` expanded; the word `SSH_AUTH_SOCK`, meaning that variable; `$NAME`,
meaning the variable `NAME`; or `none`, which turns the agent off for that
host. `ssh_agent` in the job expands `~` and the same tokens.

The last entry is the pipe of the OpenSSH Authentication Agent service that
comes with Windows, which is also where `ssh.exe` looks. The service is
disabled until someone starts it, so finding nothing there is normal, and goft
says nothing about it. An agent that the job, ssh_config or `SSH_AUTH_SOCK`
points at but that cannot be reached is different: that is reported as a
warning, at startup in the log and by `goft test`, and the job carries on
without the agent.

```console
warning       the ssh-agent at /run/user/1000/ssh-agent.sock cannot be used, so none of its keys are offered: dial unix /run/user/1000/ssh-agent.sock: connect: no such file or directory
```

Asking the agent is part of opening a connection, so it comes under
`connect_timeout`. An agent that accepts the question and never answers is
given up on when that runs out, and since the time was spent, so is that
connection attempt; it counts as a failure and is retried like any other.

Any agent that speaks the ssh-agent protocol on a Unix socket or a Windows
named pipe works. Pageant, for one, listens on a named pipe as well as its older
window-message interface; `pageant --openssh-config <file>` writes the
`IdentityAgent` line that points at it.

### On Windows

The pipe is opened the way `ssh.exe` opens its own, which matters twice over
beyond reaching it at all.

**The agent is not allowed to act as the account running the job.** The server
at the far end of a named pipe may normally impersonate the client that
connects to it, and `\\.\pipe\openssh-ssh-agent` is not a reserved name: on a
machine where the OpenSSH Authentication Agent service is not running — the
state it ships in — any process can create that pipe first and be taken for the
agent. goft asks for the connection at the identification level, so a server on
the other end can see which account is asking and nothing more; it cannot use
the token to open anything. A process that squatted the name still learns which
keys are wanted and which host is being signed for, and still cannot sign
without the key, so what is left to it is the part that does not matter.

**A pipe whose every instance is in use is waited for.** A named pipe serves
one client per instance and the agent creates the next only once the last has
been taken, so a client arriving in that gap is told every instance is busy
rather than asked to wait. goft opens the agent once while resolving and once
per worker, which is exactly the burst that lands in it, and a cycle lost to a
moment's contention would be a poor trade. The waiting is bounded by
`connect_timeout` like the rest of the attempt, so an agent that never frees an
instance costs one connection attempt rather than the run.

`SSH_AUTH_SOCK` left behind by a Git Bash, MSYS or Cygwin shell does not work,
here or with `ssh.exe`: those agents listen on a socket emulated inside an
ordinary file, which only their own libraries know how to connect to. Windows
reports that with the same words it uses for a socket that is not there at all,
so goft adds where the agent it can use would be:

```console
warning  the ssh-agent at C:/…/ssh-AbC123/agent.4711 cannot be used, so none of its keys are offered: dial unix C:/…/agent.4711: connect: A socket operation encountered a dead network. (the agent of an MSYS or Cygwin shell listens on a socket Windows cannot connect to; the OpenSSH agent for Windows listens on \\.\pipe\openssh-ssh-agent)
```

Clear `SSH_AUTH_SOCK`, or point `ssh_agent` at the pipe, and the OpenSSH agent
is used instead.

## Which keys are offered, in what order

Keys are offered in the order OpenSSH offers them, and a password is tried
only after all of them:

1. the agent's copy of each key file the job uses — `private_key`, or the
   `IdentityFile` entries in ssh_config, or failing those `~/.ssh/id_ed25519`
   and `~/.ssh/id_rsa`
2. the agent's other keys
3. key files the agent does not hold

Taking a key file's key from the agent is what lets a passphrase protected
key be used without its passphrase. goft matches the two by the public key: a
key in the OpenSSH format carries it in the clear, and for a key in the older
PEM format it is read from the `.pub` file beside it.

### Limiting the agent to the keys you name

A server counts every key it is offered against its limit — OpenSSH's
`MaxAuthTries` is 6 by default — and gives up with `Too many authentication
failures` once it is reached. An agent holding many keys can use that up before
the right key, or the password, is ever tried. Two settings stop the agent
offering keys the job did not name:

- **`private_key` in the job.** A job that names its key is saying which key to
  use, so only that key is offered. If the agent holds it, the agent signs and
  no passphrase is needed; if not, the file is used, and failing to unlock it is
  an error.
- **`IdentitiesOnly yes` in ssh_config.** The agent offers only the keys that
  match the `IdentityFile` entries, as it does for `ssh`.

`goft test` says when the agent holds more than it will offer:

```console
resolve       private_key = /home/svc-transfer/.ssh/id_invoice                             (yaml)
resolve       ssh_agent = /run/user/1000/ssh-agent.sock (offers 1 of the 2 keys it holds)  (env)
```

To leave the agent out altogether, set `use_ssh_agent: false`, or
`IdentityAgent none` in ssh_config.

### Keys goft finds for itself

A key from `IdentityFile` or the default locations that is passphrase
protected is offered when the agent holds it, and otherwise skipped with a
warning that says so, so the passphrase on a person's own key does not stand in
the way of a job that authenticates by password:

```
skipping the key /home/uploader/.ssh/id_ed25519: it is passphrase protected, private_key_passphrase is not set, and the ssh-agent does not hold it
```

## Setting the agent up

On Linux and macOS a desktop session usually has an agent running already.
Otherwise:

```bash
eval "$(ssh-agent)"
ssh-add ~/.ssh/id_ed25519
```

On Windows, start the OpenSSH Authentication Agent service once, from an
elevated PowerShell, and add the key as the account that will run goft:

```powershell
Get-Service ssh-agent | Set-Service -StartupType Automatic
Start-Service ssh-agent
ssh-add $env:USERPROFILE\.ssh\id_ed25519
```

The Windows agent keeps keys per account, so a job run under a service account
sees only the keys that account added, not those of the person who installed
it.

### Jobs run as a service

A job started by systemd, cron or the Windows service manager does not inherit
a login session's `SSH_AUTH_SOCK`, so on Linux it finds no agent unless it is
told where one is. Name the socket with `ssh_agent` — the agent has to run as
the same user as goft and outlive any login session — or give the job a key
file of its own with `private_key`, and its passphrase through
`private_key_passphrase: ${VAR}`.

## See also

- [README: Credentials](../README.md#credentials) — everything else goft fills
  in from ssh_config, netrc and the Credential Manager
- [What a run looks like on screen](console-output.md) — the rest of `goft test`
