//go:build windows

// Windows only end-to-end tests. Their POSIX counterparts, where there is one,
// are in e2e_test.go and marked with requirePOSIX.
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runCapturing is scenario.run with the output collected rather than printed.
func runCapturing(t *testing.T, s *scenario, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	resetFlags()
	rootCmd.SetArgs(append(args, "-c", s.cfgPath))
	rootCmd.SetOut(&out)
	code := Execute()
	rootCmd.SetOut(os.Stdout)
	return out.String(), code
}

// An application still writing a file holds it with no sharing, which is how
// Windows says "not yet" where POSIX says nothing at all. The transfer has to
// fail rather than send a partial file, and the source has to survive even
// under post_action: delete.
func TestLockedSourceFileFailsAndIsKept(t *testing.T) {
	s := newScenario(t, "post_action: delete\nretry:\n  max_attempts: 1\n")
	s.writeLocal("locked.csv", "payload")
	p := filepath.Join(s.localDir, "locked.csv")

	h := lockExclusive(t, p)
	defer syscall.CloseHandle(h)

	out, code := runCapturing(t, s, "send", "--console")
	if code != 1 {
		t.Errorf("exit code = %d, want 1: the file could not be read\n%s", code, out)
	}
	if _, err := os.Stat(p); err != nil {
		t.Errorf("a source that was never transferred was deleted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.remoteDir, "locked.csv")); err == nil {
		t.Error("a file that could not be read appeared at the destination")
	}
}

// A lock that clears while the transfer is still retrying is exactly what the
// retries are for: the application that held the file has finished with it.
func TestLockedSourceFileIsTransferredOnceTheLockClears(t *testing.T) {
	s := newScenario(t, "retry:\n  max_attempts: 3\n  interval: 200ms\n")
	s.writeLocal("locked.csv", "payload")

	h := lockExclusive(t, filepath.Join(s.localDir, "locked.csv"))
	// Long enough that the first attempt is certain to find the file locked,
	// short enough that the second or third finds it free.
	release := time.AfterFunc(300*time.Millisecond, func() { syscall.CloseHandle(h) })
	defer release.Stop()

	if code := s.run("send"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := s.readRemote("locked.csv"); got != "payload" {
		t.Errorf("destination = %q, want %q", got, "payload")
	}
}

// Notepad and most Windows editors write CRLF, and a job file saved by one has
// to be read the same as any other.
func TestJobFileWithCRLFLineEndings(t *testing.T) {
	s := newScenario(t, "")
	body, err := os.ReadFile(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n", "\r\n")
	if err := os.WriteFile(s.cfgPath, []byte(crlf), 0o600); err != nil {
		t.Fatal(err)
	}
	s.writeLocal("a.csv", "payload")

	if code := s.run("send"); code != 0 {
		t.Fatalf("exit code = %d, want 0: a CRLF job file was rejected", code)
	}
	if got := s.readRemote("a.csv"); got != "payload" {
		t.Errorf("destination = %q", got)
	}
}

// A name a Windows editor or a dated tree produces: spaces, a long path and
// characters outside ASCII all have to survive the round trip.
func TestJapaneseAndSpacedNames(t *testing.T) {
	s := newScenario(t, "recursive: true\n")
	for _, name := range []string{"請求書_2026年01月.csv", "monthly report.csv", "2026/01/明細.csv"} {
		s.writeLocal(name, "payload")
	}

	if code := s.run("send"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, name := range []string{"請求書_2026年01月.csv", "monthly report.csv", "2026/01/明細.csv"} {
		if got := s.readRemote(name); got != "payload" {
			t.Errorf("%s = %q", name, got)
		}
	}
}

// lockExclusive opens path denying every kind of sharing, the way an
// application still writing a file holds it.
func lockExclusive(t *testing.T, path string) syscall.Handle {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
