// Package verify compares files by hash, by length, or not at all.
//
// The same code backs two decisions: whether a freshly transferred file matches
// its source, and whether an existing destination file is already identical.
package verify

import (
	"context"
	"fmt"
	"io"

	"github.com/cespare/xxhash/v2"

	"goft/internal/config"
	"goft/internal/fsys"
)

// Format renders a hash the way it appears in logs.
func Format(h uint64) string { return fmt.Sprintf("%016x", h) }

// Hash reads r to completion and returns its XXH64 digest.
func Hash(r io.Reader) (uint64, error) {
	d := xxhash.New()
	if _, err := io.Copy(d, r); err != nil {
		return 0, err
	}
	return d.Sum64(), nil
}

// HashFile opens name on fs and hashes it.
func HashFile(ctx context.Context, fs fsys.FS, name string) (uint64, error) {
	rc, err := fs.Open(ctx, name)
	if err != nil {
		return 0, err
	}
	defer rc.Close()
	return Hash(rc)
}

// Comparison is the outcome of checking an existing destination file against
// the source.
type Comparison struct {
	// Same reports whether the two hold the same content.
	Same bool
	// SrcHash and DstHash are the digests that were compared, set only when
	// Hashed is true. A size difference settles the question without either
	// file being read, so they are absent in that case.
	SrcHash uint64
	DstHash uint64
	Hashed  bool
}

// Identical reports whether an existing destination file already matches the
// source, which lets an overwrite be skipped.
//
// For the hash method the sizes are compared first: a difference there settles
// the question without reading either file. Sizes come from listings the caller
// already holds, so this costs no extra round trip.
//
// The "none" method has nothing to compare with, so it always reports false and
// the file is transferred again.
func Identical(ctx context.Context, method config.Verify, src fsys.FS, srcName string, srcSize int64, dst fsys.FS, dstName string, dstSize int64) (Comparison, error) {
	switch method {
	case config.VerifyNone:
		return Comparison{}, nil
	case config.VerifyLength:
		return Comparison{Same: srcSize == dstSize}, nil
	case config.VerifyHash:
		if srcSize != dstSize {
			return Comparison{}, nil
		}
		srcHash, err := HashFile(ctx, src, srcName)
		if err != nil {
			return Comparison{}, fmt.Errorf("hash source: %w", err)
		}
		dstHash, err := HashFile(ctx, dst, dstName)
		if err != nil {
			return Comparison{}, fmt.Errorf("hash destination: %w", err)
		}
		return Comparison{Same: srcHash == dstHash, SrcHash: srcHash, DstHash: dstHash, Hashed: true}, nil
	default:
		return Comparison{}, fmt.Errorf("unknown verify method %q", method)
	}
}

// Transferred checks the file just written to the destination.
//
// srcHash is the digest computed from the bytes that were actually streamed to
// the destination, so it must be recomputed for every transfer rather than
// reused from an earlier comparison.
func Transferred(ctx context.Context, method config.Verify, dst fsys.FS, name string, srcSize int64, srcHash uint64) (dstHash uint64, err error) {
	switch method {
	case config.VerifyNone:
		return 0, nil
	case config.VerifyLength:
		fi, err := dst.Stat(ctx, name)
		if err != nil {
			return 0, err
		}
		if fi.Size != srcSize {
			return 0, fmt.Errorf("size mismatch: sent %d bytes, destination has %d", srcSize, fi.Size)
		}
		return 0, nil
	case config.VerifyHash:
		dstHash, err = HashFile(ctx, dst, name)
		if err != nil {
			return 0, err
		}
		if dstHash != srcHash {
			return dstHash, fmt.Errorf("hash mismatch: sent %s, destination has %s", Format(srcHash), Format(dstHash))
		}
		return dstHash, nil
	default:
		return 0, fmt.Errorf("unknown verify method %q", method)
	}
}
