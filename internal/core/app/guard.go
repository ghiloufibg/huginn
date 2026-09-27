package app

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// A panic in a background goroutine would end the process with the
// terminal left in raw mode. The goroutines of the use cases therefore
// recover, log the panic with its stack to the diagnostic log and report
// an error through their normal channel.

// recovered must be deferred at the top of a goroutine: it stops a panic,
// logs it and calls report with an error describing it.
func recovered(log *slog.Logger, what string, report func(error)) {
	r := recover()
	if r == nil {
		return
	}
	log.Error("internal error recovered", "in", what, "panic", fmt.Sprint(r), "stack", string(debug.Stack()))
	if report != nil {
		report(fmt.Errorf("%s: internal error (see the diagnostic log): %v", what, r))
	}
}

// safeDecode decodes a line; a decoder that panics on an unexpected line
// yields the raw text as an undecoded entry instead.
func safeDecode(dec ports.LogDecoder, l domain.RawLine) (e domain.LogEntry) {
	defer func() {
		if recover() != nil {
			e = domain.LogEntry{Time: l.Time, Pod: l.Pod, Container: l.Container, Raw: l.Text, Message: l.Text}
		}
		e.Received = l.Time
	}()
	return dec.Decode(l)
}
