package config

import (
	"strings"
	"testing"
)

func TestADollarSignInAValueIsKeptAsWritten(t *testing.T) {
	// Only ${NAME} refers to the environment. A password is free to contain a
	// dollar sign; it used to be read as the start of a variable and cut short.
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: example
  path: /upload
  password: "pa$word1"
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(cfg.Remote.Password); got != "pa$word1" {
		t.Errorf("password = %q, want it exactly as written", got)
	}
}

func TestVariablesAreExpandedWhereverAValueIs(t *testing.T) {
	t.Setenv("GOFT_TEST_HOST", "invoice-sftp")
	t.Setenv("GOFT_TEST_PORT", "2222")
	t.Setenv("GOFT_TEST_PATTERN", "*.csv")
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: ${GOFT_TEST_HOST}
  port: ${GOFT_TEST_PORT}
  path: /upload/${GOFT_TEST_HOST}
include: ["${GOFT_TEST_PATTERN}"]
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Remote.Host != "invoice-sftp" || cfg.Remote.Path != "/upload/invoice-sftp" {
		t.Errorf("remote = %q %q, want both expanded", cfg.Remote.Host, cfg.Remote.Path)
	}
	// A number can come from the environment too, even though YAML reads
	// ${...} as a string.
	if cfg.Remote.Port != 2222 {
		t.Errorf("port = %d, want 2222", cfg.Remote.Port)
	}
	if len(cfg.Include) != 1 || cfg.Include[0] != "*.csv" {
		t.Errorf("include = %v, want the list element expanded", cfg.Include)
	}
}

func TestAnUnsetVariableIsAnError(t *testing.T) {
	// An unset variable used to become an empty string without a word, so a
	// missing password surfaced much later as an authentication failure.
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: example
  path: /upload
  password: ${GOFT_TEST_SURELY_UNSET}
`)
	_, err := Load(p)
	if err == nil {
		t.Fatal("want an error for a variable that is not set")
	}
	for _, want := range []string{"GOFT_TEST_SURELY_UNSET", "remote.password"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestAVariableSetToEmptyIsAccepted(t *testing.T) {
	// Set but empty is a decision someone made, unlike unset.
	t.Setenv("GOFT_TEST_EMPTY", "")
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: example
  path: /upload
  user: ${GOFT_TEST_EMPTY}
`)
	if _, err := Load(p); err != nil {
		t.Fatalf("Load() = %v, want an empty variable accepted", err)
	}
}

func TestVariablesInCommentsAreLeftAlone(t *testing.T) {
	// goft.example.yaml is full of commented-out settings naming variables
	// nobody has set. Copying it must not make those an error.
	p := writeConfig(t, `
local:
  path: `+t.TempDir()+`
remote:
  protocol: sftp
  host: example
  path: /upload
  # password: ${GOFT_TEST_SURELY_UNSET}
`)
	if _, err := Load(p); err != nil {
		t.Fatalf("Load() = %v, want a variable in a comment ignored", err)
	}
}
