package fsys

import (
	"errors"
	"io/fs"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/config"
)

func TestLocalDescribesItsRoot(t *testing.T) {
	dir := t.TempDir()
	l := NewLocal(dir)

	if l.Root() != dir || l.Describe() != dir {
		t.Errorf("Root()=%q Describe()=%q, want %q", l.Root(), l.Describe(), dir)
	}
	if got := l.HostPath(""); got != dir {
		t.Errorf("HostPath(\"\") = %q, want the root itself", got)
	}
	if got := l.HostPath("2026/a.csv"); got != filepath.Join(dir, "2026", "a.csv") {
		t.Errorf("HostPath() = %q, want the rooted host path", got)
	}
}

func TestHostPathOfOnlyAnswersForLocalFileSystems(t *testing.T) {
	dir := t.TempDir()
	if p, ok := HostPathOf(NewLocal(dir), "a.csv"); !ok || p != filepath.Join(dir, "a.csv") {
		t.Errorf("HostPathOf(local) = %q %v, want the host path", p, ok)
	}
	// A remote file system has no host path, which is why post-processing
	// refuses to move for recv.
	if _, ok := HostPathOf(&ftpFS{root: "/upload"}, "a.csv"); ok {
		t.Error("HostPathOf(remote) reported a host path")
	}
}

func TestMoveFileCopiesWhenRenameCannot(t *testing.T) {
	// os.Rename onto a path whose parent is a file fails the way a cross-volume
	// move does, which is the fallback this exercises.
	from := filepath.Join(t.TempDir(), "src")
	if err := os.WriteFile(from, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := MoveFile(from, filepath.Join(blocker, "dst")); err == nil {
		t.Fatal("moving under a file should fail")
	}
	// The source must survive a move that could not be completed.
	if _, err := os.Stat(from); err != nil {
		t.Errorf("the source was lost: %v", err)
	}
}

func TestSharingViolationIsWindowsOnly(t *testing.T) {
	requirePOSIX(t, "on Windows this message is a lock and must be recognised")
	// The check is by message because no portable errno covers it. On anything
	// but Windows it must never fire, or a real failure would be retried
	// pointlessly.
	if isSharingViolation(errors.New("The process cannot access the file because it is being used by another process.")) {
		t.Error("a Windows lock message must not be treated as one on this platform")
	}
	if isSharingViolation(nil) {
		t.Error("nil is not a sharing violation")
	}
}

func TestTranslateFTPError(t *testing.T) {
	for _, tc := range []struct {
		name       string
		err        error
		wantAbsent bool
	}{
		{"missing file", &textproto.Error{Code: 550, Msg: "File not found"}, true},
		{"busy file", &textproto.Error{Code: 450, Msg: "file busy"}, false},
		{"permanent refusal", &textproto.Error{Code: 553, Msg: "Could not create file."}, false},
		{"plain error", errors.New("connection reset"), false},
		{"nil", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := translateFTPError(tc.err)
			if errors.Is(got, fs.ErrNotExist) != tc.wantAbsent {
				t.Errorf("translateFTPError(%v) = %v, ErrNotExist should be %v", tc.err, got, tc.wantAbsent)
			}
			if tc.err == nil && got != nil {
				t.Errorf("nil should stay nil, got %v", got)
			}
			// The server's own words survive, so a 550 that was really a
			// permission problem can still be diagnosed.
			if tc.err != nil && got != nil && !strings.Contains(got.Error(), "file") && !strings.Contains(got.Error(), "reset") && !strings.Contains(got.Error(), "Could not") {
				t.Errorf("translateFTPError(%v) = %q, want the reply preserved", tc.err, got)
			}
		})
	}
}

func TestRemotePathsAreRootedAndSlashSeparated(t *testing.T) {
	for _, tc := range []struct {
		name string
		fs   FS
		want string
	}{
		{"ftp", &ftpFS{root: "/upload/invoice"}, "/upload/invoice/2026/a.csv"},
		// SMB paths are share relative, so they carry no leading slash.
		{"smb", &smbFS{root: "upload/invoice"}, "upload/invoice/2026/a.csv"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got string
			switch f := tc.fs.(type) {
			case *ftpFS:
				got = f.abs("2026/a.csv")
			case *smbFS:
				got = f.abs("2026/a.csv")
			}
			if got != tc.want {
				t.Errorf("abs() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCaseFoldingIsDeclaredPerProtocol(t *testing.T) {
	// The destination index folds case only where the server does, so getting
	// this wrong either sends files twice or skips ones it should not.
	for _, tc := range []struct {
		name string
		fs   FS
		want bool
	}{
		{"ftp", &ftpFS{}, false},
		{"smb", &smbFS{}, true},
		{"sftp", &sftpFS{}, false},
	} {
		if got := tc.fs.CaseInsensitive(); got != tc.want {
			t.Errorf("%s CaseInsensitive() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestDescribeNamesTheLocation(t *testing.T) {
	for _, f := range []FS{
		&ftpFS{desc: "ftp://host/upload"},
		&smbFS{desc: "smb://host/share/upload"},
		&sftpFS{desc: "sftp://host/upload"},
	} {
		if !strings.Contains(f.Describe(), "://") {
			t.Errorf("Describe() = %q, want a location", f.Describe())
		}
	}
}

func TestResolveDispatchesOnProtocol(t *testing.T) {
	netrc := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(netrc, []byte("machine h login alice password a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	off := false

	for _, tc := range []struct {
		name     string
		remote   config.Remote
		wantPort int
	}{
		{"ftp", config.Remote{Protocol: config.ProtocolFTP, Host: "h", NetrcFile: netrc}, 21},
		{"sftp", config.Remote{Protocol: config.ProtocolSFTP, Host: "h", UseSSHConfig: &off}, 22},
		{"smb", config.Remote{Protocol: config.ProtocolSMB, Host: "h", Share: "s", User: "u"}, 445},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Resolve(tc.remote)
			if err != nil {
				t.Fatal(err)
			}
			if res.Port != tc.wantPort {
				t.Errorf("port = %d, want the protocol default %d", res.Port, tc.wantPort)
			}
			if len(res.Trace) == 0 {
				t.Error("the resolution should say where each value came from")
			}
		})
	}

	if _, err := Resolve(config.Remote{Protocol: "gopher"}); err == nil {
		t.Error("an unsupported protocol should be reported, not guessed at")
	}
}

func TestResolveSMBRecordsTheShare(t *testing.T) {
	res, err := resolveSMB(config.Remote{
		Protocol: config.ProtocolSMB, Host: "h", Share: "shared",
		User: "u", Password: config.Secret("p"), Domain: "WORKGROUP",
	})
	if err != nil {
		t.Fatal(err)
	}

	var fields []string
	for _, tr := range res.Trace {
		fields = append(fields, tr.Field)
		if strings.Contains(tr.Value, "p") && tr.Field == "password" && tr.Value != "REDACTED" {
			t.Errorf("the trace leaks the password: %+v", tr)
		}
	}
	for _, want := range []string{"host", "port", "user", "share", "domain"} {
		if !slicesContains(fields, want) {
			t.Errorf("the resolution does not record %q: %v", want, fields)
		}
	}
}

func slicesContains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
