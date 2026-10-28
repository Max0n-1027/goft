package i18n

import (
	"testing"

	"golang.org/x/text/language"
)

// The display language comes from Windows itself, so it is read for real here
// rather than stubbed: whatever the machine is set to, it has one, and it is a
// name the matching understands.
func TestTheDisplayLanguageIsRead(t *testing.T) {
	name := systemLanguage()
	if name == "" {
		t.Fatal("no display language was read")
	}
	if _, err := language.Parse(name); err != nil {
		t.Errorf("display language %q is not a language tag: %v", name, err)
	}
}
