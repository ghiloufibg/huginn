package domain

import "testing"

func TestParseEnv(t *testing.T) {
	for _, ok := range []string{"dev", "rec", "prprd", "prd", "staging-2"} {
		if _, err := ParseEnv(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "PRD", "-x", "a b", "prd!"} {
		if _, err := ParseEnv(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestPodRestartsIgnoresInit(t *testing.T) {
	p := Pod{Containers: []Container{{Restarts: 2}, {Restarts: 3, Init: true}, {Restarts: 1}}}
	if p.Restarts() != 3 {
		t.Fatalf("got %d", p.Restarts())
	}
}
