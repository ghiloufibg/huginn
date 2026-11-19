package ports

import (
	"context"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// ReleaseProject names a project of a release history: the API base URL
// of its server and the project's full path ("group/project").
type ReleaseProject struct {
	BaseURL, Path string
}

// Deployment is one successful deployment of a project to an environment,
// as a CI system recorded it.
type Deployment struct {
	ID  int64
	SHA string
	Ref string
	// FinishedAt is when it finished; User who ran it.
	FinishedAt time.Time
	User       string
	// Job is the job that deployed it; Pipeline the pipeline holding it.
	Job      Job
	Pipeline int64
	// Commit describes SHA.
	Subject, Author string
	CommittedAt     time.Time
}

// Job is a CI job.
type Job struct {
	ID   int64
	Name string
}

// ReleaseHistory reads the deployments a CI system recorded. It is read
// only. Errors are domain kinds: ErrUnauthorized (no or a refused token),
// ErrForbidden, ErrNotFound (no such project), ErrUnreachable.
type ReleaseHistory interface {
	// Deployments lists the successful deployments of p to env, newest
	// first, one page (1-based) of perPage at a time.
	Deployments(ctx context.Context, p ReleaseProject, env string, page, perPage int) ([]Deployment, error)
	// PipelineJobs lists the jobs of a pipeline (the last run of each).
	PipelineJobs(ctx context.Context, p ReleaseProject, pipeline int64) ([]Job, error)
	// LogFind streams a job's log and returns which of needles it
	// contains, stopping once all are found or after limit bytes.
	LogFind(ctx context.Context, p ReleaseProject, job int64, needles []string, limit int64) ([]string, error)
}

// GitRepo is a local clone and the name of the remote to read.
type GitRepo struct {
	Dir, Remote string
}

// Commit is what git says about one commit.
type Commit struct {
	SHA, Author, Subject string
	At                   time.Time
}

// CommitLog reads commits with git, read only on the remote and on the
// user's clone. Implementations keep their own copy of each remote, so
// the clone is never written to; when the remote cannot be reached they
// read the clone instead and say so with stale.
type CommitLog interface {
	// RemoteURL returns the URL of the clone's remote; ErrNotFound when
	// dir is not a clone or has no such remote.
	RemoteURL(ctx context.Context, r GitRepo) (string, error)
	// Refresh brings the copy of the remote up to date. trees asks for a
	// copy able to tell which paths a commit touched (Count with paths).
	Refresh(ctx context.Context, r GitRepo, trees bool) error
	// Commit resolves sha, possibly abbreviated; ErrNotFound when unknown.
	Commit(ctx context.Context, r GitRepo, sha string) (c Commit, stale bool, err error)
	// Tag returns the commit a tag points to; ErrNotFound when none.
	Tag(ctx context.Context, r GitRepo, tag string) (sha string, stale bool, err error)
	// Count returns how many commits of ref sha lacks (only those touching
	// paths, when given), and whether sha is an ancestor of ref.
	Count(ctx context.Context, r GitRepo, sha, ref string, paths []string) (behind int, onRef, stale bool, err error)
}

// ReleaseQuery asks for the releases running the images of a repository's
// workloads in an environment.
type ReleaseQuery struct {
	Env    domain.Env
	Repo   string
	Images []domain.WorkloadImage
}

// Releases is the driving port behind the COMMIT line of the services
// preview: which commit runs each image, proven by the release history
// when possible. Results are keyed by digest; images without a digest are
// skipped.
type Releases interface {
	Running(ctx context.Context, q ReleaseQuery) (map[string]domain.RunningRelease, error)
}
