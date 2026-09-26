package domain

import (
	"testing"
	"time"
)

var now = time.Date(2026, 9, 26, 19, 0, 0, 0, time.UTC)

func running(name string) Container {
	return Container{Name: name, Image: "eu.gcr.io/acme/" + name + ":v1", State: ContainerRunning, Ready: true}
}

func pod(owner string, cs ...Container) Pod {
	return Pod{Name: owner + "-x", OwnerName: owner, Phase: PodRunning, Containers: cs, Created: now}
}

func TestPodStatusPrecedence(t *testing.T) {
	pending := pod("api", Container{Name: "api", State: ContainerWaiting, Reason: "ContainerCreating"})
	pending.Phase = PodPending
	if s := Summarize("api", []Workload{wl("api", 2, 1, 2, now)}, []Pod{pod("api", running("api")), pending}, filter); s.Status != StatusPending {
		t.Errorf("a pending pod names the missing replica: %v", s.Status)
	}
	crash := Container{Name: "api", State: ContainerWaiting, Reason: "CrashLoopBackOff"}
	crashOOM := crash
	crashOOM.LastTermination = &Termination{Reason: "OOMKilled"}
	oom := Container{Name: "api", State: ContainerTerminated, Reason: "OOMKilled"}
	pull := Container{Name: "api", State: ContainerWaiting, Reason: "ErrImagePull"}
	notReady := running("api")
	notReady.Ready = false
	initDone := Container{Name: "istio-init", Init: true, State: ContainerTerminated, Reason: "Completed"}

	tests := []struct {
		name string
		pod  Pod
		want ServiceStatus
	}{
		{"healthy", pod("api", initDone, running("api"), running("istio-proxy")), StatusHealthy},
		{"crash loop", pod("api", crash, running("istio-proxy")), StatusCrashLoopBackOff},
		{"crash loop caused by OOM", pod("api", crashOOM), StatusOOMKilled},
		{"crash loop beats OOM elsewhere", pod("api", crash, oom), StatusCrashLoopBackOff},
		{"oom killed", pod("api", oom), StatusOOMKilled},
		{"image pull", pod("api", pull), StatusImagePullBackOff},
		{"oom beats image pull", pod("api", pull, oom), StatusOOMKilled},
		{"not ready", pod("api", notReady), StatusDegraded},
		{"sidecar crash counts", pod("api", running("api"), Container{Name: "istio-proxy", State: ContainerWaiting, Reason: "CrashLoopBackOff"}), StatusCrashLoopBackOff},
		{"pending", func() Pod {
			p := pod("api", Container{Name: "api", State: ContainerWaiting, Reason: "ContainerCreating"})
			p.Phase = PodPending
			return p
		}(), StatusPending},
		{"pending with pull error", func() Pod { p := pod("api", pull); p.Phase = PodPending; return p }(), StatusImagePullBackOff},
		{"failed", func() Pod { p := pod("api", running("api")); p.Phase = PodFailed; return p }(), StatusDegraded},
		{"unknown", func() Pod { p := pod("api", running("api")); p.Phase = PodUnknown; return p }(), StatusUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PodStatus(tt.pod); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

var filter = ContainerFilter{Deny: []string{"istio-proxy", "istio-init", "vault-agent"}}

func wl(name string, desired, ready, updated int, created time.Time) Workload {
	return Workload{Ref: WorkloadRef{Name: name, Kind: KindDeployment}, DesiredReplicas: desired, ReadyReplicas: ready, UpdatedReplicas: updated, Created: created}
}

func TestSummarize(t *testing.T) {
	restarted := running("api")
	restarted.Restarts = 2
	restarted.LastTermination = &Termination{Reason: "OOMKilled", At: now.Add(-2 * time.Hour)}
	proxy := running("istio-proxy")
	proxy.Restarts = 9
	pods := []Pod{pod("api", restarted, proxy), pod("api", running("api")), pod("api-worker", running("api-worker"))}
	s := Summarize("api", []Workload{wl("api", 2, 2, 2, now.Add(-time.Hour)), wl("api-worker", 1, 1, 1, now.Add(-48*time.Hour))}, pods, filter)
	if s.Status != StatusHealthy || s.ReadyPods != 3 || s.DesiredPods != 3 || s.Workloads != 2 {
		t.Fatalf("summary: %+v", s)
	}
	if s.Restarts != 2 {
		t.Errorf("restarts = %d, want 2 (sidecar restarts excluded)", s.Restarts)
	}
	if !s.LastRestart.Equal(now.Add(-2*time.Hour)) || !s.Created.Equal(now.Add(-48*time.Hour)) || s.Version != "v1" {
		t.Errorf("summary: %+v", s)
	}
}

func TestSummarizeRollout(t *testing.T) {
	old := pod("api", running("api"))
	old.Created = now.Add(-time.Hour)
	next := running("api")
	next.Image, next.Ready = "eu.gcr.io/acme/api:v2", false
	newPod := pod("api", next)
	s := Summarize("api", []Workload{wl("api", 2, 1, 1, now)}, []Pod{newPod, old}, filter)
	if s.Status != StatusProgressing {
		t.Errorf("status = %v, want Progressing (starting pod during rollout is not degraded)", s.Status)
	}
	if s.Version != "v1→v2" {
		t.Errorf("version = %q", s.Version)
	}
}

func TestSummarizeDegradedAndScaledToZero(t *testing.T) {
	notReady := running("api")
	notReady.Ready = false
	if s := Summarize("api", []Workload{wl("api", 2, 1, 2, now)}, []Pod{pod("api", running("api")), pod("api", notReady)}, filter); s.Status != StatusDegraded {
		t.Errorf("degraded: %v", s.Status)
	}
	if s := Summarize("api", []Workload{wl("api", 3, 1, 3, now)}, []Pod{pod("api", running("api"))}, filter); s.Status != StatusDegraded {
		t.Errorf("missing replicas: %v", s.Status)
	}
	if s := Summarize("api", []Workload{wl("api", 0, 0, 0, now)}, nil, filter); s.Status != StatusUnknown {
		t.Errorf("scaled to 0: %v", s.Status)
	}
	pending := pod("api", Container{Name: "api", State: ContainerWaiting, Reason: "ContainerCreating"})
	pending.Phase = PodPending
	if s := Summarize("api", []Workload{wl("api", 2, 1, 2, now)}, []Pod{pod("api", running("api")), pending}, filter); s.Status != StatusPending {
		t.Errorf("a pending pod names the missing replica: %v", s.Status)
	}
	crash := Container{Name: "api", State: ContainerWaiting, Reason: "CrashLoopBackOff"}
	if s := Summarize("api", []Workload{wl("api", 2, 1, 1, now)}, []Pod{pod("api", crash)}, filter); s.Status != StatusCrashLoopBackOff {
		t.Errorf("crash during rollout must stay a crash: %v", s.Status)
	}
}

func TestSortServices(t *testing.T) {
	rows := []ServiceSummary{
		{Repo: "b", Status: StatusHealthy, Restarts: 1, Created: now.Add(-3 * time.Hour)},
		{Repo: "a", Status: StatusHealthy, Restarts: 5, Created: now.Add(-time.Hour)},
		{Repo: "c", Status: StatusCrashLoopBackOff, Restarts: 5, Created: now.Add(-2 * time.Hour)},
	}
	order := func() string {
		s := ""
		for _, r := range rows {
			s += r.Repo
		}
		return s
	}
	for k, want := range map[SortKey]string{SortByStatus: "cab", SortByName: "abc", SortByRestarts: "acb", SortByAge: "acb"} {
		SortServices(rows, k)
		if got := order(); got != want {
			t.Errorf("%v: got %s, want %s", k, got, want)
		}
	}
	if SortByAge.Next() != SortByStatus || SortByStatus.Next() != SortByName {
		t.Error("sort key cycle")
	}
}

func TestWorstPod(t *testing.T) {
	ok := Pod{Name: "a", Phase: PodRunning, Containers: []Container{{Name: "app", State: ContainerRunning, Ready: true}}}
	bad := Pod{Name: "b", Phase: PodRunning, Containers: []Container{{Name: "app", State: ContainerWaiting, Reason: "CrashLoopBackOff"}}}
	if _, found := WorstPod(nil); found {
		t.Fatal("no pod expected")
	}
	if p, _ := WorstPod([]Pod{ok, bad, ok}); p.Name != "b" {
		t.Fatalf("worst pod = %s, want b", p.Name)
	}
	if p, _ := WorstPod([]Pod{ok, {Name: "c", Phase: PodRunning, Containers: ok.Containers}}); p.Name != "a" {
		t.Fatalf("worst pod = %s, want the first healthy one", p.Name)
	}
}
