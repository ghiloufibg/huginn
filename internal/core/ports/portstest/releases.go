package portstest

import (
	"context"
	"errors"
	"maps"
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

// FakeCommitLog is a project's history in memory. Commits are by full id;
// Tags map a tag to a commit id; Branches a branch to its tip; Behind and
// Off give Count's answer per commit id (default 0, on the branch); Capped
// makes every count a lower bound. Err, when set, is every call's error.
type FakeCommitLog struct {
	Commits  map[string]ports.Commit
	Tags     map[string]string
	Branches map[string]string
	Default  string
	Behind   map[string]int
	Off      map[string]bool
	Capped   bool
	Err      error

	mu      sync.Mutex
	calls   map[string]int
	counted [][]string
}

func (g *FakeCommitLog) call(name string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.calls == nil {
		g.calls = map[string]int{}
	}
	g.calls[name]++
	return g.Err
}

// Commit implements ports.CommitLog: an abbreviated id must be unique.
func (g *FakeCommitLog) Commit(_ context.Context, _ ports.ReleaseProject, sha string) (ports.Commit, error) {
	if err := g.call("commit"); err != nil {
		return ports.Commit{}, err
	}
	var match []ports.Commit
	for id, c := range g.Commits {
		if strings.HasPrefix(id, sha) {
			c.SHA = id
			match = append(match, c)
		}
	}
	if len(match) != 1 {
		return ports.Commit{}, domain.KindError(domain.ErrNotFound, "unknown or ambiguous commit "+sha)
	}
	return match[0], nil
}

// Tag implements ports.CommitLog.
func (g *FakeCommitLog) Tag(_ context.Context, _ ports.ReleaseProject, tag string) (string, error) {
	if err := g.call("tag"); err != nil {
		return "", err
	}
	if sha, ok := g.Tags[tag]; ok {
		return sha, nil
	}
	return "", domain.KindError(domain.ErrNotFound, "no tag "+tag)
}

// DefaultBranch implements ports.CommitLog.
func (g *FakeCommitLog) DefaultBranch(context.Context, ports.ReleaseProject) (string, error) {
	if err := g.call("default"); err != nil {
		return "", err
	}
	if g.Default == "" {
		return "main", nil
	}
	return g.Default, nil
}

// BranchTip implements ports.CommitLog; an unknown branch's tip is "tip".
func (g *FakeCommitLog) BranchTip(_ context.Context, _ ports.ReleaseProject, branch string) (string, error) {
	if err := g.call("tip"); err != nil {
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if tip, ok := g.Branches[branch]; ok {
		return tip, nil
	}
	return "tip", nil
}

// Count implements ports.CommitLog.
func (g *FakeCommitLog) Count(_ context.Context, _ ports.ReleaseProject, sha, _ string, paths []string) (int, bool, bool, error) {
	if err := g.call("count"); err != nil {
		return 0, false, false, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counted = append(g.counted, paths)
	return g.Behind[sha], g.Capped, !g.Off[sha], nil
}

// SetTip moves a branch (the trunk got a new commit).
func (g *FakeCommitLog) SetTip(branch, tip string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Branches == nil {
		g.Branches = map[string]string{}
	}
	g.Branches[branch] = tip
}

// Calls returns how many times each method ran ("commit", "tag",
// "default", "tip", "count") and the paths of each Count.
func (g *FakeCommitLog) Calls() (map[string]int, [][]string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return maps.Clone(g.calls), slices.Clone(g.counted)
}

// HistoryFixture is the content of a project's history for the CommitLog
// contract: main has First, Second, Third (oldest first, Third its tip);
// Second touches the folder "svc/"; the tag v1.0 points to First; Hotfix is
// a commit off main whose parent is First.
type HistoryFixture struct {
	First, Second, Third, Hotfix string
}

// RunCommitLogContract checks an implementation of ports.CommitLog:
// newLog returns one serving fx's history and the project to ask for.
func RunCommitLogContract(t *testing.T, newLog func(t *testing.T) (ports.CommitLog, ports.ReleaseProject, HistoryFixture)) {
	t.Helper()
	g, p, fx := newLog(t)
	ctx := context.Background()
	t.Run("commit, full and abbreviated", func(t *testing.T) {
		for _, id := range []string{fx.Second, fx.Second[:8]} {
			c, err := g.Commit(ctx, p, id)
			if err != nil || c.SHA != fx.Second || c.Subject == "" || c.Author == "" || c.At.IsZero() {
				t.Errorf("Commit(%s) = %+v, %v", id, c, err)
			}
		}
		if _, err := g.Commit(ctx, p, strings.Repeat("0", 40)); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown commit: %v", err)
		}
	})
	t.Run("tag", func(t *testing.T) {
		if sha, err := g.Tag(ctx, p, "v1.0"); err != nil || sha != fx.First {
			t.Errorf("Tag = %q, %v", sha, err)
		}
		if _, err := g.Tag(ctx, p, "v9.9"); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("unknown tag: %v", err)
		}
	})
	t.Run("default branch and tip", func(t *testing.T) {
		if b, err := g.DefaultBranch(ctx, p); err != nil || b != "main" {
			t.Errorf("DefaultBranch = %q, %v", b, err)
		}
		if tip, err := g.BranchTip(ctx, p, "main"); err != nil || tip != fx.Third {
			t.Errorf("BranchTip = %q, %v", tip, err)
		}
	})
	t.Run("count", func(t *testing.T) {
		behind, capped, on, err := g.Count(ctx, p, fx.First, "main", nil)
		if err != nil || behind != 2 || capped || !on {
			t.Errorf("Count(first) = %d, %v, %v, %v; want 2 on main", behind, capped, on, err)
		}
		behind, _, _, err = g.Count(ctx, p, fx.First, "main", []string{"svc/"})
		if err != nil || behind != 1 {
			t.Errorf("Count(first, svc/) = %d, %v; want 1", behind, err)
		}
		behind, _, on, err = g.Count(ctx, p, fx.Third, "main", nil)
		if err != nil || behind != 0 || !on {
			t.Errorf("Count(tip) = %d, %v, %v", behind, on, err)
		}
		_, _, on, err = g.Count(ctx, p, fx.Hotfix, "main", nil)
		if err != nil || on {
			t.Errorf("Count(hotfix): on %v, %v; want off main", on, err)
		}
	})
}
