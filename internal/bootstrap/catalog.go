package bootstrap

import (
	"log/slog"
	"slices"

	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// containerFilter combines the built-in sidecar denylist with the
// configured one.
func containerFilter(c *config.Config) domain.ContainerFilter {
	deny := slices.Clone(config.DefaultContainerDenylist)
	for _, d := range c.Containers.Denylist {
		if !slices.Contains(deny, d) {
			deny = append(deny, d)
		}
	}
	return domain.ContainerFilter{Deny: deny, Allow: c.Containers.Allowlist, IncludeInit: c.Containers.IncludeInit}
}

// resolverChain builds the repository resolvers in resolver.order.
func resolverChain(c *config.Config, log *slog.Logger) ports.RepoResolver {
	var chain app.ChainResolver
	for _, name := range c.Resolver.Order {
		switch name {
		case "config":
			chain.Resolvers = append(chain.Resolvers, app.MappingResolver{Repos: mappedRepos(c)})
		case "labels":
			chain.Resolvers = append(chain.Resolvers, app.LabelResolver{Keys: c.Resolver.LabelKeys})
		case "manifests":
			log.Debug("manifest resolver not available yet (milestone M4); skipped")
		}
	}
	return chain
}

// mappedRepos converts the explicit repos: section to domain repositories.
func mappedRepos(c *config.Config) []domain.Repo {
	var out []domain.Repo
	for _, r := range c.Repos {
		repo := domain.Repo{Name: r.Name}
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
		e, ok := c.Environments[env.String()]
		return ports.Scope{Env: env, Context: e.Context, Namespaces: e.Namespaces}, ok
	}
}

func newCatalog(c *config.Config, cluster ports.ClusterClient, clock ports.Clock, filter domain.ContainerFilter, log *slog.Logger) *app.Catalog {
	return &app.Catalog{
		Cluster: cluster, Resolver: resolverChain(c, log), Scopes: scopes(c),
		Filter: filter, Clock: clock, Log: log,
	}
}
