package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/lock"
)

// fleet is a configuration of three jobs copying between directories on this
// machine: alpha and beta in the main file, sharing one log, and gamma,
// switched off, in a job file of its own.
type fleet struct {
	t       *testing.T
	dir     string
	cfgPath string
	locks   string
}

const fleetMain = `service:
  log:
    path: service.log
  lock_dir: locks
job_files:
  - jobs.d/*.yaml
jobs:
  - name: alpha
    direction: send
    local:
      path: alpha/src
    remote:
      protocol: local
      path: alpha/dst
    stable_duration: 0s
    poll_interval: 50ms
    retry:
      max_attempts: 1
    log:
      path: jobs.log
  - name: beta
    direction: send
    local:
      path: beta/src
    remote:
      protocol: local
      path: beta/dst
    stable_duration: 0s
    poll_interval: 50ms
    log:
      path: jobs.log
`

// gamma's paths are relative to jobs.d, the directory its file is in.
const fleetGamma = `jobs:
  - name: gamma
    direction: send
    enabled: false
    local:
      path: ../gamma/src
    remote:
      protocol: local
      path: ../gamma/dst
    stable_duration: 0s
    poll_interval: 50ms
    log:
      path: ../gamma.log
`

func newFleet(t *testing.T) *fleet {
	t.Helper()
	f := &fleet{t: t, dir: t.TempDir()}
	f.cfgPath = filepath.Join(f.dir, "goft.yaml")
	f.locks = filepath.Join(f.dir, "locks")
	for _, d := range []string{"alpha/src", "alpha/dst", "beta/src", "beta/dst", "gamma/src", "gamma/dst", "jobs.d"} {
		if err := os.MkdirAll(filepath.Join(f.dir, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.writeFile("goft.yaml", fleetMain)
	f.writeFile("jobs.d/gamma.yaml", fleetGamma)
	return f
}

func (f *fleet) path(name string) string {
	return filepath.Join(f.dir, filepath.FromSlash(name))
}

func (f *fleet) writeFile(name, body string) {
	f.t.Helper()
	if err := os.WriteFile(f.path(name), []byte(body), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fleet) readFile(name string) string {
	f.t.Helper()
	b, err := os.ReadFile(f.path(name))
	if err != nil {
		f.t.Fatal(err)
	}
	return string(b)
}

// drop puts a file into a job's source directory.
func (f *fleet) drop(job, name string) {
	f.t.Helper()
	f.writeFile(job+"/src/"+name, job+" "+name+"\n")
}

// arrived reports whether a job has delivered the file.
func (f *fleet) arrived(job, name string) bool {
	_, err := os.Stat(f.path(job + "/dst/" + name))
	return err == nil
}

// config loads the fleet's configuration as goft would.
func (f *fleet) config() *config.Config {
	f.t.Helper()
	resetFlags()
	flagConfig = f.cfgPath
	cfg, err := loadConfig()
	if err != nil {
		f.t.Fatal(err)
	}
	return cfg
}

// hold takes the lock of a job in the main file, as another goft running it
// would.
func (f *fleet) hold(name string) (*lock.Lock, error) {
	return lock.Acquire(f.locks, name, config.PathKey(f.cfgPath))
}

// eventually fails the test unless cond holds within a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// records parses a JSON Lines log, failing the test on a line that is not a
// whole record: lines from jobs sharing a file must never run into each other.
func records(t *testing.T, log string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(log))
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("not a whole record: %q: %v", sc.Text(), err)
		}
		out = append(out, r)
	}
	return out
}

// has reports whether a record with every given field is among recs.
func has(recs []map[string]any, fields map[string]any) bool {
next:
	for _, r := range recs {
		for k, v := range fields {
			if r[k] != v {
				continue next
			}
		}
		return true
	}
	return false
}

func TestJobsListsEveryJobInTheOrderConfigured(t *testing.T) {
	f := newFleet(t)
	code, out, _ := run(t, "--lang", "en", "jobs", "-c", f.cfgPath)
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("want a header and three jobs:\n%s", out)
	}
	if got := strings.Fields(lines[0]); len(got) < 4 || got[0] != "NAME" || got[2] != "STATE" {
		t.Errorf("header = %q", lines[0])
	}
	want := [][]string{
		{"alpha", "send", "enabled", "50ms"},
		{"beta", "send", "enabled", "50ms"},
		{"gamma", "send", "disabled", "50ms"},
	}
	for i, w := range want {
		got := strings.Fields(lines[i+1])
		if len(got) < len(w) || strings.Join(got[:len(w)], " ") != strings.Join(w, " ") {
			t.Errorf("row %d = %q, want it to start %q", i+1, lines[i+1], w)
		}
	}
	// gamma's directories are relative to the job file it is written in.
	if !strings.Contains(lines[3], f.path("gamma/src")+" -> "+f.path("gamma/dst")) {
		t.Errorf("gamma's directories are not resolved against its own file:\n%s", lines[3])
	}

	_, ja, _ := run(t, "--lang", "ja", "jobs", "-c", f.cfgPath)
	for _, w := range []string{"名前", "転送元 -> 転送先", "有効", "無効"} {
		if !strings.Contains(ja, w) {
			t.Errorf("the Japanese list does not contain %q:\n%s", w, ja)
		}
	}
}

