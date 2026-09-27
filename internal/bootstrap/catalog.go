package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

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

// scopes returns the cluster scope of each configured environment. A
// namespace_from reference is decrypted the first time its environment is
// opened (sops runs only then), and remembered.
func scopes(c *config.Config, secrets ports.SecretsProvider) app.ScopeFunc {
	var mu sync.Mutex
	resolved := map[domain.Env][]string{}
	return func(ctx context.Context, env domain.Env) (ports.Scope, error) {
		e, ok := c.Environments.ByName[env.String()]
		if !ok {
			return ports.Scope{}, fmt.Errorf("environment %q is not configured", env)
		}
		sc := ports.Scope{Env: env, Context: e.Context, Namespaces: e.Namespaces}
		if e.NamespaceFrom == "" {
			return sc, nil
		}
		mu.Lock()
		defer mu.Unlock()
		if ns, ok := resolved[env]; ok {
			sc.Namespaces = ns
			return sc, nil
		}
		ns, err := namespaceFrom(ctx, secrets, e.NamespaceFrom)
		if err != nil {
			return ports.Scope{}, fmt.Errorf("%s: environments.%s.namespace_from: %w", config.FileEnvironments, env, err)
		}
		resolved[env], sc.Namespaces = ns, ns
		return sc, nil
	}
}

// namespaceFrom reads sops:<file>#<key> (validated by the config loader).
// A relative file is relative to the config folder; "~/" is the home folder.
func namespaceFrom(ctx context.Context, secrets ports.SecretsProvider, ref string) ([]string, error) {
	file, key, _ := strings.Cut(strings.TrimPrefix(ref, "sops:"), "#")
	v, err := secrets.Get(ctx, ports.SecretRef{Source: config.ExpandHome(file), Key: key})
	if err != nil {
		return nil, err
	}
	ns := strings.TrimSpace(v.Reveal())
	if ns == "" {
		return nil, fmt.Errorf("%s is empty: %w", key, domain.ErrConfig)
	}
	return []string{ns}, nil
}

func newCatalog(c *config.Config, sc app.ScopeFunc, cluster ports.ClusterClient, clock ports.Clock, filter domain.ContainerFilter, log *slog.Logger) *app.Catalog {
	return &app.Catalog{
		Cluster: cluster, Resolver: resolverChain(c, log), Scopes: sc,
		Filter: filter, Clock: clock, Log: log, Standalone: c.Services.ShowStandalone(),
	}
}
