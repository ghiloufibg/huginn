package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

var relT0 = time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

func dig(c string) string { return "sha256:" + strings.Repeat(c, 64) }
func sha(c string) string { return strings.Repeat(c, 40) }

// deploy is a deployment of sha(c) whose own job is job id/name.
func deploy(c string, id int64, job string, pipeline int64) ports.Deployment {
	return ports.Deployment{
		ID: id, SHA: sha(c), Ref: "main", FinishedAt: relT0.Add(-time.Duration(id) * time.Hour), User: "jdoe",
		Job: ports.Job{ID: id * 10, Name: job}, Pipeline: pipeline, Subject: "commit " + c, Author: "Jane",
	}
}

func newReleases(h *portstest.FakeReleaseHistory, g *portstest.FakeCommitLog) (*Releases, *portstest.FakeClock) {
	clock := portstest.NewFakeClock(relT0)
	if g.URL == "" && g.URLErr == nil {
		g.URL = "git@gitlab.test:payments/payment-service.git"
	}
	return &Releases{History: h, Git: g, Clock: clock, Settings: ReleaseSettings{
		ReposRoot: "/repos", Remote: "origin", PerPage: 2, MaxDeployments: 6, MaxLogBytes: 1 << 20,
		CompareRef: "origin/HEAD", Refresh: time.Minute,
		Envs: map[domain.Env]ReleaseNames{"prd": {Environment: "prd", Job: "promote-prd"}},
	}}, clock
}

func query(imgs ...domain.WorkloadImage) ports.ReleaseQuery {
	return ports.ReleaseQuery{Env: "prd", Repo: "payment-service", Images: imgs}
}

func img(workload, digest, tag string) domain.WorkloadImage {
	return domain.WorkloadImage{Workload: workload, Digest: digest, Tag: tag}
}

func TestReleasesFoundOnFirstLog(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {deploy("a", 1, "promote-prd", 100)}},
		Logs:        map[int64]string{10: "pushed " + dig("1")},
	}}
	g := &portstest.FakeCommitLog{Behind: map[string]int{sha("a"): 12}}
	r, _ := newReleases(h, g)
	got, err := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3")))
	if err != nil {
		t.Fatal(err)
	}
	res := got[dig("1")]
	if res.State != domain.ReleaseFound || res.Release.SHA != sha("a") || res.Release.Subject != "commit a" || res.Release.Pipeline != 100 || res.Release.DeployedBy != "jdoe" {
		t.Errorf("release = %+v", res)
	}
	if res.Count != (domain.TrunkCount{Known: true, Ref: "origin/HEAD", Behind: 12, OnTrunk: true}) {
		t.Errorf("count = %+v", res.Count)
	}
	if pages, lists, logs, _ := h.Calls(); pages != 1 || lists != 0 || logs != 1 {
		t.Errorf("calls: %d pages, %d job lists, %d logs; want 1, 0, 1 (the deployment's own job)", pages, lists, logs)
	}
	// A digest's release never changes: no request the second time.
	if _, err := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3"))); err != nil {
		t.Fatal(err)
	}
	if pages, _, logs, _ := h.Calls(); pages != 1 || logs != 1 {
		t.Errorf("cached lookup made requests: %d pages, %d logs", pages, logs)
	}
}

func TestReleasesFoundFurtherBackThroughPipelineJobs(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {
			deploy("e", 5, "deploy", 500), deploy("d", 4, "deploy", 400), deploy("c", 3, "deploy", 300),
		}},
		Jobs: map[int64][]ports.Job{
			500: {{ID: 51, Name: "build"}, {ID: 52, Name: "promote-prd"}},
			400: {{ID: 42, Name: "promote-prd"}},
			300: {{ID: 32, Name: "promote-prd"}},
		},
		Logs: map[int64]string{52: dig("9"), 42: dig("8"), 32: "image " + dig("1")},
	}}
	r, _ := newReleases(h, &portstest.FakeCommitLog{})
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "")))
	if res := got[dig("1")]; res.State != domain.ReleaseFound || res.Release.SHA != sha("c") {
		t.Errorf("release = %+v, want the third deployment", res)
	}
	if pages, lists, logs, jobs := h.Calls(); pages != 2 || lists != 3 || logs != 3 || !slices.Equal(jobs, []int64{52, 42, 32}) {
		t.Errorf("calls: %d pages, %d lists, %d logs %v", pages, lists, logs, jobs)
	}
}

