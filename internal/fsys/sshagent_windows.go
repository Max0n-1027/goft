package fsys

import (
	"os"
	"strings"
	"syscall"
	"time"
)

// defaultAgent is where to look for an ssh-agent when nothing names one: the
// pipe of the OpenSSH Authentication Agent service that ships with Windows,
// which is also where ssh.exe looks.
var defaultAgent = `\\.\pipe\openssh-ssh-agent`

// dialAgent opens a named pipe, which is how agents listen on Windows — the
// OpenSSH service, and Pageant when IdentityAgent points at its pipe — or
// failing that a Unix socket, which Windows also has.
//
// The pipe is opened for overlapped I/O, so that it goes through the runtime's
// poller like a socket and its deadline works.
func dialAgent(addr string, deadline time.Time) (agentConn, error) {
	if !isPipe(addr) {
		return dialAgentSocket(addr, deadline)
	}
	f, err := os.OpenFile(addr, os.O_RDWR|syscall.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	_ = f.SetDeadline(deadline)
	return f, nil
}

// isPipe reports whether addr names a local named pipe, \\.\pipe\name, written
// with either kind of slash.
func isPipe(addr string) bool {
	return strings.HasPrefix(strings.ToLower(strings.ReplaceAll(addr, "/", `\`)), `\\.\pipe\`)
}
