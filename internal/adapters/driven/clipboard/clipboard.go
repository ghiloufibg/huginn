// Package clipboard implements ports.Clipboard with the system's
// clipboard command: pbcopy, wl-copy, xclip, xsel or clip.exe, whichever
// the environment has (docs/DECISIONS.md D-012). No cgo, no library: the
// user's own tool does the work.
package clipboard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ErrNoTool is returned when no clipboard command is installed.
var ErrNoTool = errors.New("no clipboard command found (pbcopy, wl-copy, xclip, xsel or clip.exe)")

// timeout bounds one copy: a clipboard command must not hold the UI.
const timeout = 3 * time.Second

// tool is a clipboard command and its arguments.
type tool struct {
	name string
	args []string
	when string // environment variable that must be set, if any
}

// tools are tried in order; the first installed one whose environment is
// present is used.
var tools = []tool{
	{name: "pbcopy"},
	{name: "wl-copy", when: "WAYLAND_DISPLAY"},
	{name: "xclip", args: []string{"-selection", "clipboard"}, when: "DISPLAY"},
	{name: "xsel", args: []string{"--clipboard", "--input"}, when: "DISPLAY"},
	{name: "clip.exe"}, // Windows, and WSL
}

// System copies with the first clipboard command available.
type System struct {
	// LookPath, Getenv and Run replace the system in tests. Defaults:
	// exec.LookPath, os.Getenv, and running the command with text on its
	// standard input.
	LookPath func(string) (string, error)
	Getenv   func(string) string
	Run      func(ctx context.Context, text, name string, args ...string) error

	once sync.Once
	tool *tool
}

// Copy implements ports.Clipboard.
func (s *System) Copy(ctx context.Context, text string) error {
	s.once.Do(s.find)
	if s.tool == nil {
		return ErrNoTool
	}
	run := s.Run
	if run == nil {
		run = execute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := run(ctx, text, s.tool.name, s.tool.args...); err != nil {
		return fmt.Errorf("%s: %w", s.tool.name, err)
	}
	return nil
}

// Tool is the command Copy uses, "" when none is available.
func (s *System) Tool() string {
	s.once.Do(s.find)
	if s.tool == nil {
		return ""
	}
	return s.tool.name
}

func (s *System) find() {
	look, getenv := s.LookPath, s.Getenv
	if look == nil {
		look = exec.LookPath
	}
	if getenv == nil {
		getenv = os.Getenv
	}
	for i := range tools {
		t := &tools[i]
		if t.when != "" && getenv(t.when) == "" {
			continue
		}
		if _, err := look(t.name); err == nil {
			s.tool = t
			return
		}
	}
}

// execute runs the command with text on its standard input.
func execute(ctx context.Context, text, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(text)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			first, _, _ := strings.Cut(msg, "\n")
			return errors.New(first)
		}
		return err
	}
	return nil
}