func TestReleasesNotRecentFallsBackToTheImageTag(t *testing.T) {
	var ds []ports.Deployment
	logs := map[int64]string{}
	for i := int64(1); i <= 10; i++ {
		ds = append(ds, deploy("b", i, "promote-prd", i*100))
		logs[i*10] = dig("9")
	}
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{Deployments: map[string][]ports.Deployment{"prd": ds}, Logs: logs}}
	g := &portstest.FakeCommitLog{Commits: map[string]ports.Commit{"9f03b11c4e" + strings.Repeat("0", 30): {Subject: "fix: retry", Author: "Jane"}}}
	r, clock := newReleases(h, g)
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3-9f03b11")))
	res := got[dig("1")]
	if !res.NotRecent || res.State != domain.ReleaseGuessed || res.Guess != "image tag" || res.Release.Subject != "fix: retry" || res.Problem != nil {
		t.Errorf("release = %+v", res)
	}
	if _, _, logsRead, _ := h.Calls(); logsRead != 6 {
		t.Errorf("read %d logs, want max_deployments = 6", logsRead)
	}
	// "Not recent" is re-checked only after Refresh.
	_, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3-9f03b11")))
	if _, _, logsRead, _ := h.Calls(); logsRead != 6 {
		t.Errorf("re-searched before Refresh: %d logs", logsRead)
	}
	clock.Advance(time.Minute)
	_, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3-9f03b11")))
	if _, _, logsRead, _ := h.Calls(); logsRead != 12 {
		t.Errorf("not re-searched after Refresh: %d logs", logsRead)
	}
}

func TestReleasesGitTagFallback(t *testing.T) {
	h := &portstest.FakeReleaseHistory{}
	g := &portstest.FakeCommitLog{
		Commits: map[string]ports.Commit{sha("c"): {Subject: "release 2.14.3"}},
		Tags:    map[string]string{"v2.14.3": sha("c")},
	}
	r, _ := newReleases(h, g)
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3")))
	if res := got[dig("1")]; res.State != domain.ReleaseGuessed || res.Guess != "git tag v2.14.3" || res.Release.SHA != sha("c") || !res.NotRecent {
		t.Errorf("release = %+v", res)
	}
}

func TestReleasesUnconfirmedTagIsNotShown(t *testing.T) {
	r, _ := newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{})
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "1.0.10-deadbee")))
	if res := got[dig("1")]; res.State != domain.ReleaseUnknown || res.Release.SHA != "" {
		t.Errorf("release = %+v, want unknown: git does not know deadbee", res)
	}
}

func TestReleasesMonorepo(t *testing.T) {
	// Deployments of the same environment mix services; one pipeline
	// promotes two of them with jobs named after each.
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {
			deploy("x", 1, "promote-search-prd", 100),
			deploy("y", 2, "promote-search-prd", 200),
			deploy("a", 3, "promote-payment-prd", 300),
			deploy("a", 4, "promote-ledger-prd", 300),
		}},
		Logs: map[int64]string{30: dig("1"), 40: dig("2")},
	}}
	g := &portstest.FakeCommitLog{URL: "https://gitlab.test/platform/mono.git"}
	r, _ := newReleases(h, g)
	r.Settings.Envs["prd"] = ReleaseNames{Environment: "prd", Job: "promote-{workload}-prd"}
	r.Settings.Repos = map[string]ReleaseRepo{"payment-service": {Workloads: map[string]ReleaseWorkload{
		"payment": {Paths: []string{"services/payment/"}},
	}}}
	got, err := r.Running(context.Background(), query(img("payment", dig("1"), ""), img("ledger", dig("2"), "")))
	if err != nil {
		t.Fatal(err)
	}
	if got[dig("1")].Release.SHA != sha("a") || got[dig("2")].Release.SHA != sha("a") {
		t.Errorf("releases = %+v", got)
	}
	if _, _, _, jobs := h.Calls(); !slices.Equal(jobs, []int64{30, 40}) {
		t.Errorf("logs read %v: the other services' deployments must be skipped without reading their log", jobs)
	}
	if !got[dig("1")].Count.Scoped || got[dig("2")].Count.Scoped || !got[dig("2")].Count.WholeProject {
		t.Errorf("counts = %+v / %+v: only payment has paths", got[dig("1")].Count, got[dig("2")].Count)
	}
	if _, trees, counted := g.Refreshes(); !trees || !slices.ContainsFunc(counted, func(p []string) bool { return slices.Equal(p, []string{"services/payment/"}) }) {
		t.Errorf("trees %v, counted paths %v", trees, counted)
	}
}

