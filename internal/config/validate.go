package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Direction is the transfer direction, chosen by the command rather than by
// the configuration file.
type Direction string

// Transfer directions.
const (
	DirSend Direction = "send" // local -> remote
	DirRecv Direction = "recv" // remote -> local
)

// Arrow returns the symbol used in human readable output.
func (d Direction) Arrow() string {
	if d == DirRecv {
		return "<-"
	}
	return "->"
}

func oneOf[T ~string](v T, allowed ...T) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func quoted[T ~string](vals []T) string {
	out := make([]string, len(vals))
	for i, v := range vals {
		out[i] = string(v)
	}
	return strings.Join(out, " | ")
}

// Validate checks everything that can be decided from the configuration file
// alone. All problems are reported together rather than one at a time.
//
// Credentials are deliberately not checked here: they may be supplied by the
// Windows Credential Manager, ~/.netrc or ~/.ssh/config, so they can only be
// validated once the connection parameters have been resolved.
func Validate(c *Config) error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	validateLocal(c, add)
	validateRemote(c, add)
	validateTransfer(c, add)
	validateScanning(c, add)
	validateLog(c, add)

	return errors.Join(errs...)
}

// addFunc collects one problem. Every check takes it so that a configuration
// with several mistakes reports all of them at once.
type addFunc func(format string, args ...any)

func validateLocal(c *Config, add addFunc) {
	switch fi, err := os.Stat(c.Local.Path); {
	case c.Local.Path == "":
		add("local.path is required")
	case err != nil:
		add("local.path %q: %v", c.Local.Path, err)
	case !fi.IsDir():
		add("local.path %q is not a directory", c.Local.Path)
	}
}

func validateRemote(c *Config, add addFunc) {
	if c.Remote.Host == "" {
		add("remote.host is required")
	}
	if c.Remote.Path == "" {
		add("remote.path is required")
	}
	if c.Remote.Port < 0 || c.Remote.Port > 65535 {
		add("remote.port %d is out of range", c.Remote.Port)
	}

	protocols := []Protocol{ProtocolFTP, ProtocolSFTP, ProtocolSMB}
	switch {
	case c.Remote.Protocol == "":
		add("remote.protocol is required (%s)", quoted(protocols))
	case !oneOf(c.Remote.Protocol, protocols...):
		add("remote.protocol %q is invalid (%s)", c.Remote.Protocol, quoted(protocols))
	case c.Remote.Protocol == ProtocolSMB:
		// The share is the one thing no credential store can supply.
		if c.Remote.Share == "" {
			add("remote.share is required for protocol smb")
		}
	}
}

func validateTransfer(c *Config, add addFunc) {
	if !oneOf(c.Verify, VerifyHash, VerifyLength, VerifyNone) {
		add("verify %q is invalid (%s)", c.Verify, quoted([]Verify{VerifyHash, VerifyLength, VerifyNone}))
	}
	if !oneOf(c.OnExists, OnExistsSkip, OnExistsOverwrite) {
		add("on_exists %q is invalid (%s)", c.OnExists, quoted([]OnExists{OnExistsSkip, OnExistsOverwrite}))
	}
	if !oneOf(c.PostAction, PostNone, PostDelete, PostMove) {
		add("post_action %q is invalid (%s)", c.PostAction, quoted([]PostAction{PostNone, PostDelete, PostMove}))
	}

	if c.PostAction == PostMove {
		switch inside, err := isInside(c.Local.Path, c.MoveTo); {
		case c.MoveTo == "":
			add("move_to is required when post_action is move")
		case err != nil:
			add("move_to %q: %v", c.MoveTo, err)
		case inside:
			add("move_to %q must not be inside local.path %q: moved files would be picked up again on the next cycle",
				c.MoveTo, c.Local.Path)
		}
	}

	if c.Retry.MaxAttempts < 1 {
		add("retry.max_attempts must be >= 1 (1 disables retrying)")
	}
	if c.Retry.Interval < 0 {
		add("retry.interval must be >= 0")
	}
	if c.Retry.Backoff < 1 {
		add("retry.backoff must be >= 1")
	}
}

func validateScanning(c *Config, add addFunc) {
	if c.MaxFileSizeMB < 0 {
		add("max_file_size_mb must be >= 0")
	}
	if c.Workers < 1 {
		add("workers must be >= 1")
	}
	if c.PollInterval <= 0 {
		add("poll_interval must be > 0")
	}
	if c.StableDuration < 0 {
		add("stable_duration must be >= 0")
	}
}

func validateLog(c *Config, add addFunc) {
	if !oneOf(c.Log.Rotation, RotationSize, RotationDaily, RotationMonthly) {
		add("log.rotation %q is invalid (%s)", c.Log.Rotation, quoted([]Rotation{RotationSize, RotationDaily, RotationMonthly}))
	}
	if _, err := ParseLevel(c.Log.Level); err != nil {
		add("log.level: %v", err)
	}
	for _, f := range c.Log.Fields {
		if !oneOf(strings.ToLower(strings.TrimSpace(f)), SelectableLogFields...) {
			add("log.fields: %q is not a field (%s)", f, strings.Join(SelectableLogFields, " | "))
		}
	}
}

// ValidateForDirection checks the combinations that only make sense once the
// command, and therefore the direction, is known.
func ValidateForDirection(c *Config, dir Direction) error {
	if dir == DirRecv && c.PostAction == PostMove {
		return fmt.Errorf("post_action: move is only supported for send; use none or delete for recv")
	}
	return nil
}

// Warnings returns non-fatal configuration remarks worth logging at startup.
func Warnings(c *Config) []string {
	var w []string
	if c.StableDuration == 0 {
		w = append(w, "stable_duration is 0: files that are still being written may be transferred")
	}
	if c.Remote.InsecureSkipHostKeyCheck {
		w = append(w, "insecure_skip_host_key_check is enabled: the remote host key is not verified")
	}
	return w
}

// isInside reports whether child is parent or lives underneath it.
func isInside(parent, child string) (bool, error) {
	if parent == "" || child == "" {
		return false, nil
	}
	ap, err := filepath.Abs(parent)
	if err != nil {
		return false, err
	}
	ac, err := filepath.Abs(child)
	if err != nil {
		return false, err
	}
	rel, err := filepath.Rel(ap, ac)
	if err != nil {
		// Different volumes on Windows: definitely not inside.
		return false, nil
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}
