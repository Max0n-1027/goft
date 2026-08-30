/*
Goft watches a directory and moves files over FTP, SFTP or SMB, or between two
directories on the same machine, verifying every file it transfers.

Usage:

	goft <command> [flags]

The commands are:

	send         transfer from the local directory to the other side once
	recv         transfer from the other side to the local directory once
	serve send   watch the local directory and transfer continuously
	serve recv   watch the other side and transfer continuously
	test         check the configuration and the connection without transferring
	version      print the build information

The other side is a server reached over ftp, sftp or smb, or — with
protocol: local — a second directory on this machine. The engine does not know
which: everything below is the same either way.

For every file it finds on the sending side, goft waits until the file has
stopped changing, writes it to the receiving side under a temporary name,
verifies it, renames it onto its final name only once verification passed, and
then applies the configured post-transfer action to the source. With
remove_empty_dirs it also removes a sending directory that the transfer took the
last file out of, and its parent if that leaves it empty; the sending root
itself is never removed. A file
therefore only appears under its real name on the receiving side once it is
complete and verified; a process killed mid-transfer leaves nothing behind but
a .goft.tmp file, which the next run overwrites.

Receiving onto Windows refuses a file whose name that platform would store as
something else — a colon, which opens an NTFS alternate data stream, a trailing
dot or space, which is dropped, or a reserved device name. Such a file is
recorded as failed rather than written, because reading it back would find it
again and verification would pass over a file no other program can see.

# Configuration

One YAML file describes one job, and one job runs in one process. To run several
jobs, give each its own file and its own process. The transfer direction is not
part of the file: it is chosen by the command.

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

Credentials need not be written in plain text. Any ${VAR} in the file is
replaced from the environment, sftp fills in whatever is left out from
~/.ssh/config, and ftp does the same from ~/.netrc (%USERPROFILE%\.netrc on
Windows). On Windows a generic credential registered as goft:<protocol>://<host>
supplies the user and password for any protocol, and is read before those files:

	cmdkey /generic:goft:ftp://invoice-ftp /user:uploader /pass:secret

Run "goft test" to see the values that were resolved and where each one came
from.

goft.example.yaml documents every setting, and README.md covers the behaviour
worth knowing before putting a job into service.

# Flags

	-c, --config     path to the job configuration file
	    --log-level  override log.level (debug | info | warn | error)
	    --log-file   override log.path
	    --console    force human readable output on stdout
	    --no-console suppress human readable output on stdout
	    --dry-run    list what would be transferred, without touching the
	                 destination (send, recv and serve)

# Output

The JSON Lines log is the record of what happened; the human readable output on
stdout is for whoever is watching. Single runs print it by default and serve
does not. When no log file is configured and the console output is on, the JSON
log goes to stderr so that the two never mix.

# Exit status

	0  finished normally, including serve stopping on a signal
	1  the run completed but at least one file failed
	2  the run could not be completed: bad configuration, or the connection failed
*/
package main
