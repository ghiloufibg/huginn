package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"sync"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// A kind of workload: how to list and watch it in one namespace, and how
// to convert its objects.
type workloadKind struct {
	kind    domain.WorkloadKind
	example runtime.Object
	list    func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (runtime.Object, error)
	watch   func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (watch.Interface, error)
	items   func(runtime.Object) []runtime.Object
	convert func(domain.Env, runtime.Object) (domain.Workload, bool)
}

func itemsOf[T any, P interface {
	*T
	runtime.Object
}](items []T) []runtime.Object {
	out := make([]runtime.Object, len(items))
	for i := range items {
		out[i] = P(&items[i])
	}
	return out
}

func conv[P runtime.Object](f func(domain.Env, P) domain.Workload) func(domain.Env, runtime.Object) (domain.Workload, bool) {
	return func(env domain.Env, o runtime.Object) (domain.Workload, bool) {
		p, ok := o.(P)
		if !ok {
			return domain.Workload{}, false
		}
		return f(env, p), true
	}
}

var workloadKinds = []workloadKind{
	{
		kind: domain.KindDeployment, example: &appsv1.Deployment{},
		list: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (runtime.Object, error) {
			return cs.AppsV1().Deployments(ns).List(ctx, o)
		},
		watch: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (watch.Interface, error) {
			return cs.AppsV1().Deployments(ns).Watch(ctx, o)
		},
		items:   func(o runtime.Object) []runtime.Object { return itemsOf(o.(*appsv1.DeploymentList).Items) },
		convert: conv(fromDeployment),
	},
	{
		kind: domain.KindStatefulSet, example: &appsv1.StatefulSet{},
		list: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (runtime.Object, error) {
			return cs.AppsV1().StatefulSets(ns).List(ctx, o)
		},
		watch: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (watch.Interface, error) {
			return cs.AppsV1().StatefulSets(ns).Watch(ctx, o)
		},
		items:   func(o runtime.Object) []runtime.Object { return itemsOf(o.(*appsv1.StatefulSetList).Items) },
		convert: conv(fromStatefulSet),
	},
	{
		kind: domain.KindDaemonSet, example: &appsv1.DaemonSet{},
		list: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (runtime.Object, error) {
			return cs.AppsV1().DaemonSets(ns).List(ctx, o)
		},
		watch: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (watch.Interface, error) {
			return cs.AppsV1().DaemonSets(ns).Watch(ctx, o)
		},
		items:   func(o runtime.Object) []runtime.Object { return itemsOf(o.(*appsv1.DaemonSetList).Items) },
		convert: conv(fromDaemonSet),
	},
	{
		kind: domain.KindCronJob, example: &batchv1.CronJob{},
		list: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (runtime.Object, error) {
			return cs.BatchV1().CronJobs(ns).List(ctx, o)
		},
		watch: func(ctx context.Context, cs API, ns string, o metav1.ListOptions) (watch.Interface, error) {
			return cs.BatchV1().CronJobs(ns).Watch(ctx, o)
		},
		items:   func(o runtime.Object) []runtime.Object { return itemsOf(o.(*batchv1.CronJobList).Items) },
		convert: conv(fromCronJob),
	},
}

// namespaces returns the namespaces of a scope, which must name some:
// Huginn never lists cluster-wide (D-004).
func namespaces(scope ports.Scope) ([]string, error) {
	if len(scope.Namespaces) == 0 {
		return nil, fmt.Errorf("environment %s has no namespace: %w", scope.Env, domain.ErrConfig)
	}
	return scope.Namespaces, nil
}

// readableKinds lists each workload kind once in ns (limit 1) and keeps
// those the user may read. A developer often reads Deployments but not
// CronJobs: only when no kind is readable is the namespace an error.
func (c *Client) readableKinds(ctx context.Context, cs API, ns string) ([]workloadKind, error) {
	var ok []workloadKind
	var firstErr error
	for _, k := range workloadKinds {
		_, err := k.list(ctx, cs, ns, metav1.ListOptions{Limit: 1})
		switch {
		case err == nil:
			ok = append(ok, k)
		case firstErr == nil:
			firstErr = err
		}
		if err != nil && !isForbidden(err) {
			return nil, namespaced(ns, err)
		}
	}
	if len(ok) == 0 {
		return nil, namespaced(ns, firstErr)
	}
	return ok, nil
}

func isForbidden(err error) bool { return errors.Is(mapErr(err), domain.ErrForbidden) }

func namespaced(ns string, err error) error {
	return fmt.Errorf("namespace %s: %w", ns, mapErr(err))
}

// ListWorkloads lists the workloads of every kind the user may read.
func (c *Client) ListWorkloads(ctx context.Context, scope ports.Scope) ([]domain.Workload, error) {
	cs, nss, err := c.open(scope)
	if err != nil {
		return nil, err
	}
	var out []domain.Workload
	for _, ns := range nss {
		kinds, err := c.readableKinds(ctx, cs, ns)
		if err != nil {
			return nil, err
		}
		for _, k := range kinds {
			list, err := k.list(ctx, cs, ns, metav1.ListOptions{})
			if err != nil {
				return nil, namespaced(ns, err)
			}
			for _, o := range k.items(list) {
				if w, ok := k.convert(scope.Env, o); ok {
					out = append(out, w)
				}
			}
		}
	}
	return out, nil
}

