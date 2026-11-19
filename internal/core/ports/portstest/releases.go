package portstest

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// ReleaseFixture is the content of a release history for one project:
// deployments per environment (newest first), the jobs of each pipeline
// and the log of each job.
type ReleaseFixture struct {
	Deployments map[string][]ports.Deployment
	Jobs        map[int64][]ports.Job
	Logs        map[int64]string
}

// FakeReleaseHistory serves a ReleaseFixture; Err, when set, is returned
// by every call. It counts the calls to check that logs are not read for
// nothing.
type FakeReleaseHistory struct {
	Fixture ReleaseFixture
	Err     error

	mu                        sync.Mutex
	pages, jobLists, logsRead int
	readJobs                  []int64
}

// Deployments implements ports.ReleaseHistory.
func (f *FakeReleaseHistory) Deployments(_ context.Context, _ ports.ReleaseProject, env string, page, perPage int) ([]ports.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages++
	if f.Err != nil {
		return nil, f.Err
	}
	all := f.Fixture.Deployments[env]
	from := (page - 1) * perPage
	if from >= len(all) {
		return nil, nil
	}
	return slices.Clone(all[from:min(from+perPage, len(all))]), nil
}

// PipelineJobs implements ports.ReleaseHistory.
func (f *FakeReleaseHistory) PipelineJobs(_ context.Context, _ ports.ReleaseProject, pipeline int64) ([]ports.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobLists++
	if f.Err != nil {
		return nil, f.Err
	}
	return slices.Clone(f.Fixture.Jobs[pipeline]), nil
}

