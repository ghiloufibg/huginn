package domain

import (
	"slices"
	"strings"
	"time"
)

// Amount is a quantity of CPU and memory: CPU in millicores, memory in
// bytes. A zero field means "not set" for requests and limits.
type Amount struct {
	CPUMilli int64
	MemBytes int64
}

// Add returns the sum of a and b.
func (a Amount) Add(b Amount) Amount {
	return Amount{CPUMilli: a.CPUMilli + b.CPUMilli, MemBytes: a.MemBytes + b.MemBytes}
}

// ResourceAmounts are the requests and limits of a container as numbers.
type ResourceAmounts struct {
	Request, Limit Amount
}

// PodKey identifies a pod of an environment's namespaces.
type PodKey struct {
	Namespace, Name string
}

// Key returns the key of p.
func (p Pod) Key() PodKey { return PodKey{Namespace: p.Namespace, Name: p.Name} }

// ContainerUsage is the measured usage of one container.
type ContainerUsage struct {
	Name string
	Use  Amount
}

// PodUsage is one metrics sample of a pod. Usage is an average over Window
// (tens of seconds), not an instant: a peak between two samples is not seen.
type PodUsage struct {
	Namespace, Name string
	// SampledAt is the metric's own timestamp.
	SampledAt  time.Time
	Window     time.Duration
	Containers []ContainerUsage
}

// Key returns the key of the pod measured.
func (u PodUsage) Key() PodKey { return PodKey{Namespace: u.Namespace, Name: u.Name} }

// Coverage says, per resource, whether every container counted has a value.
// A ratio against a sum is meaningless when one container has none: it would
// hide an unbounded consumer.
type Coverage struct {
	CPU, Mem bool
}

// Rollup is the sum of the usage of a set of pods; it is the unit of display.
// Requests and limits are summed over the pods that report only, so that a
// pod without a sample does not lower the ratios.
type Rollup struct {
	// Pods is the number of pods counted, Reporting those with a sample.
	Pods, Reporting int
	Use             Amount
	Request, Limit  Amount
	// RequestSet and LimitSet say whether every counted container has a
	// request (limit); when not, the matching ratio is undefined.
	RequestSet, LimitSet Coverage
	// HottestPod is the pod using the largest share of its memory limit, and
	// HottestMemPct that share: a ratio of sums hides a single hot pod.
	HottestPod    string
	HottestMemPct float64
	// SampledAt is the oldest sample used, zero without any.
	SampledAt time.Time
}

// UsageSample is one point of a history.
type UsageSample struct {
	At  time.Time
	Use Amount
}

// ServiceUsage is the usage of one row of the services screen in one namespace.
type ServiceUsage struct {
	Repo       string
	Unassigned bool
	Rollup
	History []UsageSample
	// PodRows are the pods of the service in this namespace, by name.
	PodRows []PodDetail
}

// ContainerDetail is what one container uses against what it asked for.
type ContainerDetail struct {
	Name string
	// Measured is false for a container the sample does not cover (not
	// running); its Use is then zero.
	Measured       bool
	Use            Amount
	Request, Limit Amount
	// Restarts is the restart count of the container.
	Restarts int
}

// PodDetail is the usage of one pod and of its containers.
type PodDetail struct {
	Pod Pod
	Rollup
	Containers []ContainerDetail
}

// NamespaceUsage is the usage of one namespace: its total, its services, and
// what the services screen does not list, so that the services add up.
type NamespaceUsage struct {
	Name string
	// Total covers every pod of the namespace, Unlisted those no service
	// row owns.
	Total, Unlisted Rollup
	Services        []ServiceUsage
	History         []UsageSample
	// Err is why this namespace has no usage (forbidden, unavailable).
	Err error
}

// RollupPods sums the usage of pods. Init containers never count (they do
// not run alongside); with all false only application containers (f) do.
// Only containers the sample measures count, in the use and in the requests
// and limits: one that is not running (crashed, waiting) uses nothing, and
// counting its limit would lower every ratio.
func RollupPods(pods []Pod, usage map[PodKey]PodUsage, f ContainerFilter, all bool) Rollup {
	r := Rollup{Pods: len(pods)}
	r.RequestSet = Coverage{CPU: true, Mem: true}
	r.LimitSet = Coverage{CPU: true, Mem: true}
	for _, p := range pods {
		u, ok := usage[p.Key()]
		if !ok {
			continue
		}
		r.Reporting++
		if r.SampledAt.IsZero() || u.SampledAt.Before(r.SampledAt) {
			r.SampledAt = u.SampledAt
		}
		var use, limit Amount
		limited := true
		for _, c := range countedContainers(p, f, all) {
			cu, measured := usedBy(u, c.Name)
			if !measured { // not running: it uses nothing and its limit bounds nothing
				continue
			}
			use = use.Add(cu)
			am := c.Amounts
			r.Request = r.Request.Add(am.Request)
			r.Limit = r.Limit.Add(am.Limit)
			r.RequestSet.CPU = r.RequestSet.CPU && am.Request.CPUMilli > 0
			r.RequestSet.Mem = r.RequestSet.Mem && am.Request.MemBytes > 0
			r.LimitSet.CPU = r.LimitSet.CPU && am.Limit.CPUMilli > 0
			r.LimitSet.Mem = r.LimitSet.Mem && am.Limit.MemBytes > 0
			limited = limited && am.Limit.MemBytes > 0
			limit = limit.Add(am.Limit)
		}
		r.Use = r.Use.Add(use)
		if pct, ok := Pct(use.MemBytes, limit.MemBytes); ok && limited && pct > r.HottestMemPct {
			r.HottestMemPct, r.HottestPod = pct, p.Name
		}
	}
	if r.Reporting == 0 { // nothing to compare: no ratio is defined
		r.RequestSet, r.LimitSet = Coverage{}, Coverage{}
	}
	return r
}

