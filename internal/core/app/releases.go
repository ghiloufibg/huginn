package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// ReleaseNames are the release history's names for one environment: the
// environment and the job whose log prints the deployed digest. Both may
// hold {repo} and {workload}. An empty field inherits from the level above.
type ReleaseNames struct {
	Environment, Job string
}

// ReleaseRepo holds a repository's exceptions to the general settings.
type ReleaseRepo struct {
	CompareRef     string
	MaxDeployments int
	Envs           map[domain.Env]ReleaseNames
	Workloads      map[string]ReleaseWorkload
}

// ReleaseWorkload holds a workload's exceptions; Paths are the folders of
// its code in a monorepo, for the "not deployed" count.
type ReleaseWorkload struct {
	Paths []string
	Envs  map[domain.Env]ReleaseNames
}

// ReleaseSettings are the release settings of the config folder, as plain
// data (releases.yaml and the gitlab keys of environments.yaml).
type ReleaseSettings struct {
	// ReposRoot holds the clones, one folder per repository; Remote is the
	// clone's remote naming the project.
	ReposRoot, Remote string
	// BaseURL is the release history's API base; empty means
	// https://<host of the remote>.
	BaseURL        string
	PerPage        int
	MaxDeployments int
	MaxLogBytes    int64
	CompareRef     string
	// Refresh is how old the git copy of a remote and a "not recent"
	// answer may get before they are read again.
	Refresh time.Duration
	// Envs are the names of each environment; an environment missing here
	// is not searched (the commit is then only guessed).
	Envs  map[domain.Env]ReleaseNames
	Repos map[string]ReleaseRepo
}

// Releases implements ports.Releases: the algorithm of
// docs/plan/M16-deployed-commit.md §1. For each running digest it reads
// the newest successful deployments of the environment and the log of
// their promote job, and the first deployment whose log prints the digest
// is the running release. Otherwise the commit is guessed from the image
// tag, and kept only when git confirms it.
type Releases struct {
	History  ports.ReleaseHistory
	Git      ports.CommitLog
	Clock    ports.Clock
	Log      *slog.Logger
	Settings ReleaseSettings

	mu      sync.Mutex
	found   map[releaseKey]ports.Deployment // a digest's release never changes
	missed  map[releaseKey]time.Time        // "not recent", re-checked after Refresh
	fetched map[string]time.Time            // last refresh of each remote
	locks   map[string]*sync.Mutex          // one search per project and environment
}

type releaseKey struct{ project, env, digest string }

// Running implements ports.Releases.
func (r *Releases) Running(ctx context.Context, q ports.ReleaseQuery) (map[string]domain.RunningRelease, error) {
	images := uniqueImages(q.Images)
	out := make(map[string]domain.RunningRelease, len(images))
	if len(images) == 0 {
		return out, nil
	}
	repo := ports.GitRepo{Dir: filepath.Join(r.Settings.ReposRoot, q.Repo), Remote: r.Settings.Remote}
	url, err := r.Git.RemoteURL(ctx, repo)
	if err != nil {
		return r.failAll(images, fmt.Errorf("no clone of this repository in %s: %w", r.Settings.ReposRoot, err)), nil
	}
	project, ok := domain.ParseGitRemote(url)
	if !ok {
		return r.failAll(images, fmt.Errorf("remote %s of the clone is not a hosted project: %w", r.Settings.Remote, domain.ErrConfig)), nil
	}
	r.refresh(ctx, repo, url, r.needsTrees(q.Repo))
	deployments, searched, histErr := r.search(ctx, q, project, images)
	for _, img := range images {
		res := domain.RunningRelease{}
		if d, ok := deployments[img.Digest]; ok {
			res.State, res.Release = domain.ReleaseFound, release(d)
			if res.Release.Subject == "" {
				if c, _, err := r.Git.Commit(ctx, repo, d.SHA); err == nil {
					res.Release.Subject, res.Release.Author, res.Release.CommittedAt = c.Subject, c.Author, c.At
				}
			}
		} else {
			res.NotRecent = searched[img.Digest] && histErr == nil
			res.Problem = histErr
			r.guess(ctx, repo, img.Tag, &res)
		}
		if res.Release.SHA != "" {
			res.Count = r.count(ctx, repo, q.Repo, img.Workload, res.Release.SHA)
		}
		out[img.Digest] = res
	}
	return out, nil
}

