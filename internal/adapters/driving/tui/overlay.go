package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// placeOver draws box centered over base (both blocks of lines), keeping
// the base visible around it.
func placeOver(base, box string, w, h int) string {
	lines := strings.Split(base, "\n")
	boxLines := strings.Split(box, "\n")
	bw := lipgloss.Width(box)
	if bw > w { // a popup wider than the terminal is clipped, not spilled
		for i, bl := range boxLines {
			boxLines[i] = ansi.Truncate(bl, w, "")
		}
		bw = w
	}
	x := max((w-bw)/2, 0)
	y := max((h-len(boxLines))/3, 0)
	for i, bl := range boxLines {
		row := y + i
		if row >= len(lines) {
			break
		}
		l := lines[row]
		left := ansi.Truncate(l, x, "")
		right := ansi.TruncateLeft(l, x+bw, "")
		lines[row] = left + bl + right
	}
	return strings.Join(lines, "\n")
}
