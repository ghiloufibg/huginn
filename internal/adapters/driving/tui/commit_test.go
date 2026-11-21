package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func digest(c string) string { return "sha256:" + strings.Repeat(c, 64) }

// fakeReleases answers with fixed results and records the queries.
type fakeReleases struct {
	mu      sync.Mutex
	res     map[string]domain.RunningRelease
	err     error
	queries []ports.ReleaseQuery
}

func (f *fakeReleases) Running(_ context.Context, q ports.ReleaseQuery) (map[string]domain.RunningRelease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.res, f.err
}

// imgPod is a pod of a workload of the mockup running tag at digest.
func imgPod(owner, name, rev, tag, dg string, age time.Duration) domain.Pod {
	p := revPod(owner, name, rev, age)
	p.Containers[0].Image = "eu.gcr.io/acme/" + owner + ":" + tag
	if dg != "" {
		p.Containers[0].ImageID = "eu.gcr.io/acme/" + owner + "@" + dg
	}
	return p
}

func found(sha, subject string, behind int) domain.RunningRelease {
	return domain.RunningRelease{
		State:   domain.ReleaseFound,
		Release: domain.Release{SHA: sha, Subject: subject, DeployedBy: "jdoe", Pipeline: 48211},
		Count:   domain.TrunkCount{Known: true, Ref: "origin/HEAD", Behind: behind, OnTrunk: true},
	}
}

