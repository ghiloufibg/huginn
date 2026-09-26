// Package diag writes Huginn's own diagnostic log to a file. It never
// writes to the terminal, which belongs to the TUI.
package diag

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// EnvDebug enables debug logging when set to a non-empty value other than 0.
const EnvDebug = "HUGINN_DEBUG"

// Options configure the diagnostic log.
type Options struct {
	// Level is "" (disabled unless HUGINN_DEBUG is set), debug, info, warn
	// or error.
	Level string
	// Debug is the value of HUGINN_DEBUG.
	Debug string
	// Dir is the directory of huginn.log (usually the user cache dir).
	Dir string
}

// Open returns a logger and a closer. With logging disabled it returns a
// logger that discards everything.
func Open(o Options) (*slog.Logger, io.Closer, string, error) {
	level, enabled, err := resolveLevel(o)
	if err != nil {
		return nil, nil, "", err
	}
	if !enabled {
		return slog.New(slog.DiscardHandler), io.NopCloser(nil), "", nil
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, nil, "", fmt.Errorf("create log dir: %w", err)
	}
	path := filepath.Join(o.Dir, "huginn.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, "", fmt.Errorf("open diagnostic log: %w", err)
	}
	h := slog.NewTextHandler(f, &slog.HandlerOptions{Level: level, ReplaceAttr: redactAttr})
	return slog.New(h), f, path, nil
}

func resolveLevel(o Options) (slog.Level, bool, error) {
	if o.Level != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(o.Level)); err != nil {
			return 0, false, fmt.Errorf("invalid --log-level %q: use debug, info, warn or error", o.Level)
		}
		return l, true, nil
	}
	if o.Debug != "" && o.Debug != "0" {
		return slog.LevelDebug, true, nil
	}
	return 0, false, nil
}

var sensitiveKey = regexp.MustCompile(`(?i)(pass(word)?|secret|token|authorization|api[_-]?key|credential|cookie)`)

// redactAttr masks attributes whose key looks sensitive. domain.Secret
// values already print as [redacted] on their own.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if sensitiveKey.MatchString(a.Key) {
		return slog.String(a.Key, "[redacted]")
	}
	if a.Value.Kind() == slog.KindString && strings.Contains(strings.ToLower(a.Value.String()), "bearer ") {
		return slog.String(a.Key, "[redacted]")
	}
	return a
}

// Discard is a logger that drops everything, for tests and defaults.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }
