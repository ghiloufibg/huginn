package app

import (
	"context"
	"slices"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// PodEvents implements ports.PodEvents over a cluster client.
type PodEvents struct {
	Cluster ports.ClusterClient
	Scopes  ScopeFunc
}

// Recent implements ports.PodEvents.
func (e *PodEvents) Recent(ctx context.Context, env domain.Env, namespace, pod string) ([]domain.Event, error) {
	scope, err := e.Scopes(ctx, env)
	if err != nil {
		return nil, err
	}
	evs, err := e.Cluster.PodEvents(ctx, scope, namespace, pod)
	if err != nil {
		return nil, err
	}
	evs = slices.Clone(evs)
	slices.SortStableFunc(evs, func(a, b domain.Event) int { return b.LastSeen.Compare(a.LastSeen) })
	return evs, nil
}
