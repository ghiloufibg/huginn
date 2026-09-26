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

// LogQuery describes what the logs screen wants to see. Pod scope, levels
// and text filters are view concerns applied without reloading.
type LogQuery struct {
	Env    domain.Env
	Repo   string
	Window domain.TimeWindow
	Follow bool
}

// PodState is a pod of a log session as the pod strip shows it.
type PodState struct {
	Pod domain.Pod
	// Containers are the application containers streamed.
	Containers []string
	// New marks a pod that appeared during the session (a rollout).
	New bool
	// Terminated marks a pod that disappeared; its lines are kept.
	Terminated bool
	// Err is why the pod's logs cannot be read right now, if they cannot.
	Err error
}

// LogNotice is a message about the session, shown in the status bar.
type LogNotice struct {
	Pod  string
	Text string
}

// LogBatch is what a log session delivers, at most about 30 times a
// second. Entries are ordered by time and never reordered afterwards.
type LogBatch struct {
	Entries []domain.LogEntry
	// Pods is the full pod list when it changed, nil otherwise.
	Pods []PodState
	// Notices are new messages since the previous batch.
	Notices []LogNotice
	// HistoryDone is set on the batch carrying the end of the window's
	// history; later entries are live.
	HistoryDone bool
}

// LogSession is the driving port behind the logs screen.
type LogSession interface {
	// Open streams the logs of a repository's application containers.
	// The channel closes when ctx is cancelled.
	Open(ctx context.Context, q LogQuery) (<-chan LogBatch, error)
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

// PodEvents is the driving port behind the services preview: the recent
// events of one pod, read on demand when the cursor rests on a service —
// never watched continuously (docs/DECISIONS.md D-004).
type PodEvents interface {
	// Recent returns the pod's events, most recently seen first.
	Recent(ctx context.Context, env domain.Env, namespace, pod string) ([]domain.Event, error)
}
