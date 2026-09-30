package domain

import (
	"fmt"
	"regexp"
)

// Redacted replaces what a Redactor hides.
const Redacted = "[redacted]"

// Redactor hides the parts of text that match its patterns, in everything
// Huginn exports (copies, saved files). The patterns come from the config
// folder (ui.yaml redact); none is built in. The zero Redactor hides
// nothing.
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor compiles patterns, Go regular expressions.
func NewRedactor(patterns []string) (Redactor, error) {
	var r Redactor
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return Redactor{}, fmt.Errorf("redact pattern %q: %w", p, err)
		}
		r.patterns = append(r.patterns, re)
	}
	return r, nil
}

// Active reports whether any pattern is set.
func (r Redactor) Active() bool { return len(r.patterns) > 0 }

// Redact returns s with every match of every pattern replaced by
// Redacted. Text without matches is returned as it is, without copying.
func (r Redactor) Redact(s string) string {
	for _, re := range r.patterns {
		if re.MatchString(s) {
			s = re.ReplaceAllLiteralString(s, Redacted)
		}
	}
	return s
}
