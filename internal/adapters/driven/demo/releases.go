package demo

import (
	"context"
	"crypto/sha1" //nolint:gosec // synthetic commit ids, not security
	"crypto/sha256"
	"encoding/hex"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// imageID is the synthetic "registry@digest" a demo container runs.
func imageID(image string) string {
	sum := sha256.Sum256([]byte(image))
	name, _, _ := strings.Cut(image, ":")
	return name + "@sha256:" + hex.EncodeToString(sum[:])
}

// commitOf is the synthetic commit a demo version was built from: the
// version itself when it is a commit id (the ledger-writer tag), so the
// image-tag guess can find it.
func commitOf(repo, version string) string {
	sum := sha1.Sum([]byte(repo + "@" + version)) //nolint:gosec // synthetic
	h := hex.EncodeToString(sum[:])
	if domain.IsCommitID(version) {
		return version + h[len(version):]
	}
	return h
}

var subjects = []string{
	"fix(%s): retry the callback on timeout",
	"feat(%s): expose the settlement status",
	"perf(%s): cache the tariff lookup",
	"fix(%s): close the pool on shutdown",
	"chore(%s): bump the base image",
	"feat(%s): add the audit trail",
}

var deployers = []string{"jdoe", "amartin", "kchen", "release-bot"}

// notReleased are repositories whose running image no recent deployment
// printed, to show the fallbacks: a version tag that is a git tag
// (search-api), a tag that is a commit id (ledger-writer).
var notReleased = map[string]bool{"search-api": true, "ledger-writer": true}

func subject(repo, version string) string {
	return strings.Replace(subjects[hashOf(repo, version)%uint64(len(subjects))], "%s", strings.TrimSuffix(repo, "-service"), 1)
}

// Releases is the demo release history: each version a repository runs in
// an environment was deployed by a pipeline whose promote job printed the
// image digests of that version.
type Releases struct {
	Cluster *Cluster

	mu   sync.Mutex
	logs map[int64][]string // digests printed by each job
}

// Deployments implements ports.ReleaseHistory.
func (r *Releases) Deployments(_ context.Context, p ports.ReleaseProject, env string, page, perPage int) ([]ports.Deployment, error) {
	repo := path.Base(p.Path)
	type version struct {
		name    string
		first   time.Time
		digests []string
	}
	var vs []version
	c := r.Cluster
	c.advance()
	c.mu.Lock()
	for _, pod := range c.world.pods[domain.Env(env)] {
		if pod.Labels["app.kubernetes.io/part-of"] != repo || pod.Deleted {
			continue
		}
		v := pod.Labels["app.kubernetes.io/version"]
		i := slices.IndexFunc(vs, func(x version) bool { return x.name == v })
		if i < 0 {
			vs = append(vs, version{name: v, first: pod.Created})
			i = len(vs) - 1
		}
		if pod.Created.Before(vs[i].first) {
			vs[i].first = pod.Created
		}
		for _, ct := range pod.Containers {
			if d := domain.ImageDigest(ct.ImageID); d != "" && !slices.Contains(vs[i].digests, d) {
				vs[i].digests = append(vs[i].digests, d)
			}
		}
	}
	c.mu.Unlock()
	slices.SortFunc(vs, func(a, b version) int { return b.first.Compare(a.first) })
	// Older releases than the ones running, and for notReleased only those.
	for i := range 3 {
		old := version{name: "v0.9." + string(rune('3'-i)), first: c.world.start.Add(-time.Duration(30+i*7) * day)}
		vs = append(vs, old)
	}
	if notReleased[repo] {
		vs = vs[len(vs)-3:]
	}
	var out []ports.Deployment
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.logs == nil {
		r.logs = map[int64][]string{}
	}
	for _, v := range vs {
		id := int64(hashOf(repo, env, v.name) % 1_000_000)
		r.logs[id] = v.digests
		out = append(out, ports.Deployment{
			ID: id, SHA: commitOf(repo, v.name), Ref: "main", FinishedAt: v.first.Add(-2 * time.Minute),
			User: deployers[hashOf(repo, v.name)%uint64(len(deployers))],
			Job:  ports.Job{ID: id, Name: "promote-" + env}, Pipeline: 40000 + id%10000,
			Subject: subject(repo, v.name), Author: "Jane Doe", CommittedAt: v.first.Add(-3 * time.Hour),
		})
	}
	from := (page - 1) * perPage
	if from >= len(out) {
		return nil, nil
	}
	return out[from:min(from+perPage, len(out))], nil
}

// PipelineJobs implements ports.ReleaseHistory: the deployment's own job
// is always the promote job here, so this is never needed.
func (r *Releases) PipelineJobs(context.Context, ports.ReleaseProject, int64) ([]ports.Job, error) {
	return nil, nil
}

// LogFind implements ports.ReleaseHistory.
func (r *Releases) LogFind(_ context.Context, _ ports.ReleaseProject, job int64, needles []string, _ int64) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, n := range needles {
		if slices.Contains(r.logs[job], n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// Git is the demo git: every demo repository's history, synthetic.
type Git struct{}

// RemoteURL implements ports.CommitLog.
func (Git) RemoteURL(_ context.Context, r ports.GitRepo) (string, error) {
	return "https://gitlab.demo.example/acme/" + filepath.Base(r.Dir) + ".git", nil
}

// Refresh implements ports.CommitLog.
func (Git) Refresh(context.Context, ports.GitRepo, bool) error { return nil }

// Commit implements ports.CommitLog.
func (Git) Commit(_ context.Context, r ports.GitRepo, sha string) (ports.Commit, bool, error) {
	repo := filepath.Base(r.Dir)
	for _, v := range versions(repo) {
		if full := commitOf(repo, v); strings.HasPrefix(full, sha) {
			return ports.Commit{SHA: full, Author: "Jane Doe", Subject: subject(repo, v), At: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)}, false, nil
		}
	}
	return ports.Commit{}, false, domain.KindError(domain.ErrNotFound, "unknown commit "+sha)
}

// Tag implements ports.CommitLog: each version is a git tag.
func (Git) Tag(_ context.Context, r ports.GitRepo, tag string) (string, bool, error) {
	repo := filepath.Base(r.Dir)
	if slices.Contains(versions(repo), tag) && !domain.IsCommitID(tag) {
		return commitOf(repo, tag), false, nil
	}
	return "", false, domain.KindError(domain.ErrNotFound, "no tag "+tag)
}

// Count implements ports.CommitLog.
func (Git) Count(_ context.Context, r ports.GitRepo, sha, _ string, paths []string) (int, bool, bool, error) {
	n := int(hashOf(filepath.Base(r.Dir), sha) % 15)
	if len(paths) > 0 {
		n /= 3
	}
	return n, true, false, nil
}

// versions are the versions a demo repository may run.
func versions(repo string) []string {
	for _, r := range repos {
		if r.name == repo {
			return []string{r.version, r.version + "-prev", "v0.9.3", "v0.9.2", "v0.9.1"}
		}
	}
	return nil
}
