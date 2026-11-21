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
	// Refresh is how old a "not recent" answer may get before the
	// deployments are searched again.
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
// tag, and kept only when the project's history confirms it. The commit
// details and the count come from the project's API (CommitLog): no git
// command runs (docs/plan/M16.1-commits-from-gitlab-api.md).
type Releases struct {
	History  ports.ReleaseHistory
	Commits  ports.CommitLog
	Remotes  ports.GitRemotes
	Clock    ports.Clock
	Log      *slog.Logger
	Settings ReleaseSettings

	mu     sync.Mutex
	found  map[releaseKey]ports.Deployment // a digest's release never changes
	missed map[releaseKey]miss             // "not recent", re-checked after Refresh
	locks  map[string]*sync.Mutex          // one search per project and environment
	// The project's history: a commit and a tag never change, so they are
	// kept for the session (an unknown one for Refresh); a count is kept
	// while the trunk's tip does not move; the default branch an hour.
	commits  map[histKey]ports.Commit
	tags     map[histKey]string
	unknown  map[histKey]time.Time
	counts   map[countKey]domain.TrunkCount
	defaults map[string]defaultBranch
	// A finished job's log and a pipeline's jobs do not change: a digest
	// is never searched twice in the same log, and a pipeline's jobs are
	// listed once. Both are bounded (cleared past maxRemembered).
	absent map[logKey]struct{}
	jobs   map[pipelineKey][]ports.Job
}

type releaseKey struct{ project, env, digest string }

// histKey is a commit id or a tag of a project ("c:<sha>", "t:<tag>").
type histKey struct{ project, ref string }

type countKey struct{ project, sha, branch, tip, paths string }

type defaultBranch struct {
	name string
	at   time.Time
}

// trunk is compare_ref resolved for one lookup: the branch and its tip.
type trunk struct {
	ref, branch, tip string
}

// defaultBranchTTL is how long a project's default branch is reused.
const defaultBranchTTL = time.Hour

// miss is a search that found no deployment: when, and the newest
// deployment it read, so that the next search stops there.
type miss struct {
	at   time.Time
	upTo int64
}

type logKey struct {
	project string
	job     int64
	digest  string
}

type pipelineKey struct {
	project  string
	pipeline int64
}

// maxRemembered bounds the logs and pipelines remembered in a session.
const maxRemembered = 4096

