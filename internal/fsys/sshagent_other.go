//go:build !windows

package fsys

import "time"

// defaultAgent is where to look for an ssh-agent when nothing names one. Outside
// Windows an agent announces itself through SSH_AUTH_SOCK, so there is no fixed
// place to try.
var defaultAgent = ""

func dialAgent(addr string, deadline time.Time) (agentConn, error) {
	return dialAgentSocket(addr, deadline)
}
