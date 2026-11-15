package portstest

import (
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

func TestFakeMetricsContract(t *testing.T) {
	RunMetricsSourceContract(t, func(t *testing.T) MetricsFixture {
		m := NewFakeMetrics()
		m.Set("app-rec", domain.PodUsage{
			Namespace: "app-rec", Name: "api-1", SampledAt: t0, Window: time.Minute,
			Containers: []domain.ContainerUsage{{Name: "app", Use: domain.Amount{CPUMilli: 120, MemBytes: 1 << 29}}},
		})
		m.Set("other", domain.PodUsage{Namespace: "other", Name: "x", SampledAt: t0})
		return MetricsFixture{Source: m, Scope: ports.Scope{Env: domain.Env("rec"), Namespaces: []string{"app-rec"}}}
	})
}
