//go:build windows

package fsys

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// pipeName returns a name no other test is using.
func pipeName(t *testing.T) (string, *uint16) {
	t.Helper()
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	name := `\\.\pipe\goft-pipe-test-` + hex.EncodeToString(suffix)
	name16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		t.Fatal(err)
	}
	return name, name16
}

// serveOneInstance creates a pipe that can hold a single client at a time,
// which is the state the agent's own pipe is in for the moment between a
// client taking an instance and the agent creating the next, and accepts the
// first client to arrive.
func serveOneInstance(t *testing.T, name16 *uint16) windows.Handle {
	t.Helper()
	h, err := windows.CreateNamedPipe(name16, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { windows.CloseHandle(h) })
	go func() { _ = windows.ConnectNamedPipe(h, nil) }()
	return h
}

// freeTheInstance drops the client the pipe is holding and offers the instance
// to the next caller, the way the agent does once a client goes away.
func freeTheInstance(server windows.Handle, client agentConn) {
	_ = client.Close()
	_ = windows.DisconnectNamedPipe(server)
	go func() { _ = windows.ConnectNamedPipe(server, nil) }()
}

// An agent whose only pipe instance is taken when goft arrives: Windows
// refuses the open outright rather than queueing it, and goft used to report
// that as the connection failing. One worker starting while another is still
// opening the agent is enough to land in it.
func TestABusyAgentPipeIsWaitedFor(t *testing.T) {
	name, name16 := pipeName(t)
	server := serveOneInstance(t, name16)

	occupying, err := dialAgentPipe(name, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("taking the only instance: %v", err)
	}
	// Free it while the second attempt is still waiting.
	freed := time.AfterFunc(150*time.Millisecond, func() { freeTheInstance(server, occupying) })
	defer freed.Stop()

	start := time.Now()
	conn, err := dialAgentPipe(name, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("dialAgentPipe() = %v, want it to have waited for an instance", err)
	}
	defer conn.Close()
	if waited := time.Since(start); waited < 100*time.Millisecond {
		t.Errorf("connected after %v, so it cannot have waited for the instance to come free", waited)
	}
}

// Waiting is bounded by the deadline the rest of the attempt shares, so an
// agent that never frees an instance costs connect_timeout rather than the
// whole run.
func TestABusyAgentPipeGivesUpAtTheDeadline(t *testing.T) {
	name, name16 := pipeName(t)
	serveOneInstance(t, name16)

	occupying, err := dialAgentPipe(name, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatalf("taking the only instance: %v", err)
	}
	defer occupying.Close()

	start := time.Now()
	_, err = dialAgentPipe(name, time.Now().Add(300*time.Millisecond))
	waited := time.Since(start)

	if err == nil {
		t.Fatal("dialAgentPipe() succeeded, and there was no instance to be had")
	}
	if !errors.Is(err, windows.ERROR_PIPE_BUSY) {
		t.Errorf("dialAgentPipe() = %v, want the busy pipe reported as such", err)
	}
	if waited < 200*time.Millisecond {
		t.Errorf("gave up after %v, well inside the deadline it was given", waited)
	}
	if waited > 3*time.Second {
		t.Errorf("gave up after %v, long past the deadline it was given", waited)
	}
}

// The name the OpenSSH Authentication Agent service uses is not reserved, and
// on a machine where that service is not running any process can take it. What
// stops a process that does from acting as the user is the impersonation level
// the client asks for, which the pipe's server cannot raise.
func TestTheAgentCannotImpersonateUs(t *testing.T) {
	name, name16 := pipeName(t)
	server, err := windows.CreateNamedPipe(name16, windows.PIPE_ACCESS_DUPLEX,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT,
		1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(server)

	levels := make(chan uint32, 1)
	errs := make(chan error, 1)
	go func() {
		if err := windows.ConnectNamedPipe(server, nil); err != nil &&
			!errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
			errs <- err
			return
		}
		// A pipe server cannot impersonate until it has read from the pipe,
		// unless the client sent its security context with the connection.
		// Reading first takes that difference out of the measurement, so what
		// is left is the level itself.
		var buf [1]byte
		var n uint32
		if err := windows.ReadFile(server, buf[:], &n, nil); err != nil {
			errs <- err
			return
		}
		level, err := impersonationLevelOfClient(server)
		if err != nil {
			errs <- err
			return
		}
		levels <- level
	}()

	conn, err := dialAgentPipe(name, time.Now().Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte{0}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errs:
		t.Fatalf("the pipe server could not look at its client: %v", err)
	case level := <-levels:
		if level != windows.SecurityIdentification {
			t.Errorf("the agent was handed impersonation level %d, want SecurityIdentification (%d): it can act as us",
				level, windows.SecurityIdentification)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the pipe server never reported what it was handed")
	}
}

// impersonationLevelOfClient reports how far the pipe's server may go with the
// token of the client on the other end.
func impersonationLevelOfClient(pipe windows.Handle) (uint32, error) {
	// Impersonation is a property of the thread, so the whole exchange has to
	// happen on one.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	impersonate := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateNamedPipeClient")
	if ok, _, err := impersonate.Call(uintptr(pipe)); ok == 0 {
		return 0, err
	}
	defer windows.RevertToSelf()

	var token windows.Token
	if err := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token); err != nil {
		return 0, err
	}
	defer token.Close()

	var level uint32
	var n uint32
	err := windows.GetTokenInformation(token, windows.TokenImpersonationLevel,
		(*byte)(unsafe.Pointer(&level)), uint32(unsafe.Sizeof(level)), &n)
	if err != nil {
		return 0, err
	}
	return level, nil
}

// SSH_AUTH_SOCK set by a Git Bash or Cygwin shell names a socket only those
// libraries can reach, and Windows refuses it with the same words it uses for
// a socket that is not there at all. Neither tells an operator what to do.
func TestAnUnreachableSocketSaysWhereTheWindowsAgentIs(t *testing.T) {
	_, err := dialAgent(filepath.Join(t.TempDir(), "agent.123"), time.Now().Add(time.Second))
	if err == nil {
		t.Fatal("dialAgent() succeeded against nothing")
	}
	for _, want := range []string{defaultAgent, "MSYS or Cygwin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
