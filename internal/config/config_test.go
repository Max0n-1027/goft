package config

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig writes a configuration file and returns its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "job.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadAppliesDefaultsAndExpandsEnv(t *testing.T) {
	t.Setenv("GOFT_TEST_PASSWORD", "s3cret")
	local := t.TempDir()

	p := writeConfig(t, `
local:
  path: `+local+`
remote:
  protocol: sftp
  host: example
  path: /upload
  password: ${GOFT_TEST_PASSWORD}
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	if got := string(cfg.Remote.Password); got != "s3cret" {
		t.Errorf("password = %q, want the expanded environment value", got)
	}
	if cfg.Verify != VerifyHash {
		t.Errorf("verify = %q, want the hash default", cfg.Verify)
	}
	if cfg.Workers != 1 {
		t.Errorf("workers = %d, want the default 1", cfg.Workers)
	}
	if cfg.Name != "job" {
		t.Errorf("name = %q, want it derived from the file name", cfg.Name)
	}
	if cfg.PollInterval.Seconds() != 5 {
		t.Errorf("poll_interval = %v, want the 5s default", cfg.PollInterval)
	}
}

func TestSecretNeverFormatsItself(t *testing.T) {
	s := Secret("hunter2")
	if got := s.String(); strings.Contains(got, "hunter2") {
		t.Errorf("String() = %q, want the value masked", got)
	}
	if got := s.LogValue().String(); strings.Contains(got, "hunter2") {
		t.Errorf("LogValue() = %q, want the value masked", got)
	}
	if string(s) != "hunter2" {
		t.Error("the underlying value must still be readable via a conversion")
	}
}

func TestValidateRejectsMoveToInsideSource(t *testing.T) {
	local := t.TempDir()
	cfg := validConfig(local)
	cfg.PostAction = PostMove
	cfg.MoveTo = filepath.Join(local, "done")

	err := Validate(cfg)
	if err == nil {
		t.Fatal("move_to inside local.path must be rejected: moved files would be transferred again forever")
	}
	if !strings.Contains(err.Error(), "move_to") {
		t.Errorf("error = %v, want it to name move_to", err)
	}
}

func TestValidateAcceptsMoveToOutsideSource(t *testing.T) {
	local := t.TempDir()
	cfg := validConfig(local)
	cfg.PostAction = PostMove
	cfg.MoveTo = t.TempDir()

	if err := Validate(cfg); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Verify = "sha256"
	cfg.OnExists = "replace"
	cfg.Workers = 0

	err := Validate(cfg)
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{"verify", "on_exists", "workers"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s; all problems should be reported together", err, want)
		}
	}
}

func TestValidateForDirectionRejectsMoveOnRecv(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.PostAction = PostMove
	cfg.MoveTo = t.TempDir()

	if err := ValidateForDirection(cfg, DirRecv); err == nil {
		t.Error("post_action: move is send-only and must be rejected for recv")
	}
	if err := ValidateForDirection(cfg, DirSend); err != nil {
		t.Errorf("send with move should be accepted, got %v", err)
	}
}

func TestValidateRequiresTheSMBShare(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Remote.Protocol = ProtocolSMB

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "share") {
		t.Fatalf("Validate() = %v, want the missing share reported", err)
	}
	// The credentials are not required here: a Credential Manager entry may
	// supply them, which is only known once the connection is resolved.
	for _, notWanted := range []string{"remote.user is required", "remote.password is required"} {
		if strings.Contains(fmt.Sprint(err), notWanted) {
			t.Errorf("error %q rejects credentials that a store could still provide", err)
		}
	}
}

func TestRetryDefaultsAndBackoff(t *testing.T) {
	local := t.TempDir()
	p := writeConfig(t, "local:\n  path: "+local+"\nremote:\n  protocol: sftp\n  host: h\n  path: /p\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Retry.MaxAttempts != 3 {
		t.Errorf("retry.max_attempts = %d, want the default 3", cfg.Retry.MaxAttempts)
	}

	// 2s, then 4s: the wait grows so a server that needs a moment gets one.
	if got := cfg.Retry.Wait(1); got != 2*time.Second {
		t.Errorf("Wait(1) = %v, want 2s", got)
	}
	if got := cfg.Retry.Wait(2); got != 4*time.Second {
		t.Errorf("Wait(2) = %v, want 4s", got)
	}
}

func TestValidateRejectsImpossibleRetrySettings(t *testing.T) {
	for _, tc := range []struct {
		name  string
		retry Retry
	}{
		{"zero attempts", Retry{MaxAttempts: 0, Backoff: 2}},
		{"shrinking backoff", Retry{MaxAttempts: 3, Backoff: 0.5}},
		{"negative interval", Retry{MaxAttempts: 3, Interval: -1, Backoff: 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig(t.TempDir())
			cfg.Retry = tc.retry
			if err := Validate(cfg); err == nil {
				t.Errorf("Validate() = nil, want %+v rejected", tc.retry)
			}
		})
	}
}

func TestWarningsFlagZeroStableDuration(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.StableDuration = 0
	got := Warnings(cfg)
	if len(got) == 0 || !strings.Contains(got[0], "stable_duration") {
		t.Errorf("Warnings() = %v, want a remark about stable_duration", got)
	}
}

func validConfig(local string) *Config {
	return &Config{
		Name:  "job",
		Local: Local{Path: local},
		Remote: Remote{
			Protocol:       ProtocolSFTP,
			Host:           "example",
			Path:           "/upload",
			ConnectTimeout: DefaultConnectTimeout,
			IOTimeout:      DefaultIOTimeout,
		},
		Verify:         VerifyHash,
		OnExists:       OnExistsSkip,
		PostAction:     PostNone,
		Workers:        1,
		PollInterval:   5_000_000_000,
		StableDuration: 3_000_000_000,
		Retry:          Retry{MaxAttempts: 3, Interval: 2_000_000_000, Backoff: 2},
		Log:            Log{Level: "info", Rotation: RotationSize},
	}
}

func TestRemoteDescribe(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remote Remote
		want   string
	}{
		{"sftp", Remote{Protocol: ProtocolSFTP, Host: "h", Path: "/upload/invoice"}, "sftp://h/upload/invoice"},
		{"ftp", Remote{Protocol: ProtocolFTP, Host: "h", Path: "/pub"}, "ftp://h/pub"},
		// The share is part of where an SMB file actually lives, so a location
		// without it points somewhere else entirely.
		{"smb with share", Remote{Protocol: ProtocolSMB, Host: "h", Share: "shared", Path: "/invoice"}, "smb://h/shared/invoice"},
		{"smb at the share root", Remote{Protocol: ProtocolSMB, Host: "h", Share: "shared", Path: "/"}, "smb://h/shared"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.remote.Describe(); got != tc.want {
				t.Errorf("Describe() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLogFieldsDefaultToTheStandardSet(t *testing.T) {
	cfg := validConfig(t.TempDir())

	set := cfg.LogFields(slog.LevelInfo)
	for _, want := range DefaultLogFields {
		if !set.Has(want) {
			t.Errorf("field %q missing from the default set", want)
		}
	}
	// The transfer rate is about performance rather than about what happened,
	// so it joins the default set only when the level asks for detail.
	if set.Has(FieldRateMiBs) {
		t.Error("rate_mibs should not be in the default set at info level")
	}
	if !cfg.LogFields(slog.LevelDebug).Has(FieldRateMiBs) {
		t.Error("rate_mibs should join the default set at debug level")
	}
}

func TestLogFieldsNamedExplicitlyAreExactlyWhatIsWritten(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Log.Fields = []string{FieldSrc, FieldResult}

	// Naming fields replaces the defaults entirely, including at debug level.
	set := cfg.LogFields(slog.LevelDebug)
	if !set.Has(FieldSrc) || !set.Has(FieldResult) {
		t.Error("the named fields should be selected")
	}
	for _, unwanted := range []string{FieldBytes, FieldHashSrc, FieldRateMiBs} {
		if set.Has(unwanted) {
			t.Errorf("field %q was not named but is selected", unwanted)
		}
	}
}

func TestEmptyLogFieldsSelectsNothing(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Log.Fields = []string{}

	// An empty list is a deliberate choice and differs from omitting the key.
	if set := cfg.LogFields(slog.LevelInfo); len(set) != 0 {
		t.Errorf("set = %v, want an explicit empty list to select nothing", set)
	}
}

func TestValidateRejectsUnknownLogFields(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Log.Fields = []string{FieldSrc, "checksum"}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("an unknown field name must be rejected rather than silently ignored")
	}
	if !strings.Contains(err.Error(), "checksum") || !strings.Contains(err.Error(), FieldHashSrc) {
		t.Errorf("error = %v, want it to name the offender and list what is valid", err)
	}
}

func localConfig(t *testing.T) *Config {
	t.Helper()
	cfg := validConfig(t.TempDir())
	cfg.Remote = Remote{Protocol: ProtocolLocal, Path: t.TempDir()}
	return cfg
}

func TestValidateAcceptsALocalPairWithNoHost(t *testing.T) {
	// A directory on this machine has nothing to connect to, so requiring a
	// host would mean inventing one.
	if err := Validate(localConfig(t)); err != nil {
		t.Fatalf("Validate() = %v, want a local pair to be accepted", err)
	}
}

func TestValidateRejectsNetworkSettingsOnALocalPair(t *testing.T) {
	// Ignoring them would leave the author believing the job authenticates as
	// somebody, or reaches another machine.
	for _, tc := range []struct {
		field string
		set   func(*Remote)
	}{
		{"host", func(r *Remote) { r.Host = "fileserver" }},
		{"port", func(r *Remote) { r.Port = 22 }},
		{"user", func(r *Remote) { r.User = "uploader" }},
		{"password", func(r *Remote) { r.Password = Secret("s3cret") }},
		{"share", func(r *Remote) { r.Share = "shared" }},
	} {
		t.Run(tc.field, func(t *testing.T) {
			cfg := localConfig(t)
			tc.set(&cfg.Remote)

			err := Validate(cfg)
			if err == nil || !strings.Contains(err.Error(), "remote."+tc.field) {
				t.Fatalf("Validate() = %v, want remote.%s reported as unused", err, tc.field)
			}
		})
	}
}

func TestValidateRejectsOverlappingLocalDirectories(t *testing.T) {
	// Transferring into the directory being scanned copies the copies, which
	// with recursive: true never stops.
	root := t.TempDir()
	nested := filepath.Join(root, "done")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name          string
		local, remote string
	}{
		{"destination inside source", root, nested},
		{"source inside destination", nested, root},
		{"the same directory twice", root, root},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validConfig(tc.local)
			cfg.Remote = Remote{Protocol: ProtocolLocal, Path: tc.remote}

			if err := Validate(cfg); err == nil {
				t.Fatal("want the two sides reported as overlapping")
			}
		})
	}
}

func TestValidateRejectsALocalPathThatIsNotADirectory(t *testing.T) {
	cfg := localConfig(t)
	file := filepath.Join(t.TempDir(), "invoice.csv")
	if err := os.WriteFile(file, []byte("id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg.Remote.Path = file

	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("Validate() = %v, want the file reported", err)
	}
}

func TestRemoteDescribeRendersALocalPathAsItself(t *testing.T) {
	// A directory on this machine is clearer as itself than dressed up as a
	// URL, and it is made absolute so the console names a place a reader can go
	// to whatever directory the command ran from.
	dir := t.TempDir()
	if got := (Remote{Protocol: ProtocolLocal, Path: dir}).Describe(); got != dir {
		t.Errorf("Describe() = %q, want %q", got, dir)
	}
	if got := (Remote{Protocol: ProtocolLocal, Path: "backup"}).Describe(); !filepath.IsAbs(got) {
		t.Errorf("Describe() = %q, want an absolute path", got)
	}
}

func TestValidateRejectsPruningWithoutAPostAction(t *testing.T) {
	// With post_action: none the source files stay where they are, so no
	// directory ever becomes empty and the setting would quietly do nothing.
	cfg := validConfig(t.TempDir())
	cfg.RemoveEmptyDirs = true
	cfg.PostAction = PostNone

	err := Validate(cfg)
	if err == nil || !strings.Contains(err.Error(), "remove_empty_dirs") {
		t.Fatalf("Validate() = %v, want remove_empty_dirs reported", err)
	}

	cfg.PostAction = PostDelete
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate() = %v, want delete to be accepted", err)
	}
}

func TestWarningsFlagPruningWithoutRecursion(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.RemoveEmptyDirs = true
	cfg.PostAction = PostDelete
	cfg.Recursive = false

	var found bool
	for _, w := range Warnings(cfg) {
		if strings.Contains(w, "remove_empty_dirs") {
			found = true
		}
	}
	if !found {
		// Only subdirectories are ever removed, and without recursion none are
		// visited, so the setting is a no-op worth saying out loud.
		t.Errorf("Warnings() = %v, want the no-op reported", Warnings(cfg))
	}

	cfg.Recursive = true
	for _, w := range Warnings(cfg) {
		if strings.Contains(w, "remove_empty_dirs") {
			t.Errorf("warning %q should not appear with recursion on", w)
		}
	}
}

func TestLoadMakesLocalPathsAbsolute(t *testing.T) {
	// Every log record names both ends in full, because a path relative to a
	// working directory nobody remembers identifies nothing months later. The
	// paths the records are built from therefore have to be absolute already.
	work := t.TempDir()
	t.Chdir(work)
	for _, d := range []string{"out", "backup", "done"} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := writeConfig(t, `
local:
  path: out
remote:
  protocol: local
  path: backup
post_action: move
move_to: done
log:
  path: logs/goft.log
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	for name, got := range map[string]string{
		"local.path":  cfg.Local.Path,
		"remote.path": cfg.Remote.Path,
		"move_to":     cfg.MoveTo,
		"log.path":    cfg.Log.Path,
	} {
		if !filepath.IsAbs(got) || !strings.HasPrefix(got, work) {
			t.Errorf("%s = %q, want it resolved against %s", name, got, work)
		}
	}
}

