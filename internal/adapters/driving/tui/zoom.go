package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// zoomScreen shows one entry in full (mockup board 6).
type zoomScreen struct {
	logs     *logsScreen
	seq      uint64
	raw      bool   // pretty JSON instead of the structured view
	metadata bool   // hidden fields expanded
	field    string // key of the selected field, "" for none
	reveal   bool   // scroll the selected field into view at the next draw
	fieldRow int    // line of the selected field in the last structured view, -1 for none
	offset   int
	height   int
}

// zoomField is one row of the FIELDS section: a field a filter can use.
type zoomField struct{ key, value string }

// zoomFields lists the fields of e shown in FIELDS, in their order: the
// trace id, then the visible fields by key. Hidden fields are not listed
// there and cannot be filtered on (D-046).
func zoomFields(e *domain.LogEntry) []zoomField {
	var out []zoomField
	if e.TraceID != "" {
		out = append(out, zoomField{"trace_id", e.TraceID})
	}
	for _, k := range sortedKeys(e.Fields) {
		out = append(out, zoomField{k, e.Fields[k]})
	}
	return out
}

func newZoomScreen(l *logsScreen, seq uint64) *zoomScreen { return &zoomScreen{logs: l, seq: seq} }

func (z *zoomScreen) crumbs() []string {
	return []string{"services", z.logs.repo, "logs", fmt.Sprintf("entry %d", z.seq)}
}

func (z *zoomScreen) entry() (*domain.LogEntry, bool) {
	i, ok := z.logs.buf.Index(z.seq)
	if !ok {
		return nil, false
	}
	return z.logs.buf.At(i), true
}

func (z *zoomScreen) update(m *Model, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	keys, key := m.opts.Keys, k.String()
	switch {
	case keys.Is(key, ActDown):
		z.offset++
	case keys.Is(key, ActUp):
		z.offset = max(z.offset-1, 0)
	case keys.Is(key, ActPageDown):
		z.offset += max(z.height-2, 1)
	case keys.Is(key, ActPageUp):
		z.offset = max(z.offset-max(z.height-2, 1), 0)
	case keys.Is(key, ActTop):
		z.offset = 0
	case keys.Is(key, ActJSONView):
		z.raw, z.offset = !z.raw, 0
	case keys.Is(key, ActNextEntry):
		z.step(1)
	case keys.Is(key, ActPrevEntry):
		z.step(-1)
	case keys.Is(key, ActOpen), key == "space":
		z.metadata = !z.metadata
	case keys.Is(key, ActFieldNext):
		z.moveField(1)
	case keys.Is(key, ActFieldPrev):
		z.moveField(-1)
	case keys.Is(key, ActFieldKeep), keys.Is(key, ActFieldExclude):
		z.filterOnField(m, keys.Is(key, ActFieldExclude))
	case keys.Is(key, ActViewTrace):
		l, seq := z.logs, z.seq
		m.pop()
		l.enterTrace(m, seq)
	default:
		return false, nil
	}
	return true, nil
}

// moveField moves the field cursor, wrapping; the first move selects the
// first (or last) field.
func (z *zoomScreen) moveField(dir int) {
	e, ok := z.entry()
	if !ok || z.raw {
		return
	}
	fs := zoomFields(e)
	if len(fs) == 0 {
		return
	}
	i := slices.IndexFunc(fs, func(f zoomField) bool { return f.key == z.field })
	switch {
	case i < 0 && dir > 0:
		i = 0
	case i < 0:
		i = len(fs) - 1
	default:
		i = (i + dir + len(fs)) % len(fs)
	}
	z.field, z.reveal = fs[i].key, true
}

// filterOnField adds a filter on the selected field's value to the logs
// and goes back to them, on this entry when it still shows.
func (z *zoomScreen) filterOnField(m *Model, exclude bool) {
	e, ok := z.entry()
	if !ok || z.field == "" {
		return
	}
	v, ok := domain.FieldValue(e, z.field)
	if !ok {
		return
	}
	l, seq := z.logs, z.seq
	m.pop()
	l.addFilter(m, domain.FieldFilter(z.field, v, exclude), seq)
}