func TestReleasesOneLogPassForSeveralDigests(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {deploy("a", 1, "promote-prd", 100)}},
		Logs:        map[int64]string{10: dig("1") + "\n" + dig("2")},
	}}
	r, _ := newReleases(h, &portstest.FakeCommitLog{})
	got, _ := r.Running(context.Background(), query(img("api", dig("1"), ""), img("worker", dig("2"), ""), img("api", dig("1"), "")))
	if len(got) != 2 || got[dig("2")].Release.SHA != sha("a") {
		t.Errorf("releases = %+v", got)
	}
	if _, _, logs, _ := h.Calls(); logs != 1 {
		t.Errorf("read %d logs, want 1", logs)
	}
}

func TestReleasesProblems(t *testing.T) {
	refused := domain.KindError(domain.ErrUnauthorized, "GitLab refused the token")
	h := &portstest.FakeReleaseHistory{Err: refused}
	g := &portstest.FakeCommitLog{Commits: map[string]ports.Commit{"9f03b11" + strings.Repeat("0", 33): {Subject: "s"}}}
	r, _ := newReleases(h, g)
	got, err := r.Running(context.Background(), query(img("payment-service", dig("1"), "9f03b11")))
	if err != nil {
		t.Fatal(err)
	}
	res := got[dig("1")]
	if !errors.Is(res.Problem, domain.ErrUnauthorized) || res.NotRecent || res.State != domain.ReleaseGuessed {
		t.Errorf("release = %+v: the problem is shown, not 'not recent', and the guess is kept", res)
	}

	r, _ = newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{URLErr: domain.ErrNotFound})
	got, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "")))
	if p := got[dig("1")].Problem; p == nil || !strings.Contains(p.Error(), "no clone") {
		t.Errorf("problem = %v", p)
	}

	r, _ = newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{URL: "/srv/git/p.git"})
	got, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "")))
	if p := got[dig("1")].Problem; !errors.Is(p, domain.ErrConfig) {
		t.Errorf("problem = %v", p)
	}
}

func TestReleasesEnvironmentNotSearched(t *testing.T) {
	h := &portstest.FakeReleaseHistory{}
	r, _ := newReleases(h, &portstest.FakeCommitLog{})
	got, _ := r.Running(context.Background(), ports.ReleaseQuery{Env: "rec", Repo: "payment-service", Images: []domain.WorkloadImage{img("api", dig("1"), "")}})
	if res := got[dig("1")]; res.NotRecent || res.Problem != nil {
		t.Errorf("release = %+v", res)
	}
	if pages, _, _, _ := h.Calls(); pages != 0 {
		t.Errorf("searched an environment without names")
	}
}

func TestReleasesSettings(t *testing.T) {
	g := &portstest.FakeCommitLog{Behind: map[string]int{sha("a"): 3}, Off: map[string]bool{sha("a"): true}, Stale: true}
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd/payment": {deploy("a", 1, "release-payment", 100)}},
		Logs:        map[int64]string{10: dig("1")},
	}}
	r, clock := newReleases(h, g)
	r.Settings.Repos = map[string]ReleaseRepo{"payment-service": {
		CompareRef: "origin/release",
		Envs:       map[domain.Env]ReleaseNames{"prd": {Environment: "prd/{repo}"}},
		Workloads:  map[string]ReleaseWorkload{"payment": {Envs: map[domain.Env]ReleaseNames{"prd": {Environment: "prd/payment", Job: "release-{workload}"}}}},
	}}
	got, _ := r.Running(context.Background(), query(img("payment", dig("1"), "")))
	res := got[dig("1")]
	if res.State != domain.ReleaseFound || res.Count != (domain.TrunkCount{Known: true, Ref: "origin/release", Behind: 3, OnTrunk: false, Stale: true}) {
		t.Errorf("release = %+v", res)
	}
	if n, _, _ := g.Refreshes(); n != 1 {
		t.Errorf("refreshes = %d", n)
	}
	_, _ = r.Running(context.Background(), query(img("payment", dig("1"), "")))
	if n, _, _ := g.Refreshes(); n != 1 {
		t.Errorf("refreshed again before Refresh: %d", n)
	}
	clock.Advance(time.Minute)
	_, _ = r.Running(context.Background(), query(img("payment", dig("1"), "")))
	if n, _, _ := g.Refreshes(); n != 2 {
		t.Errorf("not refreshed after Refresh: %d", n)
	}
}

func TestReleasesSkipsImagesWithoutDigest(t *testing.T) {
	r, _ := newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{})
	got, err := r.Running(context.Background(), query(img("api", "", "1.0")))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}
