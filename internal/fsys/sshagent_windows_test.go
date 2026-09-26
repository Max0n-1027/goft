//go:build windows

package fsys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/sys/windows"

	"goft/internal/sftptest"
)

func TestIsPipe(t *testing.T) {
	for addr, want := range map[string]bool{
		`\\.\pipe\openssh-ssh-agent`:     true,
		`//./pipe/pageant.user.0123abcd`: true,
		`\\.\PIPE\Mixed`:                 true,
		`C:\Users\me\agent.sock`:         false,
		`\\server\share\agent`:           false,
	} {
		if got := isPipe(addr); got != want {
			t.Errorf("isPipe(%q) = %v, want %v", addr, got, want)
		}
	}
}

// servePipe runs an ssh-agent holding keys on a named pipe of its own, as the
// OpenSSH Authentication Agent service does, and returns the pipe's name.
func servePipe(t *testing.T, keys ...any) string {
	t.Helper()
	keyring := agent.NewKeyring()
	for _, k := range keys {
		if err := keyring.Add(agent.AddedKey{PrivateKey: k}); err != nil {
			t.Fatal(err)
		}
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\goft-agent-test-` + hex.EncodeToString(suffix)
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}

	// Each client needs an instance of the pipe to itself, so a new one is
	// made as soon as the last is taken.
	instance := func() (windows.Handle, error) {
		return windows.CreateNamedPipe(name16, windows.PIPE_ACCESS_DUPLEX,
			windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
			windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	}
	first, err := instance()
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	go func() {
		h := first
		for {
			err := windows.ConnectNamedPipe(h, nil)
			select {
			case <-stop:
				windows.CloseHandle(h)
				return
			default:
			}
			if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
				windows.CloseHandle(h)
				return
			}
			f := os.NewFile(uintptr(h), name)
			go func() {
				defer f.Close()
				_ = agent.ServeAgent(keyring, f)
			}()
			if h, err = instance(); err != nil {
				return
			}
		}
	}()
	return name
}

func TestTheAgentIsReachedThroughANamedPipe(t *testing.T) {
	emptyHome(t)
	priv, pub := newKey(t)
	r := keyOnlyServer(t, pub)
	r.SSHAgent = servePipe(t, priv)

	res, err := resolveSFTP(r)
	if err != nil {
		t.Fatal(err)
	}
	if res.Agent != r.SSHAgent || len(res.Warnings) != 0 {
		t.Fatalf("agent = %q, warnings = %v; want the pipe used", res.Agent, res.Warnings)
	}
	if err := connect(r); err != nil {
		t.Fatalf("connect with the key behind the pipe: %v", err)
	}
}

func TestAMissingPipeIsReportedAsMissing(t *testing.T) {
	_, err := dialAgent(`\\.\pipe\goft-agent-test-no-such-pipe`, time.Now().Add(time.Second))
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("dialAgent() = %v, want the pipe reported as not there", err)
	}
}

func TestAUnixSocketIsStillUsedOnWindows(t *testing.T) {
	priv, _ := newKey(t)
	keys, err := listAgentKeys(sftptest.StartAgent(t, priv), time.Now().Add(5*time.Second))
	if err != nil || len(keys) != 1 {
		t.Errorf("listAgentKeys() = %d keys, %v; want the one key", len(keys), err)
	}
}
