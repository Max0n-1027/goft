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

[Load] reads a file, expands any ${VAR} from the environment and fills in the
defaults. [Validate] then reports everything wrong with it at once, and
[ValidateForDirection] adds the checks that only make sense once the command is
known. Credentials for ftp and sftp are deliberately not checked here, because
~/.netrc and ~/.ssh/config may still supply them.

goft.example.yaml documents every setting in full.
*/
package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Secret is a string that never reveals itself through fmt or [log/slog].
// Use string(s) to obtain the actual value.
type Secret string

const redacted = "REDACTED"

// String masks the value so that accidental formatting cannot leak it.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redacted
}

// LogValue masks the value for slog.
func (s Secret) LogValue() slog.Value {
	return slog.StringValue(s.String())
}

// IsSet reports whether the secret holds a value.
func (s Secret) IsSet() bool { return s != "" }

// Protocol identifies a remote transport.
type Protocol string

// Supported remote protocols.
const (
	ProtocolFTP  Protocol = "ftp"
	ProtocolSFTP Protocol = "sftp"
	ProtocolSMB  Protocol = "smb"
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
// Anything left out may be filled in from a file the protocol already has a
// convention for: ~/.ssh/config for sftp and ~/.netrc for ftp. What is set here
// always wins over those. SMB reads no such file, because the credentials
// Windows stores for network shares cannot be read back.
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

	// Share is the smb share to mount, and Domain the NTLM domain.
	Share  string `mapstructure:"share"`
	Domain string `mapstructure:"domain"`
}

// Describe returns the remote location for logs and console output, without
// opening a connection. The share is part of an SMB location, so leaving it out
// would point at the wrong place.
func (r Remote) Describe() string {
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

// Names of the parameters a per-file transfer record can carry. They are the
// vocabulary of log.fields, which is why they live with the configuration
// rather than with the logger.
const (
	FieldSrc        = "src"
	FieldDst        = "dst"
	FieldProtocol   = "protocol"
	FieldBytes      = "bytes"
	FieldDurationMS = "duration_ms"
	FieldVerify     = "verify"
	FieldResult     = "result"
	FieldReason     = "reason"
	FieldError      = "error"
	FieldHashSrc    = "hash_src"
	FieldHashDst    = "hash_dst"
	FieldAttempt    = "attempt"
	FieldRateMiBs   = "rate_mibs"
)

// SelectableLogFields is everything log.fields accepts, in the order it is
// reported back to the user.
var SelectableLogFields = []string{
	FieldSrc, FieldDst, FieldProtocol, FieldBytes, FieldDurationMS,
	FieldVerify, FieldResult, FieldReason, FieldError,
	FieldHashSrc, FieldHashDst, FieldAttempt, FieldRateMiBs,
}

// DefaultLogFields applies when log.fields is not set.
//
// Everything but the transfer rate, which is a performance question rather than
// a record of what happened.
var DefaultLogFields = []string{
	FieldSrc, FieldDst, FieldProtocol, FieldBytes, FieldDurationMS,
	FieldVerify, FieldResult, FieldReason, FieldError,
	FieldHashSrc, FieldHashDst, FieldAttempt,
}

// LogFieldSet answers whether a field should be written.
type LogFieldSet map[string]bool

// Has reports whether the field is wanted.
func (s LogFieldSet) Has(field string) bool { return s[field] }

// LogFields resolves log.fields into a set.
//
// Record identity — time, level, msg, job, direction and event — is never
// selectable: a line missing those cannot be interpreted at all.
//
// When the configuration names no fields the default set applies, and at debug
// level it also picks up the transfer rate, which is what debug meant before
// the set became configurable. Naming fields explicitly replaces all of that:
// the list is then exactly what gets written.
func (c *Config) LogFields(level slog.Level) LogFieldSet {
	if c.Log.Fields != nil {
		set := make(LogFieldSet, len(c.Log.Fields))
		for _, n := range c.Log.Fields {
			set[strings.ToLower(strings.TrimSpace(n))] = true
		}
		return set
	}

	set := make(LogFieldSet, len(DefaultLogFields)+1)
	for _, n := range DefaultLogFields {
		set[n] = true
	}
	if level <= slog.LevelDebug {
		set[FieldRateMiBs] = true
	}
	return set
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

// Load reads path, expands ${ENV} references and decodes the result.
// Validation is performed separately by [Validate].
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	v := viper.New()
	v.SetConfigType("yaml")
	defaults(v)
	if err := v.ReadConfig(bytes.NewReader([]byte(os.ExpandEnv(string(raw))))); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	var c Config
	if err := v.Unmarshal(&c); err != nil {
		return nil, fmt.Errorf("decode config %s: %w", path, err)
	}
	c.SourceFile = path

	if c.Name == "" {
		base := filepath.Base(path)
		c.Name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	return &c, nil
}
