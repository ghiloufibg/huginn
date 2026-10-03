package tui

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// selection is the lines chosen on a logs screen for copying (M9, D-049):
// a range between two entries, and marked entries. It holds sequence
// numbers, never screen rows, so wrapping, panning and folded stacks do
// not matter; only displayed entries are ever copied.
type selection struct {
	anchor    uint64 // first end of the range (0: no range)
	end       uint64 // other end, when the range is not extending
	extending bool   // V started a range: its end follows the cursor
	marks     map[uint64]bool
}

// active reports whether any line is selected.
func (s *selection) active() bool { return s.anchor != 0 || len(s.marks) > 0 }

// toggleRange starts a range at the cursor, or ends the one being
// extended (it stays selected).
func (l *logsScreen) toggleRange(m *Model) {
	e, ok := l.entryAt(l.displayCursor())
	if !ok {
		return
	}
	s := &l.sel
	if s.extending {
		s.end, s.extending = e.Seq, false
		m.flash("range kept · y copy · Y copy raw · esc clear")
		return
	}
	l.cursor, l.tail = l.displayCursor(), false // selecting stops following the tail
	s.anchor, s.end, s.extending = e.Seq, e.Seq, true
	m.flash("select: move to extend, V to end the range")
}

// toggleMark marks or unmarks the cursor line.
func (l *logsScreen) toggleMark(m *Model) {
	e, ok := l.entryAt(l.displayCursor())
	if !ok {
		return
	}
	if l.sel.marks == nil {
		l.sel.marks = map[uint64]bool{}
	}
	if l.sel.marks[e.Seq] {
		delete(l.sel.marks, e.Seq)
		m.flash("unmarked")
		return
	}
	l.sel.marks[e.Seq] = true
	m.flash(fmt.Sprintf("marked (%d)", len(l.sel.marks)))
}

func (l *logsScreen) clearSelection(m *Model) {
	l.sel = selection{}
	m.flash("selection cleared")
}

// pruneSelection drops evicted entries from the selection.
func (l *logsScreen) pruneSelection() {
	first := l.buf.FirstSeq()
	s := &l.sel
	if s.anchor != 0 && (s.anchor < first || (!s.extending && s.end < first)) {
		s.anchor, s.end, s.extending = 0, 0, false
	}
	for seq := range s.marks {
		if seq < first {
			delete(s.marks, seq)
		}
	}
}

// rangeBounds returns the display positions of the displayed entries in
// the range, lo ≤ hi, or ok false when there is none. Outside a trace,
// rows are in sequence order: the range is every displayed entry between
// its two ends, even when a filter hides an end. In a trace (time order),
// both ends must be displayed.
func (l *logsScreen) rangeBounds() (lo, hi int, ok bool) {
	s := &l.sel
	if s.anchor == 0 {
		return 0, 0, false
	}
	end := s.end
	if s.extending {
		e, found := l.entryAt(l.displayCursor())
		if !found {
			return 0, 0, false
		}
		end = e.Seq
	}
	a, b := min(s.anchor, end), max(s.anchor, end)
	rows := l.rows[:l.shown()]
	if l.trace != nil {
		i := slices.IndexFunc(rows, func(r viewRow) bool { return r.seq == a })
		j := slices.IndexFunc(rows, func(r viewRow) bool { return r.seq == b })
		if i < 0 || j < 0 {
			return 0, 0, false
		}
		lo, hi = min(i, j), max(i, j)
	} else {
		bySeq := func(r viewRow, s uint64) int { return cmpSeq(r.seq, s) }
		lo, _ = slices.BinarySearchFunc(rows, a, bySeq)
		j, found := slices.BinarySearchFunc(rows, b, bySeq)
		hi = j - 1
		if found {
			hi = j
		}
		if lo > hi {
			return 0, 0, false
		}
	}
	if l.newestTop {
		n := len(rows)
		lo, hi = n-1-hi, n-1-lo
	}
	return lo, hi, true
}

// gutter returns the selection mark of display position i, for a frame
// whose range is lo..hi (ok false: none).
func (l *logsScreen) gutter(i int, seq uint64, lo, hi int, ok bool) string {
	switch {
	case ok && i >= lo && i <= hi:
		return "▌"
	case l.sel.marks[seq]:
		return "*"
	}
	return " "
}

// selected returns the entries to copy, in display order: the range and
// the marks, or the cursor line when nothing is selected.
func (l *logsScreen) selected() []*domain.LogEntry {
	var out []*domain.LogEntry
	if !l.sel.active() {
		if e, ok := l.entryAt(l.displayCursor()); ok {
			out = append(out, e)
		}
		return out
	}
	lo, hi, ok := l.rangeBounds()
	for i := range l.shown() {
		e, found := l.entryAt(i)
		if found && ((ok && i >= lo && i <= hi) || l.sel.marks[e.Seq]) {
			out = append(out, e)
		}
	}
	return out
}

// selectionSummary is the status-bar description of the selection.
func (l *logsScreen) selectionSummary() string {
	var parts []string
	if lo, hi, ok := l.rangeBounds(); ok {
		parts = append(parts, fmt.Sprintf("%d lines", hi-lo+1))
	}
	if n := len(l.sel.marks); n > 0 {
		parts = append(parts, fmt.Sprintf("%d marked", n))
	}
	if len(parts) == 0 {
		return "range hidden by the filters"
	}
	return "selected " + strings.Join(parts, " + ")
}