// step moves to the next or previous entry of the logs view.
func (z *zoomScreen) step(dir int) {
	v := z.logs.seqList()
	i := slices.Index(v, z.seq)
	if i < 0 {
		return
	}
	if z.logs.newestTop {
		dir = -dir
	}
	if j := i + dir; j >= 0 && j < len(v) {
		z.seq, z.offset = v[j], 0
		if e, ok := z.entry(); ok && z.field != "" {
			if _, has := domain.FieldValue(e, z.field); !has {
				z.field = "" // the next entry has no such field
			}
		}
		z.reveal = z.field != ""
	}
}

// zoomMaxBytes caps the part of one line the zoom wraps.
const zoomMaxBytes = 64 << 10

func byteSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

func (z *zoomScreen) view(m *Model, w, h int) string {
	z.height = h
	t := m.opts.Theme
	e, ok := z.entry()
	if !ok {
		return centered(t.Dim.Render("this entry left the buffer"), w, h)
	}
	var lines []string
	z.fieldRow = -1
	if z.raw {
		lines = z.rawLines(e)
	} else {
		lines = z.structured(m, e)
	}
	var out []string
	fieldOut := -1
	for i, l := range lines {
		if i == z.fieldRow {
			fieldOut = len(out)
		}
		l = safeText(l)
		more := 0
		if len(l) > zoomMaxBytes {
			// A megabyte payload would wrap into thousands of rows at every
			// frame: show its beginning and say how much is left.
			cut := zoomMaxBytes
			for cut > 0 && !utf8.RuneStart(l[cut]) {
				cut--
			}
			l, more = l[:cut], len(l)-cut
		}
		out = append(out, strings.Split(ansi.Hardwrap(l, max(w-2, 10), true), "\n")...)
		if more > 0 {
			out = append(out, t.Dim.Render(fmt.Sprintf("… %s more not shown", byteSize(more))))
		}
	}
	if z.reveal && fieldOut >= 0 {
		switch {
		case fieldOut < z.offset:
			z.offset = fieldOut
		case fieldOut >= z.offset+h:
			z.offset = fieldOut - h + 1
		}
	}
	z.reveal = false
	z.offset = min(z.offset, max(len(out)-h, 0))
	return strings.Join(out[z.offset:], "\n")
}

func (z *zoomScreen) rawLines(e *domain.LogEntry) []string {
	var b bytes.Buffer
	if err := json.Indent(&b, []byte(e.Raw), " ", "  "); err != nil {
		return []string{" " + e.Raw}
	}
	return strings.Split(" "+b.String(), "\n")
}

func (z *zoomScreen) structured(m *Model, e *domain.LogEntry) []string {
	t := m.opts.Theme
	l := z.logs
	sec := func(title, note string) string { return " " + t.Bold.Render(title) + t.Dim.Render(note) }
	pos := slices.Index(l.seqList(), e.Seq) + 1
	head := t.Dim.Render(fmt.Sprintf(" entry %d of %d   pod ", pos, len(l.rows))) +
		t.podStyle(l.podColor[e.Pod]).Render(e.Pod) + t.Dim.Render("   container ") + t.Bold.Render(e.Container)
	var line strings.Builder
	line.WriteString(" ")
	for _, s := range m.layout(e).Render(*e, ports.RenderOptions{Now: m.opts.Now(), Full: true}) {
		line.WriteString(l.segmentStyle(t, e, s.Role).Render(s.Text))
	}
	out := []string{head, "", line.String(), ""}
	if fs := zoomFields(e); len(fs) > 0 {
		keys := m.opts.Keys
		out = append(out, sec("FIELDS", fmt.Sprintf("   %s field · %s keep · %s exclude",
			keys.First(ActFieldNext), keys.First(ActFieldKeep), keys.First(ActFieldExclude))))
		for _, f := range fs {
			mark, key, value := "   ", t.Dim.Render(fmt.Sprintf("%-16s", f.key)), f.value
			if f.key == "trace_id" {
				value = t.Key.Render(value)
			}
			if f.key == z.field {
				z.fieldRow = len(out)
				mark, key = " "+t.Key.Render(">")+" ", t.Bold.Render(fmt.Sprintf("%-16s", f.key))
			}
			out = append(out, mark+key+value)
		}
		out = append(out, "")
	}
	if e.Stack != "" {
		out = append(out, sec("STACK TRACE", "   own frames in bold, framework frames dimmed"))
		out = append(out, z.stack(t, m.layout(e), e.Stack)...)
		out = append(out, "")
	}
	out = append(out, sec("CONTEXT", "   same pod, 3 entries before and after"))
	out = append(out, z.context(m, e)...)
	out = append(out, "")
	if hidden := e.HiddenFields(); len(hidden) > 0 {
		if z.metadata {
			out = append(out, sec("HIDDEN FIELDS", fmt.Sprintf("   %d fields · enter to collapse", len(hidden))))
			for _, k := range sortedKeys(hidden) {
				out = append(out, "   "+t.Dim.Render(fmt.Sprintf("%-32s", k))+hidden[k])
			}
		} else {
			out = append(out, t.Dim.Render(fmt.Sprintf(" [+] hidden fields   %d fields · enter to expand", len(hidden))))
		}
	}
	return out
}

