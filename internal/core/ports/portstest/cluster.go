package portstest

import (
	"context"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FakeCluster is an in-memory ClusterClient whose state tests set directly.
// Watchers receive the current state, then every later Put/Delete.
type FakeCluster struct {
	mu        sync.Mutex
	workloads []domain.Workload
	pods      []domain.Pod
	events    map[string][]domain.Event
	podSubs   []podSub
	// Err, when set, is returned by every call.
	Err error
	// NamespaceErr makes calls whose scope includes a namespace fail.
	NamespaceErr map[string]error
}

type podSub struct {
	ctx   context.Context
	scope ports.Scope
	sel   ports.Selector
	ch    chan domain.PodEvent
}

// NewFakeCluster returns an empty fake cluster.
func NewFakeCluster() *FakeCluster { return &FakeCluster{events: map[string][]domain.Event{}} }

// AddWorkload stores a workload.
func (f *FakeCluster) AddWorkload(w domain.Workload) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workloads = append(f.workloads, w)
}

// PutPod adds or replaces a pod and notifies watchers.
func (f *FakeCluster) PutPod(p domain.Pod) {
	f.mu.Lock()
	defer f.mu.Unlock()
	typ := domain.PodAdded
	for i := range f.pods {
		if f.pods[i].Namespace == p.Namespace && f.pods[i].Name == p.Name {
			f.pods[i], typ = p, domain.PodUpdated
		}
	}
	if typ == domain.PodAdded {
		f.pods = append(f.pods, p)
	}
	f.notify(domain.PodEvent{Type: typ, Pod: p})
}

// DeletePod removes a pod and notifies watchers.
func (f *FakeCluster) DeletePod(namespace, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, p := range f.pods {
		if p.Namespace == namespace && p.Name == name {
			f.pods = append(f.pods[:i], f.pods[i+1:]...)
			p.Deleted = true
			f.notify(domain.PodEvent{Type: domain.PodDeleted, Pod: p})
			return
		}
	}
}

// SetEvents sets the events returned for a pod.
func (f *FakeCluster) SetEvents(namespace, pod string, ev []domain.Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[namespace+"/"+pod] = ev
}

func (f *FakeCluster) notify(ev domain.PodEvent) {
	for _, s := range f.podSubs {
		if s.ctx.Err() == nil && inScope(s.scope, ev.Pod.Env, ev.Pod.Namespace) && s.sel.Matches(ev.Pod.Labels) {
			select {
			case s.ch <- ev:
			case <-s.ctx.Done():
			}
		}
	}
}

func inScope(s ports.Scope, env domain.Env, ns string) bool {
	if s.Env != env {
		return false
	}
	if len(s.Namespaces) == 0 {
		return true
	}
	for _, n := range s.Namespaces {
		if n == ns {
			return true
		}
	}
	return false
}

// SetErr sets the error returned by every call (nil clears it).
func (f *FakeCluster) SetErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Err = err
}

func (f *FakeCluster) errFor(scope ports.Scope) error {
	if f.Err != nil {
		return f.Err
	}
	for _, ns := range scope.Namespaces {
		if err := f.NamespaceErr[ns]; err != nil {
			return err
		}
	}
	return nil
}

// ListWorkloads returns the workloads in scope.
func (f *FakeCluster) ListWorkloads(_ context.Context, scope ports.Scope) ([]domain.Workload, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor(scope); err != nil {
		return nil, err
	}
	var out []domain.Workload
	for _, w := range f.workloads {
		if inScope(scope, w.Ref.Env, w.Ref.Namespace) {
			out = append(out, w)
		}
	}
	return out, nil
}

// WatchWorkloads sends the current workloads, then closes when ctx ends.
func (f *FakeCluster) WatchWorkloads(ctx context.Context, scope ports.Scope) (<-chan ports.WorkloadEvent, error) {
	ws, err := f.ListWorkloads(ctx, scope)
	if err != nil {
		return nil, err
	}
	ch := make(chan ports.WorkloadEvent, len(ws))
	for _, w := range ws {
		ch <- ports.WorkloadEvent{Workload: w}
	}
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

// ListPods returns the pods in scope matching sel.
func (f *FakeCluster) ListPods(_ context.Context, scope ports.Scope, sel ports.Selector) ([]domain.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.errFor(scope); err != nil {
		return nil, err
	}
	var out []domain.Pod
	for _, p := range f.pods {
		if inScope(scope, p.Env, p.Namespace) && sel.Matches(p.Labels) {
			out = append(out, p)
		}
	}
	return out, nil
}

// WatchPods sends the current pods as Added events, then later changes.
func (f *FakeCluster) WatchPods(ctx context.Context, scope ports.Scope, sel ports.Selector) (<-chan domain.PodEvent, error) {
	pods, err := f.ListPods(ctx, scope, sel)
	if err != nil {
		return nil, err
	}
	ch := make(chan domain.PodEvent, len(pods)+64)
	for _, p := range pods {
		ch <- domain.PodEvent{Type: domain.PodAdded, Pod: p}
	}
	f.mu.Lock()
	f.podSubs = append(f.podSubs, podSub{ctx: ctx, scope: scope, sel: sel, ch: ch})
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, s := range f.podSubs {
			if s.ch == ch {
				f.podSubs = append(f.podSubs[:i], f.podSubs[i+1:]...)
				break
			}
		}
		close(ch)
	}()
	return ch, nil
}

// PodEvents returns the events set for a pod.
func (f *FakeCluster) PodEvents(_ context.Context, _ ports.Scope, namespace, pod string) ([]domain.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Err != nil {
		return nil, f.Err
	}
	return f.events[namespace+"/"+pod], nil
}
