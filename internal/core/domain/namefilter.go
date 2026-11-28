package domain

import "path"

// NameFilter matches workload names against shell patterns ("*", "?",
// "[a-z]") over the whole name, case-sensitive. The patterns were validated
// with the config folder; one that does not compile matches nothing.
type NameFilter struct {
	Patterns []string
}

// Matches reports whether name matches one of the patterns.
func (f NameFilter) Matches(name string) bool {
	for _, p := range f.Patterns {
		if ok, err := path.Match(p, name); err == nil && ok {
			return true
		}
	}
	return false
}

// Hides reports whether pod p belongs to a workload the filter hides: it
// names one as its owner, or it is a bare pod with such a name, or a hidden
// workload of ws owns it (by selector).
func (f NameFilter) Hides(p Pod, ws []Workload) bool {
	if len(f.Patterns) == 0 {
		return false
	}
	if p.OwnerName != "" {
		return f.Matches(p.OwnerName)
	}
	if f.Matches(p.Name) {
		return true
	}
	for _, w := range ws {
		if f.Matches(w.Ref.Name) && w.Owns(p) {
			return true
		}
	}
	return false
}