// uniqueImages keeps the images with a digest, each digest once.
func uniqueImages(in []domain.WorkloadImage) []domain.WorkloadImage {
	var out []domain.WorkloadImage
	for _, img := range in {
		if img.Digest != "" && !slices.ContainsFunc(out, func(o domain.WorkloadImage) bool { return o.Digest == img.Digest }) {
			out = append(out, img)
		}
	}
	return out
}

func (r *Releases) failAll(images []domain.WorkloadImage, err error) map[string]domain.RunningRelease {
	out := make(map[string]domain.RunningRelease, len(images))
	for _, img := range images {
		out[img.Digest] = domain.RunningRelease{Problem: err}
	}
	return out
}

func release(d ports.Deployment) domain.Release {
	return domain.Release{
		SHA: d.SHA, Ref: d.Ref, Subject: d.Subject, Author: d.Author, CommittedAt: d.CommittedAt,
		DeployedAt: d.FinishedAt, DeployedBy: d.User, Pipeline: d.Pipeline,
	}
}

// names returns the release history's names for a workload of repo in env,
// placeholders expanded; ok is false when env is not searched.
func (r *Releases) names(env domain.Env, repo, workload string) (ReleaseNames, bool) {
	n, ok := r.Settings.Envs[env]
	if !ok {
		return ReleaseNames{}, false
	}
	over := func(o ReleaseNames) {
		if o.Environment != "" {
			n.Environment = o.Environment
		}
		if o.Job != "" {
			n.Job = o.Job
		}
	}
	if rp, ok := r.Settings.Repos[repo]; ok {
		over(rp.Envs[env])
		over(rp.Workloads[workload].Envs[env])
	}
	expand := strings.NewReplacer("{repo}", repo, "{workload}", workload)
	return ReleaseNames{Environment: expand.Replace(n.Environment), Job: expand.Replace(n.Job)}, n.Environment != "" && n.Job != ""
}

func (r *Releases) needsTrees(repo string) bool {
	for _, w := range r.Settings.Repos[repo].Workloads {
		if len(w.Paths) > 0 {
			return true
		}
	}
	return false
}

// refresh updates the git copy of the remote at most every Refresh. A
// failure is only logged: git then falls back to the clone, and says so.
func (r *Releases) refresh(ctx context.Context, repo ports.GitRepo, url string, trees bool) {
	now := r.Clock.Now()
	r.mu.Lock()
	if r.fetched == nil {
		r.fetched = map[string]time.Time{}
	}
	if at, ok := r.fetched[url]; ok && now.Sub(at) < r.Settings.Refresh {
		r.mu.Unlock()
		return
	}
	r.fetched[url] = now
	r.mu.Unlock()
	if err := r.Git.Refresh(ctx, repo, trees); err != nil && r.Log != nil {
		r.Log.Warn("refresh the git copy", "remote", url, "err", err)
	}
}