func TestLoadLeavesARemotePathAlone(t *testing.T) {
	// A server's path is the server's business; it is not relative to anything
	// on this machine.
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: example
  path: upload/invoice
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.Path != "upload/invoice" {
		t.Errorf("remote.path = %q, want it as written", cfg.Remote.Path)
	}
}

func TestTheExampleConfigurationLoads(t *testing.T) {
	// goft.example.yaml is what people copy. It has to parse, decode and, the
	// paths it names aside, validate — and it must leave log.fields out, so
	// that a copy of it writes the full set.
	cfg, err := Load(filepath.Join("..", "..", "goft.example.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Log.Fields != nil {
		t.Errorf("log.fields = %v, want it left out so the full set applies", cfg.Log.Fields)
	}

	// Point the paths at directories that exist; everything else is as written.
	cfg.Local.Path = t.TempDir()
	cfg.MoveTo = t.TempDir()
	if err := Validate(cfg); err != nil {
		t.Errorf("Validate: %v", err)
	}
}

func TestTimeoutsDefaultAndValidate(t *testing.T) {
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: example
  path: /upload
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.ConnectTimeout != DefaultConnectTimeout || cfg.Remote.IOTimeout != DefaultIOTimeout {
		t.Errorf("timeouts = %v / %v, want the defaults", cfg.Remote.ConnectTimeout, cfg.Remote.IOTimeout)
	}

	cfg.Remote.IOTimeout = 0
	if err := Validate(cfg); err != nil {
		t.Errorf("io_timeout: 0 should turn the check off, got %v", err)
	}
	cfg.Remote.ConnectTimeout = 0
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "connect_timeout") {
		t.Errorf("Validate() = %v, want connect_timeout: 0 refused", err)
	}
	cfg.Remote.ConnectTimeout, cfg.Remote.IOTimeout = time.Second, -time.Second
	if err := Validate(cfg); err == nil || !strings.Contains(err.Error(), "io_timeout") {
		t.Errorf("Validate() = %v, want a negative io_timeout refused", err)
	}
}
