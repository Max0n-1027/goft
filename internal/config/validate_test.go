package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestValidateReportsEachMistakeInAJob changes one setting of a valid job at a
// time and checks that the mistake is reported, in words that name it, under
// the job's name.
func TestValidateReportsEachMistakeInAJob(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")

	for _, tc := range []struct {
		name   string
		change func(j *Job)
		want   string
	}{
		{"no name", func(j *Job) { j.Name = " " }, "job #1: name is required"},
		{"no direction", func(j *Job) { j.Direction = "" }, "direction is required (send | recv)"},
		{"no local path", func(j *Job) { j.Local.Path = "" }, "local.path is required"},
		{"missing local path", func(j *Job) { j.Local.Path = missing }, "local.path " + strconv.Quote(missing)},
		{"local path is a file", func(j *Job) { j.Local.Path = file }, "is not a directory"},
		{"no remote path", func(j *Job) { j.Remote.Path = "" }, "remote.path is required"},
		{"port out of range", func(j *Job) { j.Remote.Port = 70000 }, "remote.port 70000 is out of range"},
		{"no protocol", func(j *Job) { j.Remote.Protocol = "" }, "remote.protocol is required"},
		{"unknown protocol", func(j *Job) { j.Remote.Protocol = "http" }, `remote.protocol "http" is invalid`},
		{"smb without a share", func(j *Job) { j.Remote.Protocol = ProtocolSMB }, "remote.share is required for protocol smb"},
		{"no host", func(j *Job) { j.Remote.Host = "" }, "remote.host is required"},
		{"no connect timeout", func(j *Job) { j.Remote.ConnectTimeout = 0 }, "remote.connect_timeout must be > 0"},
		{"negative io timeout", func(j *Job) { j.Remote.IOTimeout = -time.Second }, "remote.io_timeout must be >= 0"},
		{"unknown verify", func(j *Job) { j.Verify = "crc" }, `verify "crc" is invalid`},
		{"unknown on_exists", func(j *Job) { j.OnExists = "rename" }, `on_exists "rename" is invalid`},
		{"unknown post_action", func(j *Job) { j.PostAction = "archive" }, `post_action "archive" is invalid`},
		{"move without move_to", func(j *Job) { j.PostAction = PostMove }, "move_to is required when post_action is move"},
		{"move_to inside the source", func(j *Job) {
			j.PostAction, j.MoveTo = PostMove, filepath.Join(j.Local.Path, "done")
		}, "must not be inside local.path"},
		{"remove_empty_dirs with nothing taken away", func(j *Job) { j.RemoveEmptyDirs = true }, "remove_empty_dirs needs post_action delete or move"},
		{"no attempts", func(j *Job) { j.Retry.MaxAttempts = 0 }, "retry.max_attempts must be >= 1"},
		{"negative retry interval", func(j *Job) { j.Retry.Interval = -time.Second }, "retry.interval must be >= 0"},
		{"shrinking backoff", func(j *Job) { j.Retry.Backoff = 0.5 }, "retry.backoff must be >= 1"},
		{"negative size cap", func(j *Job) { j.MaxFileSizeMB = -1 }, "max_file_size_mb must be >= 0"},
		{"no workers", func(j *Job) { j.Workers = 0 }, "workers must be >= 1"},
		{"no poll interval", func(j *Job) { j.PollInterval = 0 }, "poll_interval must be > 0"},
		{"negative stable duration", func(j *Job) { j.StableDuration = -time.Second }, "stable_duration must be >= 0"},
		{"unknown rotation", func(j *Job) { j.Log.Rotation = "hourly" }, `log.rotation "hourly" is invalid`},
		{"unknown level", func(j *Job) { j.Log.Level = "loud" }, "log.level:"},
		{"unknown field", func(j *Job) { j.Log.Fields = []string{"src", "colour"} }, `log.fields: "colour" is not a field`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := validJob(t.TempDir())
			tc.change(j)
			err := Validate(configOf(j))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Validate() = %v, want it to say %q", err, tc.want)
			}
			if j.Name == "job" && !strings.Contains(err.Error(), "job job: ") {
				t.Errorf("the mistake is not reported under the job's name: %v", err)
			}
		})
	}
}

func TestValidateReportsTheServiceLogUnderItsOwnName(t *testing.T) {
	c := configOf(validJob(t.TempDir()))
	c.Service.Log = Log{Level: "loud", Rotation: "hourly"}
	err := Validate(c)
	for _, want := range []string{"service.log.level:", `service.log.rotation "hourly" is invalid`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate() = %v, want it to say %q", err, want)
		}
	}
}

func TestTheArrowShowsTheDirection(t *testing.T) {
	if DirSend.Arrow() != "->" || DirRecv.Arrow() != "<-" {
		t.Errorf("arrows = %q, %q", DirSend.Arrow(), DirRecv.Arrow())
	}
}
