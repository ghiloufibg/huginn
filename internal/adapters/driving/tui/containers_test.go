package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// sidecarPods is a pod with an application container and an istio-proxy
// sidecar, streamed in all mode.
func sidecarPods(all bool) []ports.PodState {
	pod := domain.Pod{Name: "payment-service-7f8f9cc5-5cw8s", OwnerName: "payment-service", Phase: domain.PodRunning, Containers: []domain.Container{
		{Name: "payment-service", State: domain.ContainerRunning, Ready: true},
		{Name: "istio-proxy", State: domain.ContainerRunning, Ready: true},
	}}
	st := ports.PodState{Pod: pod, Containers: []string{"payment-service"}, Roles: map[string]domain.ContainerRole{"payment-service": domain.RoleApp}}
	if all {
		st.Containers = append(st.Containers, "istio-proxy")
		st.Roles["istio-proxy"] = domain.RoleSidecar
	}
	return []ports.PodState{st}
}

func TestAllContainersKey(t *testing.T) {
	m, l := openLogs(t)
	before := len(sessions.queries)
	press(m, "A")
	if len(sessions.queries) != before+1 || sessions.queries[before].Containers != domain.ContainersAll {
		t.Fatalf("A must reopen with all containers: %+v", sessions.queries[before:])
	}
	if m.flashText != "all containers (sidecars and init)" || !strings.Contains(render(m, 200, 24), "containers all") {
		t.Fatalf("flash %q", m.flashText)
	}
	press(m, "A")
	if q := sessions.queries[len(sessions.queries)-1]; q.Containers != domain.ContainersApp || l.containerMode != domain.ContainersApp {
		t.Fatal("A again: application containers")
	}
}

// The selector chooses containers; a sidecar in app mode switches to all.
func TestSelectorChoosesContainers(t *testing.T) {
	m, l := openLogs(t)
	feed(m, l, ports.LogBatch{Pods: sidecarPods(false)})
	press(m, "S")
	out := render(m, 120, 20)
	if !strings.Contains(out, "CONTAINERS") || !strings.Contains(out, "istio-proxy") || !strings.Contains(out, "not streamed (A)") {
		t.Fatalf("selector:\n%s", out)
	}
	before := len(sessions.queries)
	// containers section: payment-service (app) then istio-proxy (sidecar)
	press(m, "tab", "space", "j", "space", "enter")
	if len(sessions.queries) != before+1 || l.containerMode != domain.ContainersAll {
		t.Fatalf("choosing a sidecar must reopen in all mode (%d queries)", len(sessions.queries)-before)
	}
	if len(l.containerScope) != 1 || !l.containerScope["istio-proxy"] {
		t.Fatalf("container scope %v", l.containerScope)
	}
	at := time.Now().Add(time.Hour)
	pod := sidecarPods(true)[0].Pod.Name
	feed(m, l, ports.LogBatch{Pods: sidecarPods(true), Entries: []domain.LogEntry{
		{Pod: pod, Container: "payment-service", Received: at, Message: "app line"},
		{Pod: pod, Container: "istio-proxy", Received: at.Add(time.Second), Message: "envoy line"},
	}})
	out = render(m, 160, 24)
	if strings.Contains(out, "app line") || !strings.Contains(out, "envoy line") || !strings.Contains(out, "5cw8s/istio-proxy") {
		t.Fatalf("only the sidecar must show, named:\n%s", out)
	}
	if !strings.Contains(out, "containers istio-proxy") {
		t.Errorf("status bar must say the container scope:\n%s", out)
	}
}

func TestStandaloneRowSaysItsKind(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	s := mockupSnapshot("rec")
	ws := domain.Workload{Ref: domain.WorkloadRef{Kind: domain.KindPod, Name: "debug-shell"}, Standalone: true, DesiredReplicas: 1, ReadyReplicas: 1}
	s.Services = append(s.Services, domain.ServiceSummary{
		Repo: "debug-shell", Unassigned: true, Workloads: 1, ReadyPods: 1, DesiredPods: 1,
		WorkloadStates: []domain.Workload{ws}, Status: domain.StatusHealthy,
	})
	snapshot(m, s)
	if out := render(m, 200, 40); !strings.Contains(out, "debug-shell (Pod)") {
		t.Fatalf("standalone row:\n%s", out)
	}
}
