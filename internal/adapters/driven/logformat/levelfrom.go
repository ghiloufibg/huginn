package logformat

import (
	"path"
	"slices"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// LevelRule maps a glob on a field value to a level.
type LevelRule struct {
	Glob  string
	Level domain.Level
}

// sortRules returns a copy of rules, longest glob first, so "5*" is tried
// before "*".
func sortRules(rules []LevelRule) []LevelRule {
	rules = slices.Clone(rules)
	slices.SortStableFunc(rules, func(a, b LevelRule) int { return len(b.Glob) - len(a.Glob) })
	return rules
}

// raise returns the level of an entry whose level_from field holds v: the
// more severe of l and the level of the first rule matching v (rules
// sorted by sortRules), or l when no rule matches. It never lowers l, so
// a line the application logged as an error stays one.
func raise(l domain.Level, rules []LevelRule, v string) domain.Level {
	for _, r := range rules {
		if ok, _ := path.Match(r.Glob, v); ok {
			return max(l, r.Level)
		}
	}
	return l
}
