package cmd

import (
	"os"
	"testing"

	"goft/internal/i18n"
)

// TestMain keeps the environment of whoever runs the tests out of them.
//
// Their ssh-agent goes, so that the keys in it, or an SSH_AUTH_SOCK left
// pointing nowhere, change nothing the tests see; tests of the agent start
// their own. And the language is fixed to English, so that a Japanese locale
// does not change every line the tests read; tests of the Japanese ask for it
// with --lang.
func TestMain(m *testing.M) {
	os.Unsetenv("SSH_AUTH_SOCK")
	os.Setenv(i18n.EnvLang, "en")
	os.Exit(m.Run())
}
