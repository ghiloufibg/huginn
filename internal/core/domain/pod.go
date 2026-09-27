package domain

import "time"

// PodPhase mirrors the Kubernetes pod phase.
type PodPhase string

// Pod phases.
const (
	PodPending   PodPhase = "Pending"
	PodRunning   PodPhase = "Running"
	PodSucceeded PodPhase = "Succeeded"
	PodFailed    PodPhase = "Failed"
	PodUnknown   PodPhase = "Unknown"
)

// Pod is the observed state of a pod, reduced to what Huginn needs.
type Pod struct {
	Env        Env
	Namespace  string
	Name       string
	Labels     map[string]string
	Phase      PodPhase
	Node       string
	Created    time.Time
	Started    time.Time
	Deleted    bool
	Containers []Container
	// OwnerName is the name of the controlling workload (the ReplicaSet's
	// owner for Deployments), when known.
	OwnerName string
	// Revision identifies the pod template the pod was made from; pods of
	// a workload with different revisions mean a rollout, even when the
	// image did not change (a restart).
	Revision string
	// Reason and Message explain a pod-level problem, e.g. Unschedulable /
	// "0/6 nodes are available: 6 Insufficient memory."
	Reason, Message string
}

// ContainerState is the current state of a container.
type ContainerState string

// Container states. Waiting and terminated reasons are kept in Reason.
const (
	ContainerWaiting    ContainerState = "Waiting"
	ContainerRunning    ContainerState = "Running"
	ContainerTerminated ContainerState = "Terminated"
)

// Termination describes how a container instance ended.
type Termination struct {
	Reason   string
	ExitCode int
	At       time.Time
}

// Resources holds requests and limits as the Kubernetes quantity strings.
type Resources struct {
	CPURequest, CPULimit       string
	MemoryRequest, MemoryLimit string
}

// Container is the observed state of one container of a pod.
type Container struct {
	Name   string
	Image  string
	Init   bool
	State  ContainerState
	Reason string
	// Message is the Kubernetes message of the current state, e.g.
	// "back-off 5m0s restarting failed container".
	Message  string
	Ready    bool
	Restarts int
	// LastTermination is the previous instance's termination, if any; it
	// is what `kubectl logs --previous` would show logs for.
	LastTermination *Termination
	Resources       Resources
}

// Restarts returns the total restart count across containers; an init
// container counts while it keeps failing, not once it completed.
func (p Pod) Restarts() int {
	n := 0
	for _, c := range p.Containers {
		if !InitDone(c) {
			n += c.Restarts
		}
	}
	return n
}

// PodEventType tells whether a watched pod was added, updated or deleted.
type PodEventType int

// Pod event types.
const (
	PodAdded PodEventType = iota
	PodUpdated
	PodDeleted
)

// PodEvent is emitted by pod watches.
type PodEvent struct {
	Type PodEventType
	Pod  Pod
}

// Event is a Kubernetes event concerning an object, reduced for display.
type Event struct {
	Type      string // Normal or Warning
	Reason    string
	Message   string
	Count     int
	FirstSeen time.Time
	LastSeen  time.Time
}