// LogFind implements ports.ReleaseHistory.
func (f *FakeReleaseHistory) LogFind(_ context.Context, _ ports.ReleaseProject, job int64, needles []string, limit int64) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logsRead++
	f.readJobs = append(f.readJobs, job)
	if f.Err != nil {
		return nil, f.Err
	}
	log := f.Fixture.Logs[job]
	if int64(len(log)) > limit {
		log = log[:limit]
	}
	var out []string
	for _, n := range needles {
		if strings.Contains(log, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Calls returns the number of deployment pages, pipeline job lists and
// logs read so far, and the ids of the jobs whose log was read.
func (f *FakeReleaseHistory) Calls() (pages, jobLists, logs int, jobs []int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pages, f.jobLists, f.logsRead, slices.Clone(f.readJobs)
}

// RunReleaseHistoryContract checks an implementation of
// ports.ReleaseHistory: newHistory returns one serving fx and the project
// to ask for.
func RunReleaseHistoryContract(t *testing.T, newHistory func(t *testing.T, fx ReleaseFixture) (ports.ReleaseHistory, ports.ReleaseProject)) {
	t.Helper()
	at := time.Date(2026, 11, 18, 16, 2, 0, 0, time.UTC)
	dg := "sha256:" + strings.Repeat("c", 64)
	fx := ReleaseFixture{
		Deployments: map[string][]ports.Deployment{"prd": {
			{ID: 3, SHA: strings.Repeat("3", 40), Ref: "main", FinishedAt: at, User: "jdoe", Job: ports.Job{ID: 31, Name: "ship-prd"}, Pipeline: 300, Subject: "third", Author: "Jane", CommittedAt: at.Add(-time.Hour)},
			{ID: 2, SHA: strings.Repeat("2", 40), Ref: "main", FinishedAt: at.Add(-24 * time.Hour), User: "jdoe", Job: ports.Job{ID: 21, Name: "deploy"}, Pipeline: 200, Subject: "second", Author: "Jane", CommittedAt: at.Add(-25 * time.Hour)},
			{ID: 1, SHA: strings.Repeat("1", 40), Ref: "main", FinishedAt: at.Add(-48 * time.Hour), User: "bob", Job: ports.Job{ID: 11, Name: "ship-prd"}, Pipeline: 100, Subject: "first", Author: "Bob", CommittedAt: at.Add(-49 * time.Hour)},
		}},
		Jobs: map[int64][]ports.Job{200: {{ID: 20, Name: "build"}, {ID: 22, Name: "ship-prd"}}},
		Logs: map[int64]string{22: "pushed\nimage@" + dg + "\ndone\n", 31: strings.Repeat("noise ", 2000) + dg},
	}
	h, p := newHistory(t, fx)
	ctx := context.Background()

	t.Run("deployments newest first, paged", func(t *testing.T) {
		first, err := h.Deployments(ctx, p, "prd", 1, 2)
		if err != nil || len(first) != 2 || first[0].ID != 3 || first[1].ID != 2 {
			t.Fatalf("page 1 = %+v, %v", first, err)
		}
		if d := first[0]; d.SHA != fx.Deployments["prd"][0].SHA || d.Job != (ports.Job{ID: 31, Name: "ship-prd"}) || d.Pipeline != 300 ||
			d.Subject != "third" || d.Author != "Jane" || d.User != "jdoe" || !d.FinishedAt.Equal(at) || !d.CommittedAt.Equal(at.Add(-time.Hour)) || d.Ref != "main" {
			t.Errorf("deployment = %+v", d)
		}
		second, err := h.Deployments(ctx, p, "prd", 2, 2)
		if err != nil || len(second) != 1 || second[0].ID != 1 {
			t.Fatalf("page 2 = %+v, %v", second, err)
		}
		if none, err := h.Deployments(ctx, p, "rec", 1, 2); err != nil || len(none) != 0 {
			t.Errorf("unknown environment = %+v, %v", none, err)
		}
	})
	t.Run("pipeline jobs", func(t *testing.T) {
		jobs, err := h.PipelineJobs(ctx, p, 200)
		if err != nil || !slices.Contains(jobs, ports.Job{ID: 22, Name: "ship-prd"}) {
			t.Errorf("jobs = %+v, %v", jobs, err)
		}
	})
	t.Run("log find", func(t *testing.T) {
		other := "sha256:" + strings.Repeat("d", 64)
		hits, err := h.LogFind(ctx, p, 22, []string{other, dg}, 1<<20)
		if err != nil || !slices.Equal(hits, []string{dg}) {
			t.Errorf("hits = %v, %v", hits, err)
		}
		hits, err = h.LogFind(ctx, p, 31, []string{dg}, 1<<20)
		if err != nil || !slices.Equal(hits, []string{dg}) {
			t.Errorf("long log: hits = %v, %v", hits, err)
		}
		hits, err = h.LogFind(ctx, p, 31, []string{dg}, 100)
		if err != nil || len(hits) != 0 {
			t.Errorf("beyond the limit: hits = %v, %v", hits, err)
		}
	})
}

// FakeCommitLog is a git repository in memory.
type FakeCommitLog struct {
	// URL is the clone's remote; URLErr, when set, is RemoteURL's error.
	URL    string
	URLErr error
	// Commits by full id; Tags map a tag to a commit id.
	Commits map[string]ports.Commit
	Tags    map[string]string
	// Behind and Off give Count's answer per commit id (default 0, on ref).
	Behind map[string]int
	Off    map[string]bool
	// Stale is the stale flag of every answer; RefreshErr Refresh's error.
	Stale      bool
	RefreshErr error

	mu        sync.Mutex
	refreshes int
	trees     bool
	counted   [][]string
}

// RemoteURL implements ports.CommitLog.
func (g *FakeCommitLog) RemoteURL(context.Context, ports.GitRepo) (string, error) {
	return g.URL, g.URLErr
}

// Refresh implements ports.CommitLog.
func (g *FakeCommitLog) Refresh(_ context.Context, _ ports.GitRepo, trees bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refreshes++
	g.trees = trees
	return g.RefreshErr
}

// Commit implements ports.CommitLog: an abbreviated id must be unique.
func (g *FakeCommitLog) Commit(_ context.Context, _ ports.GitRepo, sha string) (ports.Commit, bool, error) {
	var match []ports.Commit
	for id, c := range g.Commits {
		if strings.HasPrefix(id, sha) {
			c.SHA = id
			match = append(match, c)
		}
	}
	if len(match) != 1 {
		return ports.Commit{}, g.Stale, domain.KindError(domain.ErrNotFound, "unknown or ambiguous commit "+sha)
	}
	return match[0], g.Stale, nil
}

// Tag implements ports.CommitLog.
func (g *FakeCommitLog) Tag(_ context.Context, _ ports.GitRepo, tag string) (string, bool, error) {
	if sha, ok := g.Tags[tag]; ok {
		return sha, g.Stale, nil
	}
	return "", g.Stale, domain.KindError(domain.ErrNotFound, "no tag "+tag)
}

// Count implements ports.CommitLog.
func (g *FakeCommitLog) Count(_ context.Context, _ ports.GitRepo, sha, _ string, paths []string) (int, bool, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counted = append(g.counted, paths)
	return g.Behind[sha], !g.Off[sha], g.Stale, nil
}

// Refreshes returns how many times Refresh ran, whether the last one asked
// for trees, and the paths of each Count.
func (g *FakeCommitLog) Refreshes() (n int, trees bool, counted [][]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refreshes, g.trees, slices.Clone(g.counted)
}
