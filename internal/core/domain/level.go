package domain

import "strings"

// Level is the severity of a log entry.
type Level int

// Levels ordered by severity. LevelUnknown is used when no level could be
// detected; it is not "below debug", it is simply unknown.
const (
	LevelUnknown Level = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

// Levels returns every known level from least to most severe, then unknown.
func Levels() []Level { return []Level{LevelDebug, LevelInfo, LevelWarn, LevelError, LevelUnknown} }

var levelNames = map[Level]string{
	LevelUnknown: "UNKNOWN",
	LevelDebug:   "DEBUG",
	LevelInfo:    "INFO",
	LevelWarn:    "WARN",
	LevelError:   "ERROR",
}

// String returns the upper-case name used in the Spring Boot console layout.
func (l Level) String() string { return levelNames[l] }

// defaultLevelAliases maps common spellings (Logback, Log4j, JUL, Python,
// Cloud Logging severities, klog letters) to a Level. Adapters may extend it
// with configured aliases through ParseLevelWith.
var defaultLevelAliases = map[string]Level{
	"trace": LevelDebug, "debug": LevelDebug, "fine": LevelDebug, "finer": LevelDebug, "finest": LevelDebug, "d": LevelDebug,
	"info": LevelInfo, "information": LevelInfo, "notice": LevelInfo, "config": LevelInfo, "i": LevelInfo,
	"warn": LevelWarn, "warning": LevelWarn, "w": LevelWarn,
	"error": LevelError, "err": LevelError, "severe": LevelError, "fatal": LevelError, "critical": LevelError,
	"crit": LevelError, "alert": LevelError, "emergency": LevelError, "panic": LevelError, "e": LevelError, "f": LevelError,
}

// ParseLevel converts a textual level to a Level, case-insensitively.
// Unrecognized values return LevelUnknown and false.
func ParseLevel(s string) (Level, bool) { return ParseLevelWith(s, nil) }

// ParseLevelWith is ParseLevel with extra aliases that take precedence over
// the built-in ones. Alias keys must be lower-case.
func ParseLevelWith(s string, aliases map[string]Level) (Level, bool) {
	k := strings.ToLower(strings.TrimSpace(s))
	if l, ok := aliases[k]; ok {
		return l, true
	}
	if l, ok := defaultLevelAliases[k]; ok {
		return l, true
	}
	return LevelUnknown, false
}

// LevelSet is a set of levels, used by level filters.
type LevelSet map[Level]bool

// AllLevels returns a set containing every level, including unknown.
func AllLevels() LevelSet {
	s := LevelSet{}
	for _, l := range Levels() {
		s[l] = true
	}
	return s
}

// Contains reports whether l is in the set.
func (s LevelSet) Contains(l Level) bool { return s[l] }
