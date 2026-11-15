package ports

import (
	"context"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// MetricsSource reads the current resource usage of pods. It is strictly
// read-only and optional: a cluster may serve no metrics, which is reported
// as domain.ErrMetricsUnavailable and affects no other screen.
//
// A call returns one sample per pod that has one; a pod younger than the
// metrics window or not running has none. Samples are averages over their
// window, not instants. Errors are mapped to domain error kinds.
type MetricsSource interface {
	PodUsage(ctx context.Context, scope Scope) ([]domain.PodUsage, error)
}

// UsageQuery says what the usage screen watches.
type UsageQuery struct {
	Env domain.Env
	// Containers says which containers are summed: application containers
	// (the default) or every container but init containers. Changing it
	// opens a new watch; the history starts again.
	Containers domain.ContainerMode
}

// UsageSnapshot is the resource usage of one environment.
type UsageSnapshot struct {
	Env domain.Env
	// Namespaces holds one entry per namespace of the environment, ordered
	// by name; a namespace that cannot be read has Err set.
	Namespaces []domain.NamespaceUsage
	// SampledAt is the oldest metric timestamp used, zero without any.
	SampledAt time.Time
	// Window is the averaging window of the metrics.
	Window time.Duration
	// Synced is true once every namespace has been read at least once.
	Synced bool
	// Err is set when no namespace can be read at all (for example the
	// cluster serves no metrics).
	Err error
	// StaleSince is when the oldest failing namespace last had a good
	// sample, zero when every namespace is up to date; its values are kept.
	StaleSince time.Time
}

// ResourceUsage is the driving port behind the usage screen.
type ResourceUsage interface {
	// Watch streams a snapshot each time the usage or the services change,
	// coalescing bursts; only the latest matters. The channel closes when
	// ctx is cancelled. Polling only runs while a watch is open.
	Watch(ctx context.Context, q UsageQuery) (<-chan UsageSnapshot, error)
}
