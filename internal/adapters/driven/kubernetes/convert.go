package kubernetes

import (
	"regexp"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Conversions from API objects to the domain, reduced to what Huginn shows.

func toPod(env domain.Env, p *corev1.Pod) domain.Pod {
	out := domain.Pod{
		Env: env, Namespace: p.Namespace, Name: p.Name, Labels: p.Labels,
		Phase: domain.PodPhase(p.Status.Phase), Node: p.Spec.NodeName,
		Created: p.CreationTimestamp.Time, Deleted: p.DeletionTimestamp != nil,
		OwnerName: ownerName(p), Reason: p.Status.Reason, Message: p.Status.Message,
	}
	if p.Status.StartTime != nil {
		out.Started = p.Status.StartTime.Time
	}
	if out.Phase == "" {
		out.Phase = domain.PodPending
	}
	for _, c := range p.Status.Conditions { // Unschedulable: why the pod is Pending
		if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && out.Reason == "" {
			out.Reason, out.Message = c.Reason, c.Message
		}
	}
	out.Containers = append(containers(p.Spec.InitContainers, p.Status.InitContainerStatuses, true),
		containers(p.Spec.Containers, p.Status.ContainerStatuses, false)...)
	return out
}

func containers(specs []corev1.Container, statuses []corev1.ContainerStatus, init bool) []domain.Container {
	out := make([]domain.Container, 0, len(specs))
	for _, s := range specs {
		c := domain.Container{Name: s.Name, Image: s.Image, Init: init, State: domain.ContainerWaiting, Resources: resources(s.Resources)}
		for i := range statuses {
			if statuses[i].Name == s.Name {
				setStatus(&c, &statuses[i])
				break
			}
		}
		out = append(out, c)
	}
	return out
}

func setStatus(c *domain.Container, st *corev1.ContainerStatus) {
	c.Ready, c.Restarts = st.Ready, int(st.RestartCount)
	switch s := st.State; {
	case s.Running != nil:
		c.State = domain.ContainerRunning
	case s.Terminated != nil:
		c.State, c.Reason, c.Message = domain.ContainerTerminated, s.Terminated.Reason, s.Terminated.Message
	case s.Waiting != nil:
		c.State, c.Reason, c.Message = domain.ContainerWaiting, s.Waiting.Reason, s.Waiting.Message
	}
	if t := st.LastTerminationState.Terminated; t != nil {
		c.LastTermination = &domain.Termination{Reason: t.Reason, ExitCode: int(t.ExitCode), At: t.FinishedAt.Time}
	}
}

func resources(r corev1.ResourceRequirements) domain.Resources {
	q := func(l corev1.ResourceList, n corev1.ResourceName) string {
		if v, ok := l[n]; ok {
			return v.String()
		}
		return ""
	}
	return domain.Resources{
		CPURequest: q(r.Requests, corev1.ResourceCPU), CPULimit: q(r.Limits, corev1.ResourceCPU),
		MemoryRequest: q(r.Requests, corev1.ResourceMemory), MemoryLimit: q(r.Limits, corev1.ResourceMemory),
	}
}

// jobSuffix is the scheduled-time suffix the CronJob controller appends to
// the names of its Jobs.
var jobSuffix = regexp.MustCompile(`-\d+$`)

// ownerName returns the workload that controls a pod. Pods of Deployments
// are owned by a ReplicaSet named <deployment>-<pod-template-hash>, and
// pods of CronJobs by a Job named <cronjob>-<scheduled time>: both names
// are derived without reading the intermediate object, so no permission
// on ReplicaSets or Jobs is needed.
func ownerName(p *corev1.Pod) string {
	ref := metav1.GetControllerOf(p)
	if ref == nil {
		return ""
	}
	switch ref.Kind {
	case "ReplicaSet":
		if h := p.Labels[appsv1.DefaultDeploymentUniqueLabelKey]; h != "" {
			if name, ok := strings.CutSuffix(ref.Name, "-"+h); ok {
				return name
			}
		}
	case "Job":
		if name := jobSuffix.ReplaceAllString(ref.Name, ""); name != ref.Name {
			return name
		}
	}
	return ref.Name
}

func ref(env domain.Env, kind domain.WorkloadKind, m metav1.ObjectMeta) domain.WorkloadRef {
	return domain.WorkloadRef{Env: env, Namespace: m.Namespace, Kind: kind, Name: m.Name}
}

func workload(env domain.Env, kind domain.WorkloadKind, m metav1.ObjectMeta, sel *metav1.LabelSelector) domain.Workload {
	w := domain.Workload{Ref: ref(env, kind, m), Labels: m.Labels, Annotations: m.Annotations, Created: m.CreationTimestamp.Time}
	if sel != nil {
		w.Selector = sel.MatchLabels
	}
	return w
}

func replicas(p *int32) int {
	if p == nil {
		return 1
	}
	return int(*p)
}

func fromDeployment(env domain.Env, d *appsv1.Deployment) domain.Workload {
	w := workload(env, domain.KindDeployment, d.ObjectMeta, d.Spec.Selector)
	s := d.Status
	w.DesiredReplicas, w.ReadyReplicas, w.UpdatedReplicas = replicas(d.Spec.Replicas), int(s.ReadyReplicas), int(s.UpdatedReplicas)
	// A rollout is in progress while the controller has not seen the new
	// spec, some replicas are not updated, or old ones still run.
	w.Progressing = s.ObservedGeneration < d.Generation || w.UpdatedReplicas < w.DesiredReplicas || int(s.Replicas) > w.UpdatedReplicas
	return w
}

func fromStatefulSet(env domain.Env, d *appsv1.StatefulSet) domain.Workload {
	w := workload(env, domain.KindStatefulSet, d.ObjectMeta, d.Spec.Selector)
	s := d.Status
	w.DesiredReplicas, w.ReadyReplicas, w.UpdatedReplicas = replicas(d.Spec.Replicas), int(s.ReadyReplicas), int(s.UpdatedReplicas)
	w.Progressing = s.ObservedGeneration < d.Generation || w.UpdatedReplicas < w.DesiredReplicas ||
		(s.UpdateRevision != "" && s.CurrentRevision != s.UpdateRevision)
	return w
}

func fromDaemonSet(env domain.Env, d *appsv1.DaemonSet) domain.Workload {
	w := workload(env, domain.KindDaemonSet, d.ObjectMeta, d.Spec.Selector)
	s := d.Status
	w.DesiredReplicas, w.ReadyReplicas, w.UpdatedReplicas = int(s.DesiredNumberScheduled), int(s.NumberReady), int(s.UpdatedNumberScheduled)
	w.Progressing = s.ObservedGeneration < d.Generation || w.UpdatedReplicas < w.DesiredReplicas
	return w
}

// fromCronJob: a CronJob has no replicas; its active Jobs count as both
// desired and ready, and its pods are found by the labels of the template.
func fromCronJob(env domain.Env, c *batchv1.CronJob) domain.Workload {
	w := workload(env, domain.KindCronJob, c.ObjectMeta, c.Spec.JobTemplate.Spec.Selector)
	if len(w.Selector) == 0 {
		w.Selector = c.Spec.JobTemplate.Spec.Template.Labels
	}
	n := len(c.Status.Active)
	w.DesiredReplicas, w.ReadyReplicas, w.UpdatedReplicas = n, n, n
	return w
}

// toEvent normalizes old- and new-style events: new ones (events.k8s.io
// written through core/v1) have no count nor lastTimestamp, only
// eventTime and sometimes a series.
func toEvent(e *corev1.Event) domain.Event {
	out := domain.Event{
		Type: e.Type, Reason: e.Reason, Message: strings.TrimSpace(e.Message), Count: int(e.Count),
		FirstSeen: e.FirstTimestamp.Time, LastSeen: e.LastTimestamp.Time,
	}
	if out.LastSeen.IsZero() && e.Series != nil {
		out.LastSeen = e.Series.LastObservedTime.Time
	}
	if out.LastSeen.IsZero() {
		out.LastSeen = e.EventTime.Time
	}
	if out.FirstSeen.IsZero() {
		out.FirstSeen = e.EventTime.Time
	}
	if out.FirstSeen.IsZero() {
		out.FirstSeen = out.LastSeen
	}
	if out.Count == 0 && e.Series != nil {
		out.Count = int(e.Series.Count)
	}
	if out.Count == 0 {
		out.Count = 1
	}
	return out
}
