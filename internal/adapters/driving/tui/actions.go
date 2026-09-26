package tui

// actionInfo describes an action for the help screen and key hints. It is
// the single source of truth: hints, help and README tables follow it.
type actionInfo struct {
	group string // help section
	desc  string // help text
	short string // status-bar hint
}

// Help groups, in display order.
var helpGroups = []string{"Move", "Time and stream", "Filter", "Inspect", "Display", "Services", "Global"}

var actions = map[Action]actionInfo{
	ActQuit:         {"Global", "quit", "quit"},
	ActHelp:         {"Global", "help for this screen", "help"},
	ActBack:         {"Global", "back, close, exit fullscreen, clear the last filter", "back"},
	ActSwitchEnv:    {"Global", "switch environment", "env"},
	ActUp:           {"Move", "up", ""},
	ActDown:         {"Move", "down", ""},
	ActPageUp:       {"Move", "page up", ""},
	ActPageDown:     {"Move", "page down", ""},
	ActTop:          {"Move", "top", ""},
	ActBottom:       {"Move", "bottom (on logs: back to the live tail)", ""},
	ActOpen:         {"Inspect", "open / zoom / apply", "open"},
	ActFilter:       {"Filter", "filter as you type", "filter"},
	ActSort:         {"Services", "sort: status, name, restarts, age", "sort"},
	ActRefresh:      {"Services", "resync the watches", "refresh"},
	ActWindowNext:   {"Time and stream", "next time window", "window"},
	ActWindowPick:   {"Time and stream", "pick a time window", "windows"},
	ActWindow1:      {"Time and stream", "window 15m", ""},
	ActWindow2:      {"Time and stream", "window 30m", ""},
	ActWindow3:      {"Time and stream", "window 40m", ""},
	ActWindow4:      {"Time and stream", "window 45m", ""},
	ActWindow5:      {"Time and stream", "window 1h", ""},
	ActWindow6:      {"Time and stream", "window 1d", ""},
	ActWindow7:      {"Time and stream", "window 2d", ""},
	ActWindowTail:   {"Time and stream", "tail (last lines)", ""},
	ActFollow:       {"Time and stream", "follow on/off", "follow"},
	ActPause:        {"Time and stream", "pause / resume (keeps buffering)", "pause"},
	ActOrder:        {"Display", "newest or oldest first", "order"},
	ActLevels:       {"Filter", "level picker", "levels"},
	ActErrorsOnly:   {"Filter", "errors only", "errors"},
	ActWarnAndError: {"Filter", "warnings and errors", "warn+"},
	ActAllLevels:    {"Filter", "all levels", "all"},
	ActFilterMode:   {"Filter", "filter (hide) or highlight (keep all)", "mode"},
	ActRegex:        {"Filter", "regex on/off", "regex"},
	ActAddFilter:    {"Filter", "stack another filter (AND), in the prompt", ""},
	ActContext:      {"Filter", "context lines around matches: 0, 1, 3, 5", "context"},
	ActNextMatch:    {"Filter", "next match", "next"},
	ActPrevMatch:    {"Filter", "previous match", "prev"},
	ActNextError:    {"Move", "next ERROR line", ""},
	ActPrevError:    {"Move", "previous ERROR line", ""},
	ActPanLeft:      {"Display", "pan left (wrap off)", ""},
	ActPanRight:     {"Display", "pan right (wrap off)", ""},
	ActPanLeftHalf:  {"Display", "pan half a screen left", ""},
	ActPanRightHalf: {"Display", "pan half a screen right", ""},
	ActNextEntry:    {"Inspect", "next entry", "next"},
	ActPrevEntry:    {"Inspect", "previous entry", "prev"},
	ActJSONView:     {"Inspect", "raw JSON view", "json"},
	ActPodScope:     {"Inspect", "pod scope: all, then each pod", "pods"},
	ActPodSelector:  {"Inspect", "select pods (fullscreen)", "select pods"},
	ActFullscreen:   {"Display", "fullscreen", "fullscreen"},
	ActWrap:         {"Display", "wrap lines", "wrap"},
	ActTimestamps:   {"Display", "timestamps: local, UTC, relative, hidden", "time"},
	ActPodID:        {"Display", "pod id: short, full, hidden", "pod id"},
	ActColumns:      {"Display", "columns: time, pod, level, thread, class", "columns"},
	ActFocus:        {"Display", "focus layout: hide pod, thread and class", "focus"},
}

// hintsFor builds status-bar hints from the live keymap.
func (m *Model) hintsFor(as ...Action) []hint {
	var out []hint
	for _, a := range as {
		info, ok := actions[a]
		if !ok || info.short == "" {
			continue
		}
		out = append(out, hint{m.label(a), info.short})
	}
	return out
}
