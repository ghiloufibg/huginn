package domain

import (
	"testing"
	"time"
)

var st0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

func runningC(name string) Container {
	return Container{Name: name, State: ContainerRunning, Ready: true}
}

func TestStandaloneWorkloads(t *testing.T) {
	api := Workload{Ref: WorkloadRef{Namespace: "ns", Kind: KindDeployment, Name: "api"}, Selector: map[string]string{"app": "api"}}
	pods := []Pod{
		{Namespace: "ns", Name: "api-1", OwnerName: "api", OwnerKind: "Deployment", Labels: map[string]string{"app": "api"}, Containers: []Container{runningC("api")}},
		// a bare pod matching the Deployment's selector: adopted by it
		{Namespace: "ns", Name: "api-manual", Labels: map[string]string{"app": "api"}, Containers: []Container{runningC("api")}},
		{Namespace: "ns", Name: "debug-shell", Created: st0, Labels: map[string]string{"app.kubernetes.io/part-of": "shop"}, Containers: []Container{runningC("sh")}},
		{Namespace: "ns", Name: "migrate-x1", OwnerName: "migrate", OwnerKind: "Job", Phase: PodSucceeded, Created: st0},
		{Namespace: "ns", Name: "migrate-x2", OwnerName: "migrate", OwnerKind: "Job", Created: st0.Add(-time.Hour), Containers: []Container{{Name: "m", State: ContainerWaiting}}},
		{Namespace: "ns", Name: "roll-1", OwnerName: "canary", OwnerKind: "Rollout", Containers: []Container{runningC("app")}},
		{Namespace: "other", Name: "debug-shell", Containers: []Container{runningC("sh")}},
	}
	got := StandaloneWorkloads([]Workload{api}, pods)
	want := []string{"ns/Rollout/canary", "ns/Pod/debug-shell", "ns/Job/migrate", "other/Pod/debug-shell"}
	if len(got) != len(want) {
		t.Fatalf("got %d groups: %+v", len(got), got)
	}
	for i, w := range got {
		if s := w.Ref.Namespace + "/" + string(w.Ref.Kind) + "/" + w.Ref.Name; s != want[i] || !w.Standalone {
			t.Errorf("group %d: %s, want %s", i, s, want[i])
		}
	}
	job := got[2]
	if job.DesiredReplicas != 1 || job.ReadyReplicas != 0 || !job.Created.Equal(st0.Add(-time.Hour)) {
		t.Errorf("job group %+v", job)
	}
	if got[1].Labels["app.kubernetes.io/part-of"] != "shop" {
		t.Error("labels come from the pod, for the resolvers")
	}
	for _, p := range pods {
		n := 0
		for _, w := range append(got, api) {
			if w.Owns(p) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("pod %s/%s owned %d times", p.Namespace, p.Name, n)
		}
	}
}

func TestFinishedStandaloneJobIsHealthy(t *testing.T) {
	p := Pod{Namespace: "ns", Name: "once-1", OwnerName: "once", OwnerKind: "Job", Phase: PodSucceeded,
		Containers: []Container{{Name: "c", State: ContainerTerminated, Reason: "Completed"}}}
	ws := StandaloneWorkloads(nil, []Pod{p})
	if s := Summarize("once", ws, []Pod{p}, ContainerFilter{}); s.Status != StatusHealthy {
		t.Fatalf("status %v", s.Status)
	}
}

func TestContainerModesAndRoles(t *testing.T) {
	f := ContainerFilter{Deny: []string{"istio-proxy"}}
	p := Pod{OwnerName: "api", Containers: []Container{
		{Name: "migrate", Init: true, State: ContainerTerminated, Reason: "Completed"},
		runningC("api"), runningC("istio-proxy"),
	}}
	names := func(cs []Container) (out []string) {
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return out
	}
	if got := names(f.StreamContainers(p, ContainersApp)); len(got) != 1 || got[0] != "api" {
		t.Errorf("app mode: %v", got)
	}
	if got := names(f.StreamContainers(p, ContainersAll)); len(got) != 3 {
		t.Errorf("all mode: %v", got)
	}
	for i, want := range []ContainerRole{RoleInit, RoleApp, RoleSidecar} {
		if r := f.Role(p, p.Containers[i]); r != want {
			t.Errorf("%s: %v, want %v", p.Containers[i].Name, r, want)
		}
	}
	if m, err := ParseContainerMode("all"); err != nil || m != ContainersAll || m.String() != "all" {
		t.Error("parse all")
	}
	if _, err := ParseContainerMode("sidecars"); err == nil {
		t.Error("unknown mode accepted")
	}
}
