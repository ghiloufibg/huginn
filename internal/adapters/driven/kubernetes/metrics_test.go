package kubernetes

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stesting "k8s.io/client-go/testing"
	metricsapi "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
	metricsv1beta1 "k8s.io/metrics/pkg/client/clientset/versioned/typed/metrics/v1beta1"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// withMetrics is a clientset that also serves the metrics API.
type withMetrics struct {
	API
	m *metricsfake.Clientset
}

func (w withMetrics) MetricsV1beta1() metricsv1beta1.MetricsV1beta1Interface {
	return w.m.MetricsV1beta1()
}

func podMetrics(namespace, name string, containers ...metricsapi.ContainerMetrics) *metricsapi.PodMetrics {
	return &metricsapi.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Timestamp:  metav1.NewTime(t0), Window: metav1.Duration{Duration: time.Minute},
		Containers: containers,
	}
}

func cm(name, cpu, mem string) metricsapi.ContainerMetrics {
	return metricsapi.ContainerMetrics{Name: name, Usage: corev1.ResourceList{
		corev1.ResourceCPU: resource.MustParse(cpu), corev1.ResourceMemory: resource.MustParse(mem),
	}}
}

// metricsFixture serves the given pods through a list reactor, restricted to
// the namespace of the request: the fake tracker cannot name the resource of
// PodMetrics.
func metricsFixture() (withMetrics, *metricsfake.Clientset) {
	all := []metricsapi.PodMetrics{
		*podMetrics(ns, "api-7d9f-a1", cm("app", "250m", "128Mi"), cm("istio-proxy", "10m", "40Mi")),
		*podMetrics(ns, "api-7d9f-b2", cm("app", "1", "1Gi")),
		*podMetrics("other", "x-1", cm("app", "1", "1Mi")),
	}
	m := metricsfake.NewSimpleClientset()
	m.PrependReactor("list", "pods", func(a k8stesting.Action) (bool, runtime.Object, error) {
		list := &metricsapi.PodMetricsList{}
		for _, p := range all {
			if a.GetNamespace() == "" || a.GetNamespace() == p.Namespace {
				list.Items = append(list.Items, p)
			}
		}
		return true, list, nil
	})
	return withMetrics{API: fixture(), m: m}, m
}

func TestMetricsSourceContract(t *testing.T) {
	portstest.RunMetricsSourceContract(t, func(t *testing.T) portstest.MetricsFixture {
		cs, _ := metricsFixture()
		return portstest.MetricsFixture{Source: client(cs), Scope: scope}
	})
}

func TestPodUsageConversion(t *testing.T) {
	cs, _ := metricsFixture()
	got, err := client(cs).PodUsage(context.Background(), scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d samples, want the 2 pods of the scope", len(got))
	}
	i := slices.IndexFunc(got, func(u domain.PodUsage) bool { return u.Name == "api-7d9f-a1" })
	u := got[i]
	if !u.SampledAt.Equal(t0) || u.Window != time.Minute || u.Namespace != ns {
		t.Errorf("sample %+v", u)
	}
	want := []domain.ContainerUsage{
		{Name: "app", Use: domain.Amount{CPUMilli: 250, MemBytes: 128 << 20}},
		{Name: "istio-proxy", Use: domain.Amount{CPUMilli: 10, MemBytes: 40 << 20}},
	}
	if !slices.Equal(u.Containers, want) {
		t.Errorf("containers %+v, want %+v", u.Containers, want)
	}
	if b := got[1-i]; b.Containers[0].Use != (domain.Amount{CPUMilli: 1000, MemBytes: 1 << 30}) {
		t.Errorf("one core and 1Gi read as %+v", b.Containers[0].Use)
	}
}

func TestPodUsageErrors(t *testing.T) {
	gr := schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"}
	for _, c := range []struct {
		name string
		err  error
		want error
	}{
		{"not served", apierrors.NewNotFound(gr, ""), domain.ErrMetricsUnavailable},
		{"not ready", apierrors.NewServiceUnavailable("no endpoints"), domain.ErrMetricsUnavailable},
		{"forbidden", apierrors.NewForbidden(gr, "", errors.New("RBAC")), domain.ErrForbidden},
		{"unauthorized", apierrors.NewUnauthorized("token"), domain.ErrUnauthorized},
	} {
		cs, m := metricsFixture()
		m.PrependReactor("list", "pods", func(k8stesting.Action) (bool, runtime.Object, error) { return true, nil, c.err })
		_, err := client(cs).PodUsage(context.Background(), scope)
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
}

func TestPodUsageWithoutMetricsClient(t *testing.T) {
	_, err := client(fixture()).PodUsage(context.Background(), scope)
	if !errors.Is(err, domain.ErrMetricsUnavailable) {
		t.Errorf("got %v", err)
	}
}

// TestMetricsReadOnly checks the only call made to the metrics API is a list.
func TestMetricsReadOnly(t *testing.T) {
	cs, m := metricsFixture()
	_, _ = client(cs).PodUsage(context.Background(), scope)
	acts := m.Actions()
	if len(acts) == 0 {
		t.Fatal("no call to the metrics API")
	}
	for _, a := range acts {
		if a.GetVerb() != "list" || a.GetResource().Resource != "pods" {
			t.Errorf("%s on %s", a.GetVerb(), a.GetResource().Resource)
		}
	}
}

func TestContainerAmounts(t *testing.T) {
	p := pod("api-7d9f-a1", "api", "7d9f", nil)
	p.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m"), corev1.ResourceMemory: resource.MustParse("256Mi")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
	}
	c := toPod("rec", p).Containers[0]
	want := domain.ResourceAmounts{
		Request: domain.Amount{CPUMilli: 250, MemBytes: 256 << 20},
		Limit:   domain.Amount{MemBytes: 512 << 20},
	}
	if c.Amounts != want {
		t.Errorf("amounts %+v, want %+v", c.Amounts, want)
	}
	if c.Resources.MemoryLimit != "512Mi" {
		t.Errorf("display strings changed: %+v", c.Resources)
	}
	var _ ports.MetricsSource = (*Client)(nil)
}
