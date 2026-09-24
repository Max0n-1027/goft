/*
Package config loads and validates a goft job configuration.

One file describes exactly one job, and one job runs in one process. The
transfer direction is not part of the file: it is chosen by the command, so the
same settings can drive an upload or a download.

	name: invoice-upload
	local:
	  path: /data/out/invoice
	remote:
	  protocol: sftp
	  host: invoice-sftp
	  path: /upload/invoice
	include: ["*.csv"]
	verify: hash
	on_exists: skip
	post_action: move
	move_to: /data/done/invoice

[Load] reads a file, replaces ${NAME} in its values from the environment and
fills in the defaults. [Validate] then reports everything wrong with it at once, and
[ValidateForDirection] adds the checks that only make sense once the command is
known. Credentials are deliberately not checked here, because the Windows
Credential Manager, ~/.netrc or ~/.ssh/config may still supply them.

goft.example.yaml documents every setting in full.
*/
package config

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Protocol identifies a remote transport.
type Protocol string

// Supported remote protocols.
//
// ProtocolLocal is not remote at all: it names a second directory on this
// machine, which is what makes a copy between two local directories a job like
// any other rather than a special case in the engine.
const (
	ProtocolFTP   Protocol = "ftp"
	ProtocolSFTP  Protocol = "sftp"
	ProtocolSMB   Protocol = "smb"
	ProtocolLocal Protocol = "local"
)

// Verify selects how a transferred file is checked.
type Verify string

// Verification methods.
const (
	VerifyHash   Verify = "hash"
	VerifyLength Verify = "length"
	VerifyNone   Verify = "none"
)

// PostAction selects what happens to the source file after a transfer.
type PostAction string

// Post-transfer actions.
const (
	PostNone   PostAction = "none"
	PostDelete PostAction = "delete"
	PostMove   PostAction = "move"
)

// OnExists selects what happens when the destination file already exists.
type OnExists string

// Destination collision handling.
const (
	OnExistsSkip      OnExists = "skip"
	OnExistsOverwrite OnExists = "overwrite"
)

// Rotation selects how the log file is split.
type Rotation string

// Log rotation modes.
const (
	RotationSize    Rotation = "size"
	RotationDaily   Rotation = "daily"
	RotationMonthly Rotation = "monthly"
)

// Local describes the local side of a job.
type Local struct {
	// Path is the directory goft reads from or writes to. It must exist.
	Path string `mapstructure:"path"`
}

// Remote describes the server side of a job, including its credentials.
//
// Anything left out may be filled in from somewhere the platform already keeps
// it: ~/.ssh/config for sftp, ~/.netrc for ftp, and on Windows a generic
// Credential Manager entry registered for goft, whatever the protocol. What is
// set here always wins over all of them.
type Remote struct {
	// Protocol is ftp, sftp or smb.
	Protocol Protocol `mapstructure:"protocol"`
	// Host is the server. For sftp it may be a ~/.ssh/config Host alias, in
	// which case the real name comes from that file's HostName.
	Host string `mapstructure:"host"`
	// Port defaults to 21, 22 or 445 depending on the protocol.
	Port int `mapstructure:"port"`
	// Path is the directory on the server. For smb it is relative to Share and
	// must not repeat it.
	Path string `mapstructure:"path"`
	// User and Password authenticate the connection.
	User     string `mapstructure:"user"`
	Password Secret `mapstructure:"password"`

	// PrivateKey is the sftp key file, and PrivateKeyPassphrase unlocks it if
	// it has one. ssh-agent is not used.
	PrivateKey           string `mapstructure:"private_key"`
	PrivateKeyPassphrase Secret `mapstructure:"private_key_passphrase"`
	// KnownHosts verifies the sftp host key. Connecting without it requires
	// InsecureSkipHostKeyCheck, which is never assumed.
	KnownHosts               string `mapstructure:"known_hosts"`
	InsecureSkipHostKeyCheck bool   `mapstructure:"insecure_skip_host_key_check"`
	// UseSSHConfig turns the ~/.ssh/config lookup off, and SSHConfigFile
	// replaces the usual search with one named file. Nil means enabled.
	UseSSHConfig  *bool  `mapstructure:"use_ssh_config"`
	SSHConfigFile string `mapstructure:"ssh_config_file"`

	// UseNetrc and NetrcFile do the same for the ftp credentials lookup.
	UseNetrc  *bool  `mapstructure:"use_netrc"`
	NetrcFile string `mapstructure:"netrc_file"`

	// UseCredentialManager turns off the Windows Credential Manager lookup,
	// and CredentialTarget names the entry to read instead of the default
	// goft:<protocol>://<host>. Nil means enabled, and the lookup finds
	// nothing on other platforms.
	UseCredentialManager *bool  `mapstructure:"use_credential_manager"`
	CredentialTarget     string `mapstructure:"credential_target"`

	// Share is the smb share to mount, and Domain the NTLM domain.
	Share  string `mapstructure:"share"`
	Domain string `mapstructure:"domain"`
}

