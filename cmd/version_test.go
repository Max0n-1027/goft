package cmd

import (
	"regexp"
	"runtime/debug"
	"testing"
)

func TestStampedPrefersWhatLdflagsSet(t *testing.T) {
	// What go build records for a checkout, which build.sh overrides.
	checkout := &debug.BuildInfo{
		Main: debug.Module{Path: "github.com/Max0n-1027/goft", Version: "v0.4.2-0.20261116123225-1983243720ab+dirty"},
		Settings: []debug.BuildSetting{
			{Key: "vcs", Value: "git"},
			{Key: "vcs.revision", Value: "1983243720ab1f0e2c5f6b8b1c9e1d6a1b2c3d4e"},
			{Key: "vcs.modified", Value: "true"},
		},
	}
	tests := []struct {
		name            string
		version, commit string
		info            *debug.BuildInfo
		wantV, wantC    string
	}{
		{
			name:    "build.sh stamped both",
			version: "v0.5.0", commit: "abc1234", info: checkout,
			wantV: "v0.5.0", wantC: "abc1234",
		},
		{
			name:    "go install fetched a release",
			version: "dev", commit: "none",
			info:  &debug.BuildInfo{Main: debug.Module{Path: "github.com/Max0n-1027/goft", Version: "v0.5.0"}},
			wantV: "v0.5.0", wantC: "none",
		},
		{
			name:    "go build in a git checkout",
			version: "dev", commit: "none", info: checkout,
			wantV: "v0.4.2-0.20261116123225-1983243720ab+dirty", wantC: "1983243",
		},
		{
			name:    "go build outside version control",
			version: "dev", commit: "none",
			info:  &debug.BuildInfo{Main: debug.Module{Path: "github.com/Max0n-1027/goft", Version: "(devel)"}},
			wantV: "dev", wantC: "none",
		},
		{
			name:    "a revision shorter than the abbreviation",
			version: "dev", commit: "none",
			info:  &debug.BuildInfo{Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "1983"}}},
			wantV: "dev", wantC: "1983",
		},
		{
			name:    "no build information",
			version: "dev", commit: "none", info: nil,
			wantV: "dev", wantC: "none",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, c := stamped(tt.version, tt.commit, tt.info)
			if v != tt.wantV || c != tt.wantC {
				t.Errorf("stamped(%q, %q) = %q, %q; want %q, %q", tt.version, tt.commit, v, c, tt.wantV, tt.wantC)
			}
		})
	}
}

func TestVersionPrintsOneLineTheWorkflowCanRead(t *testing.T) {
	code, out, _ := run(t, "version")
	if want := regexp.MustCompile(`^goft \S+ \(commit \S+, built \S+\)\n$`); code != 0 || !want.MatchString(out) {
		t.Errorf("exit %d, output %q; want 0 and %v", code, out, want)
	}
}
