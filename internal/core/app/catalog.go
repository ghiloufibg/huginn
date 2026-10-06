package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Catalog implements ports.ServiceCatalog on top of cluster watches: it
// watches each namespace of an environment separately (so one forbidden
// namespace does not hide the others), resolves workloads to repositories
// and summarizes them.
type Catalog struct {
	Cluster  ports.ClusterClient
	Resolver ports.RepoResolver
	// Scopes returns the scope of an environment.
	Scopes ScopeFunc
	Filter domain.ContainerFilter
	Clock  ports.Clock
	// Coalesce is the minimum delay between two snapshots (default 100ms).
	Coalesce time.Duration
	// Backoff lists the waits between reconnection attempts; the last one
	// repeats (default 1s, 2s, 5s, 10s, 30s).
	Backoff []time.Duration
	Log     *slog.Logger
	// Standalone lists the pods no known workload claims, grouped by
	// owner (domain.StandaloneWorkloads).
	Standalone bool
}

var defaultBackoff = []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second}

// feedMsg is what a namespace feed reports to the catalog loop.
type feedMsg struct {
	ns       string
	reset    bool // a new watch started: forget the namespace's state
	workload *ports.WorkloadEvent
	pod      *domain.PodEvent
	err      error
}

// ScopeFunc returns the cluster scope of an environment. Resolving it may
// read a secret (namespace_from), hence the context and the error.
type ScopeFunc func(ctx context.Context, env domain.Env) (ports.Scope, error)

// Watch implements ports.ServiceCatalog.
func (c *Catalog) Watch(ctx context.Context, env domain.Env) (<-chan ports.CatalogSnapshot, error) {
	scope, err := c.Scopes(ctx, env)
	if err != nil {
		return nil, err
	}
	namespaces := scope.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{""}
	}
	msgs := make(chan feedMsg, 256)
	for _, ns := range namespaces {
		s := scope
		if ns != "" {
			s.Namespaces = []string{ns}
		}
		go c.feed(ctx, ns, s, msgs)
	}
	out := make(chan ports.CatalogSnapshot, 1)
	go c.loop(ctx, env, namespaces, msgs, out)
	return out, nil
}

