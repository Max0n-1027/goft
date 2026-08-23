package fsys

import (
	"context"
	"os"
	"strconv"
	"testing"

	"goft/internal/config"
)

// liveRemote builds a remote configuration from the environment, or skips.
//
// This is how a real FTP, SFTP or SMB server is brought into the test run:
//
//	GOFT_LIVE_PROTOCOL=sftp GOFT_LIVE_HOST=192.168.0.10 \
//	GOFT_LIVE_USER=user GOFT_LIVE_PASSWORD=secret \
//	GOFT_LIVE_PATH=/home/user/goft-test \
//	GOFT_LIVE_KNOWN_HOSTS=/path/to/known_hosts \
//	go test -count=1 ./internal/fsys/ -run Live -v
//
// The suite writes only inside GOFT_LIVE_PATH and removes what it creates.
//
// Always pass -count=1. Go caches successful test results, and it has no way to
// know that the server changed between runs, so a second invocation with the
// same flags will happily replay the previous output and show a state that is
// no longer there.
func liveRemote(t *testing.T) config.Remote {
	t.Helper()

	proto := os.Getenv("GOFT_LIVE_PROTOCOL")
	if proto == "" {
		t.Skip("set GOFT_LIVE_PROTOCOL and friends to run against a real server")
	}

	port := 0
	if p := os.Getenv("GOFT_LIVE_PORT"); p != "" {
		var err error
		if port, err = strconv.Atoi(p); err != nil {
			t.Fatalf("GOFT_LIVE_PORT: %v", err)
		}
	}
	off := false
	r := config.Remote{
		Protocol:   config.Protocol(proto),
		Host:       os.Getenv("GOFT_LIVE_HOST"),
		Port:       port,
		User:       os.Getenv("GOFT_LIVE_USER"),
		Password:   config.Secret(os.Getenv("GOFT_LIVE_PASSWORD")),
		Path:       os.Getenv("GOFT_LIVE_PATH"),
		Share:      os.Getenv("GOFT_LIVE_SHARE"),
		Domain:     os.Getenv("GOFT_LIVE_DOMAIN"),
		KnownHosts: os.Getenv("GOFT_LIVE_KNOWN_HOSTS"),
		// The environment is the whole story here; consulting the running
		// user's ssh_config or netrc would make results depend on the machine.
		UseSSHConfig: &off,
		UseNetrc:     &off,
	}
	if r.KnownHosts == "" && r.Protocol == config.ProtocolSFTP {
		r.InsecureSkipHostKeyCheck = true
	}
	return r
}

func TestLiveConformance(t *testing.T) {
	remote := liveRemote(t)
	runFSConformance(t, func(t *testing.T) FS {
		f, err := NewRemote(context.Background(), remote)
		if err != nil {
			t.Fatalf("connect: %v", err)
		}
		return f
	})
}

// TestLiveListing prints the remote directory, temporary files included, which
// is the one thing a transfer cannot show: the scanner hides them by design.
func TestLiveListing(t *testing.T) {
	remote := liveRemote(t)
	f, err := NewRemote(context.Background(), remote)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer f.Close()

	entries, err := f.List(context.Background(), "")
	if err != nil {
		t.Fatalf("list %s: %v", remote.Path, err)
	}
	for _, e := range entries {
		kind := "file"
		if e.IsDir {
			kind = "dir "
		}
		t.Logf("%s %10d  %s", kind, e.Size, e.Name)
	}
}
