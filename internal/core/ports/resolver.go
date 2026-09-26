package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// RepoResolver maps the workloads observed in an environment to the code
// repositories that own them. Workloads no resolver claims are returned
// unassigned so they can still be shown.
type RepoResolver interface {
	Resolve(ctx context.Context, env domain.Env, workloads []domain.Workload) (repos []domain.Repo, unassigned []domain.Workload, err error)
}
