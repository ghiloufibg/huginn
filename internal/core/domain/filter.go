package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// TextFilter matches log entries against a text or a regular expression,
// case-insensitively. Invert keeps the entries that do not match.
type TextFilter struct {
	Pattern string
	Regex   bool
	Invert  bool
	re      *regexp.Regexp // for regex filters and for match ranges
	lower   string         // lower-cased pattern for substring matching
	// For regexes: a match implies one of these lower-cased literals is
	// present (a cheap prefilter); exact means containing one of them is
	// a match (the regex is a plain alternation of words).
	literals []string
	exact    bool
}

// ParseTextFilter builds a filter from what the user typed. A leading "!"
// inverts the filter; "\!" starts a pattern with a literal "!". An invalid
// regular expression is an error.
func ParseTextFilter(input string, regex bool) (TextFilter, error) {
	f := TextFilter{Regex: regex}
	switch {
	case strings.HasPrefix(input, `\!`):
		input = input[1:]
	case strings.HasPrefix(input, "!"):
		f.Invert, input = true, input[1:]
	}
	f.Pattern = input
	expr := regexp.QuoteMeta(input)
	if regex {
		expr = input
	}
	re, err := regexp.Compile("(?i)" + expr)
	if err != nil {
		return TextFilter{}, fmt.Errorf("invalid regex: %w", err)
	}
	f.re, f.lower = re, strings.ToLower(input)
	if regex {
		f.literals, f.exact = regexLiterals(input)
	}
	return f, nil
}

// String renders the filter as typed, with regex slashes: /a|b/ or text.
func (f TextFilter) String() string {
	s := f.Pattern
	if f.Regex {
		s = "/" + s + "/"
	}
	if f.Invert {
		s = "!" + s
	}
	return s
}

// Empty reports whether the filter matches everything.
func (f TextFilter) Empty() bool { return f.Pattern == "" }

// matchLower reports whether the lower-cased text contains the pattern
// (ignoring Invert).
func (f TextFilter) matchLower(lower string) bool {
	if !f.Regex {
		return strings.Contains(lower, f.lower)
	}
	if f.literals != nil {
		found := false
		for _, l := range f.literals {
			if strings.Contains(lower, l) {
				found = true
				break
			}
		}
		if !found || f.exact {
			return found
		}
	}
	return f.re.MatchString(lower)
}

// Matches reports whether the entry satisfies the filter. It searches the
// message, logger, thread, trace id, visible fields and stack trace, or the
// raw line of unstructured entries; hidden metadata is never searched.
func (f TextFilter) Matches(e *LogEntry) bool {
	if f.Empty() {
		return true
	}
	return f.found(e) != f.Invert
}

func (f TextFilter) found(e *LogEntry) bool { return f.matchLower(e.searchText()) }

// Ranges returns the byte ranges of the matches in s, for highlighting.
// Inverted and empty filters highlight nothing.
func (f TextFilter) Ranges(s string) [][2]int {
	if f.Invert || f.Empty() {
		return nil
	}
	var out [][2]int
	for _, m := range f.re.FindAllStringIndex(s, -1) {
		if m[1] > m[0] {
			out = append(out, [2]int{m[0], m[1]})
		}
	}
	return out
}

// FilterMode tells whether non-matching entries are hidden or kept.
type FilterMode int

// Filter modes.
const (
	ModeFilter FilterMode = iota
	ModeHighlight
)

// String names the mode for the status bar.
func (m FilterMode) String() string {
	if m == ModeHighlight {
		return "HIGHLIGHT"
	}
	return "FILTER"
}

// LogFilter combines a level set and stacked text filters (all must match).
type LogFilter struct {
	Levels  LevelSet
	Texts   []TextFilter
	Mode    FilterMode
	Context int // entries shown before and after each match (filter mode)
}

// NewLogFilter returns a filter that lets everything through.
func NewLogFilter() LogFilter { return LogFilter{Levels: AllLevels()} }

// Active reports whether any text filter is set.
func (f LogFilter) Active() bool {
	for _, t := range f.Texts {
		if !t.Empty() {
			return true
		}
	}
	return false
}

// LevelOK reports whether the entry's level is selected.
func (f LogFilter) LevelOK(e *LogEntry) bool { return f.Levels == nil || f.Levels[e.Level] }

// TextOK reports whether every text filter matches.
func (f LogFilter) TextOK(e *LogEntry) bool {
	for _, t := range f.Texts {
		if !t.Matches(e) {
			return false
		}
	}
	return true
}

// Match reports whether the entry is a match: its level is selected and,
// when text filters are set, they all match.
func (f LogFilter) Match(e *LogEntry) bool { return f.LevelOK(e) && f.TextOK(e) }

// Ranges returns the highlight ranges of every text filter in s.
func (f LogFilter) Ranges(s string) [][2]int {
	var out [][2]int
	for _, t := range f.Texts {
		out = append(out, t.Ranges(s)...)
	}
	return out
}

// Row is one displayed entry after filtering.
type Row struct {
	Index   int  // position in the input sequence
	Match   bool // the entry matches (false: context or unfiltered in highlight mode)
	Context bool // shown only as context around a match
	Gap     bool // a separator precedes this row (non-contiguous context groups)
}

// Select returns the rows to display among n entries. In filter mode,
// entries of unselected levels or failing a text filter are hidden, except
// as context around matches. In highlight mode, level filtering still
// applies and matching entries are flagged.
func (f LogFilter) Select(n int, get func(int) *LogEntry) []Row {
	return f.SelectAppend(make([]Row, 0, n), n, get)
}

// SelectAppend is Select appending to dst, so a caller refreshing a view
// many times a second can reuse its buffer.
func (f LogFilter) SelectAppend(dst []Row, n int, get func(int) *LogEntry) []Row {
	rows := dst[:0]
	if f.Mode == ModeHighlight || !f.Active() || f.Context <= 0 {
		for i := range n {
			e := get(i)
			if !f.LevelOK(e) {
				continue
			}
			match := f.Active() && f.TextOK(e)
			if f.Mode == ModeFilter && f.Active() && !match {
				continue
			}
			rows = append(rows, Row{Index: i, Match: match})
		}
		return rows
	}
	matched := make([]bool, n)
	for i := range n {
		matched[i] = f.Match(get(i))
	}
	last := -1 // last index emitted
	for i := range n {
		if !matched[i] {
			continue
		}
		from := max(i-f.Context, last+1, 0)
		to := min(i+f.Context, n-1)
		for j := from; j <= to; j++ {
			if j > i && matched[j] {
				break // the next match will emit its own context
			}
			rows = append(rows, Row{Index: j, Match: matched[j], Context: !matched[j], Gap: last >= 0 && j == from && j > last+1})
			last = j
		}
	}
	return rows
}
