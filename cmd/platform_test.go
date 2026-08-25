package cmd

import (
	"runtime"
	"testing"
)

// requirePOSIX marks a test whose expectations are those of a POSIX file
// system and cannot hold on Windows. See the identical helper in
// internal/fsys, and e2e_windows_test.go for what is checked instead.
func requirePOSIX(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skipf("POSIX only: %s", why)
	}
}
