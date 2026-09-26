package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// frameRate caps how often the terminal is redrawn.
const frameRate = 30

// Run starts the UI and blocks until the user quits or ctx ends.
func Run(ctx context.Context, o Options) error {
	o.Context = ctx
	m := NewModel(o)
	defer m.stop()
	// 30 frames a second matches the log sessions' batches (33 ms): the
	// default 60 only doubles the terminal diffing while streaming.
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithFPS(frameRate)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
