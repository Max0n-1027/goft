package console

import (
	"strings"
	"testing"
)

// A Japanese file name is the ordinary case for this tool, and fmt's own "%-40s"
// counts its bytes: 請求書.csv is nine bytes of name in five columns of screen,
// which puts every following column out by four.
func TestDisplayWidthCountsColumnsNotBytes(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int
	}{
		{"report.csv", 10},
		{"", 0},
		{"請求書", 6},
		{"請求書_2026年01月.csv", 21},
		{"カタカナ", 8},
		{"ｶﾀｶﾅ", 4}, // halfwidth forms are one column each
		{"a b", 3},
	} {
		t.Run(tc.in, func(t *testing.T) {
			if got := displayWidth(tc.in); got != tc.want {
				t.Errorf("displayWidth(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// Every padded name has to end at the same column, whichever script it is in.
func TestPadRightLinesTheColumnsUp(t *testing.T) {
	const cols = 20
	for _, name := range []string{
		"a.csv",
		"請求書.csv",
		"請求書_2026年01月.csv", // wider than the column: nothing is cut
		"",
	} {
		got := padRight(name, cols)
		if !strings.HasPrefix(got, name) {
			t.Errorf("padRight(%q) = %q, want the name kept whole", name, got)
		}
		if w := displayWidth(got); w < cols || (w != cols && displayWidth(name) <= cols) {
			t.Errorf("padRight(%q) occupies %d columns, want %d", name, w, cols)
		}
	}
}
