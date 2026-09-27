package tui

import (
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func TestPodShortID(t *testing.T) {
	for in, want := range map[string]string{
		"payment-service-7f8f9cc5-5cw8s": "5cw8s",
		"ledger-writer-0":                "writer-0",
		"node-agent-kvc82":               "kvc82",
		"db-12":                          "db-12",
		"solo":                           "solo",
	} {
		if got := podShortID(in); got != want {
			t.Errorf("%s: %q, want %q", in, got, want)
		}
	}
}

func TestPodLabel(t *testing.T) {
	running := domain.Pod{Phase: domain.PodRunning, Containers: []domain.Container{{Name: "a", State: domain.ContainerRunning, Ready: true}}}
	if l, _ := podLabel(running); l != "Healthy" {
		t.Errorf("running: %q", l)
	}
	running.Deleted = true
	if l, _ := podLabel(running); l != "terminating" {
		t.Errorf("deleting: %q", l)
	}
	done := domain.Pod{Phase: domain.PodSucceeded, Containers: []domain.Container{{Name: "a", State: domain.ContainerTerminated, Reason: "Completed"}}}
	if l, st := podLabel(done); l != "completed" || st != domain.StatusHealthy {
		t.Errorf("completed: %q %v", l, st)
	}
}
