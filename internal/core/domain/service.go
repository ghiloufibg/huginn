package domain

import "time"

// ServiceStatus is the aggregated health of a repo's workloads. The worst
// state of any pod drives the row. Values are ordered from worst to best so
// that sorting by status puts problems first.
type ServiceStatus int

// Service statuses, worst first.
const (
	StatusCrashLoopBackOff ServiceStatus = iota
	StatusOOMKilled
	StatusImagePullBackOff
	StatusDegraded
	StatusPending
	StatusProgressing
	StatusUnknown
	StatusHealthy
)

var statusNames = [...]string{
	"CrashLoopBackOff", "OOMKilled", "ImagePullBackOff", "Degraded",
	"Pending", "Progressing", "Unknown", "Healthy",
}

// String returns the status as displayed.
func (s ServiceStatus) String() string {
	if int(s) < len(statusNames) {
		return statusNames[s]
	}
	return "Unknown"
}

// ServiceSummary is one row of the services screen.
type ServiceSummary struct {
	Repo        string
	Workloads   int
	ReadyPods   int
	DesiredPods int
	Status      ServiceStatus
	Restarts    int
	LastRestart time.Time
	Version     string
	Created     time.Time
	// Refs are the workloads of the repository.
	Refs []WorkloadRef
	// Pods are the pods of those workloads.
	Pods []Pod
	// Unassigned marks a workload that no resolver attributed to a
	// repository; Repo then holds the workload name.
	Unassigned bool
	// Err is set when this service could not be read (for example a
	// forbidden namespace); other rows are unaffected.
	Err error
}
