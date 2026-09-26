package ports

import (
	"context"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// CatalogSnapshot is the state of the services of one environment.
type CatalogSnapshot struct {
	Env domain.Env
	// Services holds one row per repository, then one per unassigned
	// workload (Unassigned set), ordered by name.
	Services []domain.ServiceSummary
	// UpdatedAt is when the state last changed.
	UpdatedAt time.Time
	// Synced is true once every namespace is being watched successfully.
	Synced bool
	// NamespaceErrs holds the namespaces that cannot be watched and why;
	// other namespaces are unaffected.
	NamespaceErrs map[string]error
	// Err is set when nothing can be watched at all.
	Err error
}

// ServiceCatalog is the driving port behind the services screen.
type ServiceCatalog interface {
	// Watch streams a snapshot of the services of env each time they
	// change, coalescing bursts. Only the latest snapshot matters: a slow
	// reader skips intermediate ones. The channel closes when ctx is
	// cancelled.
	Watch(ctx context.Context, env domain.Env) (<-chan CatalogSnapshot, error)
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
