package kubernetes

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// Tests against the local lab (deploy/lab): HUGINN_LAB=1 go test ./internal/adapters/driven/kubernetes

var labScope = ports.Scope{Env: "rec", Context: "kind-huginn", Namespaces: []string{"app-rec"}}

func lab(t *testing.T) *Client {
	t.Helper()
	if os.Getenv("HUGINN_LAB") == "" {
		t.Skip("set HUGINN_LAB=1 to run against the lab cluster (deploy/lab/up.sh)")
	}
	return New(Options{UserAgent: "huginn/test"})
}

type wallClock struct{}

func (wallClock) Now() time.Time                       { return time.Now() }
func (wallClock) NewTicker(time.Duration) ports.Ticker { panic("not used") }
func labPods(t *testing.T, c *Client) []domain.Pod {
	t.Helper()
	pods, err := c.ListPods(context.Background(), labScope, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pods
}

func TestLabClusterContract(t *testing.T) {
	c := lab(t)
	portstest.RunClusterContract(t, func(t *testing.T) portstest.ClusterFixture {
		return portstest.ClusterFixture{Client: c, Scope: labScope, PodLabels: ports.Selector{"app": "payment-service"}}
	})
}

func TestLabLogSourceContract(t *testing.T) {
	c := lab(t)
	// A crash-looping container (its previous instances wrote a line
	// before exiting; instances killed by a node restart may have no log)
	// and a running one never restarted.
	var prev, noPrev *ports.LogRequest
	// A young lab's crashed instance may not be readable yet (the kubelet
	// answers "unable to retrieve container logs"): wait for one.
	for try := 0; try < 30 && prev == nil; try++ {
		if try > 0 {
			time.Sleep(3 * time.Second)
		}
		prev, noPrev = labLogRequests(t, c)
	}
	if prev == nil || noPrev == nil {
		t.Skip("the lab needs a restarted container and a running one never restarted (kubectl rollout restart deploy/payment-worker)")
	}
	portstest.RunLogSourceContract(t, func(t *testing.T) portstest.LogFixture {
		return portstest.LogFixture{Source: c, Clock: wallClock{}, Request: *prev, NoPrevRequest: *noPrev}
	})
}

// labLogRequests picks a crash-looping container whose previous instance
// can be read, and a running container never restarted.
func labLogRequests(t *testing.T, c *Client) (prev, noPrev *ports.LogRequest) {
	for _, p := range labPods(t, c) {
		for _, ct := range p.Containers {
			req := ports.LogRequest{Scope: labScope, Namespace: p.Namespace, Pod: p.Name, Container: ct.Name}
			switch {
			case ct.Init:
			case ct.LastTermination != nil && ct.LastTermination.Reason == "Error" && prev == nil && previousReadable(c, req):
				prev = &req
			case ct.Restarts == 0 && ct.State == domain.ContainerRunning && noPrev == nil:
				noPrev = &req
			}
		}
	}
	return prev, noPrev
}

func previousReadable(c *Client, req ports.LogRequest) bool {
	req.Previous, req.Window = true, domain.TimeWindow{Tail: 10}
	st, err := c.Stream(context.Background(), req)
	if err != nil {
		return false
	}
	n := 0
	for range st.Lines() {
		n++
	}
	return st.Err() == nil && n > 0
}

func TestLabOwnersAndStates(t *testing.T) {
	c := lab(t)
	byOwner := map[string]domain.Pod{}
	for _, p := range labPods(t, c) {
		byOwner[p.OwnerName] = p
	}
	if p, ok := byOwner["payment-service"]; !ok || len(p.Containers) < 2 {
		t.Errorf("payment-service pod: %+v", p)
	}
	if p := byOwner["email-dispatcher"]; p.Reason != "Unschedulable" {
		t.Errorf("pending pod: reason %q", p.Reason)
	}
	if p := byOwner["catalog-indexer"]; p.Restarts() == 0 {
		t.Errorf("crash-looping pod has no restart: %+v", p.Containers)
	}
	ws, err := c.ListWorkloads(context.Background(), labScope)
	if err != nil || len(ws) < 5 {
		t.Fatalf("workloads %d, err %v", len(ws), err)
	}
}

func TestLabNotStarted(t *testing.T) {
	c := lab(t)
	for _, p := range labPods(t, c) {
		if p.OwnerName != "document-renderer" { // ImagePullBackOff
			continue
		}
		_, err := c.Stream(context.Background(), ports.LogRequest{
			Scope: labScope, Namespace: p.Namespace, Pod: p.Name,
			Container: p.Containers[0].Name, Window: domain.TimeWindow{Tail: 10},
		})
		if !errors.Is(err, domain.ErrNotStarted) {
			t.Fatalf("err = %v, want ErrNotStarted", err)
		}
		return
	}
	t.Fatal("no document-renderer pod")
}

func TestLabEvents(t *testing.T) {
	c := lab(t)
	for _, p := range labPods(t, c) {
		if p.OwnerName != "email-dispatcher" {
			continue
		}
		evs, err := c.PodEvents(context.Background(), labScope, p.Namespace, p.Name)
		if err != nil || len(evs) == 0 {
			t.Fatalf("events %v, err %v", evs, err)
		}
		for _, e := range evs {
			if e.LastSeen.IsZero() || e.Count < 1 {
				t.Errorf("event not normalized: %+v", e)
			}
			if e.Reason != "FailedScheduling" && !strings.Contains(e.Message, p.Name) {
				t.Logf("event %s: %s", e.Reason, e.Message)
			}
		}
		return
	}
	t.Fatal("no email-dispatcher pod")
}

func TestLabForbiddenNamespace(t *testing.T) {
	c := lab(t)
	restricted := func(ns string) ports.Scope {
		return ports.Scope{Env: "restricted", Context: "huginn-restricted", Namespaces: []string{ns}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := c.WatchWorkloads(ctx, restricted("app-rec")); err != nil { // CronJobs are not readable: skipped
		t.Fatalf("allowed namespace: %v", err)
	}
	if _, err := c.WatchWorkloads(ctx, restricted("app-dev")); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("forbidden namespace: %v", err)
	}
	if _, err := c.ListPods(ctx, ports.Scope{Env: "x", Context: "no-such-context", Namespaces: []string{"a"}}, nil); !errors.Is(err, domain.ErrConfig) {
		t.Fatalf("unknown context: %v", err)
	}
}
