package domain

import (
	"testing"
	"time"
)

// E3, E4, E23 of docs/plan/M4-e2e-pass2.md.

func TestFailingInitContainerIsACrashLoop(t *testing.T) {
	p := Pod{Phase: PodPending, Containers: []Container{
		{Name: "migrate", Init: true, State: ContainerTerminated, Reason: "Error", Restarts: 3},
		{Name: "app", State: ContainerWaiting, Reason: "PodInitializing"},
	}}
	if st := PodStatus(p); st != StatusCrashLoopBackOff {
		t.Fatalf("status %v", st)
	}
	f := ContainerFilter{}
	s := Summarize("init-fail", []Workload{{Ref: WorkloadRef{Name: "init-fail"}, DesiredReplicas: 1}}, []Pod{p}, f)
	if s.Restarts != 3 || s.Status != StatusCrashLoopBackOff {
		t.Fatalf("summary %+v", s)
	}
	names := []string{}
	for _, c := range f.LogContainers(p) {
		names = append(names, c.Name)
	}
	if len(names) != 2 || names[1] != "migrate" {
		t.Fatalf("log containers %v", names)
	}
	p.Containers[0] = Container{Name: "migrate", Init: true, State: ContainerTerminated, Reason: "Completed"}
	if len(f.LogContainers(p)) != 1 {
		t.Fatal("a completed init container is not followed")
	}
}

func TestCompletedJobPodsAreHealthy(t *testing.T) {
	done := Pod{Phase: PodSucceeded, OwnerName: "export", Containers: []Container{{Name: "export", State: ContainerTerminated, Reason: "Completed"}}}
	if st := PodStatus(done); st != StatusHealthy {
		t.Fatalf("status %v", st)
	}
	cron := Workload{Ref: WorkloadRef{Kind: KindCronJob, Name: "export"}}
	s := Summarize("export", []Workload{cron}, []Pod{done, done}, ContainerFilter{})
	if s.Status != StatusHealthy || Explain(s, ContainerFilter{}, time.Now()) != "" {
		t.Fatalf("idle cronjob: %v %q", s.Status, Explain(s, ContainerFilter{}, time.Now()))
	}
	zero := Workload{Ref: WorkloadRef{Kind: KindDeployment, Name: "z"}}
	if s := Summarize("z", []Workload{zero}, nil, ContainerFilter{}); s.Status != StatusUnknown {
		t.Fatalf("a deployment scaled to 0 stays so: %v", s.Status)
	}
}

func TestRestartOnlyRolloutVersion(t *testing.T) {
	pod := func(rev string) Pod {
		return Pod{OwnerName: "api", Revision: rev, Containers: []Container{{Name: "api", Image: "api:3.20"}}}
	}
	if v := WorkloadVersion("api", []Pod{pod("a"), pod("a")}, ContainerFilter{}); v != "3.20" {
		t.Fatalf("steady: %q", v)
	}
	if v := WorkloadVersion("api", []Pod{pod("a"), pod("b")}, ContainerFilter{}); v != "3.20 (restart)" {
		t.Fatalf("restart rollout: %q", v)
	}
	old := pod("a")
	old.Deleted = true
	if v := WorkloadVersion("api", []Pod{old, pod("b")}, ContainerFilter{}); v != "3.20" {
		t.Fatalf("terminating pods do not count: %q", v)
	}
}
