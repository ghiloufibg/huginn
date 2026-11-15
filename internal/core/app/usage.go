package app

import (
	"context"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Usage implements ports.ResourceUsage: it joins the services of the
// catalog (which pod belongs to which service and namespace) with the usage
// of a MetricsSource, polled per namespace while a watch is open, and keeps a
// bounded history of the totals for trends. It reads no pods of its own.
type Usage struct {
	Catalog ports.ServiceCatalog
	Metrics ports.MetricsSource
	// Scopes returns the scope of an environment.
	Scopes ScopeFunc
	Filter domain.ContainerFilter
	Clock  ports.Clock
	// Refresh is the poll interval (default 15s, never below 5s).
	Refresh time.Duration
	// History is how far back the trends reach (default 15m); a negative
	// value keeps none.
	History time.Duration
	// Coalesce is the minimum delay between two snapshots (default 500ms).
	Coalesce time.Duration
	// Concurrency bounds the namespaces read at the same time (default 4).
	Concurrency int
	Log         *slog.Logger
}

// Bounds of the poll interval and defaults of the other settings.
const (
	MinUsageRefresh     = 5 * time.Second
	defaultUsageRefresh = 15 * time.Second
	defaultUsageHistory = 15 * time.Minute
	maxBackoffTicks     = 8
)

// pollResult is what the poll of one namespace reports to the loop.
type pollResult struct {
	ns    string
	usage []domain.PodUsage
	err   error
}

// nsUsage is what is known of the usage of one polled namespace.
type nsUsage struct {
	pods     map[domain.PodKey]domain.PodUsage
	err      error
	lastGood time.Time // when pods was last read, zero if never
	synced   bool
	fails    int // consecutive failures
	skip     int // refresh ticks left to skip after a failure
	inFlight bool
}

// Watch implements ports.ResourceUsage.
func (u *Usage) Watch(ctx context.Context, q ports.UsageQuery) (<-chan ports.UsageSnapshot, error) {
	scope, err := u.Scopes(ctx, q.Env)
	if err != nil {
		return nil, err
	}
	cat, err := u.Catalog.Watch(ctx, q.Env)
	if err != nil {
		return nil, err
	}
	out := make(chan ports.UsageSnapshot, 1)
	go u.loop(ctx, q, scope, cat, out)
	return out, nil
}

func (u *Usage) logger() *slog.Logger {
	if u.Log != nil {
		return u.Log
	}
	return slog.New(slog.DiscardHandler)
}

func (u *Usage) refresh() time.Duration {
	switch {
	case u.Refresh <= 0:
		return defaultUsageRefresh
	case u.Refresh < MinUsageRefresh:
		return MinUsageRefresh
	}
	return u.Refresh
}

func (u *Usage) history() time.Duration {
	switch {
	case u.History < 0:
		return 0
	case u.History == 0:
		return defaultUsageHistory
	}
	return u.History
}

// usageState is the loop's view of one watch.
type usageState struct {
	catalog    ports.CatalogSnapshot
	haveCat    bool
	ns         map[string]*nsUsage // by polled namespace ("" when unscoped)
	history    map[string][]domain.UsageSample
	recordPoll bool // a poll finished since the history was last extended
}

func (u *Usage) loop(ctx context.Context, q ports.UsageQuery, scope ports.Scope, cat <-chan ports.CatalogSnapshot, out chan ports.UsageSnapshot) {
	defer close(out)
	defer recovered(u.logger(), "usage of "+q.Env.String(), func(err error) {
		select {
		case out <- ports.UsageSnapshot{Env: q.Env, Err: err}:
		default:
		}
	})
	polled := scope.Namespaces
	if len(polled) == 0 {
		polled = []string{""}
	}
	st := &usageState{ns: map[string]*nsUsage{}, history: map[string][]domain.UsageSample{}}
	for _, ns := range polled {
		st.ns[ns] = &nsUsage{}
	}
	conc := u.Concurrency
	if conc <= 0 {
		conc = 4
	}
	sem := make(chan struct{}, conc)
	results := make(chan pollResult, len(polled))
	poll := func() {
		for _, ns := range polled {
			n := st.ns[ns]
			switch {
			case n.inFlight:
				continue
			case n.skip > 0:
				n.skip--
				continue
			}
			n.inFlight = true
			s := scope
			if ns != "" {
				s.Namespaces = []string{ns}
			}
			go u.fetch(ctx, ns, s, sem, results)
		}
	}
	coalesce := u.Coalesce
	if coalesce <= 0 {
		coalesce = 500 * time.Millisecond
	}
	refreshTick := u.Clock.NewTicker(u.refresh())
	defer refreshTick.Stop()
	emitTick := u.Clock.NewTicker(coalesce)
	defer emitTick.Stop()

	poll()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return
		case snap, ok := <-cat:
			if !ok {
				cat = nil
				continue
			}
			st.catalog, st.haveCat, dirty = snap, true, true
		case r := <-results:
			u.applyPoll(st, r)
			dirty = true
		case <-refreshTick.C():
			poll()
		case <-emitTick.C():
			if !dirty || !st.haveCat {
				continue
			}
			dirty = false
			replaceLatestUsage(out, u.snapshot(q, polled, st))
		}
	}
}

