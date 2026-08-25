package fsys

import (
	"runtime"
	"testing"
)

// requirePOSIX marks a test whose expectations are those of a POSIX file
// system and cannot hold on Windows — mode bits, forward slashes in paths,
// names that Windows reserves. why says which of those it is.
//
// Tests calling this have a Windows counterpart in a _windows_test.go file
// next door, asserting what the same code is supposed to do there. Skipping
// without one would leave the behaviour untested on Windows rather than
// deliberately different.
func requirePOSIX(t *testing.T, why string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skipf("POSIX only: %s", why)
	}
}
