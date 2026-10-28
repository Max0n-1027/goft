package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declareCaseInsensitive adds remote.case_insensitive to a scenario's job.
func declareCaseInsensitive(t *testing.T, s *scenario, value string) {
	t.Helper()
	body, err := os.ReadFile(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body), "  use_ssh_config: false",
		"  use_ssh_config: false\n  case_insensitive: "+value, 1)
	if err := os.WriteFile(s.cfgPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

// caseDistinctSource writes A.csv and a.csv side by side, which only a file
// system that tells case apart can hold.
func caseDistinctSource(t *testing.T, s *scenario) {
	t.Helper()
	s.writeLocal("A.csv", "upper\n")
	s.writeLocal("a.csv", "lower\n")
	entries, err := os.ReadDir(s.localDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Skip("this file system folds case, so it cannot hold both names to send")
	}
}

func TestADeclaredCaseInsensitiveServerGetsNeitherOfTwoCaseTwins(t *testing.T) {
	// The test server stores its files on a directory that can hold both
	// names, as a Linux server would. Declared case insensitive, it is treated
	// as OpenSSH on Windows would have to be: neither file is sent, and both
	// are reported, since the server would keep only one of them.
	s := newScenario(t, "")
	caseDistinctSource(t, s)
	declareCaseInsensitive(t, s, "true")

	if code := s.run("send", "--no-console"); code != 1 {
		t.Errorf("exit code = %d, want 1 for the two files that were refused", code)
	}
	for _, name := range []string{"A.csv", "a.csv"} {
		if _, err := os.Stat(filepath.Join(s.remoteDir, name)); err == nil {
			t.Errorf("%s was sent to a server declared to fold case", name)
		}
	}
	for _, name := range []string{"A.csv", "a.csv"} {
		if _, err := os.Stat(filepath.Join(s.localDir, name)); err != nil {
			t.Errorf("source %s should be left in place: %v", name, err)
		}
	}
}

func TestAServerTakenToTellCaseApartGetsBothCaseTwins(t *testing.T) {
	s := newScenario(t, "")
	caseDistinctSource(t, s)

	if code := s.run("send", "--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want both files sent", code)
	}
	if s.readRemote("A.csv") != "upper\n" || s.readRemote("a.csv") != "lower\n" {
		t.Error("both files should have arrived, each under its own name")
	}
}

func TestDeclaringCaseInsensitiveFindsAnExistingFileUnderAnotherCase(t *testing.T) {
	// With on_exists: skip, a file already on the server under another case is
	// the same file on a server that folds case, and is not sent again.
	s := newScenario(t, "")
	s.writeRemote("invoice.csv", "already there\n")
	s.writeLocal("INVOICE.csv", "new\n")
	declareCaseInsensitive(t, s, "true")

	if code := s.run("send", "--no-console"); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	if _, err := os.Stat(filepath.Join(s.remoteDir, "INVOICE.csv")); err == nil {
		t.Error("the file was sent again under another case to a server declared to fold case")
	}
}
