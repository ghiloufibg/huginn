package tui

import tea "charm.land/bubbletea/v2"

// lineEdit is a minimal single-line text editor for prompts.
type lineEdit struct {
	text []rune
}

// String returns the current text.
func (e *lineEdit) String() string { return string(e.text) }

// Clear empties the editor.
func (e *lineEdit) Clear() { e.text = nil }

// handle applies an editing key and reports whether it was consumed.
func (e *lineEdit) handle(k tea.KeyPressMsg) bool {
	switch k.String() {
	case "backspace":
		if len(e.text) > 0 {
			e.text = e.text[:len(e.text)-1]
		}
		return true
	case "ctrl+u":
		e.text = nil
		return true
	case "ctrl+w":
		i := len(e.text)
		for i > 0 && e.text[i-1] == ' ' {
			i--
		}
		for i > 0 && e.text[i-1] != ' ' {
			i--
		}
		e.text = e.text[:i]
		return true
	}
	if k.Text != "" {
		e.text = append(e.text, []rune(k.Text)...)
		return true
	}
	return false
}
