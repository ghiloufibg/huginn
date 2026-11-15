package portstest

import (
	"context"
	"sync"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FakeMetrics is an in-memory MetricsSource whose samples tests set directly.
type FakeMetrics struct {
	mu      sync.Mutex
	samples map[string][]domain.PodUsage
	errs    map[string]error
	calls   map[string]int
	// Err, when set, is returned by every call.
	Err error
}

// NewFakeMetrics returns a source without samples.
func NewFakeMetrics() *FakeMetrics {
	return &FakeMetrics{samples: map[string][]domain.PodUsage{}, errs: map[string]error{}, calls: map[string]int{}}
}

// Set replaces the samples of a namespace.
func (f *FakeMetrics) Set(namespace string, us ...domain.PodUsage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.samples[namespace] = us
}

// SetErr makes calls for a namespace fail with err (nil clears it).
func (f *FakeMetrics) SetErr(namespace string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.errs, namespace)
		return
	}
	f.errs[namespace] = err
}

// Calls returns how many times a namespace was read.
func (f *FakeMetrics) Calls(namespace string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[namespace]
}

// PodUsage implements ports.MetricsSource.
func (f *FakeMetrics) PodUsage(ctx context.Context, scope ports.Scope) ([]domain.PodUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.PodUsage
	for _, ns := range scope.Namespaces {
		f.calls[ns]++
		if err := f.errs[ns]; err != nil {
			return nil, err
		}
		out = append(out, f.samples[ns]...)
	}
	return out, f.Err
}

// MetricsFixture is what a MetricsSource implementation provides to the
// contract suite: a source and a scope with at least one pod that has a
// sample.
type MetricsFixture struct {
	Source ports.MetricsSource
	Scope  ports.Scope
}

// RunMetricsSourceContract checks the behavior every MetricsSource must have.
func RunMetricsSourceContract(t *testing.T, newFixture func(t *testing.T) MetricsFixture) {
	t.Helper()
	ctx := context.Background()

	t.Run("samples are in scope and well formed", func(t *testing.T) {
		f := newFixture(t)
		us, err := f.Source.PodUsage(ctx, f.Scope)
		if err != nil || len(us) == 0 {
			t.Fatalf("PodUsage: %d samples, %v", len(us), err)
		}
		seen := map[domain.PodKey]bool{}
		for _, u := range us {
			if !contains(f.Scope.Namespaces, u.Namespace) {
				t.Errorf("sample %s/%s out of scope", u.Namespace, u.Name)
			}
			if u.Name == "" || u.SampledAt.IsZero() {
				t.Errorf("sample %+v lacks a name or a timestamp", u)
			}
			if seen[u.Key()] {
				t.Errorf("pod %s/%s sampled twice", u.Namespace, u.Name)
			}
			seen[u.Key()] = true
			for _, c := range u.Containers {
				if c.Use.CPUMilli < 0 || c.Use.MemBytes < 0 {
					t.Errorf("negative usage in %s/%s: %+v", u.Namespace, u.Name, c)
				}
			}
		}
	})

	t.Run("a cancelled context fails the call", func(t *testing.T) {
		f := newFixture(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := f.Source.PodUsage(cctx, f.Scope); err == nil {
			t.Error("PodUsage with a cancelled context returned no error")
		}
	})
}
