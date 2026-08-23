// Package scan finds transfer candidates on the sending side and decides when
// they have stopped changing.
package scan

import (
	"path"

	"goft/internal/fsys"
)

// Filter decides which names take part in a transfer.
type Filter struct {
	include []string
	exclude []string
}

// NewFilter builds a filter. An empty include list matches everything.
func NewFilter(include, exclude []string) *Filter {
	return &Filter{include: include, exclude: exclude}
}

// MatchFile reports whether a file base name is a candidate. A name must match
// one of the include patterns and none of the exclude patterns.
//
// goft's own in-flight files are excluded unconditionally: relying on the
// default exclude list would break the moment a user overrides it.
func (f *Filter) MatchFile(base string) bool {
	if fsys.IsTempName(base) {
		return false
	}
	if !f.matchAny(f.include, base, true) {
		return false
	}
	return !f.matchAny(f.exclude, base, false)
}

// MatchDir reports whether a directory should be descended into. Exclude
// patterns prune whole subtrees; include patterns apply to files only.
func (f *Filter) MatchDir(base string) bool {
	return !f.matchAny(f.exclude, base, false)
}

func (f *Filter) matchAny(patterns []string, name string, emptyMatches bool) bool {
	if len(patterns) == 0 {
		return emptyMatches
	}
	for _, p := range patterns {
		// An invalid pattern simply never matches; validation reports it.
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}
