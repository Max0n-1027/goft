package cmd

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestTestCommandProbesTheParentOfADestinationNotMadeYet(t *testing.T) {
	parent := t.TempDir()
	dst := filepath.Join(parent, "not-yet")
	p := &localPair{t: t, src: t.TempDir(), dst: dst}
	p.cfgPath = p.config(p.src, dst, "")

	out, code := testOutput(t, p.cfgPath)
	if code != 0 || !strings.Contains(out, "will be created on first transfer") {
		t.Errorf("exit %d, want the parent probed and the job accepted:\n%s", code, out)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("goft test created the destination it was only meant to check")
	}
}

func TestTestCommandChecksThatARecvJobCanDeleteWhatItTakes(t *testing.T) {
	p := newLocalPair(t, "post_action: delete")
	setDirection(t, p.cfgPath, "recv")
	out, code := testOutput(t, p.cfgPath)
	if code != 0 || !strings.Contains(out, "recv (list)") || !strings.Contains(out, "recv (delete)") {
		t.Errorf("exit %d, want listing and deleting both checked:\n%s", code, out)
	}
}

func TestTestCommandFailsASendJobThatCannotWrite(t *testing.T) {
	requirePOSIX(t, "chmod cannot make a directory read-only on Windows, so the probe would still succeed")
	if os.Getuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	p := newLocalPair(t, "")
	if err := os.Chmod(p.dst, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(p.dst, 0o755)

	out, code := testOutput(t, p.cfgPath)
	if code != 2 || !strings.Contains(out, "send (write)") || !strings.Contains(out, "NG") {
		t.Errorf("exit %d, want the write refused and the job failed:\n%s", code, out)
	}
}

func TestTestCommandReportsASettingThatCannotBeResolved(t *testing.T) {
	s := newScenario(t, "")
	s.edit("      use_ssh_config: false", "      use_ssh_config: true\n      ssh_config_file: "+filepath.Join(t.TempDir(), "absent"))
	out, code := testOutput(t, s.cfgPath)
	if code != 2 || !strings.Contains(out, "resolve") || !strings.Contains(out, "FAILED") {
		t.Errorf("exit %d, want the resolution failure reported:\n%s", code, out)
	}
}

func TestTestCommandReportsAServerThatIsNotThere(t *testing.T) {
	s := newScenario(t, "")
	body, err := os.ReadFile(s.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "      port: ") {
			s.edit(line, "      port: "+strconv.Itoa(deadPort(t)))
		}
	}
	out, code := testOutput(t, s.cfgPath)
	if code != 2 || !strings.Contains(out, "connect") || !strings.Contains(out, "FAILED") {
		t.Errorf("exit %d, want the failed connection reported:\n%s", code, out)
	}
}
