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

// remotes answers GitRemote with a fixed URL or error, and records the
// folders asked for.
type remotes struct {
	url  string
	err  error
	dirs []string
}

func (f *remotes) GitRemote(_ context.Context, dir, _ string) (string, error) {
	f.dirs = append(f.dirs, dir)
	return f.url, f.err
}

func newReleases(h *portstest.FakeReleaseHistory, g *portstest.FakeCommitLog) (*Releases, *portstest.FakeClock) {
	clock := portstest.NewFakeClock(relT0)
	rm := &remotes{url: "git@gitlab.test:payments/payment-service.git"}
	return &Releases{History: h, Commits: g, Remotes: rm, Clock: clock, Settings: ReleaseSettings{
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

// mergeHistory is a deployment of sha("a"), a merge of branch sha("c") into
// trunk sha("b"), whose own message is the host's generic one.
func mergeHistory() (*portstest.FakeReleaseHistory, *portstest.FakeCommitLog) {
	d := deploy("a", 1, "promote-prd", 100)
	d.Subject, d.Author = "Merge branch 'feature/x' into 'main'", "Merger"
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {d}},
		Logs:        map[int64]string{10: dig("1")},
	}}
	g := &portstest.FakeCommitLog{
		Commits: map[string]ports.Commit{sha("a"): {Subject: d.Subject, Author: "Merger", Parents: []string{sha("b"), sha("c")}}},
		Merged:  map[string]ports.Commit{sha("c"): {SHA: sha("d"), Subject: "fix(payment): retry the callback", Author: "Jane"}},
	}
	return h, g
}

func TestReleasesShowTheRealCommitOfAMerge(t *testing.T) {
	h, g := mergeHistory()
	r, _ := newReleases(h, g)
	got, err := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3")))
	if err != nil {
		t.Fatal(err)
	}
	rel := got[dig("1")].Release
	// The merge is what runs (its SHA is counted from); only the text is the change's.
	if rel.SHA != sha("a") || rel.Subject != "fix(payment): retry the callback" || rel.Author != "Jane" {
		t.Errorf("release = %+v", rel)
	}
	// Commits never change: the second lookup asks nothing.
	before, _ := g.Calls()
	r.mu.Lock()
	r.found = nil
	r.mu.Unlock()
	if _, err := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3"))); err != nil {
		t.Fatal(err)
	}
	after, _ := g.Calls()
	if after["commit"] != before["commit"] || after["introduced"] != before["introduced"] {
		t.Errorf("asked again: before %v, after %v", before, after)
	}
}

func TestReleasesKeepTheMergeMessageWhenNothingElseIsKnown(t *testing.T) {
	h, g := mergeHistory()
	g.Merged = nil // only merges, or the call fails
	r, _ := newReleases(h, g)
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3")))
	if rel := got[dig("1")].Release; rel.Subject != "Merge branch 'feature/x' into 'main'" || rel.Author != "Jane" && rel.Author != "Merger" {
		t.Errorf("release = %+v", rel)
	}
}