func TestJobsRefusesAConfigurationTheServiceWouldRefuse(t *testing.T) {
	f := newFleet(t)
	// Two jobs called the same, which the service could not tell apart.
	f.writeFile("jobs.d/again.yaml", strings.Replace(fleetGamma, "name: gamma", "name: ALPHA", 1))
	code, out, stderr := run(t, "--lang", "en", "jobs", "-c", f.cfgPath)
	if code != 2 || out != "" || !strings.Contains(stderr, `job "ALPHA" in `+f.path("jobs.d/again.yaml")+` has the same name as job "alpha"`) {
		t.Errorf("exit %d, stdout %q, stderr %q; want 2 and the clash named", code, out, stderr)
	}
}

func TestRunOnceRunsTheJobsNamedAndNoOthers(t *testing.T) {
	f := newFleet(t)
	for _, j := range []string{"alpha", "beta", "gamma"} {
		f.drop(j, "a.csv")
	}
	// gamma is switched off, which keeps the service from running it but not a
	// person who asks for it by name. Naming alpha twice runs it once.
	code, _, stderr := run(t, "--run-once", "gamma", "--run-once", "alpha", "--run-once", "alpha", "-c", f.cfgPath, "--no-console")
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, stderr)
	}
	if !f.arrived("alpha", "a.csv") || !f.arrived("gamma", "a.csv") {
		t.Error("a job that was named did not run")
	}
	if f.arrived("beta", "a.csv") {
		t.Error("beta ran without being named")
	}
	if n := strings.Count(f.readFile("jobs.log"), `"msg":"starting"`); n != 1 {
		t.Errorf("alpha started %d times, want once", n)
	}
}

func TestRunOnceRefusesAnUnknownJobBeforeRunningAny(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")
	code, _, stderr := run(t, "--lang", "en", "--run-once", "alpha", "--run-once", "nope", "-c", f.cfgPath, "--no-console")
	if code != 2 || !strings.Contains(stderr, `no job called "nope" is configured`) {
		t.Errorf("exit %d, stderr %q; want 2 and the unknown job named", code, stderr)
	}
	if f.arrived("alpha", "a.csv") {
		t.Error("alpha ran although the command line was refused")
	}
}

