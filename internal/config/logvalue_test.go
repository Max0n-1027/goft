package config

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

// record logs cfg and returns the decoded configuration group.
func record(t *testing.T, cfg *Config) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting", "config", cfg)

	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("the record is not JSON: %v\n%s", err, buf.String())
	}
	group, ok := out["config"].(map[string]any)
	if !ok {
		t.Fatalf("no config group in %v", out)
	}
	return group
}

func TestConfigRecordNeverCarriesASecret(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Remote.Password = Secret("hunter2")
	cfg.Remote.PrivateKeyPassphrase = Secret("open sesame")

	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("starting", "config", cfg)

	// Searching the whole line rather than the fields, because a leak anywhere
	// in the record is a leak.
	for _, secret := range []string{"hunter2", "open sesame"} {
		if strings.Contains(buf.String(), secret) {
			t.Errorf("the record leaks %q:\n%s", secret, buf.String())
		}
	}
	remote := record(t, cfg)["remote"].(map[string]any)
	if remote["password"] != redacted {
		t.Errorf("password = %v, want it reported as set but masked", remote["password"])
	}
}

func TestConfigRecordOmitsSecretsThatWereNeverSet(t *testing.T) {
	cfg := validConfig(t.TempDir())

	remote := record(t, cfg)["remote"].(map[string]any)
	if _, ok := remote["password"]; ok {
		t.Error("a password that was never configured should not appear at all")
	}
}

func TestConfigRecordMakesPathsAbsolute(t *testing.T) {
	dir := t.TempDir()
	cfg := validConfig(dir)
	cfg.Local.Path = "./out"
	cfg.MoveTo = "./done"
	cfg.SourceFile = filepath.Join(dir, "job.yaml")

	group := record(t, cfg)
	local := group["local"].(map[string]any)

	// A relative path tells a reader nothing months later, since the working
	// directory the command ran from is long gone.
	for key, got := range map[string]any{"local.path": local["path"], "move_to": group["move_to"]} {
		if !filepath.IsAbs(got.(string)) {
			t.Errorf("%s = %q, want an absolute path", key, got)
		}
	}
}

func TestConfigRecordLeavesTildePathsAlone(t *testing.T) {
	cfg := validConfig(t.TempDir())
	cfg.Remote.KnownHosts = "~/.ssh/known_hosts"

	remote := record(t, cfg)["remote"].(map[string]any)
	// It is expanded when the connection is made; rewriting it here would
	// report a path that was never configured.
	if remote["known_hosts"] != "~/.ssh/known_hosts" {
		t.Errorf("known_hosts = %v, want it as configured", remote["known_hosts"])
	}
}

func TestConfigRecordIncludesDefaultsThatWereNeverWritten(t *testing.T) {
	local := t.TempDir()
	p := writeConfig(t, "local:\n  path: "+local+"\nremote:\n  protocol: sftp\n  host: h\n  path: /p\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}

	group := record(t, cfg)
	// A setting that was never written still decided what happened, so the
	// record has to show it.
	if group["verify"] != "hash" {
		t.Errorf("verify = %v, want the default recorded", group["verify"])
	}
	if group["workers"] != float64(1) {
		t.Errorf("workers = %v, want the default recorded", group["workers"])
	}
	retry := group["retry"].(map[string]any)
	if retry["max_attempts"] != float64(3) {
		t.Errorf("retry.max_attempts = %v, want the default recorded", retry["max_attempts"])
	}
}