// feed keeps one namespace watched, reconnecting with backoff.
func (c *Catalog) feed(ctx context.Context, ns string, scope ports.Scope, msgs chan<- feedMsg) {
	defer recovered(c.logger(), "watch of namespace "+ns, nil)
	send := func(m feedMsg) bool {
		select {
		case msgs <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for attempt := 0; ctx.Err() == nil; attempt++ {
		err := c.watchOnce(ctx, ns, scope, send)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			attempt = 0
			err = fmt.Errorf("watch of namespace %q ended: %w", ns, domain.ErrUnreachable)
		}
		c.logger().Warn("namespace watch failed", "namespace", ns, "err", err)
		if domain.Permanent(err) { // retrying cannot fix the setup
			send(feedMsg{ns: ns, err: err})
			return
		}
		if !send(feedMsg{ns: ns, err: err}) || !c.sleep(ctx, c.backoff(attempt)) {
			return
		}
	}
}

// watchOnce runs one workload+pod watch until either ends. It returns nil
// when a watch channel closed, or the error that prevented watching.
func (c *Catalog) watchOnce(ctx context.Context, ns string, scope ports.Scope, send func(feedMsg) bool) error {
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wch, err := c.Cluster.WatchWorkloads(wctx, scope)
	if err != nil {
		return err
	}
	pch, err := c.Cluster.WatchPods(wctx, scope, nil)
	if err != nil {
		return err
	}
	if !send(feedMsg{ns: ns, reset: true}) {
		return nil
	}
	for {
		select {
		case ev, ok := <-wch:
			if !ok || !send(feedMsg{ns: ns, workload: &ev}) {
				return nil
			}
		case ev, ok := <-pch:
			if !ok || !send(feedMsg{ns: ns, pod: &ev}) {
				return nil
			}
		case <-ctx.Done():
			return nil
		}
	}
}

func (c *Catalog) backoff(attempt int) time.Duration {
	b := c.Backoff
	if len(b) == 0 {
		b = defaultBackoff
	}
	return b[min(attempt, len(b)-1)]
}

func (c *Catalog) sleep(ctx context.Context, d time.Duration) bool {
	t := c.Clock.NewTicker(d)
	defer t.Stop()
	select {
	case <-t.C():
		return true
	case <-ctx.Done():
		return false
	}
}

func (c *Catalog) logger() *slog.Logger {
	if c.Log != nil {
		return c.Log
	}
	return slog.New(slog.DiscardHandler)
}

// state is the catalog's view of one environment.
type state struct {
	workloads map[string]domain.Workload // namespace/name
	pods      map[string]domain.Pod      // namespace/name
	nsErr     map[string]error
	errSince  map[string]time.Time // when each namespace in nsErr lost its watch
	connected map[string]bool
	warnings  map[string][]string // by namespace
}

func key(ns, name string) string { return ns + "/" + name }

// refKey identifies a workload (a standalone group may share a name with
// a workload of another kind); the environment is the catalog's.
func refKey(r domain.WorkloadRef) domain.WorkloadRef { r.Env = ""; return r }

func (s *state) apply(m feedMsg, now time.Time) {
	switch {
	case m.err != nil:
		s.nsErr[m.ns] = m.err
		s.connected[m.ns] = false
		if _, ok := s.errSince[m.ns]; !ok { // retries keep the first failure
			if s.errSince == nil {
				s.errSince = map[string]time.Time{}
			}
			s.errSince[m.ns] = now
		}
	case m.reset:
		s.dropNamespace(m.ns)
		delete(s.nsErr, m.ns)
		delete(s.errSince, m.ns)
		delete(s.warnings, m.ns)
		s.connected[m.ns] = true
	case m.workload != nil && m.workload.Warning != "":
		if s.warnings == nil {
			s.warnings = map[string][]string{}
		}
		s.warnings[m.ns] = append(s.warnings[m.ns], m.workload.Warning)
	case m.workload != nil:
		w := m.workload.Workload
		if m.workload.Deleted {
			delete(s.workloads, key(w.Ref.Namespace, w.Ref.Name))
		} else {
			s.workloads[key(w.Ref.Namespace, w.Ref.Name)] = w
		}
	case m.pod != nil:
		p := m.pod.Pod
		if m.pod.Type == domain.PodDeleted {
			delete(s.pods, key(p.Namespace, p.Name))
		} else {
			s.pods[key(p.Namespace, p.Name)] = p
		}
	}
}

// dropNamespace forgets ns ("" means everything, for unscoped watches).
func (s *state) dropNamespace(ns string) {
	for k, w := range s.workloads {
		if ns == "" || w.Ref.Namespace == ns {
			delete(s.workloads, k)
		}
	}
	for k, p := range s.pods {
		if ns == "" || p.Namespace == ns {
			delete(s.pods, k)
		}
	}
}

func (c *Catalog) loop(ctx context.Context, env domain.Env, namespaces []string, msgs <-chan feedMsg, out chan ports.CatalogSnapshot) {
	defer close(out)
	defer recovered(c.logger(), "services of "+env.String(), func(err error) {
		select {
		case out <- ports.CatalogSnapshot{Env: env, Err: err}:
		default:
		}
	})
	st := &state{workloads: map[string]domain.Workload{}, pods: map[string]domain.Pod{}, nsErr: map[string]error{}, connected: map[string]bool{}, warnings: map[string][]string{}}
	coalesce := c.Coalesce
	if coalesce <= 0 {
		coalesce = 100 * time.Millisecond
	}
	tick := c.Clock.NewTicker(coalesce)
	defer tick.Stop()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-msgs:
			st.apply(m, c.Clock.Now())
			dirty = true
		case <-tick.C():
			if !dirty {
				continue
			}
			dirty = false
			snap := c.snapshot(ctx, env, namespaces, st)
			replaceLatest(out, snap)
		}
	}
}

// replaceLatest sends s, dropping an unread older snapshot if needed.
func replaceLatest(out chan ports.CatalogSnapshot, s ports.CatalogSnapshot) {
	select {
	case out <- s:
	default:
		select {
		case <-out:
		default:
		}
		out <- s
	}
}

