package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// ServiceCatalog is the driving port behind the services screen.
type ServiceCatalog interface {
	// Watch streams the full, sorted list of services of env each time it
	// changes. The channel closes when ctx is cancelled.
	Watch(ctx context.Context, env domain.Env) (<-chan []domain.ServiceSummary, error)
}

// LogQuery describes what the logs screen wants to see.
type LogQuery struct {
	Env    domain.Env
	Repo   string
	Pods   []string // empty: all application pods
	Window domain.TimeWindow
	Follow bool
}

// LogSession is the driving port behind the logs screen. Its methods are
// completed in M2; the interface is declared now to fix the boundary.
type LogSession interface {
	Open(ctx context.Context, q LogQuery) (<-chan []domain.LogEntry, error)
}

// PodDiagnostics is what the diagnostics panel shows for one pod.
type PodDiagnostics struct {
	Pod    domain.Pod
	Events []domain.Event
}

// Diagnostics is the driving port behind the diagnostics panel.
type Diagnostics interface {
	Pod(ctx context.Context, env domain.Env, namespace, pod string) (PodDiagnostics, error)
}