// Running implements ports.Releases.
func (r *Releases) Running(ctx context.Context, q ports.ReleaseQuery) (map[string]domain.RunningRelease, error) {
	images := uniqueImages(q.Images)
	out := make(map[string]domain.RunningRelease, len(images))
	if len(images) == 0 {
		return out, nil
	}
	url, err := r.Remotes.GitRemote(ctx, filepath.Join(r.Settings.ReposRoot, q.Repo), r.Settings.Remote)
	if err != nil {
		return r.failAll(images, fmt.Errorf("no clone of this repository in %s: %w", r.Settings.ReposRoot, err)), nil
	}
	project, ok := domain.ParseGitRemote(url)
	if !ok {
		return r.failAll(images, fmt.Errorf("remote %s of the clone is not a hosted project: %w", r.Settings.Remote, domain.ErrConfig)), nil
	}
	p := ports.ReleaseProject{BaseURL: r.Settings.BaseURL, Path: project.Path}
	if p.BaseURL == "" {
		p.BaseURL = "https://" + project.Host
	}
	deployments, searched, histErr := r.search(ctx, q, p, images)
	tr, trErr := r.trunk(ctx, p, q.Repo)
	if trErr != nil && r.Log != nil {
		r.Log.Debug("resolve the trunk", "project", p.Path, "err", trErr)
	}
	for _, img := range images {
		res := domain.RunningRelease{}
		if d, ok := deployments[img.Digest]; ok {
			res.State, res.Release = domain.ReleaseFound, release(d)
			if res.Release.Subject == "" {
				if c, err := r.commit(ctx, p, d.SHA); err == nil {
					res.Release.Subject, res.Release.Author, res.Release.CommittedAt = c.Subject, c.Author, c.At
				}
			}
		} else {
			res.NotRecent = searched[img.Digest] && histErr == nil
			res.Problem = histErr
			r.guess(ctx, p, img.Tag, &res)
		}
		if res.Release.SHA != "" && trErr == nil {
			res.Count = r.count(ctx, p, q.Repo, img.Workload, res.Release.SHA, tr)
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

// search finds, for each image, the deployment whose promote job printed
// its digest. searched tells the digests whose environment was searched
// to the end without an answer.
func (r *Releases) search(ctx context.Context, q ports.ReleaseQuery, p ports.ReleaseProject, images []domain.WorkloadImage) (map[string]ports.Deployment, map[string]bool, error) {
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
	// A digest searched before was searched down to upTo: the next search
	// reads only the deployments made since, then stops.
	floor := r.searchedUpTo(p.Path, env, remaining)
	read := map[int64]bool{} // job logs already read
	scanned, newest := 0, int64(0)
	stop := false
	for page := 1; scanned < limit && len(remaining) > 0 && !stop; page++ {
		ds, err := r.History.Deployments(ctx, p, env, page, r.Settings.PerPage)
		if err != nil {
			return fmt.Errorf("read the %s deployments: %w", env, err)
		}
		for _, d := range ds {
			if floor > 0 && d.ID <= floor {
				stop = true
				break
			}
			if scanned++; scanned > limit || len(remaining) == 0 {
				break
			}
			newest = max(newest, d.ID)
			jobs, err := r.promoteJobs(ctx, p, d, remaining, jobOf)
			if err != nil {
				return err
			}
			for _, j := range jobs {
				if read[j.ID] {
					continue
				}
				read[j.ID] = true
				needles := slices.DeleteFunc(slices.Clone(remaining), func(dg string) bool {
					return jobOf[dg] != j.Name || r.knownAbsent(logKey{p.Path, j.ID, dg})
				})
				if len(needles) == 0 {
					continue
				}
				hits, err := r.History.LogFind(ctx, p, j.ID, needles, r.Settings.MaxLogBytes)
				if err != nil {
					return fmt.Errorf("read the log of job %s: %w", j.Name, err)
				}
				for _, n := range needles {
					if !slices.Contains(hits, n) {
						r.markAbsent(logKey{p.Path, j.ID, n})
					}
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
		r.missedNow(releaseKey{p.Path, env, dg}, newest)
	}
	return nil
}

// promoteJobs returns the jobs of d's pipeline that print the digests
// still looked for: d's own job when it is one of them (no request),
// otherwise those of its pipeline with a wanted name. A deployment of
// another service (a monorepo) has none, and its log is never read.
func (r *Releases) promoteJobs(ctx context.Context, p ports.ReleaseProject, d ports.Deployment, remaining []string,
	jobOf map[string]string,
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
	pk := pipelineKey{p.Path, d.Pipeline}
	r.mu.Lock()
	jobs, ok := r.jobs[pk]
	r.mu.Unlock()
	if !ok {
		var err error
		if jobs, err = r.History.PipelineJobs(ctx, p, d.Pipeline); err != nil {
			return nil, fmt.Errorf("read the jobs of pipeline %d: %w", d.Pipeline, err)
		}
		r.mu.Lock()
		if r.jobs == nil || len(r.jobs) >= maxRemembered {
			r.jobs = map[pipelineKey][]ports.Job{}
		}
		r.jobs[pk] = jobs
		r.mu.Unlock()
	}
	var out []ports.Job
	for _, j := range jobs {
		if wanted[j.Name] {
			out = append(out, j)
		}
	}
	return out, nil
}

// guess reads the commit from the image tag, kept only when the project's
// history confirms it: a part of the tag that is a commit id, else a git
// tag named like the image tag (with or without a leading v).
func (r *Releases) guess(ctx context.Context, p ports.ReleaseProject, tag string, res *domain.RunningRelease) {
	if tag == "" {
		return
	}
	for _, c := range domain.CommitCandidates(tag) {
		if commit, err := r.commit(ctx, p, c); err == nil {
			res.State, res.Guess, res.Release = domain.ReleaseGuessed, "image tag", fromCommit(commit)
			return
		}
	}
	for _, t := range []string{tag, "v" + tag} {
		sha, err := r.tag(ctx, p, t)
		if err != nil {
			continue
		}
		if commit, err := r.commit(ctx, p, sha); err == nil {
			res.State, res.Guess, res.Release = domain.ReleaseGuessed, "git tag "+t, fromCommit(commit)
			return
		}
	}
}

func fromCommit(c ports.Commit) domain.Release {
	return domain.Release{SHA: c.SHA, Subject: c.Subject, Author: c.Author, CommittedAt: c.At}
}

// trunk resolves compare_ref (the repository's, else the general one) to a
// branch and its tip: "<remote>/HEAD" is the project's default branch,
// "<remote>/<branch>" that branch. The tip is read on each lookup, so a
// count is computed again only when the trunk moved.
func (r *Releases) trunk(ctx context.Context, p ports.ReleaseProject, repo string) (trunk, error) {
	ref := r.Settings.CompareRef
	if rp := r.Settings.Repos[repo]; rp.CompareRef != "" {
		ref = rp.CompareRef
	}
	branch, _ := strings.CutPrefix(ref, r.Settings.Remote+"/")
	if branch == "HEAD" {
		var err error
		if branch, err = r.defaultBranch(ctx, p); err != nil {
			return trunk{}, err
		}
	}
	tip, err := r.Commits.BranchTip(ctx, p, branch)
	if err != nil {
		return trunk{}, err
	}
	return trunk{ref: ref, branch: branch, tip: tip}, nil
}

func (r *Releases) defaultBranch(ctx context.Context, p ports.ReleaseProject) (string, error) {
	r.mu.Lock()
	d, ok := r.defaults[p.Path]
	r.mu.Unlock()
	if ok && r.Clock.Now().Sub(d.at) < defaultBranchTTL {
		return d.name, nil
	}
	name, err := r.Commits.DefaultBranch(ctx, p)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	if r.defaults == nil || len(r.defaults) >= maxRemembered {
		r.defaults = map[string]defaultBranch{}
	}
	r.defaults[p.Path] = defaultBranch{name, r.Clock.Now()}
	r.mu.Unlock()
	return name, nil
}

// count compares sha with the trunk; only the commits touching the
// workload's paths count when it has some (a monorepo). It is asked again
// only when the trunk's tip moved.
func (r *Releases) count(ctx context.Context, p ports.ReleaseProject, repo, workload, sha string, tr trunk) domain.TrunkCount {
	paths := r.Settings.Repos[repo].Workloads[workload].Paths
	key := countKey{p.Path, sha, tr.branch, tr.tip, strings.Join(paths, "\x00")}
	r.mu.Lock()
	c, ok := r.counts[key]
	r.mu.Unlock()
	if ok {
		return c
	}
	behind, capped, onBranch, err := r.Commits.Count(ctx, p, sha, tr.branch, paths)
	if err != nil {
		if r.Log != nil {
			r.Log.Debug("count the commits not deployed", "ref", tr.ref, "err", err)
		}
		return domain.TrunkCount{Ref: tr.ref}
	}
	c = domain.TrunkCount{
		Known: true, Ref: tr.ref, Behind: behind, OnTrunk: onBranch, Scoped: len(paths) > 0,
		WholeProject: len(paths) == 0 && r.isMonorepo(repo), Capped: capped,
	}
	r.mu.Lock()
	if r.counts == nil || len(r.counts) >= maxRemembered {
		r.counts = map[countKey]domain.TrunkCount{}
	}
	r.counts[key] = c
	r.mu.Unlock()
	return c
}

// isMonorepo tells a repository where some workload has paths.
func (r *Releases) isMonorepo(repo string) bool {
	for _, w := range r.Settings.Repos[repo].Workloads {
		if len(w.Paths) > 0 {
			return true
		}
	}
	return false
}

// commit reads a commit, kept for the session; an unknown one is asked
// again after Refresh.
func (r *Releases) commit(ctx context.Context, p ports.ReleaseProject, sha string) (ports.Commit, error) {
	k := histKey{p.Path, "c:" + sha}
	r.mu.Lock()
	c, ok := r.commits[k]
	r.mu.Unlock()
	if ok {
		return c, nil
	}
	if r.knownUnknown(k) {
		return ports.Commit{}, domain.ErrNotFound
	}
	c, err := r.Commits.Commit(ctx, p, sha)
	if err != nil {
		r.markUnknown(k, err)
		return ports.Commit{}, err
	}
	r.mu.Lock()
	if r.commits == nil || len(r.commits) >= maxRemembered {
		r.commits = map[histKey]ports.Commit{}
	}
	r.commits[k] = c
	r.mu.Unlock()
	return c, nil
}

// tag reads the commit of a tag, like commit.
func (r *Releases) tag(ctx context.Context, p ports.ReleaseProject, tag string) (string, error) {
	k := histKey{p.Path, "t:" + tag}
	r.mu.Lock()
	sha, ok := r.tags[k]
	r.mu.Unlock()
	if ok {
		return sha, nil
	}
	if r.knownUnknown(k) {
		return "", domain.ErrNotFound
	}
	sha, err := r.Commits.Tag(ctx, p, tag)
	if err != nil {
		r.markUnknown(k, err)
		return "", err
	}
	r.mu.Lock()
	if r.tags == nil || len(r.tags) >= maxRemembered {
		r.tags = map[histKey]string{}
	}
	r.tags[k] = sha
	r.mu.Unlock()
	return sha, nil
}

func (r *Releases) knownUnknown(k histKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	at, ok := r.unknown[k]
	return ok && r.Clock.Now().Sub(at) < r.Settings.Refresh
}

// markUnknown remembers a commit or tag the history does not have; other
// errors (unreachable, unauthorized) are not remembered.
func (r *Releases) markUnknown(k histKey, err error) {
	if !errors.Is(err, domain.ErrNotFound) {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.unknown == nil || len(r.unknown) >= maxRemembered {
		r.unknown = map[histKey]time.Time{}
	}
	r.unknown[k] = r.Clock.Now()
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
	if r.found == nil || len(r.found) >= maxRemembered {
		r.found = map[releaseKey]ports.Deployment{}
	}
	r.found[k] = d
	delete(r.missed, k)
}

func (r *Releases) recentlyMissed(k releaseKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.missed[k]
	return ok && r.Clock.Now().Sub(m.at) < r.Settings.Refresh
}

// missedNow records a search that found nothing; newest is the newest
// deployment it read (0 when it read none).
func (r *Releases) missedNow(k releaseKey, newest int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.missed == nil || len(r.missed) >= maxRemembered {
		r.missed = map[releaseKey]miss{}
	}
	r.missed[k] = miss{at: r.Clock.Now(), upTo: max(newest, r.missed[k].upTo)}
}

// searchedUpTo returns the deployment down to which every digest was
// already searched: 0 when one never was.
func (r *Releases) searchedUpTo(project, env string, digests []string) int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	floor := int64(-1)
	for _, dg := range digests {
		m, ok := r.missed[releaseKey{project, env, dg}]
		if !ok || m.upTo == 0 {
			return 0
		}
		if floor < 0 || m.upTo < floor {
			floor = m.upTo
		}
	}
	return max(floor, 0)
}

func (r *Releases) knownAbsent(k logKey) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.absent[k]
	return ok
}

func (r *Releases) markAbsent(k logKey) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.absent == nil || len(r.absent) >= maxRemembered {
		r.absent = map[logKey]struct{}{}
	}
	r.absent[k] = struct{}{}
}
