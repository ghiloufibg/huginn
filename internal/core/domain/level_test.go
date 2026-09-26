package domain

import "testing"

func TestParseLevel(t *testing.T) {
	tests := map[string]Level{
		"INFO": LevelInfo, "warning": LevelWarn, " Error ": LevelError, "TRACE": LevelDebug,
		"SEVERE": LevelError, "fatal": LevelError, "W": LevelWarn, "notice": LevelInfo,
	}
	for in, want := range tests {
		if got, ok := ParseLevel(in); !ok || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if got, ok := ParseLevel("loud"); ok || got != LevelUnknown {
		t.Errorf("unknown level parsed as %v", got)
	}
}

func TestParseLevelWithAliasesOverride(t *testing.T) {
	aliases := map[string]Level{"i": LevelError, "boom": LevelError}
	if got, _ := ParseLevelWith("I", aliases); got != LevelError {
		t.Errorf("alias precedence: got %v", got)
	}
	if got, _ := ParseLevelWith("boom", aliases); got != LevelError {
		t.Errorf("custom alias: got %v", got)
	}
}

func TestAllLevelsContainsUnknown(t *testing.T) {
	if !AllLevels().Contains(LevelUnknown) || !AllLevels().Contains(LevelError) {
		t.Fatal("AllLevels must contain every level")
	}
}
