package cmd

import (
	"os"
	"testing"
)

// TestMain keeps the ssh-agent of whoever runs the tests out of them, so that
// the keys in it, or an SSH_AUTH_SOCK left pointing nowhere, change nothing
// the tests see. Tests of the agent start their own.
func TestMain(m *testing.M) {
	os.Unsetenv("SSH_AUTH_SOCK")
	os.Exit(m.Run())
}
