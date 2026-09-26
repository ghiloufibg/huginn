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

// instance is one run of a container: when it started, when it ended (zero
// while running) and why.
type instance struct {
	start, end time.Time
	reason     string
}

type stream struct {
	ch  chan domain.RawLine
	mu  sync.Mutex
	err error
}

func (s *stream) Lines() <-chan domain.RawLine { return s.ch }

func (s *stream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Stream implements ports.LogSource.
func (c *Cluster) Stream(ctx context.Context, req ports.LogRequest) (ports.LogStream, error) {
	c.advance()
	c.mu.Lock()
	pod, spec, ok := c.findPod(req.Scope.Env, req.Namespace, req.Pod)
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("pod %s/%s: %w", req.Namespace, req.Pod, domain.ErrNotFound)
	}
	ctr, ok := findContainer(pod, req.Container)
	if !ok {
		return nil, fmt.Errorf("container %s in pod %s: %w", req.Container, req.Pod, domain.ErrNotFound)
	}
	inst, err := c.instanceOf(pod, spec, ctr, req.Previous)
	if err != nil {
		return nil, err
	}
	now := c.opts.Clock.Now()
	g := c.generatorFor(pod, spec)
	pc := contextOf(pod, ctr)
	hist := c.history(g, pc, ctr, inst, req, now)
	st := &stream{ch: make(chan domain.RawLine, len(hist)+1024)}
	for _, l := range hist {
		st.ch <- l
	}
	if !req.Follow || req.Previous || !inst.end.IsZero() {
		close(st.ch)
		return st, nil
	}
	go c.follow(ctx, st, g, pc, ctr, inst, now)
	return st, nil
}

func (c *Cluster) findPod(env domain.Env, ns, name string) (domain.Pod, podSpec, bool) {
	for _, p := range c.world.pods[env] {
		if p.Namespace == ns && p.Name == name {
			return p, c.world.specs[ns+"/"+name], true
		}
	}
	return domain.Pod{}, podSpec{}, false
}

func findContainer(p domain.Pod, name string) (domain.Container, bool) {
	for _, c := range p.Containers {
		if c.Name == name {
			return c, true
		}
	}
	return domain.Container{}, false
}

func contextOf(p domain.Pod, c domain.Container) podContext {
	return podContext{namespace: p.Namespace, pod: p.Name, container: c.Name, image: c.Image, node: p.Node, workload: p.OwnerName, labels: p.Labels}
}

// instanceOf returns the current or previous instance of a container, or
// ErrNotFound when it has none, like the Kubernetes API does.
func (c *Cluster) instanceOf(p domain.Pod, spec podSpec, ctr domain.Container, previous bool) (instance, error) {
	if previous {
		if !hasPrevious(p, ctr.Name) {
			return instance{}, fmt.Errorf("previous terminated container %q in pod %q not found: %w", ctr.Name, p.Name, domain.ErrNotFound)
		}
		end := ctr.LastTermination.At
		start := end.Add(-25 * time.Second)
		if spec.cond != crashLoop {
			start = latest(p.Started, end.Add(-c.opts.Retention))
		}
		return instance{start: start, end: end, reason: ctr.LastTermination.Reason}, nil
	}
	if ctr.State == domain.ContainerWaiting && (p.Started.IsZero() || !hasPrevious(p, ctr.Name)) {
		return instance{}, fmt.Errorf("container %q in pod %q is waiting to start: %s: %w", ctr.Name, p.Name, ctr.Reason, domain.ErrNotFound)
	}
	if ctr.Init || ctr.Name != spec.workload {
		return instance{start: p.Started}, nil
	}
	inst := instance{start: spec.runningSince}
	switch {
	case spec.cond == crashLoop:
		inst.end, inst.reason = spec.runningSince.Add(25*time.Second), "Error"
	case ctr.State == domain.ContainerTerminated && ctr.LastTermination != nil:
		inst.end, inst.reason = ctr.LastTermination.At, ctr.LastTermination.Reason
	}
	return inst, nil
}

func (c *Cluster) generatorFor(p domain.Pod, spec podSpec) generator {
	return generator{seed: c.opts.Seed, spec: spec, pod: p.Namespace + "/" + p.Name, start: c.world.start}
}

