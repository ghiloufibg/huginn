package kubernetes

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// PodUsage implements ports.MetricsSource: one list of pods.metrics.k8s.io
// per namespace of the scope (read-only, like every call of this adapter).
func (c *Client) PodUsage(ctx context.Context, scope ports.Scope) ([]domain.PodUsage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cs, err := c.clientset(scope.Context)
	if err != nil {
		return nil, err
	}
	m, ok := cs.(MetricsAPI)
	if !ok {
		return nil, domain.KindError(domain.ErrMetricsUnavailable, "this client reads no usage metrics")
	}
	namespaces := scope.Namespaces
	if len(namespaces) == 0 {
		namespaces = []string{metav1.NamespaceAll}
	}
	var out []domain.PodUsage
	for _, ns := range namespaces {
		list, err := m.MetricsV1beta1().PodMetricses(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, mapMetricsErr(ns, err)
		}
		for i := range list.Items {
			out = append(out, podUsage(&list.Items[i]))
		}
	}
	return out, nil
}

// mapMetricsErr classifies a failed list: a 404 means the API is not served
// at all (no metrics-server), a 503 that it is not ready; both are "no
// metrics here" and not a failure of the cluster. The rest is mapped like
// any other call.
func mapMetricsErr(ns string, err error) error {
	switch {
	case apierrors.IsNotFound(err):
		return domain.KindError(domain.ErrMetricsUnavailable, "metrics.k8s.io is not served (is metrics-server installed?)")
	case apierrors.IsServiceUnavailable(err):
		return domain.KindError(domain.ErrMetricsUnavailable, "metrics-server is not ready")
	}
	if ns == "" || ns == metav1.NamespaceAll {
		ns = "all namespaces"
	}
	return mapErr(fmt.Errorf("list pod metrics in %s: %w", ns, err))
}

// podUsage converts a PodMetrics: CPU in millicores, memory in bytes.
func podUsage(m *metricsapi.PodMetrics) domain.PodUsage {
	u := domain.PodUsage{
		Namespace: m.Namespace, Name: m.Name,
		SampledAt: m.Timestamp.Time, Window: m.Window.Duration,
		Containers: make([]domain.ContainerUsage, 0, len(m.Containers)),
	}
	for _, c := range m.Containers {
		cpu, mem := c.Usage["cpu"], c.Usage["memory"]
		u.Containers = append(u.Containers, domain.ContainerUsage{
			Name: c.Name, Use: domain.Amount{CPUMilli: cpu.MilliValue(), MemBytes: mem.Value()},
		})
	}
	return u
}