func countedContainers(p Pod, f ContainerFilter, all bool) []Container {
	if !all {
		return f.AppContainers(p)
	}
	var out []Container
	for _, c := range p.Containers {
		if !c.Init {
			out = append(out, c)
		}
	}
	return out
}

func usedBy(u PodUsage, container string) (Amount, bool) {
	for _, c := range u.Containers {
		if c.Name == container {
			return c.Use, true
		}
	}
	return Amount{}, false
}

// Pct returns use as a percentage of ref; false when ref is not set.
func Pct(use, ref int64) (float64, bool) {
	if ref <= 0 {
		return 0, false
	}
	return float64(use) * 100 / float64(ref), true
}

// Thresholds are the percentages of a limit (or request) at which a value is
// shown as a warning and as critical.
type Thresholds struct {
	Warn, Crit float64
}

// Severity is how close a ratio is to its limit.
type Severity int

// Severities, from best to worst.
const (
	SeverityOK Severity = iota
	SeverityWarn
	SeverityCrit
)

// Severity classifies pct against the thresholds.
func (t Thresholds) Severity(pct float64) Severity {
	switch {
	case pct >= t.Crit:
		return SeverityCrit
	case pct >= t.Warn:
		return SeverityWarn
	}
	return SeverityOK
}

// Provisioning says whether the requests and limits fit the usage.
type Provisioning int

// Provisioning states.
const (
	ProvisioningUnknown Provisioning = iota
	ProvisioningOK
	ProvisioningOver
	ProvisioningUnder
)

// OverProvisionedBelow is the percentage of the request under which both
// CPU and memory must stay for a rollup to read as over-provisioned.
const OverProvisionedBelow = 25

// Provision classifies r: under-provisioned when CPU or memory reaches the
// critical share of its limit, over-provisioned when both stay far below
// their requests. It is a statement of fact over the window, not advice.
func (r Rollup) Provision(t Thresholds) Provisioning {
	if r.Reporting == 0 {
		return ProvisioningUnknown
	}
	if r.LimitSet.CPU {
		if pct, ok := Pct(r.Use.CPUMilli, r.Limit.CPUMilli); ok && pct >= t.Crit {
			return ProvisioningUnder
		}
	}
	if r.LimitSet.Mem {
		if pct, ok := Pct(r.Use.MemBytes, r.Limit.MemBytes); ok && pct >= t.Crit {
			return ProvisioningUnder
		}
	}
	if !r.RequestSet.CPU || !r.RequestSet.Mem {
		return ProvisioningUnknown
	}
	cpu, _ := Pct(r.Use.CPUMilli, r.Request.CPUMilli)
	mem, _ := Pct(r.Use.MemBytes, r.Request.MemBytes)
	if cpu < OverProvisionedBelow && mem < OverProvisionedBelow {
		return ProvisioningOver
	}
	return ProvisioningOK
}

// PodDetails returns the pods with their containers' usage, ordered by pod
// name. The containers are the ones RollupPods counts.
func PodDetails(pods []Pod, usage map[PodKey]PodUsage, f ContainerFilter, all bool) []PodDetail {
	out := make([]PodDetail, 0, len(pods))
	for _, p := range pods {
		d := PodDetail{Pod: p, Rollup: RollupPods([]Pod{p}, usage, f, all)}
		u, reported := usage[p.Key()]
		for _, c := range countedContainers(p, f, all) {
			cd := ContainerDetail{Name: c.Name, Request: c.Amounts.Request, Limit: c.Amounts.Limit, Restarts: c.Restarts}
			if reported {
				cd.Use, cd.Measured = usedBy(u, c.Name)
			}
			d.Containers = append(d.Containers, cd)
		}
		out = append(out, d)
	}
	slices.SortFunc(out, func(a, b PodDetail) int { return strings.Compare(a.Pod.Name, b.Pod.Name) })
	return out
}