func TestCommitText(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	th := &m.opts.Theme
	cur := domain.WorkloadImage{Workload: "api", Digest: digest("1"), Tag: "2.14.3"}
	prev := domain.WorkloadImage{Workload: "api", Digest: digest("2"), Tag: "2.14.2"}
	sha, old := strings.Repeat("9f03b11c", 5), strings.Repeat("4c1a77e0", 5)
	for _, tt := range []struct {
		name      string
		cur, prev domain.WorkloadImage
		res       map[string]domain.RunningRelease
		want      string
	}{
		{
			"found", cur,
			domain.WorkloadImage{},
			map[string]domain.RunningRelease{digest("1"): found(sha, "fix(payment): retry", 12)},
			"9f03b11c  fix(payment): retry  · jdoe, pipeline #48211  · 12 not deployed on origin/HEAD",
		},
		{
			"up to date", cur,
			domain.WorkloadImage{},
			map[string]domain.RunningRelease{digest("1"): found(sha, "s", 0)},
			"9f03b11c  s  · jdoe, pipeline #48211  · up to date with origin/HEAD",
		},
		{"not on the trunk", cur, domain.WorkloadImage{}, map[string]domain.RunningRelease{digest("1"): func() domain.RunningRelease {
			r := found(sha, "hotfix", 2)
			r.Count.OnTrunk = false
			return r
		}()}, "9f03b11c  hotfix  · jdoe, pipeline #48211  · not on origin/HEAD"},
		{"a monorepo workload without paths", cur, domain.WorkloadImage{}, map[string]domain.RunningRelease{digest("1"): func() domain.RunningRelease {
			r := found(sha, "s", 7)
			r.Count.WholeProject = true
			return r
		}()}, "9f03b11c  s  · jdoe, pipeline #48211  · 7 commits on origin/HEAD since"},
		{"a count that is a lower bound", cur, domain.WorkloadImage{}, map[string]domain.RunningRelease{digest("1"): func() domain.RunningRelease {
			r := found(sha, "s", 1000)
			r.Count.Capped = true
			return r
		}()}, "9f03b11c  s  · jdoe, pipeline #48211  · 1000+ not deployed on origin/HEAD"},
		{
			"a rollout", cur, prev,
			map[string]domain.RunningRelease{digest("1"): found(sha, "new", 0), digest("2"): found(old, "old", 3)},
			"4c1a77e0→9f03b11c  new  · jdoe, pipeline #48211  · up to date with origin/HEAD",
		},
		{"not recent, guessed from the tag", cur, domain.WorkloadImage{}, map[string]domain.RunningRelease{digest("1"): {
			State: domain.ReleaseGuessed, NotRecent: true, Guess: "image tag", Release: domain.Release{SHA: sha, Subject: "s"},
		}}, "not a recent prd release  · ≈ 9f03b11c  s  · from the image tag"},
		{
			"not recent, unknown", cur,
			domain.WorkloadImage{},
			map[string]domain.RunningRelease{digest("1"): {NotRecent: true}},
			"not a recent prd release  · commit unknown (tag 2.14.3)",
		},
		{
			"a problem", cur,
			domain.WorkloadImage{},
			map[string]domain.RunningRelease{digest("1"): {Problem: errors.New("no GitLab token (.env GITLAB_TOKEN)")}},
			"no GitLab token (.env GITLAB_TOKEN)",
		},
		{"a problem and a guess", cur, domain.WorkloadImage{}, map[string]domain.RunningRelease{digest("1"): {
			State: domain.ReleaseGuessed, Guess: "git tag v2.14.3", Release: domain.Release{SHA: sha, Subject: "s"}, Problem: errors.New("GitLab unreachable"),
		}}, "≈ 9f03b11c  s  · from the git tag v2.14.3  · GitLab unreachable"},
		{"not pulled yet", domain.WorkloadImage{Tag: "1"}, domain.WorkloadImage{}, nil, "waiting for the image"},
		{
			"control characters in a subject", cur,
			domain.WorkloadImage{},
			map[string]domain.RunningRelease{digest("1"): found(sha, "evil\x1b[2Jsubject", 0)},
			"9f03b11c  evilsubject  · jdoe, pipeline #48211  · up to date with origin/HEAD",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ansi.Strip(commitText(th, "prd", tt.cur, tt.prev, tt.res)); got != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

// restReleases delivers the rest of the COMMIT lookup.
func restReleases(m *Model) {
	r := &m.stack[0].(*servicesScreen).preview.rel
	_, cmd := m.Update(releasesRestMsg{key: r.want, seq: r.seq})
	run(m, cmd)
}

func commitPreview(t *testing.T, w, h int, rel ports.Releases, ws []domain.Workload, pods []domain.Pod) (string, *Model) {
	t.Helper()
	m, _ := newTestModel(t, 1, "")
	m.opts.Releases = rel
	snap := mockupSnapshot("rec")
	for i := range snap.Services {
		if snap.Services[i].Repo == "catalog-indexer" {
			snap.Services[i].WorkloadStates, snap.Services[i].Pods = ws, pods
			snap.Services[i].Workloads = len(ws)
		}
	}
	snapshot(m, snap)
	rest(m)
	return ansi.Strip(render(m, w, h)), m
}

func TestPreviewCommit(t *testing.T) {
	ws := []domain.Workload{
		workload("catalog-indexer", func(w *domain.Workload) { w.RolloutDone = t0.Add(-3 * time.Hour) }),
		workload("catalog-worker", func(w *domain.Workload) { w.RolloutDone = t0.Add(-3 * time.Hour) }),
	}
	pods := []domain.Pod{
		imgPod("catalog-indexer", "catalog-indexer-a", "h1", "1.3.7-9f03b11", digest("1"), 3*time.Hour),
		imgPod("catalog-worker", "catalog-worker-a", "w1", "1.3.7-9f03b11", digest("2"), 3*time.Hour),
	}
	sha := "9f03b11c4e2a" + strings.Repeat("0", 28)
	rel := &fakeReleases{res: map[string]domain.RunningRelease{
		digest("1"): found(sha, "fix(catalog): close the pool on shutdown", 12),
		digest("2"): found(sha, "fix(catalog): close the pool on shutdown", 12),
	}}
	// Before the cursor rests, nothing is asked.
	out, model := commitPreview(t, 160, 34, rel, ws, pods)
	if len(rel.queries) != 0 || !strings.Contains(out, "COMMIT     searching GitLab releases…") {
		t.Fatalf("queries %d:\n%s", len(rel.queries), out)
	}
	restReleases(model)
	if q := rel.queries; len(q) != 1 || q[0].Repo != "catalog-indexer" || q[0].Env != "rec" || len(q[0].Images) != 2 {
		t.Fatalf("queries = %+v", q)
	}
	out = ansi.Strip(render(model, 160, 34))
	// Both workloads run the same commit: one line.
	want := "COMMIT     9f03b11c  fix(catalog): close the pool on shutdown  · jdoe, pipeline #48211  · 12 not deployed on origin/HEAD"
	if !strings.Contains(out, want) || strings.Contains(out, "catalog-worker   9f03b11c") {
		t.Fatalf("preview lacks %q:\n%s", want, out)
	}
	golden(t, "services_preview_commit_160x34", out)

	// Within the TTL, resting again asks nothing.
	restReleases(model)
	if len(rel.queries) != 1 {
		t.Errorf("asked again within the TTL: %d", len(rel.queries))
	}
}

func TestPreviewCommitPerWorkload(t *testing.T) {
	ws := []domain.Workload{workload("catalog-indexer"), workload("catalog-worker")}
	pods := []domain.Pod{
		imgPod("catalog-indexer", "catalog-indexer-a", "h1", "1.3.7", digest("1"), 3*time.Hour),
		imgPod("catalog-worker", "catalog-worker-a", "w1", "0.9", digest("2"), 30*time.Hour),
	}
	rel := &fakeReleases{res: map[string]domain.RunningRelease{
		digest("1"): found(strings.Repeat("a", 40), "feat: new", 0),
		digest("2"): {NotRecent: true},
	}}
	_, m := commitPreview(t, 160, 34, rel, ws, pods)
	restReleases(m)
	out := ansi.Strip(render(m, 160, 34))
	for _, want := range []string{
		"COMMIT     catalog-indexer  aaaaaaaa  feat: new",
		"           catalog-worker   not a recent rec release  · commit unknown (tag 0.9)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview lacks %q:\n%s", want, out)
		}
	}
	side := ansi.Strip(render(m, 220, 40))
	if !strings.Contains(side, "COMMIT") || !strings.Contains(side, "catalog-indexer  aaaaaaaa") {
		t.Errorf("side panel:\n%s", side)
	}
}

func TestPreviewCommitHiddenWithoutReleases(t *testing.T) {
	ws := []domain.Workload{workload("catalog-indexer")}
	pods := []domain.Pod{imgPod("catalog-indexer", "catalog-indexer-a", "h1", "1", digest("1"), time.Hour)}
	out, _ := commitPreview(t, 160, 34, nil, ws, pods)
	if strings.Contains(out, "COMMIT") {
		t.Errorf("COMMIT shown without releases.yaml:\n%s", out)
	}
	// An error of the whole lookup is one dim line.
	rel := &fakeReleases{err: errors.New("context deadline exceeded")}
	_, m := commitPreview(t, 160, 34, rel, ws, pods)
	restReleases(m)
	if out := ansi.Strip(render(m, 160, 34)); !strings.Contains(out, "COMMIT     context deadline exceeded") {
		t.Errorf("preview:\n%s", out)
	}
}

func TestNoticeInStatusBar(t *testing.T) {
	m, _ := newTestModel(t, 1, "")
	o := m.opts
	o.Notice = ".env is readable by others (chmod 600 .env)"
	if !strings.Contains(ansi.Strip(render(NewModel(o), 160, 30)), ".env is readable by others") {
		t.Error("the notice is not in the status bar")
	}
}

// BenchmarkPreviewCommitFrame draws the services screen with the preview of
// a service of 3 workloads and 2 000 pods, with and without the COMMIT line.
func BenchmarkPreviewCommitFrame(b *testing.B) {
	ws := []domain.Workload{workload("catalog-indexer"), workload("catalog-worker"), workload("catalog-cleanup")}
	var pods []domain.Pod
	for i := range 2000 {
		owner := ws[i%3].Ref.Name
		pods = append(pods, imgPod(owner, fmt.Sprintf("%s-%d", owner, i), "h1", "1.3.7", digest("1"), time.Duration(i)*time.Minute))
	}
	sha := strings.Repeat("a", 40)
	for _, withCommit := range []bool{false, true} {
		b.Run(fmt.Sprintf("commit=%v", withCommit), func(b *testing.B) {
			m, _ := newTestModel(b, 1, "")
			if withCommit {
				m.opts.Releases = &fakeReleases{res: map[string]domain.RunningRelease{digest("1"): found(sha, "s", 1)}}
			}
			snap := mockupSnapshot("rec")
			for i := range snap.Services {
				if snap.Services[i].Repo == "catalog-indexer" {
					snap.Services[i].WorkloadStates, snap.Services[i].Pods = ws, pods
				}
			}
			snapshot(m, snap)
			if withCommit {
				restReleases(m)
			}
			render(m, 160, 40)
			b.ReportAllocs()
			for b.Loop() {
				m.Update(clockMsg{})
				_ = m.View()
			}
		})
	}
}
