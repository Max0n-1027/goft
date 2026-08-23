package postact

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/config"
	"goft/internal/fsys"
)

func seed(t *testing.T, name, body string) (*fsys.Local, string) {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return fsys.NewLocal(dir), dir
}

func TestNoneLeavesTheSourceAlone(t *testing.T) {
	src, dir := seed(t, "a.csv", "x")
	if err := Apply(context.Background(), config.PostNone, src, "a.csv", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.csv")); err != nil {
		t.Error("the source file should still be there")
	}
}

func TestDeleteRemovesTheSource(t *testing.T) {
	src, dir := seed(t, "a.csv", "x")
	if err := Apply(context.Background(), config.PostDelete, src, "a.csv", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.csv")); !os.IsNotExist(err) {
		t.Error("the source file should be gone")
	}
}

func TestMoveKeepsTheRelativePath(t *testing.T) {
	src, dir := seed(t, "2026/a.csv", "x")
	archive := t.TempDir()

	if err := Apply(context.Background(), config.PostMove, src, "2026/a.csv", archive); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2026", "a.csv")); !os.IsNotExist(err) {
		t.Error("the source file should have been moved away")
	}
	if _, err := os.Stat(filepath.Join(archive, "2026", "a.csv")); err != nil {
		t.Errorf("the file should be under the archive with its subdirectory intact: %v", err)
	}
}

func TestMoveRefusesToOverwrite(t *testing.T) {
	src, _ := seed(t, "a.csv", "new")
	archive := t.TempDir()
	if err := os.WriteFile(filepath.Join(archive, "a.csv"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := Apply(context.Background(), config.PostMove, src, "a.csv", archive)
	if err == nil {
		t.Fatal("a collision in the archive must be reported, not renamed away silently")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %v, want it to explain the collision", err)
	}
	if body, _ := os.ReadFile(filepath.Join(archive, "a.csv")); string(body) != "old" {
		t.Error("the existing archived file must not be touched")
	}
}
