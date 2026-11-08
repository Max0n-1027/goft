package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"goft/internal/config"
	"goft/internal/nettest"
	"goft/internal/sftptest"
)

// scenario is one end-to-end setup: a local directory, a live sftp server and
// the configuration file that ties them together.
type scenario struct {
	t         *testing.T
	localDir  string
	remoteDir string
	cfgPath   string
}

func newScenario(t *testing.T, extra string) *scenario {
	t.Helper()
	s := &scenario{t: t, localDir: t.TempDir(), remoteDir: t.TempDir()}
	remote := sftptest.Start(t, s.remoteDir)

	body := fmt.Sprintf(`
name: e2e
local:
  path: %s
remote:
  protocol: sftp
  host: %s
  port: %d
  user: %s
  password: %s
  known_hosts: %s
  path: %s
  use_ssh_config: false
stable_duration: 0s
log:
  level: info
%s
`, s.localDir, remote.Host, remote.Port, remote.User, string(remote.Password),
		remote.KnownHosts, s.remoteDir, extra)

	s.cfgPath = filepath.Join(t.TempDir(), "goft.yaml")
	if err := os.WriteFile(s.cfgPath, []byte(jobYAML(body)), 0o600); err != nil {
		t.Fatal(err)
	}
	return s
}

// send and recv run the scenario's job once in that direction, the way a
// person would, and return the exit code.
func (s *scenario) send(args ...string) int {
	s.t.Helper()
	return runJob(s.t, s.cfgPath, "send", args...)
}
func (s *scenario) recv(args ...string) int {
	s.t.Helper()
	return runJob(s.t, s.cfgPath, "recv", args...)
}

// resetFlags clears the package level flag state between runs, which cobra
// keeps across Execute calls.
func resetFlags() {
	flagConfig, flagLogLevel, flagLang = "", "", ""
	flagConsole, flagNoConsole, flagDryRun = false, false, false
	flagRunOnce = nil

	// Setting the variables back is not enough: pflag also records that a flag
	// was given at all, and it is that record which MarkFlagsMutuallyExclusive
	// consults. Left alone, a --no-console in one test makes --console in a
	// later one fail as a conflict, and the tests only pass one at a time.
	cmds := rootCmd.Commands()
	for i := 0; i < len(cmds); i++ {
		cmds = append(cmds, cmds[i].Commands()...)
	}
	for _, c := range append(cmds, rootCmd) {
		for _, name := range []string{"config", "log-level", "console", "no-console", "dry-run", "lang", "run-once", "help"} {
			if f := c.Flags().Lookup(name); f != nil {
				f.Changed = false
			}
			if f := c.PersistentFlags().Lookup(name); f != nil {
				f.Changed = false
			}
		}
		// --help is not one of ours, so nothing above set it back. Once given,
		// it would turn every later run of the same command into help.
		if f := c.Flags().Lookup("help"); f != nil {
			_ = f.Value.Set("false")
		}
	}
}