func TestRunOnceExitsWithTheWorstOutcome(t *testing.T) {
	f := newFleet(t)
	// A directory where alpha's file would go makes that one file fail, when
	// alpha has to replace what it finds there.
	f.writeFile("goft.yaml", strings.Replace(fleetMain, "      max_attempts: 1\n", "      max_attempts: 1\n    on_exists: overwrite\n", 1))
	f.drop("alpha", "a.csv")
	if err := os.MkdirAll(f.path("alpha/dst/a.csv/taken"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.drop("beta", "b.csv")

	code, _, stderr := run(t, "--lang", "en", "--run-once", "alpha", "--run-once", "beta", "-c", f.cfgPath, "--no-console")
	if code != 1 || !strings.Contains(stderr, `job "alpha": 1 file(s) failed`) {
		t.Errorf("exit %d, stderr %q; want 1 for alpha's file", code, stderr)
	}
	if !f.arrived("beta", "b.csv") {
		t.Error("alpha's failure kept beta from running")
	}

	// Another process running beta makes it unable to start, which is worse.
	held, err := f.hold("beta")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	code, _, stderr = run(t, "--lang", "en", "--run-once", "alpha", "--run-once", "beta", "-c", f.cfgPath, "--no-console")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	for _, want := range []string{`job "alpha": 1 file(s) failed`, `job "beta": another goft process is running it`} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr does not contain %q:\n%s", want, stderr)
		}
	}
}

func TestDryRunWithoutRunOnceCoversEveryEnabledJob(t *testing.T) {
	f := newFleet(t)
	for _, j := range []string{"alpha", "beta", "gamma"} {
		f.drop(j, j+".csv")
	}
	code, out, _ := run(t, "--lang", "en", "--dry-run", "-c", f.cfgPath)
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	for _, want := range []string{"alpha", "alpha.csv", "beta", "beta.csv"} {
		if !strings.Contains(out, want) {
			t.Errorf("the dry run does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "gamma") {
		t.Errorf("the dry run covered a job that is switched off:\n%s", out)
	}
	for _, j := range []string{"alpha", "beta"} {
		if f.arrived(j, j+".csv") {
			t.Errorf("the dry run transferred %s's file", j)
		}
	}
}

func TestTheServiceRunsEveryEnabledJobUntilStopped(t *testing.T) {
	f := newFleet(t)
	cfg := f.config()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runService(ctx, cfg, nil) }()

	for _, j := range []string{"alpha", "beta", "gamma"} {
		f.drop(j, "first.csv")
	}
	eventually(t, "alpha and beta delivered their first files", func() bool {
		return f.arrived("alpha", "first.csv") && f.arrived("beta", "first.csv")
	})
	// The service keeps watching: a file that turns up later goes too.
	f.drop("beta", "later.csv")
	eventually(t, "beta delivered a file that turned up later", func() bool {
		return f.arrived("beta", "later.csv")
	})

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the service stopped with %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not stop")
	}
	if f.arrived("gamma", "first.csv") {
		t.Error("the service ran a job that is switched off")
	}

	svc := records(t, f.readFile("service.log"))
	for _, msg := range []string{"service starting", "service stopping", "service stopped"} {
		if !has(svc, map[string]any{"msg": msg, "event": "service"}) {
			t.Errorf("service.log has no %q record", msg)
		}
	}
	if !strings.Contains(f.readFile("service.log"), `"jobs":["alpha","beta"]`) {
		t.Errorf("service.log does not say which jobs it ran:\n%s", f.readFile("service.log"))
	}

	// alpha and beta share a log; each record says whose it is.
	jobs := records(t, f.readFile("jobs.log"))
	for _, name := range []string{"alpha", "beta"} {
		for _, msg := range []string{"starting", "stopped"} {
			if !has(jobs, map[string]any{"job": name, "msg": msg}) {
				t.Errorf("jobs.log has no %q record for %s", msg, name)
			}
		}
	}

	// The locks went with the service.
	for _, name := range []string{"alpha", "beta"} {
		l, err := f.hold(name)
		if err != nil {
			t.Errorf("%s is still locked: %v", name, err)
			continue
		}
		_ = l.Release()
	}
}

