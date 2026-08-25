package cmd

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// localPair is an end-to-end setup with both sides on this machine: no server,
// no credentials, and the same engine as every other job.
type localPair struct {
	t       *testing.T
	src     string
	dst     string
	cfgPath string
}

func newLocalPair(t *testing.T, extra string) *localPair {
	t.Helper()
	p := &localPair{t: t, src: t.TempDir(), dst: t.TempDir()}
	p.cfgPath = p.config(p.src, p.dst, extra)
	return p
}

func (p *localPair) config(local, remote, extra string) string {
	p.t.Helper()
	body := fmt.Sprintf(`
name: local-copy
local:
  path: %s
remote:
  protocol: local
  path: %s
recursive: true
stable_duration: 0s
%s
`, local, remote, extra)

	path := filepath.Join(p.t.TempDir(), "job.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		p.t.Fatal(err)
	}
	return path
}

func (p *localPair) write(dir, name, body string) {
	p.t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		p.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		p.t.Fatal(err)
	}
}

func (p *localPair) read(dir, name string) string {
	p.t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil {
		p.t.Fatalf("%s: %v", name, err)
	}
	return string(b)
}

func (p *localPair) run(args ...string) int {
	p.t.Helper()
	resetFlags()
	rootCmd.SetArgs(append(args, "-c", p.cfgPath))
	rootCmd.SetOut(os.Stdout)
	return Execute()
}

func TestLocalSendCopiesBetweenTwoDirectories(t *testing.T) {
	p := newLocalPair(t, "verify: hash\n")
	p.write(p.src, "invoice.csv", "id,amount\n1,100\n")
	p.write(p.src, "2026-08/invoice.csv", "id,amount\n2,200\n")

	if code := p.run("send", "--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := p.read(p.dst, "invoice.csv"); got != "id,amount\n1,100\n" {
		t.Errorf("copied content = %q", got)
	}
	// The directory structure is kept, as it is over a network.
	if got := p.read(p.dst, "2026-08/invoice.csv"); got != "id,amount\n2,200\n" {
		t.Errorf("copied content = %q", got)
	}
	if exists(t, filepath.Join(p.dst, "invoice.csv.goft.tmp")) {
		t.Error("the temporary file should have been renamed away")
	}
}

func TestLocalRecvCopiesTheOtherWay(t *testing.T) {
	p := newLocalPair(t, "verify: hash\n")
	p.write(p.dst, "report.csv", "downloaded\n")

	if code := p.run("recv", "--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := p.read(p.src, "report.csv"); got != "downloaded\n" {
		t.Errorf("copied content = %q", got)
	}
}

func TestLocalSendSkipsWhatIsAlreadyThere(t *testing.T) {
	p := newLocalPair(t, "on_exists: skip\n")
	p.write(p.src, "invoice.csv", "id,amount\n1,100\n")
	if code := p.run("send", "--no-console"); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	// A second pass has nothing to do, and says so rather than failing.
	if code := p.run("send", "--no-console"); code != 0 {
		t.Fatalf("second run exit code = %d, want 0", code)
	}
}

func TestLocalPairPassesGoftTest(t *testing.T) {
	p := newLocalPair(t, "")
	var out bytes.Buffer
	resetFlags()
	rootCmd.SetArgs([]string{"test", "-c", p.cfgPath})
	rootCmd.SetOut(&out)

	if code := Execute(); code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out.String())
	}
	report := out.String()
	// Nothing was dialled, so the report should not claim a connection.
	if strings.Contains(report, "connect") {
		t.Errorf("report speaks of connecting to a local directory:\n%s", report)
	}
	if !strings.Contains(report, p.dst) {
		t.Errorf("report does not name the destination:\n%s", report)
	}
}

func TestLocalPairRejectsOverlappingDirectories(t *testing.T) {
	// Copying into the directory being scanned copies the copies.
	p := newLocalPair(t, "")
	p.cfgPath = p.config(p.src, filepath.Join(p.src, "done"), "")

	if code := p.run("send", "--no-console"); code != 2 {
		t.Errorf("exit code = %d, want 2: a configuration mistake stops the run before it starts", code)
	}
}

func TestLocalPairAcceptsADestinationThatDoesNotExistYet(t *testing.T) {
	// The destination is created on the first transfer, so its absence before
	// then is the normal state rather than a fault. The parent is probed
	// instead, which is where it will be created.
	p := newLocalPair(t, "")
	p.cfgPath = p.config(p.src, filepath.Join(p.dst, "2026-08"), "")

	var out bytes.Buffer
	resetFlags()
	rootCmd.SetArgs([]string{"test", "-c", p.cfgPath})
	rootCmd.SetOut(&out)

	if code := Execute(); code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "will be created on first transfer") {
		t.Errorf("report does not say the destination will be created:\n%s", out.String())
	}
}
