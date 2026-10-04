package fsys

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
)

// defaultAgent is where to look for an ssh-agent when nothing names one: the
// pipe of the OpenSSH Authentication Agent service that ships with Windows,
// which is also where ssh.exe looks.
var defaultAgent = `\\.\pipe\openssh-ssh-agent`

// agentPipeBusyWait is how long to leave between tries when every instance of
// the agent's pipe is in use. Short, because the gap it waits out is the one
// between a client taking an instance and the agent creating the next.
const agentPipeBusyWait = 25 * time.Millisecond

// dialAgent opens a named pipe, which is how agents listen on Windows — the
// OpenSSH service, and Pageant when IdentityAgent points at its pipe — or
// failing that a Unix socket, which Windows also has.
func dialAgent(addr string, deadline time.Time) (agentConn, error) {
	if isPipe(addr) {
		return dialAgentPipe(addr, deadline)
	}
	conn, err := dialAgentSocket(addr, deadline)
	if err != nil {
		// Windows answers a socket it cannot reach and one that is not there
		// with the same refusal, and neither says what is wrong. The usual
		// cause is SSH_AUTH_SOCK set by an MSYS or Cygwin shell, whose agent
		// listens on a file that only its own libraries can connect to — and
		// which ssh.exe cannot use either, so the way out is the pipe.
		return nil, fmt.Errorf("%w (the agent of an MSYS or Cygwin shell listens on a socket Windows cannot connect to; the OpenSSH agent for Windows listens on %s)", err, defaultAgent)
	}
	return conn, nil
}

// dialAgentPipe connects to an agent listening on a named pipe, asking for the
// pipe the way ssh.exe asks for its own.
func dialAgentPipe(addr string, deadline time.Time) (agentConn, error) {
	name, err := windows.UTF16PtrFromString(addr)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: addr, Err: err}
	}
	h, err := openAgentPipe(name, deadline)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: addr, Err: err}
	}

	f := os.NewFile(uintptr(h), addr)
	// Opened for overlapped I/O the handle goes through the runtime's poller
	// like a socket, so it takes a deadline. Without one, connect_timeout
	// would not cover an agent that answers the connection and then stops
	// talking, so a handle that will not take the deadline is no use.
	if err := f.SetDeadline(deadline); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("ssh-agent pipe %s will not take a deadline: %w", addr, err)
	}
	return f, nil
}

// openAgentPipe opens the pipe, waiting for an instance to come free for as
// long as the deadline allows.
//
// A named pipe serves one client per instance and the agent creates the next
// only once the last has been taken, so a client arriving in that gap is told
// every instance is busy rather than asked to wait. goft opens the agent once
// while resolving and once per worker connection, which is exactly the burst
// that lands in the gap, and a transfer that fails because the agent was busy
// for a moment would be a poor reason to lose a cycle. ssh.exe retries for as
// long as it takes; here connect_timeout bounds the waiting, so a job against
// an agent that never frees an instance still gives up when it said it would.
//
// SECURITY_IDENTIFICATION is what ssh.exe asks for too, and it matters more
// here than it looks: it lets the pipe's server check who is asking and no
// more. Without it that server may impersonate this process, and the name is
// not reserved — on a machine where the OpenSSH Authentication Agent service
// is not running, which is most of them, any process can create the pipe
// first and be taken for the agent.
func openAgentPipe(name *uint16, deadline time.Time) (windows.Handle, error) {
	for {
		h, err := windows.CreateFile(name,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			0, // no sharing, as ssh.exe opens it
			nil,
			windows.OPEN_EXISTING,
			windows.FILE_FLAG_OVERLAPPED|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION,
			0)
		switch {
		case err == nil:
			return h, nil
		case !errors.Is(err, windows.ERROR_PIPE_BUSY):
			return windows.InvalidHandle, err
		case time.Until(deadline) <= agentPipeBusyWait:
			// No room left for another wait and another try.
			return windows.InvalidHandle, err
		}
		time.Sleep(agentPipeBusyWait)
	}
}

// isPipe reports whether addr names a local named pipe, \\.\pipe\name, written
// with either kind of slash.
func isPipe(addr string) bool {
	return strings.HasPrefix(strings.ToLower(strings.ReplaceAll(addr, "/", `\`)), `\\.\pipe\`)
}
