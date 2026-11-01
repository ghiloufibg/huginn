package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

func TestDeployText(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	th := &m.opts.Theme
	cet := time.FixedZone("CET", 3600)
	at := time.Date(2026, 9, 26, 15, 32, 0, 0, time.UTC) // t0 is 19:14:02 UTC the same day

	for _, tt := range []struct {
		name string
		info domain.DeployInfo
		loc  *time.Location
		now  time.Time
		want string
	}{
		{"controller record", domain.DeployInfo{At: at, Source: domain.DeployController}, time.UTC, t0, "Sep 26 15:32  3h ago"},
		{"in the user's zone", domain.DeployInfo{At: at, Source: domain.DeployController}, cet, t0, "Sep 26 16:32  3h ago"},
		{"a day change in that zone", domain.DeployInfo{At: time.Date(2026, 9, 26, 23, 30, 0, 0, time.UTC), Source: domain.DeployController}, cet, t0.Add(8 * time.Hour), "Sep 27 00:30  3h ago"},
		{"pods only says so", domain.DeployInfo{At: at, Source: domain.DeployPods}, time.UTC, t0, "pods since Sep 26 15:32  3h ago"},
		{"days", domain.DeployInfo{At: at.Add(-5 * 24 * time.Hour), Source: domain.DeployController}, time.UTC, t0, "Sep 21 15:32  5d ago"},
		{"another year shows the year", domain.DeployInfo{At: time.Date(2025, 12, 31, 23, 59, 0, 0, time.UTC), Source: domain.DeployController}, time.UTC, t0, "Dec 31 2025 23:59  268d ago"},
		{"seconds", domain.DeployInfo{At: t0.Add(-40 * time.Second), Source: domain.DeployController}, time.UTC, t0, "Sep 26 19:13  40s ago"},
		{"the cluster's clock is ahead: never negative", domain.DeployInfo{At: t0.Add(2 * time.Minute), Source: domain.DeployController}, time.UTC, t0, "Sep 26 19:16  just now"},
		{"in progress", domain.DeployInfo{At: at, Source: domain.DeployPods, InProgress: true, Updated: 1, Desired: 3}, time.UTC, t0, "rolling out since Sep 26 15:32  3h ago  1/3 updated"},
		{"in progress, no new pod", domain.DeployInfo{InProgress: true, Desired: 3}, time.UTC, t0, "rolling out, no new pod yet"},
		{"no record", domain.DeployInfo{}, time.UTC, t0, "no rollout record"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ansi.Strip(deployText(th, tt.info, tt.loc, tt.now)); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// revPod is a pod of a Deployment of the mockup, made from a template revision.
func revPod(owner, name, rev string, age time.Duration) domain.Pod {
	return domain.Pod{
		Namespace: "app-rec", Name: name, OwnerName: owner, OwnerKind: "Deployment", Revision: rev,
		Phase: domain.PodRunning, Created: t0.Add(-age),
		Containers: []domain.Container{{Name: "app", Image: "eu.gcr.io/acme/" + owner + ":1", State: domain.ContainerRunning, Ready: true}},
	}
}

// previewOf selects catalog-indexer, the first row, replaces its workloads
// and pods, and returns the preview as text.
func previewOf(t *testing.T, w, h int, ws []domain.Workload, pods []domain.Pod) string {
	t.Helper()
	m, _ := newTestModel(t, 1, "")
	snap := mockupSnapshot("rec")
	for i := range snap.Services {
		if snap.Services[i].Repo == "catalog-indexer" {
			snap.Services[i].WorkloadStates, snap.Services[i].Pods = ws, pods
			snap.Services[i].Workloads = len(ws)
		}
	}
	snapshot(m, snap)
	rest(m)
	return ansi.Strip(render(m, w, h))
}

func workload(name string, mods ...func(*domain.Workload)) domain.Workload {
	w := domain.Workload{
		Ref:             domain.WorkloadRef{Env: "rec", Namespace: "app-rec", Kind: domain.KindDeployment, Name: name},
		DesiredReplicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2,
	}
	for _, m := range mods {
		m(&w)
	}
	return w
}

func TestPreviewDeployedPerWorkload(t *testing.T) {
	ws := []domain.Workload{
		workload("catalog-indexer", func(w *domain.Workload) { w.RolloutDone = t0.Add(-3 * time.Hour) }),
		workload("catalog-worker", func(w *domain.Workload) { w.Progressing, w.UpdatedReplicas = true, 1 }),
		workload("catalog-cleanup", func(w *domain.Workload) { w.Ref.Kind = domain.KindStatefulSet }),
		workload("catalog-report", func(w *domain.Workload) { w.Ref.Kind = domain.KindCronJob }),
	}
	pods := []domain.Pod{
		revPod("catalog-indexer", "catalog-indexer-a", "h1", 30*24*time.Hour), // older than the record: the record wins
		revPod("catalog-worker", "catalog-worker-old", "w1", 48*time.Hour),
		revPod("catalog-worker", "catalog-worker-new", "w2", 5*time.Minute),
		func() domain.Pod {
			p := revPod("catalog-cleanup", "catalog-cleanup-0", "c1", 26*time.Hour)
			p.OwnerKind = "StatefulSet"
			return p
		}(),
	}
	out := previewOf(t, 160, 34, ws, pods)
	for _, want := range []string{
		"DEPLOYED   catalog-indexer  Sep 26 16:14  3h ago",
		"           catalog-worker   rolling out since Sep 26 19:09  5m ago  1/2 updated",
		"           catalog-cleanup  pods since Sep 25 17:14  26h ago",
		"           catalog-report   no rollout record",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	golden(t, "services_preview_deployed_160x34", out)
}

func TestPreviewDeployedSideAndSqueezed(t *testing.T) {
	ws := []domain.Workload{
		workload("catalog-indexer", func(w *domain.Workload) { w.RolloutDone = t0.Add(-3 * time.Hour) }),
		workload("catalog-worker", func(w *domain.Workload) { w.RolloutDone = t0.Add(-26 * time.Hour) }),
		workload("catalog-cleanup", func(w *domain.Workload) { w.RolloutDone = t0.Add(-100 * time.Hour) }),
	}
	// The side panel (220 columns) puts the label on its own line.
	side := previewOf(t, 220, 40, ws, nil)
	if !strings.Contains(side, "DEPLOYED") || !strings.Contains(side, "catalog-cleanup  Sep 22") {
		t.Fatalf("side panel:\n%s", side)
	}
	golden(t, "services_preview_deployed_side_220x40", side)

	// With little room every section keeps a line and the rest folds into "+n more".
	for _, h := range []int{14, 18, 24} {
		out := previewOf(t, 120, h, ws, nil)
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("height %d: %d lines", h, len(lines))
		}
		for i, l := range lines {
			if w := ansi.StringWidth(l); w != 120 {
				t.Fatalf("height %d, line %d is %d cells: %q", h, i, w, l)
			}
		}
	}
}

func TestPreviewDeployedTimeZone(t *testing.T) {
	ws := []domain.Workload{workload("catalog-indexer", func(w *domain.Workload) { w.RolloutDone = t0.Add(-3 * time.Hour) })}
	m, _ := newTestModel(t, 1, "")
	m.opts.Location = time.FixedZone("JST", 9*3600)
	snap := mockupSnapshot("rec")
	for i := range snap.Services {
		if snap.Services[i].Repo == "catalog-indexer" {
			snap.Services[i].WorkloadStates = ws
		}
	}
	snapshot(m, snap)
	rest(m)
	if out := ansi.Strip(render(m, 160, 34)); !strings.Contains(out, "Sep 27 01:14  3h ago") {
		t.Fatalf("19:14 UTC minus 3h is 16:14 UTC, 01:14 the next day in JST:\n%s", out)
	}
}

// Scaling a Deployment makes the watched Progressing flag true for the seconds
// the controller's counts lag; the preview must keep showing when it was
// deployed, not "rolling out since" the age of the old pods.
func TestPreviewDeployedIsNotARolloutWhileScaling(t *testing.T) {
	ws := []domain.Workload{workload("catalog-indexer", func(w *domain.Workload) {
		w.RolloutDone = t0.Add(-3 * time.Hour)
		w.Progressing, w.DesiredReplicas, w.UpdatedReplicas = true, 3, 2 // scaled 2 -> 3, the third pod not counted yet
	})}
	pods := []domain.Pod{revPod("catalog-indexer", "catalog-indexer-a", "h1", 4*time.Hour), revPod("catalog-indexer", "catalog-indexer-b", "h1", 4*time.Hour)}
	out := previewOf(t, 160, 34, ws, pods)
	if !strings.Contains(out, "DEPLOYED   catalog-indexer  Sep 26 16:14  3h ago") {
		t.Fatalf("a scale-up must keep the deployment time:\n%s", out)
	}
	if strings.Contains(out, "rolling out") {
		t.Fatalf("a scale-up is not a rollout:\n%s", out)
	}
}
