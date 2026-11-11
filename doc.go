/*
Goft watches directories and moves files over FTP, SFTP or SMB, or between two
directories on the same machine, verifying every file it transfers. One process
runs every job in its configuration, as a service, until it is stopped.

Usage:

	goft [flags]                    run every enabled job until stopped
	goft --run-once <job> [flags]   run a job once; repeat for several
	goft <command> [flags]

The commands are:

	jobs                  list the jobs in the configuration
	test [job...]         check the configuration and each job's connection
	                      without transferring anything
	service install       register goft as a Windows service (Windows only)
	service uninstall     stop and remove the Windows service (Windows only)
	schema                print the JSON Schema of the configuration file
	version               print the build information

Each job moves files one way: a send job from a local directory to the other
side, a recv job from the other side to a local directory. The other side is a
server reached over ftp, sftp or smb, or — with protocol: local — a second
directory on this machine. The engine does not know which: everything below is
the same either way.

For every file it finds on the sending side, goft waits until the file has
stopped changing, writes it to the receiving side under a temporary name,
verifies it, renames it onto its final name only once verification passed, and
then applies the configured post-transfer action to the source. With
remove_empty_dirs it also removes a sending directory that the transfer took the
last file out of, and its parent if that leaves it empty; the sending root
itself is never removed. A file therefore only appears under its real name on
the receiving side once it is complete and verified; a process killed
mid-transfer leaves nothing behind but a .goft.tmp file, which the next run
overwrites. A retry writes under a name of its own, <name>.2.goft.tmp, since the
attempt it replaces may still be in progress at the other end.

Writing onto a Windows disk — a recv job, or a send job with protocol: local —
refuses a file whose name that platform would store as something else: a colon,
which opens an NTFS alternate data stream, a trailing dot or space, which is
dropped, or a reserved device name. Such a file is recorded as failed rather
than written, because reading it back would find it again and verification
would pass over a file no other program can see. Two files whose names differ
only in case are not sent either when the destination cannot tell them apart;
remote.case_insensitive says so for a server goft cannot ask, such as OpenSSH
on Windows.

# Configuration

One YAML file describes the service and its jobs. job_files names more files,
each holding a jobs: list and nothing else; a pattern may match nothing, while
a name written out in full must exist.

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
	      host: invoice-sftp        # a ~/.ssh/config Host alias works here
	      path: /upload/invoice
	    include: ["*.csv"]
	    post_action: move
	    move_to: /data/done/invoice
	    log:
	      path: /var/log/goft/invoice-upload.log

A job needs a name, unique ignoring case, and a direction, send or recv;
enabled: false keeps the service from running it while leaving it runnable by
name. A relative path is resolved against the directory of the file it is
written in. Every job is checked at startup, alone and against the others —
two jobs taking from one directory when either deletes or moves what it sends,
or jobs passing files round in a loop, are refused — and one mistake stops goft
starting at all.

Before that, every file is checked against the JSON Schema of the
configuration, which "goft schema" prints and schema/goft.schema.json holds: a
setting goft does not have, misspelt or in capitals, a value of the wrong type
or a choice that does not exist is reported on its line, and stops goft too. An
editor that understands JSON Schema can check the files against it as they are
written; docs/schema.md shows how.

Credentials need not be written in plain text. Any ${VAR} in a value is
replaced from the environment, and one that is not set is an error; a $ not
followed by a brace is an ordinary character. sftp fills in whatever is left
out from ~/.ssh/config, and ftp does the same from ~/.netrc
(%USERPROFILE%\.netrc on Windows); sftp also takes keys from ssh-agent, as ssh
does. On Windows a generic credential registered as goft:<protocol>://<host>
supplies the user and password for any protocol, and is read before those
files:

	cmdkey /generic:goft:ftp://invoice-ftp /user:uploader /pass:secret

Run "goft test" to see the values that were resolved and where each one came
from.

goft.example.yaml documents every setting, README.md covers the behaviour worth
knowing before putting a job into service, and docs/service.md how the service
runs its jobs.

# The service

Without --run-once, goft runs every enabled job side by side, each watching its
own directory, until Ctrl+C, SIGTERM or the Windows service manager stops it.
Every job starts or none does. The service writes its own log, service.log,
recording which configuration and jobs it started with and when it stopped;
each job writes to its own log.

Every job holds a lock while it runs, so the same job is never run by two
processes at once: a --run-once of a job the service is running is refused with
exit status 2. Locks are kept in service.lock_dir, by default
%ProgramData%\goft\locks on Windows and /tmp/goft-<uid> elsewhere.

On Windows, "goft service install" registers goft with the service manager, to
run as LocalSystem, start with Windows and restart a minute after it stops on
an error. A service has no console, so every log must name a file; starting and
stopping are written to the event log.

# Flags

	-c, --config     path to the configuration file
	    --run-once   run the named job once and exit; repeat for several
	    --dry-run    list what would be transferred, without touching the
	                 destination: for the --run-once jobs, or every enabled job
	    --log-level  override log.level of the service and every job
	                 (debug | info | warn | error)
	    --console    force human readable output on stdout
	    --no-console suppress human readable output on stdout
	    --lang       language to write in, en or ja

# Output

The JSON Lines log is the record of what happened; the human readable output on
stdout is for whoever is watching. --run-once prints it by default and the
service does not. When no log file is configured and the console output is on,
the JSON log goes to stderr so that the two never mix.

What goft says to a person — the console, goft test, help and error messages —
is in English or Japanese: --lang decides, then GOFT_LANG, then the locale
(LANGUAGE, LC_ALL, LC_MESSAGES, LANG), then on Windows the account's display
language, and anything else is English. The log is in English whatever the
language, so that the same event is always written the same way.

# Exit status

	0  finished normally, including the service stopping on a signal
	1  the run completed but at least one file failed
	2  the run could not be completed: bad configuration, the connection failed,
	   the job was locked by another goft, or it was stopped before it finished

With several --run-once, the worst of them decides. goft test exits 2 when a
job it checked cannot be used as configured; each job is checked in the
direction it runs, so a read-only account is a good setup for a recv job.

Ctrl+C or SIGTERM stops every job from starting anything new; a file already
being transferred is finished first.
*/
package main
