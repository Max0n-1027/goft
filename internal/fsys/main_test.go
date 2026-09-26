package fsys

import (
	"os"
	"testing"
)

// TestMain keeps the ssh-agent of whoever runs the tests out of them: without
// this, the keys in it, or an SSH_AUTH_SOCK left pointing nowhere, would change
// what every sftp test resolves. Tests of the agent set up their own.
func TestMain(m *testing.M) {
	os.Unsetenv("SSH_AUTH_SOCK")
	defaultAgent = ""
	os.Exit(m.Run())
}
