package logging

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"goft/internal/config"
)

// sized is a log section with the defaults a configuration gets: one file,
// rotated by size.
func sized(path string) config.Log {
	return config.Log{Path: path, Rotation: config.RotationSize, MaxSizeMB: 100}
}

func TestJobsNamingOneFileShareItWithoutMixingTheirRecords(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	o := NewOutputs(nil)

	// The same file, named once relatively and once in full.
	a, err := New(Options{Log: sized("jobs.log"), Job: "a", Direction: config.DirSend, Outputs: o})
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(Options{Log: sized(filepath.Join(dir, "jobs.log")), Job: "b", Direction: config.DirRecv, Outputs: o})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.files) != 1 {
		t.Fatalf("%d files opened for one path, want 1", len(o.files))
	}

	const each = 200
	var wg sync.WaitGroup
	for _, l := range []func(string, ...any){a.Info, b.Info} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				l("record", "i", i, "padding", strings.Repeat("x", 512))
			}
		}()
	}
	wg.Wait()
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(filepath.Join(dir, "jobs.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	count := map[string]int{}
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("records ran into each other: %q", sc.Text())
		}
		count[r[KeyJob].(string)+" "+r[KeyDirection].(string)]++
	}
	if count["a send"] != each || count["b recv"] != each {
		t.Errorf("records by job = %v, want %d of each", count, each)
	}
}

func TestALogFileGetsTheDirectoryItNeeds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "var", "log", "goft", "job.log")
	o := NewOutputs(nil)
	l, err := New(Options{Log: sized(path), Job: "j", Outputs: o})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("hello")
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || !strings.Contains(string(b), `"msg":"hello"`) {
		t.Errorf("read %s: %q, %v", path, b, err)
	}
}

func TestALogFileThatCannotBeCreatedIsAnError(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A directory cannot be made inside a file.
	_, err := New(Options{Log: sized(filepath.Join(blocker, "job.log")), Outputs: NewOutputs(nil)})
	if err == nil {
		t.Error("New() accepted a log under a file")
	}
}

func TestWithoutAFileTheLogGivesStdoutToTheConsole(t *testing.T) {
	var stdout, stderr bytes.Buffer
	o := NewOutputs(nil)
	o.stdout, o.stderr = &lockedWriter{w: &stdout}, &lockedWriter{w: &stderr}

	plain, err := New(Options{Outputs: o})
	if err != nil {
		t.Fatal(err)
	}
	plain.Info("to stdout")
	withConsole, err := New(Options{ConsoleEnabled: true, Outputs: o})
	if err != nil {
		t.Fatal(err)
	}
	withConsole.Info("to stderr")
	if _, err := o.Stdout().Write([]byte("console line\n")); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(stdout.String(), "to stdout") || strings.Contains(stdout.String(), "to stderr") {
		t.Errorf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "to stderr") {
		t.Errorf("stderr = %q, want the log moved off the console's stdout", stderr.String())
	}
	if !strings.HasSuffix(stdout.String(), "console line\n") {
		t.Errorf("the console does not write through the shared stdout: %q", stdout.String())
	}
}

func TestTheServiceLogNamesNoJob(t *testing.T) {
	var buf bytes.Buffer
	o := NewOutputs(nil)
	o.stdout = &lockedWriter{w: &buf}
	l, err := New(Options{Outputs: o})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("service starting", KeyEvent, EventService)
	var r map[string]any
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if _, ok := r[KeyJob]; ok {
		t.Errorf("the service's record names a job: %v", r)
	}
	if _, ok := r[KeyDirection]; ok {
		t.Errorf("the service's record names a direction: %v", r)
	}
}

func TestTwoSpellingsOfOneFileShareItsWriter(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("file names keep their case here, so the two spellings are two files")
	}
	dir := t.TempDir()
	o := NewOutputs(nil)
	defer o.Close()
	for _, name := range []string{"jobs.log", "JOBS.log"} {
		if _, err := New(Options{Log: sized(filepath.Join(dir, name)), Outputs: o}); err != nil {
			t.Fatal(err)
		}
	}
	if len(o.files) != 1 {
		t.Errorf("%d writers for one file, want 1", len(o.files))
	}
}
