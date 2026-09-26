package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// DiscoveredWorkload is a workload found in a repository's manifests.
type DiscoveredWorkload struct {
	Ref    domain.WorkloadRef
	Labels map[string]string
}

// ManifestScanner discovers the workloads a repository deploys to an
// environment by reading its manifests (Kustomize overlays, Helm values…).
// The layout it understands is configuration of the implementation.
type ManifestScanner interface {
	Scan(ctx context.Context, repoDir string, env domain.Env) ([]DiscoveredWorkload, error)
}
