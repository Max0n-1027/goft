package fsys

import (
	"context"
	"path/filepath"
	"testing"

	"goft/internal/config"
	"goft/internal/sftptest"
)

func boolPtr(b bool) *bool { return &b }

func TestNoDeclarationLeavesTheFSAlone(t *testing.T) {
	local := NewLocal(t.TempDir())
	if got := declareCase(local, nil); got != FS(local) {
		t.Errorf("declareCase(nil) = %T, want the FS it was given", got)
	}
}

func TestADeclarationOverridesTheGuess(t *testing.T) {
	for _, want := range []bool{true, false} {
		local := NewLocal(t.TempDir())
		fs := declareCase(local, boolPtr(want))
		if fs.CaseInsensitive() != want {
			t.Errorf("CaseInsensitive() = %v, want the declared %v", fs.CaseInsensitive(), want)
		}
		// A directory on this machine still gives its paths to post-processing.
		if p, ok := HostPathOf(fs, "a.csv"); !ok || p != filepath.Join(local.Root(), "a.csv") {
			t.Errorf("HostPathOf() = %q, %v; want the local path", p, ok)
		}
	}
}

// remoteOnly is an FS that is not on this machine, and so has no host paths.
type remoteOnly struct{ FS }

func TestADeclarationDoesNotInventHostPaths(t *testing.T) {
	fs := declareCase(remoteOnly{NewLocal(t.TempDir())}, boolPtr(true))
	if _, ok := HostPathOf(fs, "a.csv"); ok {
		t.Error("a remote FS gained a host path by being declared case insensitive")
	}
}

func TestNewRemoteAppliesTheDeclaration(t *testing.T) {
	ctx := context.Background()

	local := config.Remote{Protocol: config.ProtocolLocal, Path: t.TempDir(), CaseInsensitive: boolPtr(true)}
	fs, err := NewRemote(ctx, local)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	if !fs.CaseInsensitive() {
		t.Error("a local remote declared case insensitive says it is not")
	}
	if _, ok := HostPathOf(fs, "a.csv"); !ok {
		t.Error("a local remote lost its host paths to the declaration")
	}

	server := sftptest.Start(t, t.TempDir())
	plain, err := NewRemote(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	defer plain.Close()
	if plain.CaseInsensitive() {
		t.Error("an sftp server should be taken to tell case apart unless told otherwise")
	}

	server.CaseInsensitive = boolPtr(true)
	declared, err := NewRemote(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	defer declared.Close()
	if !declared.CaseInsensitive() {
		t.Error("an sftp server declared case insensitive says it is not")
	}
}

func TestResolveShowsADeclaredAnswerOnly(t *testing.T) {
	r := config.Remote{Protocol: config.ProtocolLocal, Path: t.TempDir()}
	res, err := Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range res.Trace {
		if tr.Field == "case_insensitive" {
			t.Errorf("case_insensitive shown although it was not set: %+v", tr)
		}
	}

	r.CaseInsensitive = boolPtr(false)
	res, err = Resolve(r)
	if err != nil {
		t.Fatal(err)
	}
	last := res.Trace[len(res.Trace)-1]
	if last.Field != "case_insensitive" || last.Value != "false" || last.Source != SourceYAML {
		t.Errorf("last trace = %+v, want case_insensitive = false from yaml", last)
	}
}
