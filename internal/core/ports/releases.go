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

// GitRemotes finds the remote of a local clone by reading its
// .git/config as a file: no git command runs.
type GitRemotes interface {
	// GitRemote returns the URL of remote in the clone at dir; ErrNotFound
	// when dir is not a clone or has no such remote.
	GitRemote(ctx context.Context, dir, remote string) (string, error)
}

// Commit is what a project's history says about one commit.
type Commit struct {
	SHA, Author, Subject string
	At                   time.Time
}

// CommitLog reads the history of a hosted project through its API, read
// only. Errors are domain kinds: ErrUnauthorized, ErrForbidden,
// ErrNotFound (no such commit, tag or branch), ErrUnreachable.
type CommitLog interface {
	// Commit resolves sha, possibly abbreviated.
	Commit(ctx context.Context, p ReleaseProject, sha string) (Commit, error)
	// Tag returns the commit a tag points to.
	Tag(ctx context.Context, p ReleaseProject, tag string) (sha string, err error)
	// DefaultBranch returns the project's default branch.
	DefaultBranch(ctx context.Context, p ReleaseProject) (string, error)
	// BranchTip returns the commit a branch points to.
	BranchTip(ctx context.Context, p ReleaseProject, branch string) (sha string, err error)
	// Count returns how many commits of branch sha lacks (only those
	// touching paths, when given), whether that number is a lower bound
	// (capped), and whether sha is on branch.
	Count(ctx context.Context, p ReleaseProject, sha, branch string, paths []string) (behind int, capped, onBranch bool, err error)
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