// copyForm is how lines are copied.
type copyForm int

const (
	copyShown copyForm = iota // as the layout draws them, uncolored and whole
	copyRaw                   // the original line as received
)

func (f copyForm) String() string {
	if f == copyRaw {
		return "raw"
	}
	return "as shown"
}

// copyLines copies entries to the clipboards.
func (l *logsScreen) copyLines(m *Model, entries []*domain.LogEntry, form copyForm) tea.Cmd {
	if len(entries) == 0 {
		m.flash("nothing to copy")
		return nil
	}
	l.lastForm = form
	w := l.lineWriter(m, form)
	var b strings.Builder
	for _, e := range entries {
		w.write(&b, e)
		if b.Len() > m.opts.CopyMaxBytes {
			m.flash(fmt.Sprintf("selection too large to copy (over %s, ui.yaml copy.max_bytes): %s saves it to a file", byteSize(m.opts.CopyMaxBytes), m.label(ActSave)))
			return nil
		}
	}
	text := cleanCopy(m.opts.Redactor.Redact(b.String()))
	return m.copyText(text, plural(len(entries), "line"), form.String())
}

// lineWriter writes entries as copied or saved: it holds the display
// settings of the moment, so it can run away from the UI goroutine.
type lineWriter struct {
	form       copyForm
	podID      podIDMode
	containers bool // name the container after the pod id
	opts       ports.RenderOptions
	layout     func(*domain.LogEntry) ports.LogLayout
	trace      bool
	traceStart time.Time
	redact     domain.Redactor
}

func (l *logsScreen) lineWriter(m *Model, form copyForm) lineWriter {
	w := lineWriter{
		form: form, podID: l.podID, containers: l.multiContainer, layout: m.layout, redact: m.opts.Redactor,
		// The columns the user hid (c, C, z) are left out; the ones only a
		// narrow terminal hides are kept: a copy is not bound by the width.
		opts: ports.RenderOptions{Timestamps: l.timestamps, Now: m.opts.Now(), Hide: l.hide},
	}
	if l.trace != nil {
		w.trace, w.traceStart = true, l.trace.start
	}
	return w
}

// write writes one entry and a new line: raw, or as the logs screen draws
// it, without colors, truncation or wrapping, followed by its whole stack
// trace.
func (w lineWriter) write(b *strings.Builder, e *domain.LogEntry) {
	if w.form == copyRaw {
		b.WriteString(e.Raw)
		b.WriteByte('\n')
		return
	}
	if w.trace {
		b.WriteString(formatDelta(e.Time.Sub(w.traceStart)))
		b.WriteByte(' ')
	}
	if id := podLabelFor(e.Pod, w.podID); id != "" {
		b.WriteString(id)
		if w.containers && e.Container != "" {
			b.WriteString("/" + e.Container)
		}
		b.WriteByte(' ')
	}
	for _, s := range w.layout(e).Render(*e, w.opts) {
		b.WriteString(s.Text)
	}
	if e.Stack != "" {
		b.WriteByte('\n')
		b.WriteString(strings.TrimRight(e.Stack, "\n"))
	}
	b.WriteByte('\n')
}

// cleanCopy removes control characters from copied text, but tab and new
// line: log text is untrusted (D-037), and a pasted escape sequence could
// act on the terminal it is pasted in.
func cleanCopy(s string) string {
	clean := true
	for i := range len(s) {
		if c := s[i]; (c < 0x20 && c != '\t' && c != '\n') || c == 0x7f {
			clean = false
			break
		}
	}
	if clean && utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			b.WriteRune(r)
		case (r < 0x20 && r != '\t' && r != '\n') || r == 0x7f || (r >= 0x80 && r < 0xa0):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// clipboardDoneMsg reports the system clipboard's result.
type clipboardDoneMsg struct {
	err   error
	osc52 bool // the terminal got the text too
	what  string
}

// copyText sends text to the terminal (OSC 52) and to the system
// clipboard, as ui.yaml clipboard says; what and form describe it.
func (m *Model) copyText(text, what, form string) tea.Cmd {
	osc, system := m.opts.ClipboardOSC52, m.opts.Clipboard
	if !osc && system == nil {
		m.flash("copying is off (ui.yaml clipboard)")
		return nil
	}
	desc := fmt.Sprintf("%s (%s, %s)", what, byteSize(len(text)), form)
	m.flash("copied " + desc)
	var cmds []tea.Cmd
	if osc {
		cmds = append(cmds, tea.SetClipboard(text))
	}
	if system != nil {
		ctx := m.opts.Context
		cmds = append(cmds, func() tea.Msg {
			return clipboardDoneMsg{err: system.Copy(ctx, text), osc52: osc, what: desc}
		})
	}
	return tea.Batch(cmds...)
}

// clipboardDone reports a system clipboard failure; success was already
// flashed.
func (m *Model) clipboardDone(msg clipboardDoneMsg) {
	switch {
	case msg.err == nil:
	case msg.osc52:
		m.flash("copied " + msg.what + " via the terminal (OSC 52); system clipboard: " + msg.err.Error())
	default:
		m.flash("copy failed: " + msg.err.Error())
	}
}

func cmpSeq(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
