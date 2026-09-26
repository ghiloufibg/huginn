package app

import (
	"context"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func w(name string, labels map[string]string) domain.Workload {
	return domain.Workload{Ref: domain.WorkloadRef{Env: domain.Env("rec"), Namespace: "app-rec", Kind: domain.KindDeployment, Name: name}, Labels: labels, Annotations: map[string]string{}}
}

func TestChainResolver(t *testing.T) {
	ws := []domain.Workload{
		w("payment-worker", map[string]string{"app.kubernetes.io/part-of": "payment-service"}),
		w("payment-service", map[string]string{"app.kubernetes.io/part-of": "payment-service"}),
		w("legacy-batch", map[string]string{"team": "core"}),
		w("billing-api", map[string]string{"app.kubernetes.io/part-of": "wrong", "app": "billing-api"}),
		w("orphan", nil),
	}
	ws[2].Annotations["source-repo"] = "batch-jobs"
	chain := ChainResolver{Resolvers: []ports.RepoResolver{
		MappingResolver{Repos: []domain.Repo{{Name: "billing", Workloads: []domain.WorkloadRef{{Env: domain.Env("rec"), Name: "billing-api"}}}}},
		LabelResolver{Keys: []string{"app.kubernetes.io/part-of", "source-repo"}},
	}}
	repos, rest, err := chain.Resolve(context.Background(), domain.Env("rec"), ws)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, r := range repos {
		for _, ref := range r.Workloads {
			got[r.Name] = append(got[r.Name], ref.Name)
		}
	}
	if len(got) != 3 || got["billing"][0] != "billing-api" || got["batch-jobs"][0] != "legacy-batch" {
		t.Fatalf("repos: %v", got)
	}
	if p := got["payment-service"]; len(p) != 2 || p[0] != "payment-service" {
		t.Fatalf("primary workload must come first: %v", p)
	}
	if len(rest) != 1 || rest[0].Ref.Name != "orphan" {
		t.Fatalf("unassigned: %v", rest)
	}
}

func TestMappingResolverMatchesEnvNamespaceKind(t *testing.T) {
	r := MappingResolver{Repos: []domain.Repo{{Name: "x", Workloads: []domain.WorkloadRef{
		{Env: domain.Env("prd"), Name: "api"},
		{Env: domain.Env("rec"), Name: "api", Namespace: "other"},
		{Env: domain.Env("rec"), Name: "api", Kind: domain.KindStatefulSet},
	}}}}
	repos, rest, _ := r.Resolve(context.Background(), domain.Env("rec"), []domain.Workload{w("api", nil)})
	if len(repos) != 0 || len(rest) != 1 {
		t.Fatalf("nothing should match: %v %v", repos, rest)
	}
}