// search finds, for each image, the deployment whose promote job printed
// its digest. searched tells the digests whose environment was searched
// to the end without an answer.
func (r *Releases) search(ctx context.Context, q ports.ReleaseQuery, project domain.GitProject, images []domain.WorkloadImage) (map[string]ports.Deployment, map[string]bool, error) {
	found, searched := map[string]ports.Deployment{}, map[string]bool{}
	byEnv := map[string][]domain.WorkloadImage{}
	jobOf := map[string]string{}
	var envs []string
	for _, img := range images {
		n, ok := r.names(q.Env, q.Repo, img.Workload)
		if !ok {
			continue
		}
		if _, seen := byEnv[n.Environment]; !seen {
			envs = append(envs, n.Environment)
		}
		byEnv[n.Environment] = append(byEnv[n.Environment], img)
		jobOf[img.Digest] = n.Job
	}
	if len(envs) == 0 {
		return found, searched, nil
	}
	p := ports.ReleaseProject{BaseURL: r.Settings.BaseURL, Path: project.Path}
	if p.BaseURL == "" {
		p.BaseURL = "https://" + project.Host
	}
	var errs []error
	for _, env := range envs {
		if err := r.searchEnv(ctx, q.Repo, p, env, byEnv[env], jobOf, found, searched); err != nil {
			errs = append(errs, err)
		}
	}
	return found, searched, errors.Join(errs...)
}

// searchEnv runs the algorithm for the images of one environment.
func (r *Releases) searchEnv(ctx context.Context, repo string, p ports.ReleaseProject, env string, images []domain.WorkloadImage,
	jobOf map[string]string, found map[string]ports.Deployment, searched map[string]bool,
) error {
	lock := r.lock(p.Path + "\x00" + env)
	lock.Lock()
	defer lock.Unlock()
	var remaining []string
	for _, img := range images {
		key := releaseKey{p.Path, env, img.Digest}
		if d, ok := r.cached(key); ok {
			found[img.Digest] = d
		} else if !r.recentlyMissed(key) {
			remaining = append(remaining, img.Digest)
		} else {
			searched[img.Digest] = true
		}
	}
	if len(remaining) == 0 {
		return nil
	}
	limit := r.Settings.MaxDeployments
	if m := r.Settings.Repos[repo].MaxDeployments; m > 0 {
		limit = m
	}
	jobsOf := map[int64][]ports.Job{} // pipeline jobs, read once per pipeline
	read := map[int64]bool{}          // job logs already read
	scanned := 0
	for page := 1; scanned < limit && len(remaining) > 0; page++ {
		ds, err := r.History.Deployments(ctx, p, env, page, r.Settings.PerPage)
		if err != nil {
			return fmt.Errorf("read the %s deployments: %w", env, err)
		}
		for _, d := range ds {
			if scanned++; scanned > limit || len(remaining) == 0 {
				break
			}
			jobs, err := r.promoteJobs(ctx, p, d, remaining, jobOf, jobsOf)
			if err != nil {
				return err
			}
			for _, j := range jobs {
				if read[j.ID] {
					continue
				}
				read[j.ID] = true
				needles := slices.DeleteFunc(slices.Clone(remaining), func(dg string) bool { return jobOf[dg] != j.Name })
				hits, err := r.History.LogFind(ctx, p, j.ID, needles, r.Settings.MaxLogBytes)
				if err != nil {
					return fmt.Errorf("read the log of job %s: %w", j.Name, err)
				}
				for _, h := range hits {
					found[h] = d
					r.remember(releaseKey{p.Path, env, h}, d)
				}
				remaining = slices.DeleteFunc(remaining, func(dg string) bool { return slices.Contains(hits, dg) })
			}
		}
		if len(ds) < r.Settings.PerPage {
			break // the last page
		}
	}
	for _, dg := range remaining {
		searched[dg] = true
		r.missedNow(releaseKey{p.Path, env, dg})
	}
	return nil
}

