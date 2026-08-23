package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"goft/internal/config"
	"goft/internal/fsys"
)

func fsWith(t *testing.T, files map[string]string) (*fsys.ErrFS, string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return fsys.NewErrFS(fsys.NewLocal(dir)), dir
}

func TestIdenticalByHash(t *testing.T) {
	src, _ := fsWith(t, map[string]string{"a": "same"})
	dst, _ := fsWith(t, map[string]string{"a": "same"})

	ok, err := Identical(context.Background(), config.VerifyHash, src, "a", 4, dst, "a", 4)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("identical contents must compare equal")
	}
}

func TestIdenticalShortCircuitsOnSize(t *testing.T) {
	src, _ := fsWith(t, map[string]string{"a": "aaaa"})
	dst, _ := fsWith(t, map[string]string{"a": "bb"})

	ok, err := Identical(context.Background(), config.VerifyHash, src, "a", 4, dst, "a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("different sizes cannot be identical")
	}
	// Differing sizes settle the question, so neither file should be read.
	if n := src.Count(fsys.OpOpen) + dst.Count(fsys.OpOpen); n != 0 {
		t.Errorf("%d files were opened; a size difference should avoid reading either", n)
	}
}

func TestIdenticalSameSizeDifferentContent(t *testing.T) {
	src, _ := fsWith(t, map[string]string{"a": "aaaa"})
	dst, _ := fsWith(t, map[string]string{"a": "bbbb"})

	ok, err := Identical(context.Background(), config.VerifyHash, src, "a", 4, dst, "a", 4)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("same size but different bytes must not compare equal")
	}
}

func TestIdenticalByLengthIgnoresContent(t *testing.T) {
	src, _ := fsWith(t, map[string]string{"a": "aaaa"})
	dst, _ := fsWith(t, map[string]string{"a": "bbbb"})

	ok, err := Identical(context.Background(), config.VerifyLength, src, "a", 4, dst, "a", 4)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("the length method only compares sizes")
	}
}

func TestIdenticalWithoutVerificationAlwaysTransfers(t *testing.T) {
	src, _ := fsWith(t, map[string]string{"a": "same"})
	dst, _ := fsWith(t, map[string]string{"a": "same"})

	ok, err := Identical(context.Background(), config.VerifyNone, src, "a", 4, dst, "a", 4)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("verify: none has no way to compare, so it must never claim a match")
	}
}

func TestTransferredDetectsCorruption(t *testing.T) {
	dst, _ := fsWith(t, map[string]string{"a": "corrupted"})
	sourceHash, err := Hash(strings.NewReader("original"))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := Transferred(context.Background(), config.VerifyHash, dst, "a", 8, sourceHash); err == nil {
		t.Error("a destination that does not match the sent bytes must fail verification")
	}
}

func TestTransferredDetectsTruncation(t *testing.T) {
	dst, _ := fsWith(t, map[string]string{"a": "short"})
	if _, err := Transferred(context.Background(), config.VerifyLength, dst, "a", 100, 0); err == nil {
		t.Error("a truncated destination must fail the length check")
	}
}

func TestFormatIsFixedWidthHex(t *testing.T) {
	if got := Format(1); got != "0000000000000001" {
		t.Errorf("Format(1) = %q, want zero padded 16 hex digits", got)
	}
}
