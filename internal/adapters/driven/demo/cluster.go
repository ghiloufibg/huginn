package demo

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Options configure the demo cluster.
type Options struct {
	Seed int64
	// Rate is the average number of live lines per second per container.
	Rate float64
	// Namespaces gives the namespace of each environment (one cluster, one
	// namespace per environment).
	Namespaces map[domain.Env]string
	Clock      ports.Clock
	// Retention is how far back "the node" keeps logs; older lines are
	// not returned, to reproduce the real retention caveat. Default 6h.
	Retention time.Duration
}

// Cluster is the demo implementation of ports.ClusterClient and
// ports.LogSource.
type Cluster struct {
	mu        sync.Mutex
	opts      Options
	world     *world
	stage     int
	podSubs   []*podSub
	wlSubs    []*wlSub
	rolloutNS string
}

type podSub struct {
	scope ports.Scope
	sel   ports.Selector
	ch    chan domain.PodEvent
}

type wlSub struct {
	scope ports.Scope
	ch    chan ports.WorkloadEvent
}

// New builds the demo cluster at the clock's current time.
func New(opts Options) *Cluster {
	if opts.Rate <= 0 {
		opts.Rate = 0.5
	}
	if opts.Retention <= 0 {
		opts.Retention = 6 * time.Hour
	}
	if len(opts.Namespaces) == 0 {
		opts.Namespaces = map[domain.Env]string{}
		for _, e := range domain.DefaultEnvs() {
			opts.Namespaces[e] = "app-" + e.String()
		}
	}
	return &Cluster{
		opts:      opts,
		world:     newWorld(opts.Seed, opts.Clock.Now(), opts.Namespaces),
		rolloutNS: opts.Namespaces[domain.EnvRec],
	}
}

func inScope(s ports.Scope, env domain.Env, ns string) bool {
	return s.Env == env && (len(s.Namespaces) == 0 || slices.Contains(s.Namespaces, ns))
}

// ListWorkloads implements ports.ClusterClient.
func (c *Cluster) ListWorkloads(_ context.Context, scope ports.Scope) ([]domain.Workload, error) {
	c.advance()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workloadsLocked(scope), nil
}

func (c *Cluster) workloadsLocked(scope ports.Scope) []domain.Workload {
	var out []domain.Workload
	for _, w := range c.world.workloads[scope.Env] {
		if inScope(scope, w.Ref.Env, w.Ref.Namespace) {
			out = append(out, w)
		}
	}
	return out
}

// ListPods implements ports.ClusterClient.
func (c *Cluster) ListPods(_ context.Context, scope ports.Scope, sel ports.Selector) ([]domain.Pod, error) {
	c.advance()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.podsLocked(scope, sel), nil
}

func (c *Cluster) podsLocked(scope ports.Scope, sel ports.Selector) []domain.Pod {
	var out []domain.Pod
	for _, p := range c.world.pods[scope.Env] {
		if inScope(scope, p.Env, p.Namespace) && sel.Matches(p.Labels) {
			out = append(out, p)
		}
	}
	return out
}

// WatchPods implements ports.ClusterClient.
func (c *Cluster) WatchPods(ctx context.Context, scope ports.Scope, sel ports.Selector) (<-chan domain.PodEvent, error) {
	c.advance()
	c.mu.Lock()
	pods := c.podsLocked(scope, sel)
	sub := &podSub{scope: scope, sel: sel, ch: make(chan domain.PodEvent, len(pods)+256)}
	for _, p := range pods {
		sub.ch <- domain.PodEvent{Type: domain.PodAdded, Pod: p}
	}
	c.podSubs = append(c.podSubs, sub)
	c.mu.Unlock()
	go c.pump(ctx, func() {
		c.podSubs = slices.DeleteFunc(c.podSubs, func(s *podSub) bool { return s == sub })
		close(sub.ch)
	})
	return sub.ch, nil
}

// WatchWorkloads implements ports.ClusterClient.
func (c *Cluster) WatchWorkloads(ctx context.Context, scope ports.Scope) (<-chan ports.WorkloadEvent, error) {
	c.advance()
	c.mu.Lock()
	ws := c.workloadsLocked(scope)
	sub := &wlSub{scope: scope, ch: make(chan ports.WorkloadEvent, len(ws)+256)}
	for _, w := range ws {
		sub.ch <- ports.WorkloadEvent{Workload: w}
	}
	c.wlSubs = append(c.wlSubs, sub)
	c.mu.Unlock()
	go c.pump(ctx, func() {
		c.wlSubs = slices.DeleteFunc(c.wlSubs, func(s *wlSub) bool { return s == sub })
		close(sub.ch)
	})
	return sub.ch, nil
}

// pump advances the simulation every second until ctx ends, then runs
// done under the lock.
func (c *Cluster) pump(ctx context.Context, done func()) {
	t := c.opts.Clock.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			c.mu.Lock()
			done()
			c.mu.Unlock()
			return
		case <-t.C():
			c.advance()
		}
	}
}