func TestReleasesLeaveAPlainCommitAlone(t *testing.T) {
	h, g := mergeHistory()
	g.Commits[sha("a")] = ports.Commit{Subject: "squashed", Author: "Merger", Parents: []string{sha("b")}}
	r, _ := newReleases(h, g)
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3")))
	if rel := got[dig("1")].Release; rel.Subject != "Merge branch 'feature/x' into 'main'" {
		t.Errorf("a one-parent commit was replaced: %+v", rel)
	}
	if calls, _ := g.Calls(); calls["introduced"] != 0 {
		t.Errorf("Introduced asked for a commit that is not a merge: %v", calls)
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
	for i := int64(10); i >= 1; i-- { // newest first, as GitLab lists them
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
	// After Refresh, only the deployments made since are read: here none,
	// so one list request and no log.
	clock.Advance(time.Minute)
	_, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3-9f03b11")))
	if pages, _, logsRead, _ := h.Calls(); logsRead != 6 || pages != 4 {
		t.Errorf("re-check read %d logs and %d pages in all, want 6 and 4 (3 + 1)", logsRead, pages)
	}
	// A new deployment of that image: its log alone is read, and found.
	h.Fixture.Deployments["prd"] = append([]ports.Deployment{deploy("c", 11, "promote-prd", 1100)}, ds...)
	h.Fixture.Logs[110] = dig("1")
	clock.Advance(time.Minute)
	got, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3-9f03b11")))
	if res := got[dig("1")]; res.State != domain.ReleaseFound || res.Release.SHA != sha("c") {
		t.Errorf("release = %+v", res)
	}
	if _, _, logsRead, jobs := h.Calls(); logsRead != 7 || jobs[6] != 110 {
		t.Errorf("read %d logs %v, want only the new one", logsRead, jobs)
	}
}

func TestReleasesNeverReadsALogTwiceForTheSameDigest(t *testing.T) {
	// Two digests searched one after the other in the same deployments:
	// each log is read once per digest, and a pipeline's jobs listed once.
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {deploy("b", 2, "deploy", 200), deploy("a", 1, "deploy", 100)}},
		Jobs:        map[int64][]ports.Job{200: {{ID: 21, Name: "promote-prd"}}, 100: {{ID: 11, Name: "promote-prd"}}},
		Logs:        map[int64]string{21: dig("2"), 11: dig("1")},
	}}
	r, clock := newReleases(h, &portstest.FakeCommitLog{})
	_, _ = r.Running(context.Background(), query(img("api", dig("1"), "")))
	_, lists, logs, _ := h.Calls()
	if lists != 2 || logs != 2 {
		t.Fatalf("first search: %d job lists, %d logs", lists, logs)
	}
	clock.Advance(time.Minute)
	got, _ := r.Running(context.Background(), query(img("api", dig("1"), ""), img("worker", dig("2"), "")))
	if got[dig("2")].Release.SHA != sha("b") {
		t.Errorf("release = %+v", got[dig("2")])
	}
	if _, lists, logs, _ := h.Calls(); lists != 2 || logs != 3 {
		t.Errorf("in all %d job lists and %d logs, want 2 and 3: pipeline jobs are listed once", lists, logs)
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
	g := &portstest.FakeCommitLog{}
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
	if _, counted := g.Calls(); !slices.ContainsFunc(counted, func(p []string) bool { return slices.Equal(p, []string{"services/payment/"}) }) {
		t.Errorf("counted paths %v", counted)
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

	r, _ = newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{})
	r.Remotes = &remotes{err: domain.ErrNotFound}
	got, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "")))
	if p := got[dig("1")].Problem; p == nil || !strings.Contains(p.Error(), "no clone") {
		t.Errorf("problem = %v", p)
	}

	r, _ = newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{})
	r.Remotes = &remotes{url: "/srv/git/p.git"}
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
	g := &portstest.FakeCommitLog{Behind: map[string]int{sha("a"): 3}, Off: map[string]bool{sha("a"): true}, Capped: true}
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
	if res.State != domain.ReleaseFound || res.Count != (domain.TrunkCount{Known: true, Ref: "origin/release", Behind: 3, OnTrunk: false, Capped: true}) {
		t.Errorf("release = %+v", res)
	}
	// "origin/release" is the branch "release": no default-branch request.
	if calls, _ := g.Calls(); calls["default"] != 0 || calls["count"] != 1 || calls["tip"] != 1 {
		t.Errorf("calls = %v", calls)
	}
	// The trunk did not move: the count is reused, only its tip is read.
	clock.Advance(time.Minute)
	_, _ = r.Running(context.Background(), query(img("payment", dig("1"), "")))
	if calls, _ := g.Calls(); calls["count"] != 1 || calls["tip"] != 2 {
		t.Errorf("calls = %v, want the count reused", calls)
	}
	// The trunk moved: counted again.
	g.SetTip("release", "t2")
	_, _ = r.Running(context.Background(), query(img("payment", dig("1"), "")))
	if calls, _ := g.Calls(); calls["count"] != 2 {
		t.Errorf("calls = %v, want a new count after the trunk moved", calls)
	}
}

