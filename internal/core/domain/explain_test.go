package domain

import (
	"testing"
	"time"
)

func TestExplain(t *testing.T) {
	crash := Container{
		Name: "api", State: ContainerWaiting, Reason: "CrashLoopBackOff", Restarts: 23,
		Message:         "back-off 5m0s restarting failed container=api pod=api-1_ns(uid)",
		LastTermination: &Termination{Reason: "Error", ExitCode: 1, At: now.Add(-4 * time.Minute)},
	}
	oom := Container{
		Name: "api", State: ContainerTerminated, Reason: "OOMKilled", Restarts: 12,
		Resources:       Resources{MemoryLimit: "1Gi"},
		LastTermination: &Termination{Reason: "OOMKilled", ExitCode: 137, At: now.Add(-38 * time.Minute)},
	}
	pull := Container{
		Name: "renderer", Image: "eu.gcr.io/acme/renderer:v1.11.0", State: ContainerWaiting, Reason: "ImagePullBackOff",
		Message: `Back-off pulling image "eu.gcr.io/acme/renderer:v1.11.0"`,
	}
	pending := pod("api", Container{Name: "api", State: ContainerWaiting, Reason: "ContainerCreating"})
	pending.Phase, pending.Reason, pending.Message = PodPending, "Unschedulable", "0/6 nodes are available: 6 Insufficient memory. preemption: 0/6 nodes are available"
	notReady := running("api")
	notReady.Ready = false
	notReady.LastTermination = &Termination{Reason: "Error", ExitCode: 143, At: now.Add(-9 * time.Minute)}
	starting := running("api")
	starting.Ready = false

	tests := []struct {
		name string
		s    ServiceSummary
		want string
	}{
		{
			"crash loop",
			ServiceSummary{Status: StatusCrashLoopBackOff, Restarts: 23, Pods: []Pod{pod("api", crash)}},
			"api: exit 1 (Error) 4m ago · 23 restarts · back-off 5m0s restarting failed",
		},
		{
			"oom",
			ServiceSummary{Status: StatusOOMKilled, Restarts: 12, Pods: []Pod{pod("api", running("api")), pod("api", oom)}},
			"api: OOMKilled exit 137, 38m ago · limit 1Gi · 12 restarts",
		},
		{
			"image pull",
			ServiceSummary{Status: StatusImagePullBackOff, Pods: []Pod{pod("renderer", pull)}},
			`cannot pull renderer:v1.11.0 — Back-off pulling image "eu.gcr.io/acme/renderer:v1.11.0"`,
		},
		{
			"pending",
			ServiceSummary{Status: StatusPending, Pods: []Pod{pod("api", running("api")), pending}},
			"1 pod pending: 0/6 nodes are available: 6 Insufficient memory",
		},
		{
			"degraded",
			ServiceSummary{Status: StatusDegraded, DesiredPods: 3, ReadyPods: 1, Pods: []Pod{pod("api", running("api")), pod("api", notReady), pod("api", notReady)}},
			"2 of 3 pods not ready · last exit 143 (Error) 9m ago",
		},
		{
			"missing replicas",
			ServiceSummary{Status: StatusDegraded, DesiredPods: 3, ReadyPods: 1, Pods: []Pod{pod("api", running("api"))}},
			"1 of 3 replicas available",
		},
		{
			"rollout",
			ServiceSummary{Status: StatusProgressing, Version: "v1→v2", UpdatedPods: 2, DesiredPods: 3, Pods: []Pod{pod("api", running("api")), pod("api", starting)}},
			"rollout v1→v2: 2/3 updated, 1 starting",
		},
		{"scaled to 0", ServiceSummary{Status: StatusUnknown}, "scaled to 0"},
		{
			"healthy recent restart",
			ServiceSummary{Status: StatusHealthy, LastRestart: now.Add(-2 * time.Hour), Pods: []Pod{pod("api", Container{Name: "api", LastTermination: &Termination{Reason: "OOMKilled", At: now.Add(-2 * time.Hour)}})}},
			"last restart 2h ago (OOMKilled)",
		},
		{"healthy old restart", ServiceSummary{Status: StatusHealthy, LastRestart: now.Add(-72 * time.Hour)}, ""},
		{"crash without pods", ServiceSummary{Status: StatusCrashLoopBackOff}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Explain(tt.s, filter, now); got != tt.want {
				t.Fatalf("\ngot  %q\nwant %q", got, tt.want)
			}
		})
	}
}
