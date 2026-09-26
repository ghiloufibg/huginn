package tui

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// frameworkPrefixes mark stack frames that are not the application's own
// code; own frames are shown bold, these dimmed.
var frameworkPrefixes = []string{
	"java.", "javax.", "jdk.", "sun.", "com.sun.", "jakarta.", "kotlin.", "scala.",
	"org.springframework.", "org.apache.", "org.hibernate.", "io.netty.", "io.lettuce.",
	"reactor.", "io.micrometer.", "com.zaxxer.", "org.eclipse.", "io.undertow.", "feign.",
}

// zoomScreen shows one entry in full (mockup board 6).
type zoomScreen struct {
	logs     *logsScreen
	seq      uint64
	raw      bool // pretty JSON instead of the structured view
	metadata bool // Kubernetes metadata expanded
	offset   int
	height   int
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
	default:
		return false, nil
	}
	return true, nil
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
	}
}

func (z *zoomScreen) view(m *Model, w, h int) string {
	z.height = h
	t := m.opts.Theme
	e, ok := z.entry()
	if !ok {
		return centered(t.Dim.Render("this entry left the buffer"), w, h)
	}
	var lines []string
	if z.raw {
		lines = z.rawLines(e)
	} else {
		lines = z.structured(m, e)
	}
	var out []string
	for _, l := range lines {
		out = append(out, strings.Split(ansi.Hardwrap(l, max(w-2, 10), true), "\n")...)
	}
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
	for _, s := range m.opts.FullRenderer.Render(*e, ports.RenderOptions{Now: m.opts.Now()}) {
		line.WriteString(l.segmentStyle(t, e, s.Role).Render(s.Text))
	}
	out := []string{head, "", line.String(), ""}
	if len(e.Fields) > 0 || e.TraceID != "" {
		out = append(out, sec("FIELDS", ""))
		if e.TraceID != "" {
			out = append(out, "   "+t.Dim.Render(fmt.Sprintf("%-16s", "traceId"))+t.Key.Render(e.TraceID))
		}
		for _, k := range sortedKeys(e.Fields) {
			out = append(out, "   "+t.Dim.Render(fmt.Sprintf("%-16s", k))+e.Fields[k])
		}
		out = append(out, "")
	}
	if e.Stack != "" {
		out = append(out, sec("STACK TRACE", "   own frames in bold, framework frames dimmed"))
		out = append(out, z.stack(t, e.Stack)...)
		out = append(out, "")
	}
	out = append(out, sec("CONTEXT", "   same pod, 3 entries before and after"))
	out = append(out, z.context(m, e)...)
	out = append(out, "")
	if len(e.Hidden) > 0 {
		if z.metadata {
			out = append(out, sec("KUBERNETES METADATA", fmt.Sprintf("   %d fields · enter to collapse", len(e.Hidden))))
			for _, k := range sortedKeys(e.Hidden) {
				out = append(out, "   "+t.Dim.Render(fmt.Sprintf("%-32s", k))+e.Hidden[k])
			}
		} else {
			out = append(out, t.Dim.Render(fmt.Sprintf(" [+] kubernetes metadata   %d fields · enter to expand", len(e.Hidden))))
		}
	}
	return out
}

func (z *zoomScreen) stack(t Theme, stack string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(stack, "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		frame, isFrame := strings.CutPrefix(trimmed, "at ")
		style := t.Stack.Bold(true)
		switch {
		case isFrame && isFramework(frame):
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

func isFramework(frame string) bool {
	frame = strings.TrimPrefix(frame, "java.base/")
	for _, p := range frameworkPrefixes {
		if strings.HasPrefix(frame, p) {
			return true
		}
	}
	return false
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
		for _, s := range m.opts.Renderer.Render(*c, ports.RenderOptions{Now: m.opts.Now()}) {
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
	return []hint{
		{m.label(ActNextEntry) + "/" + m.label(ActPrevEntry), "next/prev"},
		{m.label(ActJSONView), "json"},
		{m.label(ActOpen), "metadata"},
		{m.label(ActBack), "back"},
	}
}

func (z *zoomScreen) prompt(*Model) string { return "" }
