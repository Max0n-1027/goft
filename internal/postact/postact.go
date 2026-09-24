// Package postact performs the post-transfer action on the sending side.
package postact

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"goft/internal/config"
	"goft/internal/fsys"
)

// Apply runs the configured action against a file that has just been
// transferred. name is relative to the sending root.
//
// move is only reachable for send, where the sending side is always local;
// [goft/internal/config.ValidateForDirection] rejects it for recv.
func Apply(ctx context.Context, action config.PostAction, src fsys.FS, name, moveTo string) error {
	switch action {
	case config.PostNone, "":
		return nil
	case config.PostDelete:
		return src.Remove(ctx, name)
	case config.PostMove:
		return move(src, name, moveTo)
	default:
		return fmt.Errorf("unknown post_action %q", action)
	}
}

// move relocates the source file under moveTo, keeping its relative path.
//
// A name that already exists in the archive is reported as a failure rather
// than silently renamed: quietly inventing a new name hides the collision from
// whoever is watching the archive directory.
func move(src fsys.FS, name, moveTo string) error {
	from, ok := fsys.HostPathOf(src, name)
	if !ok {
		return errors.New("post_action: move requires a local sending side")
	}
	to := filepath.Join(moveTo, filepath.FromSlash(name))

	if _, err := os.Stat(to); err == nil {
		// fs.ErrExist, so that it is recognised as permanent: the name will
		// still be taken on the next attempt, and retrying it only spends the
		// backoff.
		return fmt.Errorf("move target %s: %w", to, fs.ErrExist)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	return fsys.MoveFile(from, to)
}