func TestReleasesTrunkAndHistoryCaches(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {deploy("a", 1, "promote-prd", 100)}},
		Logs:        map[int64]string{10: dig("1")},
	}}
	g := &portstest.FakeCommitLog{Default: "trunk", Tags: map[string]string{}}
	r, clock := newReleases(h, g)
	q := query(img("payment-service", dig("1"), ""), img("worker", dig("2"), "1.0.0"))
	_, _ = r.Running(context.Background(), q)
	calls, _ := g.Calls()
	if calls["default"] != 1 || calls["tip"] != 1 {
		t.Errorf("first lookup calls = %v: default branch once, the tip once per lookup", calls)
	}
	tags := calls["tag"]
	// Within the hour the default branch is reused; an unknown tag is not
	// asked again before Refresh.
	clock.Advance(30 * time.Second)
	_, _ = r.Running(context.Background(), q)
	if calls, _ := g.Calls(); calls["default"] != 1 || calls["tag"] != tags {
		t.Errorf("calls = %v, want the default branch and the unknown tags reused", calls)
	}
	clock.Advance(time.Hour)
	_, _ = r.Running(context.Background(), q)
	if calls, _ := g.Calls(); calls["default"] != 2 || calls["tag"] == tags {
		t.Errorf("calls = %v, want the default branch and the tags asked again", calls)
	}
}

func TestReleasesNoCountWhenTheTrunkCannotBeRead(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {deploy("a", 1, "promote-prd", 100)}},
		Logs:        map[int64]string{10: dig("1")},
	}}
	g := &portstest.FakeCommitLog{}
	r, _ := newReleases(h, g)
	_, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "")))
	g.Err = domain.KindError(domain.ErrUnreachable, "GitLab unreachable")
	got, _ := r.Running(context.Background(), query(img("payment-service", dig("1"), "")))
	// The release found before is kept; there is no count offline.
	if res := got[dig("1")]; res.State != domain.ReleaseFound || res.Release.SHA != sha("a") || res.Count.Known {
		t.Errorf("release = %+v", res)
	}
}

func TestReleasesSkipsImagesWithoutDigest(t *testing.T) {
	r, _ := newReleases(&portstest.FakeReleaseHistory{}, &portstest.FakeCommitLog{})
	got, err := r.Running(context.Background(), query(img("api", "", "1.0")))
	if err != nil || len(got) != 0 {
		t.Errorf("got %+v, %v", got, err)
	}
}

// TestReleasesRequestBudget pins what a lookup costs: the numbers are the
// feature's whole load on GitLab and on the disk.
func TestReleasesRequestBudget(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Fixture: portstest.ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {deploy("a", 1, "promote-prd", 100)}},
		Logs:        map[int64]string{10: dig("1")},
	}}
	// The deployed commit is read once (to tell a merge), then kept.
	g := &portstest.FakeCommitLog{Commits: map[string]ports.Commit{sha("a"): {Subject: "commit a"}}}
	r, clock := newReleases(h, g)
	rm := r.Remotes.(*remotes)
	q := query(img("payment-service", dig("1"), ""))
	_, _ = r.Running(context.Background(), q)
	// A minute later (the TUI asks again while the service stays
	// selected), nothing changed: one request, the trunk's tip.
	clock.Advance(time.Minute)
	_, _ = r.Running(context.Background(), q)
	pages, lists, logs, _ := h.Calls()
	calls, _ := g.Calls()
	if pages != 1 || lists != 0 || logs != 1 {
		t.Errorf("history: %d pages, %d job lists, %d logs; want 1, 0, 1 in all", pages, lists, logs)
	}
	if calls["tip"] != 2 || calls["count"] != 1 || calls["default"] != 1 || calls["commit"] != 1 || calls["introduced"] != 0 {
		t.Errorf("history of the project: %v; want 2 tips, 1 count, 1 default branch, 1 commit", calls)
	}
	if len(rm.dirs) != 1 {
		t.Errorf(".git/config read %d times, want once (reused for a minute)", len(rm.dirs))
	}
}

// TestReleasesNoTrunkRequestWithoutACommit: when no commit is known (no
// token, GitLab down, an unknown image) there is nothing to count, so the
// trunk is not asked for.
func TestReleasesNoTrunkRequestWithoutACommit(t *testing.T) {
	h := &portstest.FakeReleaseHistory{Err: domain.KindError(domain.ErrUnauthorized, "no GitLab token")}
	g := &portstest.FakeCommitLog{}
	r, _ := newReleases(h, g)
	_, _ = r.Running(context.Background(), query(img("payment-service", dig("1"), "2.14.3")))
	if calls, _ := g.Calls(); calls["tip"] != 0 || calls["default"] != 0 {
		t.Errorf("calls = %v: no trunk request without a commit to count", calls)
	}
}