func TestTheServiceStartsNoJobUnlessEveryJobCanStart(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")
	held, err := f.hold("beta")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	err = runService(context.Background(), f.config(), nil)
	var ee *exitError
	if !errors.As(err, &ee) || ee.code != 2 || !strings.Contains(err.Error(), `job "beta": another goft process is running it`) {
		t.Fatalf("got %v, want exit 2 naming beta's lock", err)
	}
	if f.arrived("alpha", "a.csv") {
		t.Error("alpha ran although the service could not start beta")
	}
	if !has(records(t, f.readFile("service.log")), map[string]any{"msg": "service could not start"}) {
		t.Errorf("service.log does not say the service could not start:\n%s", f.readFile("service.log"))
	}
	// alpha had its lock already, and gave it back.
	l, err := f.hold("alpha")
	if err != nil {
		t.Fatalf("alpha is still locked: %v", err)
	}
	_ = l.Release()
}

func TestTheServiceNeedsAJobToRun(t *testing.T) {
	f := newFleet(t)
	f.writeFile("goft.yaml", strings.ReplaceAll(fleetMain, "    direction: send\n", "    direction: send\n    enabled: false\n"))
	code, _, stderr := run(t, "--lang", "en", "-c", f.cfgPath)
	if code != 2 || !strings.Contains(stderr, "no job is enabled in "+f.cfgPath) {
		t.Errorf("exit %d, stderr %q; want 2 and nothing to run reported", code, stderr)
	}
}

func TestGoftWithoutACommandRunsTheServiceUntilInterrupted(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rootCmd.SetContext(ctx)
	t.Cleanup(func() { rootCmd.SetContext(context.Background()) })

	resetFlags()
	rootCmd.SetArgs([]string{"-c", f.cfgPath})
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	defer rootCmd.SetOut(os.Stdout)
	done := make(chan int, 1)
	go func() { done <- Execute() }()

	eventually(t, "the service delivered alpha's file", func() bool { return f.arrived("alpha", "a.csv") })
	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0 for a service that was stopped", code)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the service did not stop")
	}
	// A service writes to its logs, not to the screen, unless asked to.
	if out.Len() != 0 {
		t.Errorf("the service wrote to stdout:\n%s", out.String())
	}
}

func TestLogLevelReachesTheServiceAndEveryJob(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")
	if code, _, stderr := run(t, "--run-once", "alpha", "-c", f.cfgPath, "--no-console", "--log-level", "debug"); code != 0 {
		t.Fatalf("exit code = %d\n%s", code, stderr)
	}
	if !has(records(t, f.readFile("jobs.log")), map[string]any{"job": "alpha", "level": "DEBUG"}) {
		t.Errorf("--log-level debug did not reach alpha's log:\n%s", f.readFile("jobs.log"))
	}

	code, _, stderr := run(t, "--lang", "en", "jobs", "-c", f.cfgPath, "--log-level", "loud")
	if code != 2 || !strings.Contains(stderr, "--log-level") {
		t.Errorf("exit %d, stderr %q; want 2 and the flag named", code, stderr)
	}
}

