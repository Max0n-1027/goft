package cmd

import (
	"fmt"
	"os"
	"testing"

	"goft/internal/i18n"
)

// TestMain keeps the environment of whoever runs the tests out of them.
//
// Their ssh-agent goes, so that the keys in it, or an SSH_AUTH_SOCK left
// pointing nowhere, change nothing the tests see; tests of the agent start
// their own. The language is fixed to English, so that a Japanese locale does
// not change every line the tests read; tests of the Japanese ask for it with
// --lang. And the places goft keeps its locks by default are moved into a
// directory of the run's own, so that a test neither leaves locks behind nor
// is refused by a goft the person running the tests has going.
func TestMain(m *testing.M) {
	os.Unsetenv("SSH_AUTH_SOCK")
	os.Setenv(i18n.EnvLang, "en")

	scratch, err := os.MkdirTemp("", "goft-cmd-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, name := range []string{"TMPDIR", "TEMP", "TMP", "ProgramData"} {
		os.Setenv(name, scratch)
	}
	code := m.Run()
	os.RemoveAll(scratch)
	os.Exit(code)
}