// edit replaces old in the scenario's configuration with new, and fails the
// test if old is not there to replace.
func (s *scenario) edit(old, new string) {
	s.t.Helper()
	body, err := os.ReadFile(s.cfgPath)
	if err != nil {
		s.t.Fatal(err)
	}
	if !strings.Contains(string(body), old) {
		s.t.Fatalf("the configuration has no %q to replace:\n%s", old, body)
	}
	updated := strings.Replace(string(body), old, new, 1)
	if err := os.WriteFile(s.cfgPath, []byte(updated), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// logTo sends the scenario job's log to a file, whose path it returns, so
// that the records can be inspected.
func (s *scenario) logTo() string {
	s.t.Helper()
	p := filepath.Join(s.t.TempDir(), "job.log")
	s.edit("    log:\n      level: info\n", "    log:\n      level: info\n      path: "+p+"\n")
	return p
}

func (s *scenario) writeLocal(name, body string) {
	s.t.Helper()
	p := filepath.Join(s.localDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *scenario) writeRemote(name, body string) {
	s.t.Helper()
	p := filepath.Join(s.remoteDir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func (s *scenario) readRemote(name string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.remoteDir, filepath.FromSlash(name)))
	if err != nil {
		s.t.Fatalf("remote %s: %v", name, err)
	}
	return string(b)
}

func (s *scenario) readLocal(name string) string {
	s.t.Helper()
	b, err := os.ReadFile(filepath.Join(s.localDir, filepath.FromSlash(name)))
	if err != nil {
		s.t.Fatalf("local %s: %v", name, err)
	}
	return string(b)
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path)
	return err == nil
}

func TestSendUploadsAndVerifies(t *testing.T) {
	s := newScenario(t, "verify: hash\n")
	s.writeLocal("invoice.csv", "id,amount\n1,100\n")

	if code := s.send("--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := s.readRemote("invoice.csv"); got != "id,amount\n1,100\n" {
		t.Errorf("remote content = %q", got)
	}
	if exists(t, filepath.Join(s.remoteDir, "invoice.csv.goft.tmp")) {
		t.Error("the temporary file should have been renamed away")
	}
}

func TestSendMovesTheSourceAside(t *testing.T) {
	archive := t.TempDir()
	s := newScenario(t, "post_action: move\nmove_to: "+archive+"\n")
	s.writeLocal("a.csv", "x")

	if code := s.send("--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if exists(t, filepath.Join(s.localDir, "a.csv")) {
		t.Error("the source file should have been moved out of the watched directory")
	}
	if !exists(t, filepath.Join(archive, "a.csv")) {
		t.Error("the source file should be in the archive")
	}
}

func TestRecvDownloadsFromTheServer(t *testing.T) {
	s := newScenario(t, "verify: hash\n")
	s.writeRemote("report.csv", "downloaded\n")

	if code := s.recv("--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := s.readLocal("report.csv"); got != "downloaded\n" {
		t.Errorf("local content = %q", got)
	}
}

func TestRecvDeletesTheRemoteSource(t *testing.T) {
	s := newScenario(t, "post_action: delete\n")
	s.writeRemote("report.csv", "x")

	if code := s.recv("--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if exists(t, filepath.Join(s.remoteDir, "report.csv")) {
		t.Error("post_action: delete should have removed the remote file")
	}
}

func TestRecvRejectsMoveAtStartup(t *testing.T) {
	archive := t.TempDir()
	s := newScenario(t, "post_action: move\nmove_to: "+archive+"\n")
	s.writeRemote("report.csv", "x")

	// move would have to create directories on the remote side, which is out of
	// scope, so the combination is refused before anything is transferred.
	if code := s.recv("--no-console"); code != 2 {
		t.Errorf("exit code = %d, want 2 for a configuration that cannot run", code)
	}
	if exists(t, filepath.Join(s.localDir, "report.csv")) {
		t.Error("nothing should have been transferred")
	}
}

func TestSecondSendSkipsWhatIsAlreadyThere(t *testing.T) {
	s := newScenario(t, "on_exists: skip\n")
	s.writeLocal("a.csv", "first")
	if code := s.send("--no-console"); code != 0 {
		t.Fatal("first send failed")
	}

	s.writeLocal("a.csv", "second")
	if code := s.send("--no-console"); code != 0 {
		t.Fatal("second send failed")
	}
	if got := s.readRemote("a.csv"); got != "first" {
		t.Errorf("remote = %q, want the original kept under on_exists: skip", got)
	}
}

func TestOverwriteReplacesChangedFiles(t *testing.T) {
	s := newScenario(t, "on_exists: overwrite\n")
	s.writeLocal("a.csv", "first")
	s.send("--no-console")

	s.writeLocal("a.csv", "second")
	if code := s.send("--no-console"); code != 0 {
		t.Fatal("second send failed")
	}
	if got := s.readRemote("a.csv"); got != "second" {
		t.Errorf("remote = %q, want it replaced", got)
	}
}

func TestDryRunTransfersNothing(t *testing.T) {
	s := newScenario(t, "")
	s.writeLocal("a.csv", "x")

	if code := s.send("--dry-run"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if exists(t, filepath.Join(s.remoteDir, "a.csv")) {
		t.Error("a dry run must not transfer anything")
	}
}

func TestInvalidConfigurationExitsTwo(t *testing.T) {
	s := newScenario(t, "verify: sha256\n")
	if code := s.send("--no-console"); code != 2 {
		t.Errorf("exit code = %d, want 2 for an invalid configuration", code)
	}
}

func TestMissingConfigurationExitsTwo(t *testing.T) {
	resetFlags()
	rootCmd.SetArgs([]string{"--run-once", "job", "-c", filepath.Join(t.TempDir(), "absent.yaml")})
	if code := Execute(); code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// testOutput runs goft test against cfgPath, with args after it, and returns
// what it printed and its exit code.
func testOutput(t *testing.T, cfgPath string, args ...string) (string, int) {
	t.Helper()
	var out strings.Builder
	resetFlags()
	rootCmd.SetArgs(append([]string{"test", "-c", cfgPath}, args...))
	rootCmd.SetOut(&out)
	code := Execute()
	rootCmd.SetOut(os.Stdout)
	return out.String(), code
}

func TestTestCommandChecksTheDirectionTheJobRuns(t *testing.T) {
	s := newScenario(t, "")
	out, code := testOutput(t, s.cfgPath)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	for _, want := range []string{"config", "job", "e2e", "connect", "send (write)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "recv (list)") {
		t.Errorf("a send job was checked for recv:\n%s", out)
	}

	setDirection(t, s.cfgPath, "recv")
	out, code = testOutput(t, s.cfgPath)
	if code != 0 || !strings.Contains(out, "recv (list)") || strings.Contains(out, "send (write)") {
		t.Errorf("exit %d; want a recv job checked for listing only:\n%s", code, out)
	}

	// The probe directory must not survive the check.
	entries, err := os.ReadDir(s.remoteDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".goft-test-") {
			t.Errorf("test left %s behind on the remote", e.Name())
		}
	}
}

func TestTestCommandChecksOnlyTheJobsNamed(t *testing.T) {
	s := newScenario(t, "")
	if out, code := testOutput(t, s.cfgPath, "e2e"); code != 0 || !strings.Contains(out, "e2e") {
		t.Errorf("exit %d, want the named job checked:\n%s", code, out)
	}
	if out, code := testOutput(t, s.cfgPath, "nope"); code != 2 {
		t.Errorf("exit %d, want an unknown job refused:\n%s", code, out)
	}
}

func TestTestCommandFailsAJobThatCannotBeUsed(t *testing.T) {
	s := newScenario(t, "")
	// recv from a directory the server does not have.
	setDirection(t, s.cfgPath, "recv")
	if err := os.RemoveAll(s.remoteDir); err != nil {
		t.Fatal(err)
	}
	out, code := testOutput(t, s.cfgPath)
	if code != 2 || !strings.Contains(out, "remote path does not exist yet") {
		t.Errorf("exit %d, want 2 and the missing directory named:\n%s", code, out)
	}
}

func TestConfiguredDirectionIsRecordedInTheLog(t *testing.T) {
	s := newScenario(t, "")
	logPath := s.logTo()
	s.writeLocal("a.csv", "x")

	if code := s.send("--no-console"); code != 0 {
		t.Fatal("send failed")
	}
	recorded, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), `"direction":"`+string(config.DirSend)+`"`) {
		t.Errorf("log does not record the direction:\n%s", recorded)
	}
	if !strings.Contains(string(recorded), `"job":"e2e"`) {
		t.Errorf("log does not record the job name:\n%s", recorded)
	}
}

func TestUnsupportedSSHConfigDirectiveIsWarnedAboutOnEveryRun(t *testing.T) {
	s := newScenario(t, "")

	// A ProxyJump that goft cannot honour used to be reported only by
	// `goft test`, so a serve job would quietly connect somewhere else.
	sshCfg := filepath.Join(t.TempDir(), "ssh_config")
	body := "Host " + "127.0.0.1" + "\n  ProxyJump bastion.example.com\n"
	if err := os.WriteFile(sshCfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	s.edit("      use_ssh_config: false", "      use_ssh_config: true\n      ssh_config_file: "+sshCfg)
	logPath := s.logTo()
	s.writeLocal("a.csv", "x")

	s.send("--no-console")

	recorded, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(recorded), "ProxyJump") {
		t.Errorf("the log does not mention the directive that was ignored:\n%s", recorded)
	}
}

func TestTestCommandProbesTheLocalDirectory(t *testing.T) {
	s := newScenario(t, "")
	var out strings.Builder
	resetFlags()
	rootCmd.SetArgs([]string{"test", "-c", s.cfgPath})
	rootCmd.SetOut(&out)
	Execute()
	rootCmd.SetOut(os.Stdout)

	if !strings.Contains(out.String(), "readable and writable") {
		t.Errorf("output does not report on the local directory:\n%s", out.String())
	}
	// The probe must not survive the check, on either side.
	entries, err := os.ReadDir(s.localDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".goft-test-") {
			t.Errorf("test left %s behind locally", e.Name())
		}
	}
}