// IsLocal reports whether the far side is another directory on this machine.
func (r Remote) IsLocal() bool { return r.Protocol == ProtocolLocal }

// Describe returns the remote location for logs and console output, without
// opening a connection. The share is part of an SMB location, so leaving it out
// would point at the wrong place.
func (r Remote) Describe() string {
	if r.IsLocal() {
		// A path on this machine is clearer as itself than dressed up as a URL.
		return absPath(r.Path)
	}
	loc := string(r.Protocol) + "://" + r.Host
	if r.Protocol == ProtocolSMB && r.Share != "" {
		loc += "/" + r.Share
	}
	if r.Path != "" && r.Path != "/" {
		loc += path.Join("/", r.Path)
	}
	return loc
}

// SSHConfigEnabled reports whether ~/.ssh/config should be consulted.
func (r Remote) SSHConfigEnabled() bool { return r.UseSSHConfig == nil || *r.UseSSHConfig }

// NetrcEnabled reports whether ~/.netrc should be consulted.
func (r Remote) NetrcEnabled() bool { return r.UseNetrc == nil || *r.UseNetrc }

// CredentialManagerEnabled reports whether the Windows Credential Manager
// should be consulted.
func (r Remote) CredentialManagerEnabled() bool {
	return r.UseCredentialManager == nil || *r.UseCredentialManager
}

// Retry controls how often a failed file is attempted again within one cycle.
//
// A retry only happens when the error looks like something that could succeed
// next time; see engine.Retryable. Between attempts the connection is rebuilt,
// because a dropped connection is the most common reason to be here and
// retrying over the dead one would fail identically.
type Retry struct {
	// MaxAttempts counts the first try, so 1 disables retrying.
	MaxAttempts int `mapstructure:"max_attempts"`
	// Interval is the wait before the first retry.
	Interval time.Duration `mapstructure:"interval"`
	// Backoff multiplies the wait after each attempt. 1 keeps it constant.
	Backoff float64 `mapstructure:"backoff"`
}

// Wait returns how long to pause before the given attempt (1 is the first
// retry).
func (r Retry) Wait(attempt int) time.Duration {
	d := float64(r.Interval)
	for i := 1; i < attempt; i++ {
		d *= r.Backoff
	}
	return time.Duration(d)
}

// Log describes where the record goes, how much of it is written, and how the
// file is split up over time.
type Log struct {
	// Path is the log file. When empty the log goes to stdout, or to stderr if
	// the human readable console output is using stdout.
	Path string `mapstructure:"path"`
	// Level is debug, info, warn or error. It decides which records are
	// written; Fields decides what each transfer record carries.
	Level string `mapstructure:"level"`
	// Fields selects the parameters written for each transferred file. A nil
	// slice means the default set; an empty one means none of them.
	Fields []string `mapstructure:"fields"`
	// Rotation splits the file by size, by day or by month.
	Rotation Rotation `mapstructure:"rotation"`
	// MaxSizeMB starts a new file once the current one grows past it. It
	// applies to every rotation mode.
	MaxSizeMB int `mapstructure:"max_size_mb"`
	// MaxBackups and MaxAgeDays bound how many old files are kept and how long.
	MaxBackups int `mapstructure:"max_backups"`
	MaxAgeDays int `mapstructure:"max_age_days"`
	// Compress gzips the files that have been rotated away.
	Compress bool `mapstructure:"compress"`
}

