package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// column describes one table column.
type column struct {
	title string
	width int  // fixed width; for the flex column, the minimum width
	flex  bool // takes the remaining width, up to its content width
	fill  bool // takes whatever width is left after the flex columns
	right bool // right-aligned
	// drop orders the columns removed when the terminal is too narrow:
	// 1 goes first; 0 never.
	drop int
}

// cell is one styled table cell.
type cell struct {
	text  string
	style lipgloss.Style
}

// table renders rows of cells in a fixed-height viewport. Only visible
// rows are rendered, so large tables stay cheap.
type table struct {
	cols   []column
	cursor int
	offset int
	// titles are group titles drawn above the rows they index, only when
	// all rows and titles fit (a title never costs a row).
	titles map[int]string
}

const colGap = 2

// layout returns the width of each column for the given total width; a
// width of 0 means the column is hidden. content is the widest cell of the
// flex column, so a wide terminal does not push columns far apart.
func (t *table) layout(total, content int) []int {
	widths := make([]int, len(t.cols))
	for i, c := range t.cols {
		widths[i] = c.width
	}
	need := func() int {
		n, shown := 1, 0
		for _, w := range widths {
			if w > 0 {
				n += w
				shown++
			}
		}
		return n + colGap*max(shown-1, 0)
	}
	for order := 1; need() > total; order++ {
		dropped := false
		for i, c := range t.cols {
			if c.drop == order {
				widths[i], dropped = 0, true
			}
		}
		if !dropped && order > len(t.cols) {
			break
		}
	}
	for i, c := range t.cols {
		if c.flex {
			widths[i] += min(max(total-need(), 0), max(content-widths[i], 0))
		}
	}
	for i, c := range t.cols {
		if c.fill && widths[i] > 0 {
			widths[i] += max(total-need(), 0)
		}
	}
	return widths
}

// scroll keeps the cursor inside a viewport of h rows.
func (t *table) scroll(n, h int) {
	t.cursor = max(min(t.cursor, n-1), 0)
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+h {
		t.offset = t.cursor - h + 1
	}
	t.offset = max(min(t.offset, n-h), 0)
}

// render draws the header and the visible rows in exactly h lines of w
// cells. The selected row is drawn with sel, ignoring cell colors, so it
// stays readable in every theme.
// render draws n rows in a w×h viewport. Only the visible rows are asked
// for (get), so large tables stay cheap; flex is the widest text of the
// flex column over all rows.
func (t *table) render(n int, get func(i int) []cell, flex, w, h int, th *Theme) string {
	widths := t.layout(w, flex)
	lines := make([]string, 0, h)
	head := make([]cell, len(t.cols))
	for i, c := range t.cols {
		head[i] = cell{text: c.title, style: th.TableHeader}
	}
	lines = append(lines, t.line(head, widths, w, nil))
	t.scroll(n, h-1)
	titles := t.titles
	if n+len(titles) > h-1 {
		titles = nil
	}
	for i := t.offset; i < n && len(lines) < h; i++ {
		if title, ok := titles[i]; ok {
			title = ansi.Truncate(title, w-1, "…")
			lines = append(lines, " "+th.Dim.Render(title)+strings.Repeat(" ", w-1-textWidth(title)))
		}
		var sel *lipgloss.Style
		if i == t.cursor {
			sel = &th.Selected
		}
		lines = append(lines, t.line(get(i), widths, w, sel))
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return strings.Join(lines, "\n")
}

// textWidth is the display width of s, which may hold ANSI escape
// sequences. ASCII text (the common case for tables and log lines) is
// measured by skipping escape sequences and counting bytes; anything else
// uses the full grapheme-aware measure.
func textWidth(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 0x80:
			return ansi.StringWidth(s)
		case c == 0x1b && i+1 < len(s) && s[i+1] == '[': // CSI: ESC [ params final
			i += 2
			for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
				i++
			}
		case c == 0x1b:
			return ansi.StringWidth(s) // OSC and others: let ansi parse them
		case c >= 0x20 && c != 0x7f:
			n++
		}
	}
	return n
}

func (t *table) line(cells []cell, widths []int, w int, sel *lipgloss.Style) string {
	var b strings.Builder
	b.WriteString(" ")
	first := true
	for i, c := range cells {
		if widths[i] == 0 {
			continue
		}
		if !first {
			b.WriteString(strings.Repeat(" ", colGap))
		}
		first = false
		text := c.text
		if textWidth(text) > widths[i] {
			text = ansi.Truncate(text, widths[i], "…")
		}
		pad := strings.Repeat(" ", max(widths[i]-textWidth(text), 0))
		if t.cols[i].right {
			text = pad + text
		} else {
			text += pad
		}
		if sel == nil {
			text = c.style.Render(text)
		}
		b.WriteString(text)
	}
	out := b.String()
	if lw := textWidth(out); lw > w {
		out = ansi.Truncate(out, w, "")
	}
	out += strings.Repeat(" ", max(w-textWidth(out), 0))
	if sel != nil {
		return sel.Render(out)
	}
	return out
}
