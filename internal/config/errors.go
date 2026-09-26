package config

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Problem is one mistake in the config folder.
type Problem struct {
	File string // relative to the folder, e.g. formats/nginx.yaml
	Line int    // 1-based, 0 if unknown
	Col  int    // 1-based, 0 if unknown
	Path string // key path in the file, e.g. environments.prd.namespaces
	Msg  string
}

// Error lists every problem found in a config folder.
type Error struct {
	Dir      string
	Problems []Problem
}

// Error formats one problem per line, sorted by file and position:
//
//	environments.yaml:7:5  environments.prd: set namespaces or namespace_from
func (e *Error) Error() string {
	probs := slices.Clone(e.Problems)
	slices.SortStableFunc(probs, func(a, b Problem) int {
		return cmp.Or(cmp.Compare(a.File, b.File), cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col))
	})
	locs := make([]string, len(probs))
	width := 0
	for i, p := range probs {
		locs[i] = p.File
		if p.File == "" {
			locs[i] = "."
		}
		if p.Line > 0 {
			locs[i] += fmt.Sprintf(":%d:%d", p.Line, max(p.Col, 1))
		}
		width = max(width, len(locs[i]))
	}
	var b strings.Builder
	n := len(probs)
	fmt.Fprintf(&b, "the config folder %s has %d error%s (see docs/CONFIG.md):", e.Dir, n, map[bool]string{true: "s"}[n > 1])
	for i, p := range probs {
		msg := p.Msg
		if p.Path != "" {
			msg = p.Path + ": " + msg
		}
		fmt.Fprintf(&b, "\n  %-*s  %s", width, locs[i], msg)
	}
	return b.String()
}
