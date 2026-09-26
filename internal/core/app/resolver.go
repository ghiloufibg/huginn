package app

import (
	"context"
	"slices"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// MappingResolver attributes workloads listed explicitly in configuration.
// A reference matches on environment and name, and on namespace and kind
// when those are set.
type MappingResolver struct {
	Repos []domain.Repo
}

// Resolve implements ports.RepoResolver.
func (r MappingResolver) Resolve(_ context.Context, env domain.Env, ws []domain.Workload) ([]domain.Repo, []domain.Workload, error) {
	name := func(w domain.Workload) string {
		for _, repo := range r.Repos {
			if slices.ContainsFunc(repo.Workloads, func(ref domain.WorkloadRef) bool { return matchesRef(ref, env, w.Ref) }) {
				return repo.Name
			}
		}
		return ""
	}
	return split(ws, name)
}

func matchesRef(ref domain.WorkloadRef, env domain.Env, w domain.WorkloadRef) bool {
	return ref.Env == env && ref.Name == w.Name &&
		(ref.Namespace == "" || ref.Namespace == w.Namespace) &&
		(ref.Kind == "" || ref.Kind == w.Kind)
}

// LabelResolver attributes a workload to the repository named by the first
// of Keys present in its labels, then its annotations.
type LabelResolver struct {
	Keys []string
}

// Resolve implements ports.RepoResolver.
func (r LabelResolver) Resolve(_ context.Context, _ domain.Env, ws []domain.Workload) ([]domain.Repo, []domain.Workload, error) {
	name := func(w domain.Workload) string {
		for _, k := range r.Keys {
			if v := w.Labels[k]; v != "" {
				return v
			}
			if v := w.Annotations[k]; v != "" {
				return v
			}
		}
		return ""
	}
	return split(ws, name)
}

// ChainResolver asks each resolver in turn about the workloads the previous
// ones did not attribute; the first match wins. Repositories found by
// several resolvers are merged.
type ChainResolver struct {
	Resolvers []ports.RepoResolver
}

// Resolve implements ports.RepoResolver.
func (c ChainResolver) Resolve(ctx context.Context, env domain.Env, ws []domain.Workload) ([]domain.Repo, []domain.Workload, error) {
	byName := map[string]*domain.Repo{}
	var order []string
	rest := ws
	for _, r := range c.Resolvers {
		if len(rest) == 0 {
			break
		}
		repos, left, err := r.Resolve(ctx, env, rest)
		if err != nil {
			return nil, nil, err
		}
		for _, repo := range repos {
			if byName[repo.Name] == nil {
				byName[repo.Name] = &domain.Repo{Name: repo.Name}
				order = append(order, repo.Name)
			}
			byName[repo.Name].Workloads = append(byName[repo.Name].Workloads, repo.Workloads...)
		}
		rest = left
	}
	out := make([]domain.Repo, 0, len(order))
	for _, n := range order {
		repo := *byName[n]
		sortPrimaryFirst(&repo)
		out = append(out, repo)
	}
	return out, rest, nil
}

// sortPrimaryFirst puts the workload named like the repository first (it
// gives the version), then the others by name.
func sortPrimaryFirst(r *domain.Repo) {
	slices.SortStableFunc(r.Workloads, func(a, b domain.WorkloadRef) int {
		ap, bp := a.Name == r.Name, b.Name == r.Name
		switch {
		case ap && !bp:
			return -1
		case bp && !ap:
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}

// split groups workloads by the repository name returned by name, in
// first-seen order; workloads with an empty name are returned unassigned.
func split(ws []domain.Workload, name func(domain.Workload) string) ([]domain.Repo, []domain.Workload, error) {
	var repos []domain.Repo
	var rest []domain.Workload
	index := map[string]int{}
	for _, w := range ws {
		n := name(w)
		if n == "" {
			rest = append(rest, w)
			continue
		}
		i, ok := index[n]
		if !ok {
			i = len(repos)
			index[n] = i
			repos = append(repos, domain.Repo{Name: n})
		}
		repos[i].Workloads = append(repos[i].Workloads, w.Ref)
	}
	return repos, rest, nil
}