// PodEvents implements ports.ClusterClient.
func (c *Cluster) PodEvents(_ context.Context, _ ports.Scope, namespace, pod string) ([]domain.Event, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.world.specs[namespace+"/"+pod]; !ok {
		return nil, fmt.Errorf("pod %s/%s: %w", namespace, pod, domain.ErrNotFound)
	}
	return slices.Clone(c.world.events[namespace+"/"+pod]), nil
}

// advance applies the live rollout stages that are due. Notifications are
// sent after the lock is released, into buffered channels.
func (c *Cluster) advance() {
	c.mu.Lock()
	elapsed := c.opts.Clock.Now().Sub(c.world.start)
	var podEvs []domain.PodEvent
	var wlEvs []ports.WorkloadEvent
	for c.stage < 3 && elapsed >= c.stageAt(c.stage) {
		pe, we := c.applyStage(c.stage)
		podEvs, wlEvs = append(podEvs, pe...), append(wlEvs, we...)
		c.stage++
	}
	podSubs, wlSubs := slices.Clone(c.podSubs), slices.Clone(c.wlSubs)
	c.mu.Unlock()
	for _, ev := range podEvs {
		for _, s := range podSubs {
			if inScope(s.scope, ev.Pod.Env, ev.Pod.Namespace) && s.sel.Matches(ev.Pod.Labels) {
				trySend(s.ch, ev)
			}
		}
	}
	for _, ev := range wlEvs {
		for _, s := range wlSubs {
			if inScope(s.scope, ev.Workload.Ref.Env, ev.Workload.Ref.Namespace) {
				trySend(s.ch, ev)
			}
		}
	}
}

// trySend never blocks and tolerates a channel closed concurrently by an
// ending watch.
func trySend[T any](ch chan T, v T) {
	defer func() { _ = recover() }()
	select {
	case ch <- v:
	default:
	}
}

func (c *Cluster) stageAt(stage int) time.Duration {
	return [...]time.Duration{liveRollout.start, liveRollout.ready, liveRollout.retire}[stage]
}

// applyStage runs one step of the live rollout in rec: 0 adds a new pod,
// 1 makes it ready, 2 retires the oldest pod.
func (c *Cluster) applyStage(stage int) ([]domain.PodEvent, []ports.WorkloadEvent) {
	env := domain.EnvRec
	if _, ok := c.opts.Namespaces[env]; !ok {
		return nil, nil
	}
	wi := slices.IndexFunc(c.world.workloads[env], func(w domain.Workload) bool { return w.Ref.Name == liveRollout.workload })
	wl := &c.world.workloads[env][wi]
	now := c.opts.Clock.Now()
	var evs []domain.PodEvent
	switch stage {
	case 0:
		r := repos[slices.IndexFunc(repos, func(r repoSpec) bool { return r.name == liveRollout.repo })]
		rs := randomName(10, c.world.seed, env, wl.Ref.Name, liveRollout.version)
		p, spec := c.world.newPod(env, c.rolloutNS, r, wl.Ref.Name, rs, liveRollout.version, 0, now, healthy)
		p.Containers[1].Ready = false
		c.world.pods[env] = append(c.world.pods[env], p)
		c.world.specs[p.Namespace+"/"+p.Name] = spec
		c.world.events[p.Namespace+"/"+p.Name] = []domain.Event{{Type: "Normal", Reason: "Scheduled", Message: "Successfully assigned " + p.Namespace + "/" + p.Name, Count: 1, LastSeen: now}}
		wl.Progressing = true
		evs = append(evs, domain.PodEvent{Type: domain.PodAdded, Pod: p})
	case 1:
		i := c.newestPodIndex(env, wl.Ref.Name)
		c.world.pods[env][i].Containers[1].Ready = true
		wl.ReadyReplicas++
		evs = append(evs, domain.PodEvent{Type: domain.PodUpdated, Pod: c.world.pods[env][i]})
	case 2:
		i := slices.IndexFunc(c.world.pods[env], func(p domain.Pod) bool { return p.OwnerName == wl.Ref.Name })
		old := c.world.pods[env][i]
		c.world.pods[env] = slices.Delete(c.world.pods[env], i, i+1)
		old.Deleted = true
		wl.Progressing, wl.ReadyReplicas = false, wl.ReadyReplicas-1
		wl.Labels["app.kubernetes.io/version"] = liveRollout.version
		evs = append(evs, domain.PodEvent{Type: domain.PodDeleted, Pod: old})
	}
	return evs, []ports.WorkloadEvent{{Workload: *wl}}
}

func (c *Cluster) newestPodIndex(env domain.Env, workload string) int {
	best := -1
	for i, p := range c.world.pods[env] {
		if p.OwnerName == workload && (best < 0 || p.Created.After(c.world.pods[env][best].Created)) {
			best = i
		}
	}
	return best
}
