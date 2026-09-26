package config

import (
	"fmt"
	"strings"
)

// Problem is one configuration mistake.
type Problem struct {
	Path string // dotted key path, e.g. environments.rec.namespaces
	Line int    // 1-based line in the file, 0 if unknown
	Msg  string
}

// Error lists every problem found in a configuration.
type Error struct {
	File     string
	Problems []Problem
}

// Error formats one problem per line: file:line: path: message.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "invalid configuration (%d problem", len(e.Problems))
	if len(e.Problems) > 1 {
		b.WriteString("s")
	}
	b.WriteString("):")
	for _, p := range e.Problems {
		b.WriteString("\n  ")
		b.WriteString(displayName(e.File))
		if p.Line > 0 {
			fmt.Fprintf(&b, ":%d", p.Line)
		}
		b.WriteString(": ")
		if p.Path != "" {
			b.WriteString(p.Path + ": ")
		}
		b.WriteString(p.Msg)
	}
	return b.String()
}
