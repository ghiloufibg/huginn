package bootstrap

import (
	"log/slog"

	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// containerFilter hides the sidecars listed in containers.yaml.
func containerFilter(c *config.Config) domain.ContainerFilter {
	return domain.ContainerFilter{Deny: c.Containers.Hide, Allow: c.Containers.AlwaysShow, IncludeInit: c.Containers.ShowInit}
}

// resolverChain builds the repository resolvers in services.yaml resolve
// order.
func resolverChain(c *config.Config, log *slog.Logger) ports.RepoResolver {
	var chain app.ChainResolver
	for _, name := range c.Services.Resolve {
		switch name {
		case "explicit":
			chain.Resolvers = append(chain.Resolvers, app.MappingResolver{Repos: mappedRepos(c)})
		case "labels":
			chain.Resolvers = append(chain.Resolvers, app.LabelResolver{Keys: c.Services.LabelKeys})
		case "manifests":
			log.Debug("manifest resolver not available yet (milestone M4); skipped")
		}
	}
	return chain
}

// mappedRepos converts the explicit list to domain repositories.
func mappedRepos(c *config.Config) []domain.Repo {
	var out []domain.Repo
	for _, r := range c.Services.Explicit {
		repo := domain.Repo{Name: r.Repo}
		for _, w := range r.Workloads {
			repo.Workloads = append(repo.Workloads, domain.WorkloadRef{
				Env: domain.Env(w.Env), Namespace: w.Namespace, Kind: domain.WorkloadKind(w.Kind), Name: w.Name,
			})
		}
		out = append(out, repo)
	}
	return out
}

// scopes returns the cluster scope of each configured environment.
func scopes(c *config.Config) func(domain.Env) (ports.Scope, bool) {
	return func(env domain.Env) (ports.Scope, bool) {
		e, ok := c.Environments.ByName[env.String()]
		return ports.Scope{Env: env, Context: e.Context, Namespaces: e.Namespaces}, ok
	}
}

func newCatalog(c *config.Config, cluster ports.ClusterClient, clock ports.Clock, filter domain.ContainerFilter, log *slog.Logger) *app.Catalog {
	return &app.Catalog{
		Cluster: cluster, Resolver: resolverChain(c, log), Scopes: scopes(c),
		Filter: filter, Clock: clock, Log: log,
	}
}