// history returns the lines of inst inside window w, bounded by retention.
func (c *Cluster) history(g generator, pc podContext, ctr domain.Container, inst instance, req ports.LogRequest, now time.Time) []domain.RawLine {
	w := req.Window
	to := now
	if !inst.end.IsZero() && inst.end.Before(now) {
		to = inst.end
	}
	lower := latest(inst.start, now.Add(-c.opts.Retention))
	from := lower
	if !req.SinceTime.IsZero() {
		return c.lines(g, pc, ctr, inst, latest(lower, req.SinceTime), to, true)
	}
	limit := req.Limit
	if w.IsTail() && (limit <= 0 || w.Tail < limit) {
		limit = w.Tail
	}
	if w.Since > 0 {
		from = latest(lower, now.Add(-w.Since))
	}
	if limit > 0 {
		// Only generate what the limit keeps (twice the expected span).
		from = latest(from, to.Add(-time.Duration(float64(limit)*2/c.opts.Rate*float64(time.Second))))
	}
	lines := c.lines(g, pc, ctr, inst, from, to, true)
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines
}

// follow emits new lines as the clock advances until ctx ends or the pod
// disappears.
func (c *Cluster) follow(ctx context.Context, st *stream, g generator, pc podContext, ctr domain.Container, inst instance, from time.Time) {
	defer close(st.ch)
	t := c.opts.Clock.NewTicker(200 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
		}
		c.advance()
		now := c.opts.Clock.Now()
		c.mu.Lock()
		_, _, alive := c.findPod(pc.env(c), pc.namespace, pc.pod)
		c.mu.Unlock()
		for _, l := range c.lines(g, pc, ctr, inst, from, now, false) {
			select {
			case st.ch <- l:
			case <-ctx.Done():
				return
			}
		}
		from = now
		if !alive {
			return
		}
	}
}

func (pc podContext) env(c *Cluster) domain.Env {
	for env, ns := range c.opts.Namespaces {
		if ns == pc.namespace {
			return env
		}
	}
	return ""
}

// lines generates the lines of inst in [from, to] (or (from, to] when
// inclusive is false), ordered by time.
func (c *Cluster) lines(g generator, pc podContext, ctr domain.Container, inst instance, from, to time.Time, inclusive bool) []domain.RawLine {
	in := func(t time.Time) bool {
		return (t.After(from) || (inclusive && t.Equal(from))) && !t.After(to)
	}
	app := ctr.Name == g.spec.workload && !ctr.Init
	var es []entry
	if app {
		for _, e := range startupEntries(g.spec.repo, g.spec.workload, g.spec.version, inst.start) {
			if in(e.at) {
				es = append(es, e)
			}
		}
		if ready := readyEntry(g.spec.repo, g.spec.workload, inst.start.Add(8*time.Second)); in(ready.at) {
			es = append(es, ready)
		}
		if !inst.end.IsZero() {
			for _, e := range g.crashEntries(inst.reason, inst.end) {
				if in(e.at) {
					es = append(es, e)
				}
			}
		}
	}
	interval := time.Duration(float64(time.Second) / c.opts.Rate)
	if !app {
		interval *= 4
	}
	quietUntil := inst.start.Add(9 * time.Second)
	stop := to
	if !inst.end.IsZero() {
		stop = earliest(to, inst.end.Add(-4*time.Second))
	}
	for k := from.UnixNano() / int64(interval); ; k++ {
		t := time.Unix(0, k*int64(interval)+int64(hashOf(pc.pod, ctr.Name, k)%uint64(interval)))
		if t.After(stop) {
			break
		}
		if !in(t) || t.Before(quietUntil) {
			continue
		}
		if app {
			es = append(es, g.at(k, t))
		} else {
			es = append(es, g.sidecar(ctr.Name, k, t))
		}
	}
	slices.SortStableFunc(es, func(a, b entry) int { return a.at.Compare(b.at) })
	out := make([]domain.RawLine, len(es))
	for i, e := range es {
		out[i] = domain.RawLine{Time: e.at, Pod: pc.pod, Container: pc.container, Text: encode(e, pc)}
	}
	return out
}

func latest(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
