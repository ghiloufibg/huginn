package demo

import (
	"fmt"
	"hash/fnv"
	"slices"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// world is the demo cluster state for all environments.
type world struct {
	seed      int64
	start     time.Time
	workloads map[domain.Env][]domain.Workload
	pods      map[domain.Env][]domain.Pod
	events    map[string][]domain.Event // namespace/pod
	// specs remembers how each pod behaves, for the log generator.
	specs map[string]podSpec // namespace/pod
}

// podSpec is what the log generator needs to know about a pod.
type podSpec struct {
	repo      repoSpec
	workload  string
	cond      condition
	version   string
	createdAt time.Time
	// runningSince is when the current app container instance started.
	runningSince time.Time
}

const namePool = "bcdfghjklmnpqrstvwxz2456789"

func hashOf(parts ...any) uint64 {
	h := fnv.New64a()
	for _, p := range parts {
		fmt.Fprint(h, p, "|")
	}
	return h.Sum64()
}

func randomName(n int, parts ...any) string {
	h := hashOf(parts...)
	b := make([]byte, n)
	for i := range b {
		b[i] = namePool[h%uint64(len(namePool))]
		h = h/uint64(len(namePool)) ^ hashOf(h, i)
	}
	return string(b)
}

func newWorld(seed int64, start time.Time, namespaces map[domain.Env]string) *world {
	w := &world{
		seed: seed, start: start,
		workloads: map[domain.Env][]domain.Workload{},
		pods:      map[domain.Env][]domain.Pod{},
		events:    map[string][]domain.Event{},
		specs:     map[string]podSpec{},
	}
	envs := make([]domain.Env, 0, len(namespaces))
	for env := range namespaces {
		envs = append(envs, env)
	}
	slices.Sort(envs)
	for _, env := range envs {
		for _, r := range repos {
			for i, wl := range r.workloads {
				cond := healthy
				if i == 0 {
					cond = conditions[env][r.name]
				}
				w.addWorkload(env, namespaces[env], r, wl, cond)
			}
		}
		w.addUnlabelled(env, namespaces[env])
	}
	return w
}

// addUnlabelled adds a legacy workload that carries none of the usual
// repository labels, so it shows up as "without repo".
func (w *world) addUnlabelled(env domain.Env, ns string) {
	r := repoSpec{name: "", workloads: []string{"nightly-report"}, replicas: 1, version: "1.0.3", age: 400 * day, pkg: "com.acme.reports"}
	w.addWorkload(env, ns, r, "nightly-report", healthy)
	last := len(w.workloads[env]) - 1
	w.workloads[env][last].Labels = map[string]string{"k8s-app": "nightly-report"}
}

func (w *world) addWorkload(env domain.Env, ns string, r repoSpec, name string, cond condition) {
	replicas := r.replicas
	created := w.start.Add(-r.age)
	ref := domain.WorkloadRef{Env: env, Namespace: ns, Kind: domain.KindDeployment, Name: name}
	labels := map[string]string{
		"app.kubernetes.io/name":    name,
		"app.kubernetes.io/part-of": r.name,
		"app.kubernetes.io/version": r.version,
	}
	rs := randomName(10, w.seed, env, name, r.version)
	var pods []domain.Pod
	for i := range replicas {
		p, spec := w.newPod(env, ns, r, name, rs, r.version, i, created, cond)
		pods = append(pods, p)
		w.specs[ns+"/"+p.Name] = spec
	}
	if cond == rollingOut {
		pods = w.rollingOut(env, ns, r, name, pods, created)
	}
	ready, updated := 0, 0
	for _, p := range pods {
		if podReady(p) {
			ready++
		}
		if p.Labels["app.kubernetes.io/version"] == r.version {
			updated++
		}
	}
	w.workloads[env] = append(w.workloads[env], domain.Workload{
		Ref: ref, Labels: labels, Annotations: map[string]string{},
		DesiredReplicas: replicas, ReadyReplicas: ready, UpdatedReplicas: updated,
		Progressing: cond == rollingOut,
		Selector:    map[string]string{"app.kubernetes.io/name": name},
		Created:     created,
	})
	w.pods[env] = append(w.pods[env], pods...)
}

// rollingOut replaces the first pod by one of the previous version and
// makes the last new pod still starting.
func (w *world) rollingOut(env domain.Env, ns string, r repoSpec, name string, pods []domain.Pod, created time.Time) []domain.Pod {
	prev := r.version + "-prev"
	oldRS := randomName(10, w.seed, env, name, prev)
	old, spec := w.newPod(env, ns, r, name, oldRS, prev, 0, created.Add(-3*day), healthy)
	delete(w.specs, ns+"/"+pods[0].Name)
	w.specs[ns+"/"+old.Name] = spec
	pods[0] = old
	last := &pods[len(pods)-1]
	app := &last.Containers[1]
	app.Ready, app.State = false, domain.ContainerRunning
	return pods
}

func podReady(p domain.Pod) bool {
	if p.Phase != domain.PodRunning {
		return false
	}
	for _, c := range p.Containers {
		if !c.Init && !c.Ready {
			return false
		}
	}
	return true
}

func (w *world) newPod(env domain.Env, ns string, r repoSpec, workload, rs, version string, i int, created time.Time, cond condition) (domain.Pod, podSpec) {
	name := fmt.Sprintf("%s-%s-%s", workload, rs, randomName(5, w.seed, env, workload, rs, i))
	podCreated := created.Add(time.Duration(i) * 7 * time.Second)
	p := domain.Pod{
		Env: env, Namespace: ns, Name: name,
		Labels: map[string]string{
			"app.kubernetes.io/name":    workload,
			"app.kubernetes.io/part-of": r.name,
			"app.kubernetes.io/version": version,
			"pod-template-hash":         rs,
		},
		Phase: domain.PodRunning, Node: "gke-main-pool-" + randomName(4, w.seed, env, name),
		Created: podCreated, Started: podCreated.Add(2 * time.Second), OwnerName: workload,
	}
	app := domain.Container{
		Name: workload, Image: "eu.gcr.io/acme/" + workload + ":" + version,
		State: domain.ContainerRunning, Ready: true,
		Resources: domain.Resources{CPURequest: "500m", CPULimit: "1", MemoryRequest: "768Mi", MemoryLimit: "1Gi"},
	}
	p.Containers = []domain.Container{
		{Name: "istio-init", Image: "docker.io/istio/proxyv2:1.24.2", Init: true, State: domain.ContainerTerminated, Reason: "Completed"},
		app,
		{Name: "istio-proxy", Image: "docker.io/istio/proxyv2:1.24.2", State: domain.ContainerRunning, Ready: true},
	}
	if r.vault {
		p.Containers = append(p.Containers, domain.Container{Name: "vault-agent", Image: "hashicorp/vault:1.18", State: domain.ContainerRunning, Ready: true})
	}
	spec := podSpec{repo: r, workload: workload, cond: cond, version: version, createdAt: podCreated, runningSince: p.Started}
	w.applyCondition(&p, &spec, i)
	for j := range p.Containers {
		c := &p.Containers[j]
		switch {
		case c.State == domain.ContainerWaiting:
		case j == 1:
			c.Started = spec.runningSince
		default:
			c.Started = p.Started
		}
	}
	return p, spec
}

func (w *world) applyCondition(p *domain.Pod, spec *podSpec, i int) {
	app := &p.Containers[1]
	key := p.Namespace + "/" + p.Name
	switch spec.cond {
	case crashLoop:
		app.State, app.Reason, app.Ready = domain.ContainerWaiting, "CrashLoopBackOff", false
		app.Message = fmt.Sprintf("back-off 5m0s restarting failed container=%s pod=%s_%s", app.Name, p.Name, p.Namespace)
		app.Restarts = 12 - i
		app.LastTermination = &domain.Termination{Reason: "Error", ExitCode: 1, At: w.start.Add(-4 * time.Minute)}
		spec.runningSince = w.start.Add(-4*time.Minute - 25*time.Second)
		w.events[key] = []domain.Event{
			{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container " + app.Name + " in pod " + p.Name, Count: 87, LastSeen: w.start.Add(-30 * time.Second)},
			{Type: "Normal", Reason: "Pulled", Message: `Container image "` + app.Image + `" already present on machine`, Count: 12, LastSeen: w.start.Add(-4 * time.Minute)},
		}
	case oomKilled:
		if i != 2 {
			return
		}
		app.State, app.Reason, app.Ready = domain.ContainerTerminated, "OOMKilled", false
		app.Restarts = 12
		app.LastTermination = &domain.Termination{Reason: "OOMKilled", ExitCode: 137, At: w.start.Add(-38 * time.Minute)}
		spec.runningSince = w.start.Add(-41 * time.Minute)
		w.events[key] = []domain.Event{
			{Type: "Warning", Reason: "OOMKilling", Message: "Memory cgroup out of memory: Killed process 1 (java)", Count: 12, LastSeen: w.start.Add(-38 * time.Minute)},
			{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container " + app.Name, Count: 30, LastSeen: w.start.Add(-time.Minute)},
		}
	case imagePull:
		p.Phase, p.Started = domain.PodPending, time.Time{}
		app.State, app.Reason, app.Ready = domain.ContainerWaiting, "ImagePullBackOff", false
		app.Message = `Back-off pulling image "` + app.Image + `": manifest unknown`
		w.events[key] = []domain.Event{
			{Type: "Warning", Reason: "Failed", Message: `Failed to pull image "` + app.Image + `": manifest unknown`, Count: 9, LastSeen: w.start.Add(-time.Minute)},
			{Type: "Normal", Reason: "BackOff", Message: `Back-off pulling image "` + app.Image + `"`, Count: 40, LastSeen: w.start.Add(-20 * time.Second)},
		}
	case degraded:
		if i == 0 {
			return
		}
		app.Ready, app.Restarts = false, 3+i
		app.LastTermination = &domain.Termination{Reason: "Error", ExitCode: 143, At: w.start.Add(-9 * time.Minute)}
		spec.runningSince = w.start.Add(-9 * time.Minute)
		w.events[key] = []domain.Event{
			{Type: "Warning", Reason: "Unhealthy", Message: "Readiness probe failed: HTTP probe failed with statuscode: 503", Count: 54, LastSeen: w.start.Add(-10 * time.Second)},
		}
	case pending:
		if i != 1 {
			return
		}
		p.Phase, p.Started, p.Node = domain.PodPending, time.Time{}, ""
		p.Reason, p.Message = "Unschedulable", "0/6 nodes are available: 6 Insufficient memory. preemption: 0/6 nodes are available: 6 No preemption victims found for incoming pod."
		for j := range p.Containers {
			if !p.Containers[j].Init {
				p.Containers[j].State, p.Containers[j].Reason, p.Containers[j].Ready = domain.ContainerWaiting, "ContainerCreating", false
			}
		}
		w.events[key] = []domain.Event{
			{Type: "Warning", Reason: "FailedScheduling", Message: "0/6 nodes are available: 6 Insufficient memory.", Count: 14, LastSeen: w.start.Add(-15 * time.Second)},
		}
	case restartedOnce:
		if i != 1 {
			return
		}
		app.Restarts = 1
		app.LastTermination = &domain.Termination{Reason: "OOMKilled", ExitCode: 137, At: w.start.Add(-2 * time.Hour)}
		spec.runningSince = w.start.Add(-2 * time.Hour)
		w.events[key] = []domain.Event{
			{Type: "Warning", Reason: "OOMKilling", Message: "Memory cgroup out of memory: Killed process 1 (java)", Count: 1, LastSeen: w.start.Add(-2 * time.Hour)},
			{Type: "Normal", Reason: "Started", Message: "Started container " + app.Name, Count: 2, LastSeen: w.start.Add(-2 * time.Hour)},
		}
	}
}

// hasPrevious reports whether the container has a previous instance.
func hasPrevious(p domain.Pod, container string) bool {
	for _, c := range p.Containers {
		if c.Name == container {
			return c.LastTermination != nil
		}
	}
	return false
}
