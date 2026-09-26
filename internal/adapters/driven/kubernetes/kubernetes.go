// Package kubernetes will implement ports.ClusterClient and ports.LogSource
// with client-go (milestone M4). Until then it is registered so that
// configuration selecting it fails with a clear message.
package kubernetes

import (
	"context"
	"fmt"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Client is the placeholder Kubernetes adapter.
type Client struct{}

// New returns the placeholder client.
func New() *Client { return &Client{} }

var errPending = fmt.Errorf("the kubernetes cluster client arrives in milestone M4; run with --demo for now: %w", domain.ErrNotImplemented)

// ListWorkloads is not implemented yet.
func (*Client) ListWorkloads(context.Context, ports.Scope) ([]domain.Workload, error) {
	return nil, errPending
}

// WatchWorkloads is not implemented yet.
func (*Client) WatchWorkloads(context.Context, ports.Scope) (<-chan ports.WorkloadEvent, error) {
	return nil, errPending
}

// ListPods is not implemented yet.
func (*Client) ListPods(context.Context, ports.Scope, ports.Selector) ([]domain.Pod, error) {
	return nil, errPending
}

// WatchPods is not implemented yet.
func (*Client) WatchPods(context.Context, ports.Scope, ports.Selector) (<-chan domain.PodEvent, error) {
	return nil, errPending
}

// PodEvents is not implemented yet.
func (*Client) PodEvents(context.Context, ports.Scope, string, string) ([]domain.Event, error) {
	return nil, errPending
}

// Stream is not implemented yet.
func (*Client) Stream(context.Context, ports.LogRequest) (ports.LogStream, error) {
	return nil, errPending
}
