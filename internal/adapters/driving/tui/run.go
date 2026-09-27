package tui

import (
	"context"
	"errors"
	"os"

	"golang.org/x/term"

	tea "charm.land/bubbletea/v2"
)

// frameRate caps how often the terminal is redrawn.
const frameRate = 30

// Run starts the UI and blocks until the user quits or ctx ends.
func Run(ctx context.Context, o Options) error {
	if !interactive() {
		return ErrNoTerminal
	}
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

// ErrNoTerminal reports that Huginn was started without an interactive terminal (in a
// pipe, a CI job, a cron), where a full-screen UI cannot run.
var ErrNoTerminal = errors.New("needs an interactive terminal: run it in a terminal, not through a pipe or a redirection")

// interactive reports whether the UI can run: output to a terminal, and
// input from one (Bubble Tea falls back to /dev/tty when stdin is piped).
func interactive() bool {
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return false
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		return true
	}
	f, err := os.Open("/dev/tty")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
