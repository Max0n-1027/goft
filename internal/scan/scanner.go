package scan

import (
	"context"
	"time"

	"goft/internal/fsys"
)

// File is one transfer candidate, addressed relative to the sending root.
type File struct {
	// Path is slash separated and relative to the root, on every platform.
	Path string
	// Size and ModTime are what the settling check compares between cycles.
	Size    int64
	ModTime time.Time
}

// Scan lists candidates below src. With recursive false only the root is read;
// otherwise directories are descended unless the filter prunes them.
func Scan(ctx context.Context, src fsys.FS, recursive bool, f *Filter) ([]File, error) {
	var out []File
	if err := walk(ctx, src, "", recursive, f, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func walk(ctx context.Context, src fsys.FS, dir string, recursive bool, f *Filter, out *[]File) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := src.List(ctx, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		switch {
		case e.IsDir:
			if recursive && f.MatchDir(e.Name) {
				if err := walk(ctx, src, fsys.Join(dir, e.Name), recursive, f, out); err != nil {
					return err
				}
			}
		case e.IsRegular:
			if f.MatchFile(e.Name) {
				*out = append(*out, File{
					Path:    fsys.Join(dir, e.Name),
					Size:    e.Size,
					ModTime: e.ModTime,
				})
			}
		}
	}
	return nil
}
