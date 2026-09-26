package tui

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
)

// Run starts the UI and blocks until the user quits or ctx ends.
func Run(ctx context.Context, o Options) error {
	p := tea.NewProgram(NewModel(o), tea.WithContext(ctx))
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}