// Config is one job: which files, between where and where, and what happens to
// them afterwards.
//
// The filtering and timing fields — Recursive through Workers — apply to
// whichever side is sending, so the same settings describe an upload and a
// download.
type Config struct {
	// Name identifies the job in every log record. It defaults to the
	// configuration file name without its extension.
	Name string `mapstructure:"name"`
	// Local is the source for send and the destination for recv.
	Local Local `mapstructure:"local"`
	// Remote is the other end, whichever direction is being run.
	Remote Remote `mapstructure:"remote"`

	// Recursive descends into subdirectories, recreating the tree on the
	// receiving side.
	Recursive bool `mapstructure:"recursive"`
	// Include and Exclude are glob patterns matched against the file name. A
	// file must match one of Include and none of Exclude; Exclude also prunes
	// directories when Recursive is set.
	Include []string `mapstructure:"include"`
	Exclude []string `mapstructure:"exclude"`
	// MaxFileSizeMB skips anything larger. Zero means no limit.
	MaxFileSizeMB int64 `mapstructure:"max_file_size_mb"`
	// PollInterval is the pause between cycles; it only affects serve.
	PollInterval time.Duration `mapstructure:"poll_interval"`
	// StableDuration is how long a file's size and modification time must stay
	// unchanged before it counts as finished being written.
	StableDuration time.Duration `mapstructure:"stable_duration"`
	// Workers is how many files move at once, and therefore how many
	// connections a cycle opens to each side.
	Workers int `mapstructure:"workers"`

	// Verify is how a transferred file is checked: hash, length or none.
	Verify Verify `mapstructure:"verify"`
	// OnExists decides what to do about a destination file that is already
	// there: leave it alone, or replace it unless the two already match.
	OnExists OnExists `mapstructure:"on_exists"`
	// PostAction is what becomes of the source file once it has arrived.
	PostAction PostAction `mapstructure:"post_action"`
	// MoveTo receives the source file when PostAction is move. It must not be
	// inside Local.Path, or the moved files would be transferred again.
	MoveTo string `mapstructure:"move_to"`
	// RemoveEmptyDirs deletes a subdirectory of the sending side once the
	// transfer has taken the last file out of it. The sending root itself is
	// never removed, and neither is a directory goft did not empty.
	RemoveEmptyDirs bool `mapstructure:"remove_empty_dirs"`

	// Retry decides how a failed file is attempted again.
	Retry Retry `mapstructure:"retry"`
	// Log decides where the record goes, how detailed it is and what each
	// transfer record carries.
	Log Log `mapstructure:"log"`

	// SourceFile is the path the configuration was read from. It is filled in
	// by [Load] rather than read from the file.
	SourceFile string
}

// Defaults are applied to every key the configuration file does not set.
func defaults(v *viper.Viper) {
	v.SetDefault("recursive", false)
	v.SetDefault("include", []string{"*"})
	v.SetDefault("exclude", []string{"*.tmp", "*.part", ".*"})
	v.SetDefault("max_file_size_mb", 0)
	v.SetDefault("poll_interval", "5s")
	v.SetDefault("stable_duration", "3s")
	v.SetDefault("workers", 1)
	v.SetDefault("verify", string(VerifyHash))
	v.SetDefault("on_exists", string(OnExistsSkip))
	v.SetDefault("post_action", string(PostNone))
	v.SetDefault("remove_empty_dirs", false)
	v.SetDefault("retry.max_attempts", 3)
	v.SetDefault("retry.interval", "2s")
	v.SetDefault("retry.backoff", 2.0)
	v.SetDefault("log.level", "info")
	v.SetDefault("log.rotation", string(RotationSize))
	v.SetDefault("log.max_size_mb", 100)
	v.SetDefault("log.max_backups", 7)
	v.SetDefault("log.max_age_days", 30)
	v.SetDefault("log.compress", true)
}

// Load reads path, replaces ${NAME} in its values from the environment and
// decodes the result. A variable that is not set is an error. Validation is
// performed separately by [Validate].
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	defaults(v)
	if err := v.ReadConfig(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := expandEnv(v); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	// Stored absolute: the log has to identify the file long after whatever
	// working directory the command was run from is forgotten.
	if abs, err := filepath.Abs(path); err == nil {
		c.SourceFile = abs
	} else {
		c.SourceFile = path
	}

	if c.Name == "" {
		base := filepath.Base(path)
		c.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}

	// Paths on this machine are resolved once, here. Every transfer record
	// names both ends in full, because a path relative to a working directory
	// nobody remembers identifies nothing when the log is read months later,
	// and those records are built from these values.
	c.Local.Path = absPath(c.Local.Path)
	c.MoveTo = absPath(c.MoveTo)
	c.Log.Path = absPath(c.Log.Path)
	if c.Remote.IsLocal() {
		c.Remote.Path = absPath(c.Remote.Path)
	}
	return &c, nil
}
