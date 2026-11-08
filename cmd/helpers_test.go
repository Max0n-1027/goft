package cmd

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// jobYAML is a configuration holding one job, given as the job's settings would
// stand on their own, so that each test can say what it needs in a few lines.
// The job is called job and sends unless the settings say otherwise.
func jobYAML(body string) string {
	var b strings.Builder
	b.WriteString("jobs:\n  - ")
	first := true
	item := func(line string) {
		if first {
			b.WriteString(line + "\n")
			first = false
			return
		}
		b.WriteString("    " + line + "\n")
	}
	if !regexp.MustCompile(`(?m)^name:`).MatchString(body) {
		item("name: job")
	}
	if !regexp.MustCompile(`(?m)^direction:`).MatchString(body) {
		item("direction: send")
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) != "" {
			item(line)
		}
	}
	return b.String()
}

var (
	jobNameLine   = regexp.MustCompile(`(?m)^(?:  - |    )name: (.+)$`)
	directionLine = regexp.MustCompile(`(?m)^(  - |    )direction: \w+$`)
)

// jobName is the name of the first job in the configuration at path.
func jobName(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := jobNameLine.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no job in %s", path)
	}
	return strings.TrimSpace(string(m[1]))
}

// setDirection makes the job in the configuration at path run in dir.
func setDirection(t *testing.T, path, dir string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := directionLine.ReplaceAll(body, []byte("${1}direction: "+dir))
	if err := os.WriteFile(path, updated, 0o600); err != nil {
		t.Fatal(err)
	}
}

// runJobArgs is the command line that runs the job in the configuration at
// path once, in direction dir, followed by args.
func runJobArgs(t *testing.T, path, dir string, args ...string) []string {
	t.Helper()
	setDirection(t, path, dir)
	return append([]string{"--run-once", jobName(t, path), "-c", path}, args...)
}

// runJob runs the job in the configuration at path once, in direction dir,
// the way a person would, and returns the exit code.
func runJob(t *testing.T, path, dir string, args ...string) int {
	t.Helper()
	resetFlags()
	rootCmd.SetArgs(runJobArgs(t, path, dir, args...))
	rootCmd.SetOut(os.Stdout)
	return Execute()
}
