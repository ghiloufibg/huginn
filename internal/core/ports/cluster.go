package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Scope is where a ClusterClient looks: one environment, reached through a
// kube context, restricted to some namespaces. Several environments may
// share one context (one cluster, one namespace per environment) or not.
type Scope struct {
	Env        domain.Env
	Context    string
	Namespaces []string
}

// Selector is an equality-based label selector; empty matches everything.
type Selector map[string]string

// Matches reports whether labels satisfy the selector.
func (s Selector) Matches(labels map[string]string) bool {
	for k, v := range s {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// WorkloadEvent is emitted by workload watches.
type WorkloadEvent struct {
	Deleted  bool
	Workload domain.Workload
	// Warning, when set, is not a workload: it says that the watch of this
	// namespace is incomplete (e.g. "cronjobs not readable"). It comes
	// before the workloads.
	Warning string
}

// ClusterClient reads workload and pod state. It is strictly read-only.
//
// Watch methods return a channel that is closed when ctx is cancelled or the
// watch ends; implementations reconnect internally where possible and send
// the full current state first (as Added events) so callers need no
// separate list call. Errors are mapped to domain error kinds.
type ClusterClient interface {
	ListWorkloads(ctx context.Context, scope Scope) ([]domain.Workload, error)
	WatchWorkloads(ctx context.Context, scope Scope) (<-chan WorkloadEvent, error)
	ListPods(ctx context.Context, scope Scope, sel Selector) ([]domain.Pod, error)
	WatchPods(ctx context.Context, scope Scope, sel Selector) (<-chan domain.PodEvent, error)
	// PodEvents returns the Kubernetes events of one pod, newest first.
	PodEvents(ctx context.Context, scope Scope, namespace, pod string) ([]domain.Event, error)
}
