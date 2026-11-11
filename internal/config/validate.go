package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"goft/internal/i18n"
)

// Direction is the way a job moves files: send, from the local side to the
// remote, or recv, the other way.
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

// Validate checks everything that can be decided from the configuration
// alone: the service settings, each job on its own, and the jobs against each
// other. All problems are reported together rather than one at a time, and a
// problem with a job names the job.
//
// Credentials are deliberately not checked here: they may be supplied by the
// Windows Credential Manager, ~/.netrc or ~/.ssh/config, so they can only be
// validated once the connection parameters have been resolved.
func Validate(c *Config) error {
	var errs []error
	add := func(err error) { errs = append(errs, err) }

	if len(c.Jobs) == 0 {
		add(i18n.Errorf("no jobs are configured in %s", c.SourceFile))
	}
	validateLog(c.Service.Log, "service.log", add)
	for i, j := range c.Jobs {
		label := j.Name
		if strings.TrimSpace(label) == "" {
			label = fmt.Sprintf("#%d", i+1)
		}
		validateJob(j, func(err error) {
			add(i18n.Errorf("job %s: %w", label, err))
		})
	}
	validateAcrossJobs(c, add)

	return errors.Join(errs...)
}

// validateJob checks one job on its own.
func validateJob(j *Job, add addFunc) {
	if strings.TrimSpace(j.Name) == "" {
		add(i18n.Errorf("name is required"))
	} else if strings.ContainsFunc(j.Name, unicode.IsControl) {
		add(i18n.Errorf("name %q contains a control character", j.Name))
	}
	switch {
	case j.Direction == "":
		add(i18n.Errorf("direction is required (%s)", quoted([]Direction{DirSend, DirRecv})))
	case !oneOf(j.Direction, DirSend, DirRecv):
		add(i18n.Errorf("direction %q is invalid (%s)", j.Direction, quoted([]Direction{DirSend, DirRecv})))
	case j.Direction == DirRecv && j.PostAction == PostMove:
		add(i18n.Errorf("post_action: move is only supported for send; use none or delete for recv"))
	}

	validateLocal(j, add)
	validateRemote(j, add)
	validateTransfer(j, add)
	validateScanning(j, add)
	validateLog(j.Log, "log", add)
}

// addFunc collects one problem. Every check takes it so that a configuration
// with several mistakes reports all of them at once.
type addFunc func(err error)

func validateLocal(c *Job, add addFunc) {
	switch fi, err := os.Stat(c.Local.Path); {
	case c.Local.Path == "":
		add(i18n.Errorf("local.path is required"))
	case err != nil:
		add(i18n.Errorf("local.path %q: %v", c.Local.Path, err))
	case !fi.IsDir():
		add(i18n.Errorf("local.path %q is not a directory", c.Local.Path))
	}
}

func validateRemote(c *Job, add addFunc) {
	if c.Remote.Path == "" {
		add(i18n.Errorf("remote.path is required"))
	}
	if c.Remote.Port < 0 || c.Remote.Port > 65535 {
		add(i18n.Errorf("remote.port %d is out of range", c.Remote.Port))
	}

	protocols := []Protocol{ProtocolFTP, ProtocolSFTP, ProtocolSMB, ProtocolLocal}
	switch {
	case c.Remote.Protocol == "":
		add(i18n.Errorf("remote.protocol is required (%s)", quoted(protocols)))
	case !oneOf(c.Remote.Protocol, protocols...):
		add(i18n.Errorf("remote.protocol %q is invalid (%s)", c.Remote.Protocol, quoted(protocols)))
	case c.Remote.Protocol == ProtocolSMB:
		// The share is the one thing no credential store can supply.
		if c.Remote.Share == "" {
			add(i18n.Errorf("remote.share is required for protocol smb"))
		}
	case c.Remote.Protocol == ProtocolLocal:
		validateLocalRemote(c, add)
		return
	}

	if c.Remote.Host == "" {
		add(i18n.Errorf("remote.host is required"))
	}
	if c.Remote.ConnectTimeout <= 0 {
		add(i18n.Errorf("remote.connect_timeout must be > 0"))
	}
	if c.Remote.IOTimeout < 0 {
		add(i18n.Errorf("remote.io_timeout must be >= 0 (0 turns it off)"))
	}
}

