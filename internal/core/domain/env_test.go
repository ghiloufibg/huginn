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

func TestPodRestartsIgnoresCompletedInit(t *testing.T) {
	done := Container{Restarts: 3, Init: true, State: ContainerTerminated, Reason: "Completed"}
	p := Pod{Containers: []Container{{Restarts: 2}, done, {Restarts: 1}}}
	if p.Restarts() != 3 {
		t.Fatalf("got %d", p.Restarts())
	}
	failing := Container{Restarts: 4, Init: true, State: ContainerTerminated, Reason: "Error"}
	p.Containers[1] = failing
	if p.Restarts() != 7 {
		t.Fatalf("failing init: got %d", p.Restarts())
	}
}
