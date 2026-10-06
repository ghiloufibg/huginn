package tui

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// saveDoneMsg reports a save (M9.2).
type saveDoneMsg struct {
	path  string
	lines int
	bytes int64
	err   error
}

// save writes the selection, or every displayed line when nothing is
// selected, to a new file, in the form of the last copy (as shown unless
// Y was used). The lines are copied on the UI goroutine, since the
// buffer's slots are reused by new lines; the file is written away from
// it, streamed.
func (l *logsScreen) save(m *Model) tea.Cmd {
	if m.opts.Files == nil {
		m.flash("saving is not available")
		return nil
	}
	var entries []*domain.LogEntry
	if l.sel.active() {
		entries = l.selected()
	} else {
		for i := range l.shown() {
			if e, ok := l.entryAt(i); ok {
				entries = append(entries, e)
			}
		}
	}
	if len(entries) == 0 {
		m.flash("nothing to save")
		return nil
	}
	snap := make([]domain.LogEntry, len(entries))
	for i, e := range entries {
		snap[i] = *e
	}
	w := l.lineWriter(m, l.lastForm)
	name := saveName(l.repo, m.env.Name, m.opts.Now().Format("20060102-150405"), l.lastForm)
	files, ctx := m.opts.Files, m.opts.Context
	m.flash(fmt.Sprintf("saving %s (%s)…", plural(len(snap), "line"), l.lastForm))
	return func() tea.Msg {
		var n countWriter
		path, err := files.Save(ctx, name, func(out io.Writer) error {
			n.w = out
			var b strings.Builder
			for i := range snap {
				b.Reset()
				w.write(&b, &snap[i])
				if _, err := io.WriteString(&n, cleanCopy(w.redact.Redact(b.String()))); err != nil {
					return err
				}
			}
			return nil
		})
		return saveDoneMsg{path: path, lines: len(snap), bytes: n.n, err: err}
	}
}

func (m *Model) saveDone(msg saveDoneMsg) {
	if msg.err != nil {
		m.flash("save failed: " + msg.err.Error())
		return
	}
	m.flash(fmt.Sprintf("saved %s (%s) to %s", plural(msg.lines, "line"), byteSize(int(msg.bytes)), msg.path))
}

// saveName is <repo>-<env>-<time>.log, or .raw.log for raw lines (which
// are JSON only when the logs are): only letters, digits, '.', '_' and
// '-' are kept.
func saveName(repo, env, stamp string, form copyForm) string {
	ext := ".log"
	if form == copyRaw {
		ext = ".raw.log"
	}
	return safeName(repo) + "-" + safeName(env) + "-" + stamp + ext
}

func safeName(s string) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '_'
	}, s)
	s = strings.TrimLeft(s, ".")
	if s == "" {
		return "logs"
	}
	return s
}

// countWriter counts the bytes written through it.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