// validateLocalRemote checks the far side of a copy between two directories on
// this machine.
//
// Settings that only mean something over a network are rejected rather than
// ignored: a job that names a host or a password for a directory on its own
// disk is not the job its author thought they were writing.
func validateLocalRemote(c *Job, add addFunc) {
	for _, unused := range []struct {
		name string
		set  bool
	}{
		{"host", c.Remote.Host != ""},
		{"port", c.Remote.Port != 0},
		{"user", c.Remote.User != ""},
		{"password", c.Remote.Password.IsSet()},
		{"share", c.Remote.Share != ""},
	} {
		if unused.set {
			add(i18n.Errorf("remote.%s is not used with protocol local", unused.name))
		}
	}

	if fi, err := os.Stat(c.Remote.Path); err == nil && !fi.IsDir() {
		add(i18n.Errorf("remote.path %q is not a directory", c.Remote.Path))
	}

	// Two directories where one contains the other would transfer a file into
	// the place it is scanned from, and with recursive: true keep copying the
	// copies. The same directory on both sides is the same mistake written
	// more briefly.
	if c.Local.Path == "" || c.Remote.Path == "" {
		return
	}
	switch inside, err := isInside(c.Local.Path, c.Remote.Path); {
	case err != nil:
		add(i18n.Errorf("remote.path %q: %v", c.Remote.Path, err))
	case inside:
		add(i18n.Errorf("remote.path %q is inside local.path %q: the two sides of a transfer must be separate directories",
			c.Remote.Path, c.Local.Path))
	}
	switch inside, err := isInside(c.Remote.Path, c.Local.Path); {
	case err != nil:
		add(i18n.Errorf("local.path %q: %v", c.Local.Path, err))
	case inside:
		add(i18n.Errorf("local.path %q is inside remote.path %q: the two sides of a transfer must be separate directories",
			c.Local.Path, c.Remote.Path))
	}
}

func validateTransfer(c *Job, add addFunc) {
	if !oneOf(c.Verify, VerifyHash, VerifyLength, VerifyNone) {
		add(i18n.Errorf("verify %q is invalid (%s)", c.Verify, quoted([]Verify{VerifyHash, VerifyLength, VerifyNone})))
	}
	if !oneOf(c.OnExists, OnExistsSkip, OnExistsOverwrite) {
		add(i18n.Errorf("on_exists %q is invalid (%s)", c.OnExists, quoted([]OnExists{OnExistsSkip, OnExistsOverwrite})))
	}
	if !oneOf(c.PostAction, PostNone, PostDelete, PostMove) {
		add(i18n.Errorf("post_action %q is invalid (%s)", c.PostAction, quoted([]PostAction{PostNone, PostDelete, PostMove})))
	}

	if c.PostAction == PostMove {
		switch inside, err := isInside(c.Local.Path, c.MoveTo); {
		case c.MoveTo == "":
			add(i18n.Errorf("move_to is required when post_action is move"))
		case err != nil:
			add(i18n.Errorf("move_to %q: %v", c.MoveTo, err))
		case inside:
			add(i18n.Errorf("move_to %q must not be inside local.path %q: moved files would be picked up again on the next cycle",
				c.MoveTo, c.Local.Path))
		}
	}

	if c.RemoveEmptyDirs && c.PostAction == PostNone {
		add(i18n.Errorf("remove_empty_dirs needs post_action delete or move: with none the source files stay where they are, so no directory ever becomes empty"))
	}

	if c.Retry.MaxAttempts < 1 {
		add(i18n.Errorf("retry.max_attempts must be >= 1 (1 disables retrying)"))
	}
	if c.Retry.Interval < 0 {
		add(i18n.Errorf("retry.interval must be >= 0"))
	}
	if c.Retry.Backoff < 1 {
		add(i18n.Errorf("retry.backoff must be >= 1"))
	}
}

func validateScanning(c *Job, add addFunc) {
	if c.MaxFileSizeMB < 0 {
		add(i18n.Errorf("max_file_size_mb must be >= 0"))
	}
	if c.Workers < 1 {
		add(i18n.Errorf("workers must be >= 1"))
	}
	if c.PollInterval <= 0 {
		add(i18n.Errorf("poll_interval must be > 0"))
	}
	if c.StableDuration < 0 {
		add(i18n.Errorf("stable_duration must be >= 0"))
	}
}

func validateLog(l Log, key string, add addFunc) {
	if !oneOf(l.Rotation, RotationSize, RotationDaily, RotationMonthly) {
		add(i18n.Errorf("%s.rotation %q is invalid (%s)", key, l.Rotation, quoted([]Rotation{RotationSize, RotationDaily, RotationMonthly})))
	}
	if _, err := ParseLevel(l.Level); err != nil {
		add(i18n.Errorf("%s.level: %v", key, err))
	}
	for _, f := range l.Fields {
		if !oneOf(strings.ToLower(strings.TrimSpace(f)), SelectableLogFields...) {
			add(i18n.Errorf("%s.fields: %q is not a field (%s)", key, f, strings.Join(SelectableLogFields, " | ")))
		}
	}
	for _, limit := range []struct {
		name  string
		value int
	}{
		{"max_size_mb", l.MaxSizeMB},
		{"max_backups", l.MaxBackups},
		{"max_age_days", l.MaxAgeDays},
	} {
		if limit.value < 0 {
			add(i18n.Errorf("%s.%s must be >= 0", key, limit.name))
		}
	}
}

// Warnings returns non-fatal remarks about a job worth logging at startup.
func Warnings(c *Job) []i18n.Message {
	var w []i18n.Message
	if c.StableDuration == 0 {
		w = append(w, i18n.Msg("stable_duration is 0: files that are still being written may be transferred"))
	}
	if c.Remote.InsecureSkipHostKeyCheck {
		w = append(w, i18n.Msg("insecure_skip_host_key_check is enabled: the remote host key is not verified"))
	}
	if c.RemoveEmptyDirs && !c.Recursive {
		w = append(w, i18n.Msg("remove_empty_dirs has nothing to do without recursive: only subdirectories are removed, and without recursion none are visited"))
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
