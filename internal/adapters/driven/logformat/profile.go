package logformat

import "github.com/ghiloufibg/huginn/internal/core/domain"

// Profile tells the JSON decoder where each canonical field lives. Each
// field lists candidate paths; the first present wins. A path is looked up
// as a literal top-level key first ("log.level" as one key), then as a
// dotted walk into nested objects.
type Profile struct {
	Timestamp, Level, Logger, Thread, Message, Stack, TraceID, App, PID []string
	// LevelAliases maps extra lower-case level spellings, including
	// numeric ones such as "40000" or "50", to levels.
	LevelAliases map[string]domain.Level
	// Hidden lists glob patterns (path.Match syntax) of flattened keys
	// moved to LogEntry.Hidden, e.g. "kubernetes.*".
	Hidden []string
}
