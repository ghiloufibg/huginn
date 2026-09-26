package tui

// actionInfo describes an action for the help screen. Help, the key bar
// (keybar.go, built from the same keymap) and the README follow it.
type actionInfo struct {
	group string // help section
	desc  string // help text
}

// Help groups, in display order.
var helpGroups = []string{"Move", "Time and stream", "Filter", "Inspect", "Display", "Services", "Global"}

var actions = map[Action]actionInfo{
	ActQuit:         {"Global", "quit"},
	ActHelp:         {"Global", "help for this screen"},
	ActBack:         {"Global", "back, close, exit fullscreen, clear the last filter"},
	ActSwitchEnv:    {"Global", "switch environment"},
	ActKeyBar:       {"Global", "key bar: compact, full, hidden"},
	ActUp:           {"Move", "up"},
	ActDown:         {"Move", "down"},
	ActPageUp:       {"Move", "page up"},
	ActPageDown:     {"Move", "page down"},
	ActTop:          {"Move", "top"},
	ActBottom:       {"Move", "bottom (on logs: back to the live tail)"},
	ActOpen:         {"Inspect", "open / zoom / apply"},
	ActFilter:       {"Filter", "filter as you type"},
	ActSort:         {"Services", "sort: status, name, restarts, age"},
	ActPreview:      {"Services", "preview of the selected service: on/off"},
	ActRefresh:      {"Services", "resync the watches"},
	ActWindowNext:   {"Time and stream", "next time window"},
	ActWindowPick:   {"Time and stream", "pick a time window"},
	ActWindow1:      {"Time and stream", "window 15m"},
	ActWindow2:      {"Time and stream", "window 30m"},
	ActWindow3:      {"Time and stream", "window 40m"},
	ActWindow4:      {"Time and stream", "window 45m"},
	ActWindow5:      {"Time and stream", "window 1h"},
	ActWindow6:      {"Time and stream", "window 1d"},
	ActWindow7:      {"Time and stream", "window 2d"},
	ActWindowTail:   {"Time and stream", "tail (last lines)"},
	ActFollow:       {"Time and stream", "follow on/off"},
	ActPause:        {"Time and stream", "pause / resume (keeps buffering)"},
	ActOrder:        {"Display", "newest or oldest first"},
	ActLevels:       {"Filter", "level picker"},
	ActErrorsOnly:   {"Filter", "errors only"},
	ActWarnAndError: {"Filter", "warnings and errors"},
	ActAllLevels:    {"Filter", "all levels"},
	ActFilterMode:   {"Filter", "filter (hide) or highlight (keep all)"},
	ActRegex:        {"Filter", "regex on/off"},
	ActAddFilter:    {"Filter", "stack another filter (AND), in the prompt"},
	ActContext:      {"Filter", "context lines around matches: 0, 1, 3, 5"},
	ActNextMatch:    {"Filter", "next match"},
	ActPrevMatch:    {"Filter", "previous match"},
	ActNextError:    {"Move", "next ERROR line"},
	ActPrevError:    {"Move", "previous ERROR line"},
	ActPanLeft:      {"Display", "pan left (wrap off)"},
	ActPanRight:     {"Display", "pan right (wrap off)"},
	ActPanLeftHalf:  {"Display", "pan half a screen left"},
	ActPanRightHalf: {"Display", "pan half a screen right"},
	ActNextEntry:    {"Inspect", "next entry"},
	ActPrevEntry:    {"Inspect", "previous entry"},
	ActJSONView:     {"Inspect", "raw JSON view"},
	ActPodScope:     {"Inspect", "pod scope: all, then each pod"},
	ActPodSelector:  {"Inspect", "select pods (fullscreen)"},
	ActFullscreen:   {"Display", "fullscreen"},
	ActWrap:         {"Display", "wrap lines"},
	ActTimestamps:   {"Display", "timestamps: local, UTC, relative, hidden"},
	ActPodID:        {"Display", "pod id: short, full, hidden"},
	ActColumns:      {"Display", "columns: time, pod, level, thread, class"},
	ActFocus:        {"Display", "focus layout: hide pod, thread and class"},
}