func (c *Catalog) snapshot(ctx context.Context, env domain.Env, namespaces []string, st *state) ports.CatalogSnapshot {
	snap := ports.CatalogSnapshot{Env: env, UpdatedAt: c.Clock.Now(), Synced: true}
	if len(st.nsErr) > 0 {
		snap.NamespaceErrs = map[string]error{}
		for ns, err := range st.nsErr {
			snap.NamespaceErrs[ns] = err
			if since := st.errSince[ns]; snap.StaleSince.IsZero() || since.Before(snap.StaleSince) {
				snap.StaleSince = since
			}
		}
	}
	for _, ns := range namespaces {
		if !st.connected[ns] {
			snap.Synced = false
		}
		snap.Warnings = append(snap.Warnings, st.warnings[ns]...)
	}
	if len(st.nsErr) == len(namespaces) {
		snap.Err = firstErr(st.nsErr, namespaces)
	}
	ws := make([]domain.Workload, 0, len(st.workloads))
	for _, w := range st.workloads {
		ws = append(ws, w)
	}
	slices.SortFunc(ws, func(a, b domain.Workload) int {
		return strings.Compare(key(a.Ref.Namespace, a.Ref.Name), key(b.Ref.Namespace, b.Ref.Name))
	})
	idx := indexPods(st.pods)
	if c.Standalone {
		ws = append(ws, domain.StandaloneWorkloads(ws, idx.all)...)
	}
	repos, rest, err := c.Resolver.Resolve(ctx, env, ws)
	if err != nil {
		snap.Err = errors.Join(snap.Err, fmt.Errorf("resolve repositories: %w", err))
		return snap
	}
	byKey := make(map[domain.WorkloadRef]domain.Workload, len(ws))
	for _, w := range ws {
		byKey[refKey(w.Ref)] = w
	}
	for _, r := range repos {
		var rws []domain.Workload
		for _, ref := range r.Workloads {
			rws = append(rws, byKey[refKey(ref)])
		}
		snap.Services = append(snap.Services, domain.Summarize(r.Name, rws, idx.podsOf(rws), c.Filter))
	}
	domain.SortServices(snap.Services, domain.SortByName)
	var orphans []domain.ServiceSummary
	for _, w := range rest {
		s := domain.Summarize(w.Ref.Name, []domain.Workload{w}, idx.podsOf([]domain.Workload{w}), c.Filter)
		s.Unassigned = true
		orphans = append(orphans, s)
	}
	domain.SortServices(orphans, domain.SortByName)
	snap.Services = append(snap.Services, orphans...)
	return snap
}

// podIndex finds the pods of a workload quickly: by owner name when the
// adapter reports it, else by label selector over the namespace's pods
// without owner information.
type podIndex struct {
	byOwner map[string][]domain.Pod // namespace/owner
	unowned map[string][]domain.Pod // namespace
	all     []domain.Pod
}

func indexPods(pods map[string]domain.Pod) podIndex {
	idx := podIndex{byOwner: map[string][]domain.Pod{}, unowned: map[string][]domain.Pod{}, all: make([]domain.Pod, 0, len(pods))}
	for _, p := range pods {
		idx.all = append(idx.all, p)
		if p.OwnerName == "" {
			idx.unowned[p.Namespace] = append(idx.unowned[p.Namespace], p)
			continue
		}
		k := key(p.Namespace, p.OwnerName)
		idx.byOwner[k] = append(idx.byOwner[k], p)
	}
	return idx
}

// podsOf returns the pods of the workloads (domain.Workload.Owns), ordered
// by name. Candidates come from the index: pods naming the workload as
// owner, and pods without owner (matched by selector, or by name for a
// standalone bare pod).
func (idx podIndex) podsOf(ws []domain.Workload) []domain.Pod {
	var out []domain.Pod
	for _, w := range ws {
		for _, p := range idx.byOwner[key(w.Ref.Namespace, w.Ref.Name)] {
			if w.Owns(p) {
				out = append(out, p)
			}
		}
		bare := w.Standalone && w.Ref.Kind == domain.KindPod
		if len(w.Selector) == 0 && !bare {
			continue
		}
		for _, p := range idx.unowned[w.Ref.Namespace] {
			if w.Owns(p) {
				out = append(out, p)
			}
		}
	}
	slices.SortFunc(out, func(a, b domain.Pod) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func firstErr(errs map[string]error, order []string) error {
	for _, ns := range order {
		if err := errs[ns]; err != nil {
			return err
		}
	}
	return nil
}