// fetch reads the usage of one namespace and reports it, however it ends.
func (u *Usage) fetch(ctx context.Context, ns string, scope ports.Scope, sem chan struct{}, results chan<- pollResult) {
	r := pollResult{ns: ns}
	defer func() {
		select {
		case results <- r:
		case <-ctx.Done():
		}
	}()
	defer recovered(u.logger(), "usage of namespace "+ns, func(err error) { r.err = err })
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		r.err = ctx.Err()
		return
	}
	r.usage, r.err = u.Metrics.PodUsage(ctx, scope)
}

func (u *Usage) applyPoll(st *usageState, r pollResult) {
	n := st.ns[r.ns]
	n.inFlight = false
	n.synced = true
	if r.err != nil {
		u.logger().Warn("usage poll failed", "namespace", r.ns, "err", r.err)
		n.err = r.err
		n.fails++
		n.skip = min(1<<(n.fails-1), maxBackoffTicks) - 1
		return
	}
	n.err, n.fails, n.skip = nil, 0, 0
	n.pods = make(map[domain.PodKey]domain.PodUsage, len(r.usage))
	for _, p := range r.usage {
		n.pods[p.Key()] = p
	}
	n.lastGood = u.Clock.Now()
	st.recordPoll = true
}

func replaceLatestUsage(out chan ports.UsageSnapshot, s ports.UsageSnapshot) {
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

// snapshot builds what the screen shows from the catalog and the last
// samples, and extends the history when a poll finished since the last one.
func (u *Usage) snapshot(q ports.UsageQuery, polled []string, st *usageState) ports.UsageSnapshot {
	now := u.Clock.Now()
	all := q.Containers == domain.ContainersAll
	snap := ports.UsageSnapshot{Env: q.Env, Synced: true}

	// Usage by pod across the namespaces polled.
	usage := map[domain.PodKey]domain.PodUsage{}
	var firstErr error
	for _, ns := range polled {
		n := st.ns[ns]
		if !n.synced {
			snap.Synced = false
		}
		maps.Copy(usage, n.pods)
		if n.err != nil {
			if firstErr == nil {
				firstErr = n.err
			}
			if !n.lastGood.IsZero() && (snap.StaleSince.IsZero() || n.lastGood.Before(snap.StaleSince)) {
				snap.StaleSince = n.lastGood
			}
		}
	}
	if len(usage) == 0 && firstErr != nil && allFailed(polled, st) {
		snap.Err = firstErr // nothing can be read: the namespaces show no error of their own
	}

	// Pods of the catalog by namespace and service.
	type group struct {
		repo       string
		unassigned bool
		pods       []domain.Pod
	}
	byNS := map[string][]*group{}
	listed := map[domain.PodKey]bool{}
	for _, svc := range st.catalog.Services {
		per := map[string]*group{}
		for _, p := range svc.Pods {
			if p.Deleted {
				continue
			}
			g := per[p.Namespace]
			if g == nil {
				g = &group{repo: svc.Repo, unassigned: svc.Unassigned}
				per[p.Namespace] = g
				byNS[p.Namespace] = append(byNS[p.Namespace], g)
			}
			g.pods = append(g.pods, p)
			listed[p.Key()] = true
		}
	}
	nsNames := slices.Clone(polled)
	if len(polled) == 1 && polled[0] == "" { // unscoped: namespaces come from what is seen
		seen := map[string]bool{}
		for ns := range byNS {
			seen[ns] = true
		}
		for k := range usage {
			seen[k.Namespace] = true
		}
		nsNames = slices.Sorted(maps.Keys(seen))
	}
	slices.Sort(nsNames)

	for _, ns := range nsNames {
		nu := domain.NamespaceUsage{Name: ns}
		if n, ok := st.ns[ns]; ok && n.err != nil && n.pods == nil {
			nu.Err = n.err
		}
		var nsPods []domain.Pod
		groups := byNS[ns]
		slices.SortFunc(groups, func(a, b *group) int { return strings.Compare(a.repo, b.repo) })
		for _, g := range groups {
			nsPods = append(nsPods, g.pods...)
			su := domain.ServiceUsage{
				Repo: g.repo, Unassigned: g.unassigned,
				Rollup: domain.RollupPods(g.pods, usage, u.Filter, all),
			}
			su.History = u.record(st, ns+"/"+g.repo, su.Rollup, now)
			nu.Services = append(nu.Services, su)
		}
		nu.Total = domain.RollupPods(nsPods, usage, u.Filter, all)
		nu.Unlisted = unlistedRollup(ns, usage, listed)
		nu.Total = withUnlisted(nu.Total, nu.Unlisted)
		nu.History = u.record(st, ns, nu.Total, now)
		snap.Namespaces = append(snap.Namespaces, nu)
		if t := nu.Total.SampledAt; !t.IsZero() && (snap.SampledAt.IsZero() || t.Before(snap.SampledAt)) {
			snap.SampledAt = t
		}
	}
	for _, p := range usage {
		snap.Window = max(snap.Window, p.Window)
	}
	st.recordPoll = false
	return snap
}

// record appends the use of r to the history of key when a poll finished
// since the last snapshot, trims what is older than the window, and returns
// a copy for the snapshot.
func (u *Usage) record(st *usageState, key string, r domain.Rollup, now time.Time) []domain.UsageSample {
	keep := u.history()
	if keep == 0 {
		return nil
	}
	h := st.history[key]
	if st.recordPoll && r.Reporting > 0 {
		h = append(h, domain.UsageSample{At: now, Use: r.Use})
	}
	cut := 0
	for cut < len(h) && now.Sub(h[cut].At) > keep {
		cut++
	}
	h = h[cut:]
	st.history[key] = h
	return slices.Clone(h)
}

// unlistedRollup sums the usage of the pods of ns that no service lists
// (finished jobs, filtered pods): their requests and limits are not known.
func unlistedRollup(ns string, usage map[domain.PodKey]domain.PodUsage, listed map[domain.PodKey]bool) domain.Rollup {
	var r domain.Rollup
	for k, p := range usage {
		if k.Namespace != ns || listed[k] {
			continue
		}
		r.Pods++
		r.Reporting++
		for _, c := range p.Containers {
			r.Use = r.Use.Add(c.Use)
		}
		if r.SampledAt.IsZero() || p.SampledAt.Before(r.SampledAt) {
			r.SampledAt = p.SampledAt
		}
	}
	return r
}

// withUnlisted adds the unlisted pods to a namespace total. Their requests
// and limits are unknown, so no ratio of the total stays defined.
func withUnlisted(total, unlisted domain.Rollup) domain.Rollup {
	if unlisted.Pods == 0 {
		return total
	}
	total.Pods += unlisted.Pods
	total.Reporting += unlisted.Reporting
	total.Use = total.Use.Add(unlisted.Use)
	total.RequestSet, total.LimitSet = domain.Coverage{}, domain.Coverage{}
	if total.SampledAt.IsZero() || unlisted.SampledAt.Before(total.SampledAt) {
		total.SampledAt = unlisted.SampledAt
	}
	return total
}

func allFailed(polled []string, st *usageState) bool {
	return !slices.ContainsFunc(polled, func(ns string) bool { return st.ns[ns].err == nil })
}