// ListPods lists the pods matching sel.
func (c *Client) ListPods(ctx context.Context, scope ports.Scope, sel ports.Selector) ([]domain.Pod, error) {
	cs, nss, err := c.open(scope)
	if err != nil {
		return nil, err
	}
	var out []domain.Pod
	for _, ns := range nss {
		list, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: selector(sel)})
		if err != nil {
			return nil, namespaced(ns, err)
		}
		for i := range list.Items {
			out = append(out, toPod(scope.Env, &list.Items[i]))
		}
	}
	return out, nil
}

func selector(sel ports.Selector) string {
	if len(sel) == 0 {
		return ""
	}
	return labels.SelectorFromSet(labels.Set(sel)).String()
}

func (c *Client) open(scope ports.Scope) (API, []string, error) {
	nss, err := namespaces(scope)
	if err != nil {
		return nil, nil, err
	}
	cs, err := c.clientset(scope.Context)
	if err != nil {
		return nil, nil, err
	}
	return cs, nss, nil
}

// WatchWorkloads watches the workloads of every readable kind with one
// informer per kind and namespace. The informers send the current state as
// additions, then the changes, and reconnect by themselves.
func (c *Client) WatchWorkloads(ctx context.Context, scope ports.Scope) (<-chan ports.WorkloadEvent, error) {
	cs, nss, err := c.open(scope)
	if err != nil {
		return nil, err
	}
	var sources []informerSource
	for _, ns := range nss {
		kinds, err := c.readableKinds(ctx, cs, ns)
		if err != nil {
			return nil, err
		}
		for _, k := range kinds {
			sources = append(sources, informerSource{
				example: k.example,
				lw: cache.ToListWatcherWithWatchListSemantics(&cache.ListWatch{
					ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
						return k.list(ctx, cs, ns, o)
					},
					WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
						return k.watch(ctx, cs, ns, o)
					},
				}, cs),
			})
		}
	}
	env := scope.Env
	return run(ctx, sources, func(o any, deleted bool) (ports.WorkloadEvent, bool) {
		for _, k := range workloadKinds {
			if w, ok := k.convert(env, asObject(o)); ok {
				return ports.WorkloadEvent{Deleted: deleted, Workload: w}, true
			}
		}
		return ports.WorkloadEvent{}, false
	}), nil
}

// WatchPods watches the pods matching sel with one informer per namespace.
func (c *Client) WatchPods(ctx context.Context, scope ports.Scope, sel ports.Selector) (<-chan domain.PodEvent, error) {
	cs, nss, err := c.open(scope)
	if err != nil {
		return nil, err
	}
	ls := selector(sel)
	var sources []informerSource
	for _, ns := range nss {
		if _, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: ls, Limit: 1}); err != nil {
			return nil, namespaced(ns, err)
		}
		sources = append(sources, informerSource{
			example: &corev1.Pod{},
			lw: cache.ToListWatcherWithWatchListSemantics(&cache.ListWatch{
				ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
					o.LabelSelector = ls
					return cs.CoreV1().Pods(ns).List(ctx, o)
				},
				WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
					o.LabelSelector = ls
					return cs.CoreV1().Pods(ns).Watch(ctx, o)
				},
			}, cs),
		})
	}
	env := scope.Env
	return run(ctx, sources, func(o any, deleted bool) (domain.PodEvent, bool) {
		p, ok := asObject(o).(*corev1.Pod)
		if !ok {
			return domain.PodEvent{}, false
		}
		ev := domain.PodEvent{Type: domain.PodUpdated, Pod: toPod(env, p)}
		if deleted {
			ev.Type, ev.Pod.Deleted = domain.PodDeleted, true
		}
		return ev, true
	}), nil
}

type informerSource struct {
	example runtime.Object
	lw      cache.ListerWatcher
}

// asObject unwraps the tombstone the informer delivers for objects deleted
// while it was disconnected.
func asObject(o any) runtime.Object {
	if t, ok := o.(cache.DeletedFinalStateUnknown); ok {
		o = t.Obj
	}
	r, _ := o.(runtime.Object)
	return r
}

// run starts one informer per source and forwards their notifications to
// the returned channel, converted by conv: the initial state as additions
// (conv's result with Added set by the caller's type), then changes. The
// channel is closed once ctx is cancelled and every informer has stopped.
func run[E any](ctx context.Context, sources []informerSource, conv func(o any, deleted bool) (E, bool)) <-chan E {
	out := make(chan E, 64)
	var (
		mu     sync.RWMutex
		closed bool
		wg     sync.WaitGroup
	)
	send := func(o any, deleted bool, added bool) {
		ev, ok := conv(o, deleted)
		if !ok {
			return
		}
		if added {
			setAdded(&ev)
		}
		mu.RLock()
		defer mu.RUnlock()
		if closed {
			return
		}
		select {
		case out <- ev:
		case <-ctx.Done():
		}
	}
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(o any) { send(o, false, true) },
		UpdateFunc: func(_, o any) { send(o, false, false) },
		DeleteFunc: func(o any) { send(o, true, false) },
	}
	for _, s := range sources {
		inf := cache.NewSharedIndexInformer(s.lw, s.example, 0, cache.Indexers{})
		if _, err := inf.AddEventHandler(handler); err != nil {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			inf.RunWithContext(ctx)
		}()
	}
	go func() {
		<-ctx.Done()
		wg.Wait()
		mu.Lock()
		closed = true
		close(out)
		mu.Unlock()
	}()
	return out
}

// setAdded marks the first notification of an object as an addition.
func setAdded(ev any) {
	if p, ok := ev.(*domain.PodEvent); ok {
		p.Type = domain.PodAdded
	}
}