// promoteJobs returns the jobs of d's pipeline that print the digests
// still looked for: d's own job when it is one of them (no request),
// otherwise those of its pipeline with a wanted name. A deployment of
// another service (a monorepo) has none, and its log is never read.
func (r *Releases) promoteJobs(ctx context.Context, p ports.ReleaseProject, d ports.Deployment, remaining []string,
	jobOf map[string]string, jobsOf map[int64][]ports.Job,
) ([]ports.Job, error) {
	wanted := map[string]bool{}
	for _, dg := range remaining {
		wanted[jobOf[dg]] = true
	}
	if wanted[d.Job.Name] {
		return []ports.Job{d.Job}, nil
	}
	if d.Pipeline == 0 {
		return nil, nil
	}
	jobs, ok := jobsOf[d.Pipeline]
	if !ok {
		var err error
		if jobs, err = r.History.PipelineJobs(ctx, p, d.Pipeline); err != nil {
			return nil, fmt.Errorf("read the jobs of pipeline %d: %w", d.Pipeline, err)
		}
		jobsOf[d.Pipeline] = jobs
	}
	var out []ports.Job
	for _, j := range jobs {
		if wanted[j.Name] {
			out = append(out, j)
		}
	}
	return out, nil
}

// guess reads the commit from the image tag, kept only when git confirms
// it: a part of the tag that is a commit id, else a git tag named like the
// image tag (with or without a leading v).
func (r *Releases) guess(ctx context.Context, repo ports.GitRepo, tag string, res *domain.RunningRelease) {
	if tag == "" {
		return
	}
	for _, c := range domain.CommitCandidates(tag) {
		if commit, _, err := r.Git.Commit(ctx, repo, c); err == nil {
			res.State, res.Guess, res.Release = domain.ReleaseGuessed, "image tag", fromCommit(commit)
			return
		}
	}
	for _, t := range []string{tag, "v" + tag} {
		sha, _, err := r.Git.Tag(ctx, repo, t)
		if err != nil {
			continue
		}
		if commit, _, err := r.Git.Commit(ctx, repo, sha); err == nil {
			res.State, res.Guess, res.Release = domain.ReleaseGuessed, "git tag "+t, fromCommit(commit)
			return
		}
	}
}

func fromCommit(c ports.Commit) domain.Release {
	return domain.Release{SHA: c.SHA, Subject: c.Subject, Author: c.Author, CommittedAt: c.At}
}

// count compares sha with the trunk; only the commits touching the
// workload's paths count when it has some (a monorepo).
func (r *Releases) count(ctx context.Context, repo ports.GitRepo, name, workload, sha string) domain.TrunkCount {
	ref := r.Settings.CompareRef
	rp := r.Settings.Repos[name]
	if rp.CompareRef != "" {
		ref = rp.CompareRef
	}
	paths := rp.Workloads[workload].Paths
	behind, onRef, stale, err := r.Git.Count(ctx, repo, sha, ref, paths)
	if err != nil {
		if r.Log != nil {
			r.Log.Debug("count the commits not deployed", "ref", ref, "err", err)
		}
		return domain.TrunkCount{Ref: ref}
	}
	return domain.TrunkCount{
		Known: true, Ref: ref, Behind: behind, OnTrunk: onRef, Scoped: len(paths) > 0,
		WholeProject: len(paths) == 0 && r.needsTrees(name), Stale: stale,
	}
}

func (r *Releases) lock(key string) *sync.Mutex {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.locks == nil {
		r.locks = map[string]*sync.Mutex{}
	}
	l, ok := r.locks[key]
	if !ok {
		l = &sync.Mutex{}
		r.locks[key] = l
	}
	return l
}

func (r *Releases) cached(k releaseKey) (ports.Deployment, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.found[k]
	return d, ok
}

func (r *Releases) remember(k releaseKey, d ports.Deployment) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.found == nil {
		r.found = map[releaseKey]ports.Deployment{}
	}
	r.found[k] = d
	delete(r.missed, k)
}

func (r *Releases) recentlyMissed(k releaseKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.missed[k]
	return ok && r.Clock.Now().Sub(at) < r.Settings.Refresh
}

func (r *Releases) missedNow(k releaseKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.missed == nil {
		r.missed = map[releaseKey]time.Time{}
	}
	r.missed[k] = r.Clock.Now()
}