func TestTestCommandJudgesAReadOnlyLocalDirectoryByWhatTheJobNeeds(t *testing.T) {
	requirePOSIX(t, "chmod cannot make a directory read-only on Windows, so the probe would still succeed")
	if os.Getuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	s := newScenario(t, "")
	if err := os.Chmod(s.localDir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(s.localDir, 0o755)

	// A send job is perfectly happy with a read-only source.
	out, code := testOutput(t, s.cfgPath)
	if code != 0 || !strings.Contains(out, "which this job does not need") {
		t.Errorf("exit %d, want a read-only source accepted for send:\n%s", code, out)
	}

	// A recv job has to write there.
	setDirection(t, s.cfgPath, "recv")
	out, code = testOutput(t, s.cfgPath)
	if code != 2 || !strings.Contains(out, "which this job needs") {
		t.Errorf("exit %d, want a read-only destination refused for recv:\n%s", code, out)
	}
}

func TestSendRemovesTheDirectoryItEmptied(t *testing.T) {
	s := newScenario(t, "recursive: true\npost_action: delete\nremove_empty_dirs: true\n")
	s.writeLocal("2026-08/invoice.csv", "id,amount\n1,100\n")

	if code := s.send("--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := s.readRemote("2026-08/invoice.csv"); got != "id,amount\n1,100\n" {
		t.Errorf("remote content = %q", got)
	}
	if exists(t, filepath.Join(s.localDir, "2026-08")) {
		t.Error("the directory the transfer emptied should have been removed")
	}
	if !exists(t, s.localDir) {
		t.Error("the watched directory itself must survive: the job has nothing to watch otherwise")
	}
}

func TestRecvRemovesTheRemoteDirectoryItEmptied(t *testing.T) {
	// The tidying up goes over the protocol like everything else, so it is
	// worth proving against a real server rather than only over a local disk.
	s := newScenario(t, "recursive: true\npost_action: delete\nremove_empty_dirs: true\n")
	s.writeRemote("2026-08/report.csv", "downloaded\n")

	if code := s.recv("--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := s.readLocal("2026-08/report.csv"); got != "downloaded\n" {
		t.Errorf("local content = %q", got)
	}
	if exists(t, filepath.Join(s.remoteDir, "2026-08")) {
		t.Error("the emptied directory should have been removed from the server")
	}
}

func TestAStalledTransferIsRetriedOverANewConnection(t *testing.T) {
	// The first connection goes silent part way through the file. The job
	// should neither wait on it for ever nor give the file up: io_timeout
	// drops it, and the retry sends the file again over a new connection.
	localDir, remoteDir := t.TempDir(), t.TempDir()
	server := sftptest.Start(t, remoteDir)
	proxy := nettest.Start(t, fmt.Sprintf("%s:%d", server.Host, server.Port), 1<<20)

	logFile := filepath.Join(t.TempDir(), "goft.log")
	cfg := filepath.Join(t.TempDir(), "job.yaml")
	body := fmt.Sprintf(`
local:
  path: %s
remote:
  protocol: sftp
  host: %s
  port: %d
  user: %s
  password: %s
  path: %s
  use_ssh_config: false
  insecure_skip_host_key_check: true
  io_timeout: 500ms
stable_duration: 0s
retry:
  max_attempts: 3
  interval: 0s
log:
  path: %s
`, localDir, proxy.Host, proxy.Port, server.User, string(server.Password), remoteDir, logFile)
	if err := os.WriteFile(cfg, []byte(jobYAML(body)), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("0123456789abcdef", 1<<18) // 4 MiB
	if err := os.WriteFile(filepath.Join(localDir, "big.dat"), []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan int, 1)
	go func() {
		resetFlags()
		rootCmd.SetArgs(runJobArgs(t, cfg, "send", "--no-console"))
		done <- Execute()
	}()
	select {
	case code := <-done:
		if code != 0 {
			b, _ := os.ReadFile(logFile)
			t.Fatalf("exit code = %d, want 0\n%s", code, b)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("still waiting on a stalled connection after 30s")
	}

	got, err := os.ReadFile(filepath.Join(remoteDir, "big.dat"))
	if err != nil || string(got) != payload {
		t.Fatalf("the file did not arrive intact (%d bytes, %v)", len(got), err)
	}
	b, _ := os.ReadFile(logFile)
	if !strings.Contains(string(b), "retrying transfer") || !strings.Contains(string(b), "stalled") {
		t.Errorf("the log should show the stall and the retry:\n%s", b)
	}
	if proxy.Connections() < 2 {
		t.Errorf("%d connections, want a new one for the retry", proxy.Connections())
	}
}
