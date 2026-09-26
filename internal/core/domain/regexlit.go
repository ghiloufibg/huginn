package domain

import (
	"regexp/syntax"
	"strings"
	"unicode/utf8"
)

// regexLiterals analyses a case-insensitive pattern and returns
// lower-cased literals such that any match contains at least one of them.
// exact is true when the pattern is only an alternation of literals, in
// which case containing a literal is a match. It returns nil when no
// useful prefilter exists (e.g. literals shorter than 2 bytes).
func regexLiterals(pattern string) (lits []string, exact bool) {
	re, err := syntax.Parse(pattern, syntax.Perl|syntax.FoldCase)
	if err != nil {
		return nil, false
	}
	re = re.Simplify()
	lits, exact, ok := required(re)
	if !ok || len(lits) == 0 {
		return nil, false
	}
	for _, l := range lits {
		if len(l) < 2 {
			return nil, false
		}
	}
	return lits, exact
}

// required returns literals one of which every match contains, and
// whether the expression is exactly an alternation of those literals.
func required(re *syntax.Regexp) ([]string, bool, bool) {
	switch re.Op {
	case syntax.OpLiteral:
		return []string{strings.ToLower(string(re.Rune))}, true, true
	case syntax.OpCapture:
		return required(re.Sub[0])
	case syntax.OpPlus:
		lits, _, ok := required(re.Sub[0])
		return lits, false, ok
	case syntax.OpConcat:
		var best []string
		exact := len(re.Sub) == 1
		for _, sub := range re.Sub {
			if lits, ex, ok := required(sub); ok && shortest(lits) > shortest(best) {
				best = lits
				exact = exact && ex
			}
		}
		return best, exact, best != nil
	case syntax.OpAlternate:
		var all []string
		exact := true
		for _, sub := range re.Sub {
			lits, ex, ok := required(sub)
			if !ok {
				return nil, false, false
			}
			all, exact = append(all, lits...), exact && ex
		}
		return all, exact, true
	}
	return nil, false, false
}

func shortest(lits []string) int {
	if len(lits) == 0 {
		return 0
	}
	n := utf8.RuneCountInString(lits[0])
	for _, l := range lits[1:] {
		n = min(n, utf8.RuneCountInString(l))
	}
	return n
}
