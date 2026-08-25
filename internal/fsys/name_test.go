package fsys

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"
)

// The rules are a pure function of the name and the target platform, so they
// are checked from whatever platform happens to be running the suite. What
// they mean for a real transfer is in local_windows_test.go.
func TestCheckStorableNameOnWindows(t *testing.T) {
	for _, tc := range []struct {
		name      string
		wantWords string // a phrase the message has to contain, "" to accept
	}{
		// The ones Windows accepts and then quietly stores as something else.
		// These are the reason this check exists at all.
		{"2026:01.csv", "alternate data stream"},
		{"report.csv.", "trailing dot"},
		{"report.csv ", "trailing space"},
		{"con", "reserved device name"},
		{"AUX.csv", "reserved device name"},
		{"com1.txt", "reserved device name"},
		{"lpt9", "reserved device name"},

		// The ones Windows refuses anyway, caught here for a message that says
		// which character was the problem.
		{"a*.csv", "not allowed"},
		{"a?.csv", "not allowed"},
		{`a"b.csv`, "not allowed"},
		{"a<b.csv", "not allowed"},
		{"a>b.csv", "not allowed"},
		{"a|b.csv", "not allowed"},
		{`a\b.csv`, "separates directories"},
		{"a\tb.csv", "control character"},

		// Every element of a path is judged, not just the last.
		{"2026:01/report.csv", "alternate data stream"},
		{"2026/01:report.csv", "alternate data stream"},

		// And the ordinary names have to keep working.
		{"report.csv", ""},
		{"2026/01/report.csv", ""},
		{"請求書_2026年01月.csv", ""},
		{"a file with spaces.csv", ""},
		{"report.csv.goft.tmp", ""},
		{"console.csv", ""}, // only the exact device names are reserved
		{"2026/auxiliary.log", ""},
		{"nul-report.csv", ""},
		{"", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkStorableName(tc.name, "windows")
			if tc.wantWords == "" {
				if err != nil {
					t.Fatalf("checkStorableName(%q) = %v, want it accepted", tc.name, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("checkStorableName(%q) accepted a name Windows would not store as written", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantWords) {
				t.Errorf("message %q does not explain %q", err, tc.wantWords)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("%q", tc.name)) {
				t.Errorf("message %q does not name the file", err)
			}
			// The engine must not spend three attempts on a name that cannot
			// become storable between them.
			if !errors.Is(err, fs.ErrInvalid) {
				t.Errorf("%v does not wrap fs.ErrInvalid, so it would be retried", err)
			}
		})
	}
}

// Elsewhere a file name is a string of bytes and none of this applies.
func TestCheckStorableNameElsewhere(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		for _, name := range []string{"2026:01.csv", "report.csv.", "con", `a\b.csv`, "a*.csv"} {
			if err := checkStorableName(name, goos); err != nil {
				t.Errorf("checkStorableName(%q, %q) = %v, want it accepted", name, goos, err)
			}
		}
	}
}

// The reserved names are matched whatever case the server used.
func TestCheckStorableNameFoldsDeviceNameCase(t *testing.T) {
	for _, name := range []string{"con", "CON", "Con", "cOn.csv", "COM1", "com1"} {
		if err := checkStorableName(name, "windows"); err == nil {
			t.Errorf("checkStorableName(%q) accepted a reserved device name", name)
		}
	}
}
