package domain

// Secret wraps a sensitive value so that it cannot be printed by accident:
// String, GoString and MarshalText all return a placeholder. Call Reveal only
// where the value is handed to a library that needs it.
type Secret struct{ v string }

// NewSecret wraps v.
func NewSecret(v string) Secret { return Secret{v: v} }

// Reveal returns the underlying value. Never pass it to a renderer, a logger
// or an exporter.
func (s Secret) Reveal() string { return s.v }

// IsZero reports whether the secret is empty.
func (s Secret) IsZero() bool { return s.v == "" }

const redacted = "[redacted]"

// String returns a placeholder, never the value.
func (s Secret) String() string { return redacted }

// GoString returns a placeholder, never the value.
func (s Secret) GoString() string { return redacted }

// MarshalText returns a placeholder, never the value.
func (s Secret) MarshalText() ([]byte, error) { return []byte(redacted), nil }
