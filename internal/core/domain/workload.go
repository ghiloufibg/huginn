package domain

import "time"

// Repo is a source code repository: the unit a developer thinks in, and the
// row of the services screen. One repo may own several workloads.
type Repo struct {
	Name      string
	Workloads []WorkloadRef
}

// WorkloadKind is the Kubernetes kind of a workload.
type WorkloadKind string

// Supported workload kinds.
const (
	KindDeployment  WorkloadKind = "Deployment"
	KindStatefulSet WorkloadKind = "StatefulSet"
	KindDaemonSet   WorkloadKind = "DaemonSet"
	KindCronJob     WorkloadKind = "CronJob"
)

// WorkloadRef identifies a workload in an environment.
type WorkloadRef struct {
	Env       Env
	Namespace string
	Kind      WorkloadKind
	Name      string
}

// Workload is the observed state of a workload.
type Workload struct {
	Ref             WorkloadRef
	Labels          map[string]string
	Annotations     map[string]string
	DesiredReplicas int
	ReadyReplicas   int
	UpdatedReplicas int
	// Progressing is true while a rollout is in progress.
	Progressing bool
	// Selector is the label selector of the workload's pods.
	Selector map[string]string
	Created  time.Time
}
