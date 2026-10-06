package ports

import "context"

// Clipboard puts text on the system clipboard (docs/DECISIONS.md D-012).
// The terminal's own clipboard (OSC 52) is written by the TUI, which owns
// the terminal; this port is the system one, used alongside or instead.
type Clipboard interface {
	Copy(ctx context.Context, text string) error
}
