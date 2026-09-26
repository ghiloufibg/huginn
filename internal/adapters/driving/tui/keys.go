package tui

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Action is a user intent bound to keys. Names are stable: they are the
// keys of ui.keymap in the configuration.
type Action string

// Actions. Screens handle the subset that applies to them; the same key
// may mean different actions on different screens (enter opens a service
// on the home screen and zooms a line in the logs).
const (
	ActQuit         Action = "quit"
	ActHelp         Action = "help"
	ActBack         Action = "back"
	ActSwitchEnv    Action = "switch_env"
	ActFindService  Action = "find_service"
	ActUp           Action = "up"
	ActDown         Action = "down"
	ActPageUp       Action = "page_up"
	ActPageDown     Action = "page_down"
	ActTop          Action = "top"
	ActBottom       Action = "bottom"
	ActOpen         Action = "open"
	ActFilter       Action = "filter"
	ActSort         Action = "sort"
	ActRefresh      Action = "refresh"
	ActWindowNext   Action = "window_next"
	ActWindowPick   Action = "window_pick"
	ActWindow1      Action = "window_1"
	ActWindow2      Action = "window_2"
	ActWindow3      Action = "window_3"
	ActWindow4      Action = "window_4"
	ActWindow5      Action = "window_5"
	ActWindow6      Action = "window_6"
	ActWindow7      Action = "window_7"
	ActWindowTail   Action = "window_tail"
	ActFollow       Action = "follow"
	ActPause        Action = "pause"
	ActOrder        Action = "order"
	ActLevels       Action = "levels"
	ActErrorsOnly   Action = "errors_only"
	ActWarnAndError Action = "warn_and_error"
	ActAllLevels    Action = "all_levels"
	ActFilterMode   Action = "filter_mode"
	ActRegex        Action = "regex"
	ActAddFilter    Action = "add_filter"
	ActNextMatch    Action = "next_match"
	ActPrevMatch    Action = "prev_match"
	ActNextError    Action = "next_error"
	ActPrevError    Action = "prev_error"
	ActPanLeft      Action = "pan_left"
	ActPanRight     Action = "pan_right"
	ActPanLeftHalf  Action = "pan_left_half"
	ActPanRightHalf Action = "pan_right_half"
	ActNextEntry    Action = "next_entry"
	ActPrevEntry    Action = "prev_entry"
	ActJSONView     Action = "json_view"
	ActViewTrace    Action = "view_trace"
	ActDiagnostics  Action = "diagnostics"
	ActPreviousLogs Action = "previous_logs"
	ActErrorGroups  Action = "error_groups"
	ActPodScope     Action = "pod_scope"
	ActPodSelector  Action = "pod_selector"
	ActFullscreen   Action = "fullscreen"
	ActWrap         Action = "wrap"
	ActTimestamps   Action = "timestamps"
	ActPodID        Action = "pod_id"
	ActMark         Action = "mark"
	ActCopy         Action = "copy"
	ActSave         Action = "save"
	ActBugReport    Action = "bug_report"
)

// defaultKeys are the default bindings (docs/DECISIONS.md D-007, D-008,
// D-019, D-020). Every core action has a binding reachable on AZERTY and
// QWERTZ without AltGr; the number-row presets also accept the unshifted
// AZERTY characters.
var defaultKeys = map[Action][]string{
	ActQuit: {"q", "ctrl+c"}, ActHelp: {"?", "f1"}, ActBack: {"esc"},
	ActSwitchEnv: {"ctrl+e"}, ActFindService: {"ctrl+p"},
	ActUp: {"k", "up"}, ActDown: {"j", "down"}, ActPageUp: {"pgup", "ctrl+b"}, ActPageDown: {"pgdown", "ctrl+d"},
	ActTop: {"g", "home"}, ActBottom: {"G", "end"}, ActOpen: {"enter"},
	ActFilter: {"/", "ctrl+f"}, ActSort: {"s"}, ActRefresh: {"r"},
	ActWindowNext: {"t"}, ActWindowPick: {"T"},
	ActWindow1: {"1", "&"}, ActWindow2: {"2", "é"}, ActWindow3: {"3", `"`}, ActWindow4: {"4", "'"},
	ActWindow5: {"5", "("}, ActWindow6: {"6", "-"}, ActWindow7: {"7", "è"}, ActWindowTail: {"0", "à"},
	ActFollow: {"f"}, ActPause: {"space"}, ActOrder: {"o"},
	ActLevels: {"l"}, ActErrorsOnly: {"e"}, ActWarnAndError: {"w"}, ActAllLevels: {"a"},
	ActFilterMode: {"x"}, ActRegex: {"ctrl+r"}, ActAddFilter: {"ctrl+a"},
	ActNextMatch: {"n"}, ActPrevMatch: {"N"}, ActNextError: {">"}, ActPrevError: {"<"},
	ActPanLeft: {"left"}, ActPanRight: {"right"}, ActPanLeftHalf: {"H"}, ActPanRightHalf: {"L"},
	ActNextEntry: {"J"}, ActPrevEntry: {"K"},
	ActJSONView: {"p"}, ActViewTrace: {"v"}, ActDiagnostics: {"d"}, ActPreviousLogs: {"P"},
	ActErrorGroups: {"E"}, ActPodScope: {"tab"}, ActPodSelector: {"S"}, ActFullscreen: {"F"},
	ActWrap: {"W"}, ActTimestamps: {"c"}, ActPodID: {"I"}, ActMark: {"m"},
	ActCopy: {"ctrl+y"}, ActSave: {"ctrl+s"}, ActBugReport: {"B"},
}

// Keymap binds actions to keys.
type Keymap struct {
	keys  map[Action][]string
	byKey map[string][]Action
}

// NewKeymap returns the default keymap with overrides applied; an
// override replaces all keys of its action. Unknown action names fail with
// a suggestion.
func NewKeymap(overrides map[string][]string) (Keymap, error) {
	keys := map[Action][]string{}
	for a, ks := range defaultKeys {
		keys[a] = slices.Clone(ks)
	}
	var problems []string
	for name, ks := range overrides {
		a := Action(name)
		if _, ok := defaultKeys[a]; !ok {
			problems = append(problems, fmt.Sprintf("unknown action %q in ui.keymap", name))
			continue
		}
		if len(ks) == 0 {
			problems = append(problems, fmt.Sprintf("ui.keymap.%s: at least one key is required", name))
			continue
		}
		keys[a] = slices.Clone(ks)
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return Keymap{}, fmt.Errorf("%s (actions: see huginn config example)", strings.Join(problems, "; "))
	}
	km := Keymap{keys: keys, byKey: map[string][]Action{}}
	for a, ks := range keys {
		for _, k := range ks {
			km.byKey[k] = append(km.byKey[k], a)
		}
	}
	return km, nil
}

// Keys returns the keys bound to a.
func (k Keymap) Keys(a Action) []string { return k.keys[a] }

// Is reports whether key is bound to a.
func (k Keymap) Is(key string, a Action) bool { return slices.Contains(k.byKey[key], a) }

// Label is the first key of a, for hints ("ctrl+e").
func (k Keymap) Label(a Action) string {
	if ks := k.keys[a]; len(ks) > 0 {
		return ks[0]
	}
	return ""
}
