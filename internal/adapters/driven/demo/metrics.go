package demo

import (
	"context"
	"math"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// metricsWindow and metricsStep imitate metrics-server: a sample is an
// average over one minute, refreshed every 15 seconds.
const (
	metricsWindow = time.Minute
	metricsStep   = 15 * time.Second
)

// PodUsage implements ports.MetricsSource. The usage is deterministic: it
// depends on the seed, the pod and the clock only. Scenarios: a repository
// whose pods are close to their memory limit (the OOM one), repositories far
// below their requests, containers with no limit (the vault agent), and pods
// without a sample (not running, or running for less than a window).
func (c *Cluster) PodUsage(ctx context.Context, scope ports.Scope) ([]domain.PodUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.advance()
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Clock.Now()
	at := now.Truncate(metricsStep)
	var out []domain.PodUsage
	for _, p := range c.podsLocked(scope, nil) {
		if p.Phase != domain.PodRunning || p.Deleted || now.Sub(p.Started) < metricsWindow {
			continue
		}
		spec := c.world.specs[p.Namespace+"/"+p.Name]
		u := domain.PodUsage{Namespace: p.Namespace, Name: p.Name, SampledAt: at, Window: metricsWindow}
		for _, ct := range p.Containers {
			if ct.Init || ct.State != domain.ContainerRunning {
				continue
			}
			u.Containers = append(u.Containers, domain.ContainerUsage{Name: ct.Name, Use: c.usageOf(p, spec, ct, now)})
		}
		out = append(out, u)
	}
	return out, nil
}

// usageOf is what a container uses at now: a share of its memory limit and
// of its CPU request, moving slowly around a base fixed by the repository.
func (c *Cluster) usageOf(p domain.Pod, spec podSpec, ct domain.Container, now time.Time) domain.Amount {
	h := hashOf(c.opts.Seed, spec.repo.name)
	cpuShare := 0.15 + float64(h%60)/100 // 15% to 74% of the request
	memShare := 0.25 + float64(h/7%45)/100
	switch {
	case spec.cond == oomKilled:
		memShare = 0.93 + float64(hashOf(p.Name)%5)/100
	case h%5 == 0: // over-provisioned
		cpuShare, memShare = 0.05, 0.12
	}
	if ct.Name != spec.workload { // a sidecar
		cpuShare, memShare = 0.3, 0.45
	}
	wave := 1 + 0.08*math.Sin(float64(now.Unix())/90+float64(hashOf(p.Name)%628)/100)
	am := ct.Amounts
	mem := min(float64(am.Limit.MemBytes)*memShare*wave, float64(am.Limit.MemBytes)*0.99)
	if am.Limit.MemBytes == 0 { // no limit: base it on the request
		mem = float64(am.Request.MemBytes) * memShare * wave
	}
	return domain.Amount{CPUMilli: int64(float64(am.Request.CPUMilli) * cpuShare * wave), MemBytes: int64(mem)}
}
