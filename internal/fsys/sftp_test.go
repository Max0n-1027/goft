package fsys

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/sftptest"
)

func TestSFTPConformance(t *testing.T) {
	root := t.TempDir()
	remote := sftptest.Start(t, root)
	runFSConformance(t, func(t *testing.T) FS {
		f, err := NewRemote(context.Background(), remote)
		if err != nil {
			t.Fatal(err)
		}
		return f
	})
}

func TestSFTPRejectsAnUnknownHostKey(t *testing.T) {
	remote := sftptest.Start(t, t.TempDir())
	// Point the check at an empty known_hosts: the server's key is not in it.
	empty := filepath.Join(t.TempDir(), "known_hosts")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	remote.KnownHosts = empty

	if _, err := NewRemote(context.Background(), remote); err == nil {
		t.Fatal("an unverified host key must stop the connection")
	}
}

func TestSFTPRequiresSomeAuthentication(t *testing.T) {
	remote := sftptest.Start(t, t.TempDir())
	remote.Password = ""
	remote.PrivateKey = filepath.Join(t.TempDir(), "no-such-key")

	_, err := NewRemote(context.Background(), remote)
	if err == nil {
		t.Fatal("want an error when no usable credential is available")
	}
	if !strings.Contains(err.Error(), "authentication") {
		t.Errorf("error = %v, want it to explain that no credential was found", err)
	}
}