func (z *zoomScreen) stack(t Theme, layout ports.LogLayout, stack string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(stack, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		frame, isFrame := strings.CutPrefix(trimmed, "at ")
		style := t.Stack.Bold(true)
		switch {
		case isFrame && layout.FrameworkFrame(frame):
			style = t.Dim
		case isFrame:
			style = t.Bold
		case strings.HasPrefix(trimmed, "..."):
			style = t.Dim
		}
		out = append(out, "   "+style.Render(strings.ReplaceAll(line, "\t", "    ")))
	}
	return out
}

// context renders the entries of the same pod around e.
func (z *zoomScreen) context(m *Model, e *domain.LogEntry) []string {
	t := m.opts.Theme
	buf := z.logs.buf
	idx, _ := buf.Index(e.Seq)
	var before, after []*domain.LogEntry
	for i := idx - 1; i >= 0 && len(before) < 3; i-- {
		if c := buf.At(i); c.Pod == e.Pod {
			before = append([]*domain.LogEntry{c}, before...)
		}
	}
	for i := idx + 1; i < buf.Len() && len(after) < 3; i++ {
		if c := buf.At(i); c.Pod == e.Pod {
			after = append(after, c)
		}
	}
	render := func(c *domain.LogEntry, style func(...string) string) string {
		var b strings.Builder
		for _, s := range m.layout(c).Render(*c, ports.RenderOptions{Now: m.opts.Now()}) {
			b.WriteString(s.Text)
		}
		return "   " + style(b.String())
	}
	var out []string
	for _, c := range before {
		out = append(out, render(c, t.Dim.Render))
	}
	out = append(out, render(e, func(s ...string) string { return t.Bold.Render(s...) + t.Dim.Render("   <- this entry") }))
	for _, c := range after {
		out = append(out, render(c, t.Dim.Render))
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func (z *zoomScreen) statusLeft(m *Model) string {
	bar := m.opts.Theme.Status
	if m.env.Production {
		bar = m.opts.Theme.StatusProd
	}
	mode := "structured view"
	if z.raw {
		mode = "raw JSON"
	}
	return m.opts.Theme.Chip.Render("ZOOM") + bar.Render("  "+mode+"  ·  wrap on")
}

func (z *zoomScreen) hints(m *Model) []hint {
	hs := []hint{m.pair(ActNextEntry, ActPrevEntry, "next/prev entry"), m.pair(ActDown, ActUp, "scroll"), m.h(ActJSONView, "raw json")}
	if z.field != "" {
		hs = append(hs, m.h(ActFieldKeep, "keep"), m.h(ActFieldExclude, "exclude"))
	} else if e, ok := z.entry(); ok && !z.raw && len(zoomFields(e)) > 0 {
		hs = append(hs, m.h(ActFieldNext, "field"))
	}
	if e, ok := z.entry(); ok && e.TraceID != "" {
		hs = append(hs, m.h(ActViewTrace, "trace"))
	}
	return append(hs, m.h(ActOpen, "hidden fields"), m.h(ActBack, "back"), m.h(ActHelp, "help"))
}

func (z *zoomScreen) prompt(*Model) string { return "" }
