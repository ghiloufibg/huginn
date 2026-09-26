package app

import (
	"context"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

func TestPodEventsNewestFirst(t *testing.T) {
	fc := portstest.NewFakeCluster()
	at := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	fc.SetEvents("app-rec", "api-1", []domain.Event{
		{Reason: "Pulled", LastSeen: at.Add(-time.Hour)},
		{Reason: "BackOff", LastSeen: at},
	})
	e := &PodEvents{Cluster: fc, Scopes: func(env domain.Env) (ports.Scope, bool) {
		return ports.Scope{Env: env, Namespaces: []string{"app-rec"}}, env == "rec"
	}}
	evs, err := e.Recent(context.Background(), "rec", "app-rec", "api-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Reason != "BackOff" {
		t.Fatalf("events = %+v, want BackOff first", evs)
	}
	if _, err := e.Recent(context.Background(), "prd", "app-prd", "api-1"); err == nil {
		t.Fatal("unknown environment: error expected")
	}
}
