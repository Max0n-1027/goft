package config

import (
	"log/slog"
	"path/filepath"
	"strings"
)

// LogValue renders the whole job configuration as one record, so that a
// transfer can be explained afterwards without the file being at hand: what was
// picked up, where it went, how it was verified and what became of the source.
//
// Defaults are included, because a setting that was never written in the file
// still decided what happened. Secrets are reported as set, never as their
// value.
func (c *Config) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("file", c.SourceFile),
		slog.String("name", c.Name),
		slog.Group("local", slog.String("path", absPath(c.Local.Path))),
		slog.Any("remote", c.Remote),
		slog.Bool("recursive", c.Recursive),
		slog.Any("include", c.Include),
		slog.Any("exclude", c.Exclude),
		slog.Int64("max_file_size_mb", c.MaxFileSizeMB),
		slog.String("poll_interval", c.PollInterval.String()),
		slog.String("stable_duration", c.StableDuration.String()),
		slog.Int("workers", c.Workers),
		slog.String("verify", string(c.Verify)),
		slog.String("on_exists", string(c.OnExists)),
		slog.String("post_action", string(c.PostAction)),
	}
	if c.MoveTo != "" {
		attrs = append(attrs, slog.String("move_to", absPath(c.MoveTo)))
	}
	attrs = append(attrs, slog.Any("retry", c.Retry), slog.Any("log", c.Log))
	return slog.GroupValue(attrs...)
}

// LogValue renders the remote side. Only the settings that were actually given
// appear, so the record shows the job rather than a wall of empty fields.
//
// Where the connection details finally came from — the file, ssh_config, netrc
// or a built-in default — is a separate question, recorded at debug level when
// the connection is made.
func (r Remote) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("protocol", string(r.Protocol)),
	}
	if r.IsLocal() {
		// No host to name, and the path is on this machine, so it is recorded
		// in full like every other local path.
		attrs = append(attrs, slog.String("path", absPath(r.Path)))
	} else {
		attrs = append(attrs, slog.String("host", r.Host), slog.String("path", r.Path))
	}
	add := func(key, value string) {
		if value != "" {
			attrs = append(attrs, slog.String(key, value))
		}
	}
	if r.Port != 0 {
		attrs = append(attrs, slog.Int("port", r.Port))
	}
	add("user", r.User)
	if r.Password.IsSet() {
		// Secret masks itself, so this records that a password was configured
		// without recording the password.
		attrs = append(attrs, slog.Any("password", r.Password))
	}
	add("private_key", absPath(r.PrivateKey))
	if r.PrivateKeyPassphrase.IsSet() {
		attrs = append(attrs, slog.Any("private_key_passphrase", r.PrivateKeyPassphrase))
	}
	add("known_hosts", absPath(r.KnownHosts))
	if r.InsecureSkipHostKeyCheck {
		attrs = append(attrs, slog.Bool("insecure_skip_host_key_check", true))
	}
	if !r.SSHConfigEnabled() {
		attrs = append(attrs, slog.Bool("use_ssh_config", false))
	}
	add("ssh_config_file", absPath(r.SSHConfigFile))
	if !r.NetrcEnabled() {
		attrs = append(attrs, slog.Bool("use_netrc", false))
	}
	add("netrc_file", absPath(r.NetrcFile))
	add("share", r.Share)
	add("domain", r.Domain)
	return slog.GroupValue(attrs...)
}

// LogValue renders the retry settings.
func (r Retry) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Int("max_attempts", r.MaxAttempts),
		slog.String("interval", r.Interval.String()),
		slog.Float64("backoff", r.Backoff),
	)
}

// LogValue renders the logging settings, including the field selection that
// decided what the surrounding records look like.
func (l Log) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.String("path", absPath(l.Path)),
		slog.String("level", l.Level),
		slog.String("rotation", string(l.Rotation)),
		slog.Int("max_size_mb", l.MaxSizeMB),
		slog.Int("max_backups", l.MaxBackups),
		slog.Int("max_age_days", l.MaxAgeDays),
		slog.Bool("compress", l.Compress),
	}
	if l.Fields != nil {
		attrs = append(attrs, slog.Any("fields", l.Fields))
	}
	return slog.GroupValue(attrs...)
}

// absPath makes a configured path absolute for the record. A relative path is
// meaningless to whoever reads the log later, who has no idea which directory
// the command was run from.
//
// A leading ~ is left alone: it is expanded when the connection is made, and
// rewriting it here would report a path that was never configured.
func absPath(p string) string {
	if p == "" || strings.HasPrefix(p, "~") {
		return p
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
