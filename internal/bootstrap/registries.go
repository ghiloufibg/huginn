package bootstrap

import (
	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/kubernetes"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Cluster is what a cluster adapter provides: state and logs.
type Cluster interface {
	ports.ClusterClient
	ports.LogSource
}

// ClusterFactory builds a cluster adapter from the configuration.
type ClusterFactory func(c *config.Config, clock ports.Clock) (Cluster, error)

// clusterRegistry lists the cluster adapters selectable with
// the --demo flag (kubernetes otherwise). Adding one is a new package plus a line here.
func clusterRegistry() *ports.Registry[ClusterFactory] {
	r := ports.NewRegistry[ClusterFactory]("cluster client")
	r.Register("demo", func(c *config.Config, clock ports.Clock) (Cluster, error) {
		ns := map[domain.Env]string{}
		for name, e := range c.Environments.ByName {
			if len(e.Namespaces) > 0 {
				ns[domain.Env(name)] = e.Namespaces[0]
			} else {
				ns[domain.Env(name)] = "demo-" + name
			}
		}
		h := c.Huginn
		return demo.New(demo.Options{Seed: h.Demo.Seed, Rate: h.Demo.Rate, Namespaces: ns, Clock: clock, RolloutEnv: domain.Env(h.DefaultEnv)}), nil
	})
	r.Register("kubernetes", func(*config.Config, ports.Clock) (Cluster, error) {
		return kubernetes.New(), nil
	})
	return r
}