func TestTheConfigurationIsFoundWithoutBeingNamed(t *testing.T) {
	f := newFleet(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
	t.Setenv("ProgramData", t.TempDir())

	t.Chdir(f.dir)
	if code, out, stderr := run(t, "jobs"); code != 0 || !strings.Contains(out, "alpha") {
		t.Errorf("exit %d, stdout %q, stderr %q; want ./goft.yaml used", code, out, stderr)
	}

	t.Chdir(t.TempDir())
	if _, err := os.Stat("/etc/goft/goft.yaml"); err == nil {
		t.Skip("this machine has a configuration in /etc/goft")
	}
	code, _, stderr := run(t, "--lang", "en", "jobs")
	if code != 2 || !strings.Contains(stderr, "no configuration found") {
		t.Errorf("exit %d, stderr %q; want 2 and the places looked at", code, stderr)
	}
}

func TestAWindowsServiceNeedsEveryLogInAFile(t *testing.T) {
	f := newFleet(t)
	if err := requireLogFiles(f.config()); err != nil {
		t.Errorf("requireLogFiles() = %v, want the fleet accepted: every log it writes is a file", err)
	}

	// beta logging to stdout, and the service too. gamma is switched off, so
	// its log is not the service's concern.
	main := strings.Replace(fleetMain, "    path: service.log\n", "    level: info\n", 1)
	betaLog := "    log:\n      path: jobs.log\n"
	main = main[:strings.LastIndex(main, betaLog)] // beta is the last job in the file
	f.writeFile("goft.yaml", main)
	f.writeFile("jobs.d/gamma.yaml", strings.Replace(fleetGamma, "    log:\n      path: ../gamma.log\n", "", 1))

	err := requireLogFiles(f.config())
	if err == nil {
		t.Fatal("requireLogFiles() accepted logs with nowhere to go")
	}
	for _, want := range []string{"service.log.path is not set", "job beta: log.path is not set"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not say %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "alpha") || strings.Contains(err.Error(), "gamma") {
		t.Errorf("error names a job whose log is fine, or that does not run:\n%v", err)
	}
}

func TestAJobIsRunByItsNameInAnyCase(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")
	code, _, stderr := run(t, "--run-once", "ALPHA", "--run-once", "alpha", "-c", f.cfgPath, "--no-console")
	if code != 0 || !f.arrived("alpha", "a.csv") {
		t.Fatalf("exit %d, stderr %q; want alpha run", code, stderr)
	}
	if n := strings.Count(f.readFile("jobs.log"), `"msg":"starting"`); n != 1 {
		t.Errorf("alpha started %d times, want once however its name was spelled", n)
	}
}

func TestNothingEnabledIsAnErrorForEveryEnabledJob(t *testing.T) {
	f := newFleet(t)
	f.writeFile("goft.yaml", strings.ReplaceAll(fleetMain, "    direction: send\n", "    direction: send\n    enabled: false\n"))
	for _, args := range [][]string{{"--dry-run"}, {"test"}} {
		code, _, stderr := run(t, append([]string{"--lang", "en", "-c", f.cfgPath}, args...)...)
		if code != 2 || !strings.Contains(stderr, "no job is enabled in") {
			t.Errorf("%v: exit %d, stderr %q; want 2 and nothing to do reported", args, code, stderr)
		}
	}
	// Named, a job switched off is still checked.
	if code, out, _ := run(t, "-c", f.cfgPath, "test", "gamma"); code != 0 || !strings.Contains(out, "gamma") {
		t.Errorf("exit %d, want gamma checked when named:\n%s", code, out)
	}
}

func TestAJobOfTheSameNameInAnotherConfigurationIsAnotherJob(t *testing.T) {
	f := newFleet(t)
	f.drop("alpha", "a.csv")
	// Another goft running its own alpha, from a configuration of its own.
	other, err := lock.Acquire(f.locks, "alpha", config.PathKey(filepath.Join(t.TempDir(), "test.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Release()

	if code, _, stderr := run(t, "--run-once", "alpha", "-c", f.cfgPath, "--no-console"); code != 0 {
		t.Fatalf("exit %d, want alpha run regardless of another configuration's alpha: %s", code, stderr)
	}
	if !f.arrived("alpha", "a.csv") {
		t.Error("alpha did not run")
	}
}

func TestAJobFileReadByTwoConfigurationsIsOneJob(t *testing.T) {
	f := newFleet(t)
	f.writeFile("jobs.d/gamma.yaml", strings.Replace(fleetGamma, "    enabled: false\n", "", 1))
	// A second main file naming the same job file runs the same gamma.
	f.writeFile("second.yaml", "service:\n  lock_dir: locks\njob_files:\n  - jobs.d/gamma.yaml\n")
	held, err := lock.Acquire(f.locks, "gamma", config.PathKey(f.path("jobs.d/gamma.yaml")))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	code, _, stderr := run(t, "--lang", "en", "--run-once", "gamma", "-c", f.path("second.yaml"), "--no-console")
	if code != 2 || !strings.Contains(stderr, "another goft process is running it") {
		t.Errorf("exit %d, stderr %q; want gamma refused while the first configuration runs it", code, stderr)
	}
}
