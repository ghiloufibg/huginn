package kubernetes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	k8s "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/cache"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

var t0 = time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)

const ns = "app-rec"

func ptr[T any](v T) *T { return &v }

func controller(kind, name string) []metav1.OwnerReference {
	return []metav1.OwnerReference{{Kind: kind, Name: name, Controller: ptr(true)}}
}

func deployment(name string, labels map[string]string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: 2, Labels: labels},
		Spec:       appsv1.DeploymentSpec{Replicas: ptr[int32](2), Selector: &metav1.LabelSelector{MatchLabels: labels}},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 2, Replicas: 2, ReadyReplicas: 2, UpdatedReplicas: 2},
	}
}

func pod(name, deploy, hash string, labels map[string]string) *corev1.Pod {
	l := map[string]string{"pod-template-hash": hash}
	for k, v := range labels {
		l[k] = v
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: ns, Labels: l, OwnerReferences: controller("ReplicaSet", deploy+"-"+hash),
			CreationTimestamp: metav1.NewTime(t0),
		},
		Spec: corev1.PodSpec{NodeName: "node-1", Containers: []corev1.Container{{Name: "app", Image: "app:1.0"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}},
	}
}

// fixture is a namespace with one Deployment, two of its pods and a pod
// of another app.
func fixture() *fake.Clientset {
	api := map[string]string{"app": "api"}
	return fake.NewClientset(
		deployment("api", api),
		pod("api-7d9f-a1", "api", "7d9f", api),
		pod("api-7d9f-b2", "api", "7d9f", api),
		pod("web-5c4b-c3", "web", "5c4b", map[string]string{"app": "web"}),
	)
}

func client(cs API) *Client {
	return New(Options{NewClientset: func(string) (API, error) { return cs, nil }})
}

var scope = ports.Scope{Env: "rec", Context: "kind-huginn", Namespaces: []string{ns}}

func TestClusterContract(t *testing.T) {
	portstest.RunClusterContract(t, func(t *testing.T) portstest.ClusterFixture {
		return portstest.ClusterFixture{Client: client(fixture()), Scope: scope, PodLabels: ports.Selector{"app": "api"}}
	})
}

func TestOwnerResolution(t *testing.T) {
	p := pod("api-7d9f-a1", "api", "7d9f", nil)
	if got := ownerName(p); got != "api" {
		t.Errorf("ReplicaSet owner: %q, want api", got)
	}
	p.OwnerReferences = controller("Job", "nightly-export-29123456")
	if got := ownerName(p); got != "nightly-export" {
		t.Errorf("Job owner: %q, want nightly-export", got)
	}
	p.OwnerReferences = controller("Job", "one-off")
	if got := ownerName(p); got != "one-off" {
		t.Errorf("plain Job owner: %q", got)
	}
	p.OwnerReferences = controller("StatefulSet", "ledger-writer")
	if got := ownerName(p); got != "ledger-writer" {
		t.Errorf("StatefulSet owner: %q", got)
	}
	p.OwnerReferences = nil
	if got := ownerName(p); got != "" {
		t.Errorf("no owner: %q", got)
	}
	for owner, want := range map[string]string{
		"ReplicaSet/api-7d9f":         "Deployment/api",
		"ReplicaSet/orphan":           "ReplicaSet/orphan", // no pod-template-hash suffix
		"Job/nightly-export-29123456": "CronJob/nightly-export",
		"Job/one-off":                 "Job/one-off",
		"Rollout/canary":              "Rollout/canary",
		"StatefulSet/ledger-writer":   "StatefulSet/ledger-writer",
	} {
		kind, name, _ := strings.Cut(owner, "/")
		p := pod("x", "api", "7d9f", nil)
		p.OwnerReferences = controller(kind, name)
		if k, n := ownerOf(p); k+"/"+n != want {
			t.Errorf("%s: %s/%s, want %s", owner, k, n, want)
		}
	}
}

func TestPodConversion(t *testing.T) {
	p := pod("api-7d9f-a1", "api", "7d9f", nil)
	p.Spec.InitContainers = []corev1.Container{{Name: "migrate", Image: "migrate:1"}}
	p.Spec.Containers[0].Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("250m")},
		Limits:   corev1.ResourceList{corev1.ResourceMemory: resource.MustParse("512Mi")},
	}
	p.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "migrate", State: corev1.ContainerState{
		Terminated: &corev1.ContainerStateTerminated{Reason: "Completed"},
	}}}
	p.Status.ContainerStatuses[0] = corev1.ContainerStatus{
		Name: "app", RestartCount: 4,
		State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "back-off 5m0s"}},
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
			Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(t0),
		}},
	}
	got := toPod("rec", p)
	if got.OwnerName != "api" || got.Node != "node-1" || got.Phase != domain.PodRunning || len(got.Containers) != 2 {
		t.Fatalf("pod: %+v", got)
	}
	init, app := got.Containers[0], got.Containers[1]
	if !init.Init || init.State != domain.ContainerTerminated || init.Reason != "Completed" {
		t.Errorf("init: %+v", init)
	}
	want := domain.Container{
		Name: "app", Image: "app:1.0", State: domain.ContainerWaiting, Reason: "CrashLoopBackOff",
		Message: "back-off 5m0s", Restarts: 4, LastTermination: &domain.Termination{Reason: "OOMKilled", ExitCode: 137, At: t0},
		Resources: domain.Resources{CPURequest: "250m", MemoryLimit: "512Mi"},
	}
	lt := app.LastTermination
	app.LastTermination, want.LastTermination = nil, nil
	if fmt.Sprint(app) != fmt.Sprint(want) || lt == nil || *lt != (domain.Termination{Reason: "OOMKilled", ExitCode: 137, At: t0}) {
		t.Errorf("app:\n got %+v\nwant %+v", app, want)
	}

	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p"}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{Phase: corev1.PodPending, Conditions: []corev1.PodCondition{{
			Type:   corev1.PodScheduled,
			Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "0/1 nodes are available: 1 Insufficient memory.",
		}}},
	}
	gp := toPod("rec", pending)
	if gp.Reason != "Unschedulable" || !strings.Contains(gp.Message, "Insufficient memory") || gp.Containers[0].State != domain.ContainerWaiting {
		t.Errorf("pending: %+v", gp)
	}
}

func TestWorkloadConversion(t *testing.T) {
	d := deployment("api", map[string]string{"app": "api"})
	if w := fromDeployment("rec", d); w.Progressing || w.DesiredReplicas != 2 || w.Selector["app"] != "api" || w.Ref.Kind != domain.KindDeployment {
		t.Errorf("stable deployment: %+v", w)
	}
	d.Generation = 3 // the controller has not seen the new spec yet
	if w := fromDeployment("rec", d); !w.Progressing {
		t.Error("unobserved generation is a rollout")
	}
	d.Generation, d.Status.UpdatedReplicas, d.Status.Replicas = 2, 1, 3
	if w := fromDeployment("rec", d); !w.Progressing || w.UpdatedReplicas != 1 {
		t.Errorf("rolling deployment: %+v", w)
	}
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: "export", Namespace: ns},
		Spec: batchv1.CronJobSpec{JobTemplate: batchv1.JobTemplateSpec{Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "export"}}},
		}}},
		Status: batchv1.CronJobStatus{Active: []corev1.ObjectReference{{Name: "export-1"}}},
	}
	if w := fromCronJob("rec", cj); w.Selector["app"] != "export" || w.DesiredReplicas != 1 {
		t.Errorf("cronjob: %+v", w)
	}
}

func TestEventNormalization(t *testing.T) {
	old := &corev1.Event{
		Type: "Warning", Reason: "BackOff", Message: "Back-off restarting\n", Count: 7,
		FirstTimestamp: metav1.NewTime(t0), LastTimestamp: metav1.NewTime(t0.Add(time.Minute)),
	}
	if e := toEvent(old); e.Count != 7 || !e.LastSeen.Equal(t0.Add(time.Minute)) || e.Message != "Back-off restarting" {
		t.Errorf("old style: %+v", e)
	}
	// New style: eventTime only (the Scheduled event of the lab).
	fresh := &corev1.Event{Type: "Normal", Reason: "Scheduled", EventTime: metav1.NewMicroTime(t0)}
	if e := toEvent(fresh); e.Count != 1 || !e.LastSeen.Equal(t0) || !e.FirstSeen.Equal(t0) {
		t.Errorf("new style: %+v", e)
	}
	series := &corev1.Event{
		EventTime: metav1.NewMicroTime(t0),
		Series:    &corev1.EventSeries{Count: 12, LastObservedTime: metav1.NewMicroTime(t0.Add(time.Hour))},
	}
	if e := toEvent(series); e.Count != 12 || !e.LastSeen.Equal(t0.Add(time.Hour)) || !e.FirstSeen.Equal(t0) {
		t.Errorf("series: %+v", e)
	}
}

func TestPodEventsNewestFirst(t *testing.T) {
	ev := func(name, pod string, at time.Time) *corev1.Event {
		return &corev1.Event{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Reason: name,
			InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: pod}, LastTimestamp: metav1.NewTime(at),
		}
	}
	cs := fake.NewClientset(ev("old", "api-1", t0), ev("new", "api-1", t0.Add(time.Minute)), ev("other", "web-1", t0))
	got, err := client(cs).PodEvents(context.Background(), scope, ns, "api-1")
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, e := range got {
		reasons = append(reasons, e.Reason)
	}
	// The fake ignores field selectors: only check the order here; the
	// selector itself is checked against a real API server (lab tests).
	if len(reasons) < 2 || reasons[0] != "new" && reasons[0] != "other" {
		t.Errorf("events: %v", reasons)
	}
}

func TestErrorMapping(t *testing.T) {
	gr := schema.GroupResource{Resource: "pods"}
	cases := []struct {
		err  error
		want error
	}{
		{apierrors.NewUnauthorized("token expired"), domain.ErrUnauthorized},
		{apierrors.NewForbidden(gr, "", errors.New("no")), domain.ErrForbidden},
		{apierrors.NewNotFound(gr, "api-1"), domain.ErrNotFound},
		{apierrors.NewBadRequest(`container "app" in pod "doc-1" is waiting to start: trying and failing to pull image`), domain.ErrNotStarted},
		{apierrors.NewBadRequest(`previous terminated container "app" in pod "api-1" not found`), domain.ErrNotFound},
		{apierrors.NewServiceUnavailable("down"), domain.ErrUnreachable},
		{&net.OpError{Op: "dial", Err: errors.New("connection refused")}, domain.ErrUnreachable},
		{errors.New(`an error on the server ("unable to decode an event from the watch stream: http2: client connection lost")`), domain.ErrUnreachable},
		{errors.New("read tcp 10.0.0.1:443: i/o timeout"), domain.ErrUnreachable},
	}
	for _, c := range cases {
		if got := mapErr(c.err); !errors.Is(got, c.want) || got.Error() != c.err.Error() {
			t.Errorf("mapErr(%v) = %v, want %v", c.err, got, c.want)
		}
	}
	if err := mapErr(context.Canceled); err != context.Canceled {
		t.Errorf("cancel: %v", err)
	}
}

func TestUnknownContextIsAConfigError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("KUBECONFIG", dir+"/none")
	_, err := New(Options{UserAgent: "huginn/test"}).ListPods(context.Background(), ports.Scope{Env: "rec", Context: "x", Namespaces: []string{ns}}, nil)
	if !errors.Is(err, domain.ErrConfig) || !strings.Contains(err.Error(), "no kubeconfig found") || !strings.Contains(err.Error(), "none") {
		t.Fatalf("missing kubeconfig: %v", err)
	}
	cfg := dir + "/config"
	if err := os.WriteFile(cfg, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", cfg)
	c := New(Options{UserAgent: "huginn/test"})
	_, err = c.ListPods(context.Background(), ports.Scope{Env: "rec", Context: "nope", Namespaces: []string{ns}}, nil)
	if !errors.Is(err, domain.ErrConfig) || !domain.Permanent(err) || !strings.Contains(err.Error(), "nope") || strings.HasSuffix(err.Error(), "configuration") {
		t.Fatalf("err = %v", err)
	}
	_, err = client(fixture()).ListPods(context.Background(), ports.Scope{Env: "rec"}, nil)
	if !errors.Is(err, domain.ErrConfig) {
		t.Fatalf("no namespace: %v", err)
	}
}

func forbid(cs *fake.Clientset, verb, resource string) {
	cs.PrependReactor(verb, resource, func(a k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "", errors.New("RBAC"))
	})
}

func TestUnreadableKindsAreSkipped(t *testing.T) {
	cs := fixture()
	forbid(cs, "list", "cronjobs") // the lab's restricted identity cannot read CronJobs
	ws, err := client(cs).ListWorkloads(context.Background(), scope)
	if err != nil || len(ws) != 1 || ws[0].Ref.Name != "api" {
		t.Fatalf("workloads %v, err %v", ws, err)
	}
	for _, r := range []string{"deployments", "statefulsets", "daemonsets"} {
		forbid(cs, "list", r)
	}
	_, err = client(cs).ListWorkloads(context.Background(), scope)
	if !errors.Is(err, domain.ErrForbidden) || !strings.Contains(err.Error(), ns) {
		t.Fatalf("all forbidden: %v", err)
	}
	if _, err := client(cs).WatchWorkloads(context.Background(), scope); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("watch, all forbidden: %v", err)
	}
	forbid(cs, "list", "pods")
	if _, err := client(cs).WatchPods(context.Background(), scope, nil); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("pod watch forbidden: %v", err)
	}
}

func TestWatchPodsDeliversChanges(t *testing.T) {
	cs := fixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := client(cs).WatchPods(ctx, scope, ports.Selector{"app": "api"})
	if err != nil {
		t.Fatal(err)
	}
	next := func() domain.PodEvent {
		t.Helper()
		select {
		case ev := <-ch:
			return ev
		case <-time.After(5 * time.Second):
			t.Fatal("no event")
		}
		return domain.PodEvent{}
	}
	for range 2 {
		if ev := next(); ev.Type != domain.PodAdded || ev.Pod.OwnerName != "api" || ev.Pod.Env != "rec" {
			t.Fatalf("initial: %+v", ev)
		}
	}
	p := pod("api-7d9f-a1", "api", "7d9f", map[string]string{"app": "api"})
	p.Status.ContainerStatuses[0].RestartCount = 1
	if _, err := cs.CoreV1().Pods(ns).Update(ctx, p, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Type != domain.PodUpdated || ev.Pod.Restarts() != 1 {
		t.Fatalf("update: %+v", ev)
	}
	if err := cs.CoreV1().Pods(ns).Delete(ctx, "api-7d9f-b2", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if ev := next(); ev.Type != domain.PodDeleted || !ev.Pod.Deleted || ev.Pod.Name != "api-7d9f-b2" {
		t.Fatalf("delete: %+v", ev)
	}
	cancel()
	for range ch { // closes once the informers stopped
	}
}

// TestReadOnly drives every method and checks the adapter only used read
// verbs: Huginn must never change a cluster.
func TestReadOnly(t *testing.T) {
	cs := fixture()
	c := client(cs)
	ctx, cancel := context.WithCancel(context.Background())
	_, _ = c.ListWorkloads(ctx, scope)
	_, _ = c.ListPods(ctx, scope, nil)
	_, _ = c.PodEvents(ctx, scope, ns, "api-7d9f-a1")
	w, _ := c.WatchWorkloads(ctx, scope)
	p, _ := c.WatchPods(ctx, scope, nil)
	if st, err := c.Stream(ctx, ports.LogRequest{Scope: scope, Namespace: ns, Pod: "api-7d9f-a1", Container: "app"}); err == nil {
		for range st.Lines() {
		}
	}
	time.Sleep(100 * time.Millisecond) // let the informers list and watch
	cancel()
	for range w {
	}
	for range p {
	}
	read := []string{"get", "list", "watch"}
	var verbs []string
	for _, a := range cs.Actions() {
		v := a.GetVerb()
		if !slices.Contains(verbs, v) {
			verbs = append(verbs, v)
		}
		if !slices.Contains(read, v) {
			t.Errorf("write verb %s on %s", v, a.GetResource().Resource)
		}
	}
	if !slices.Contains(verbs, "watch") || !slices.Contains(verbs, "list") || !slices.Contains(verbs, "get") {
		t.Errorf("verbs used: %v (the test no longer covers all paths)", verbs)
	}
}

func TestLogOptions(t *testing.T) {
	since := t0.Add(1500 * time.Millisecond)
	cases := []struct {
		req  ports.LogRequest
		want string
	}{
		{ports.LogRequest{Container: "app", Window: domain.TimeWindow{Tail: 50}}, "tail=50"},
		{ports.LogRequest{Window: domain.TimeWindow{Since: 90 * time.Second}}, "since=90s"},
		{ports.LogRequest{Window: domain.TimeWindow{Since: 1500 * time.Millisecond}}, "since=2s"},
		{ports.LogRequest{Window: domain.TimeWindow{Since: time.Hour}, Limit: 5000}, "since=3600s tail=5000"},
		{ports.LogRequest{Window: domain.TimeWindow{Tail: 10}, Limit: 5000}, "tail=10"},
		{ports.LogRequest{Window: domain.TimeWindow{Tail: 10}, SinceTime: since, Follow: true}, "sinceTime follow"},
		{ports.LogRequest{Previous: true}, "previous"},
		{ports.LogRequest{Window: domain.TimeWindow{Head: 500}, Limit: 5000, Follow: true}, ""},
		{ports.LogRequest{Window: domain.TimeWindow{Head: 500}, Previous: true}, "previous"},
	}
	for _, c := range cases {
		o := logOptions(c.req)
		var got []string
		if o.SinceSeconds != nil {
			got = append(got, fmt.Sprintf("since=%ds", *o.SinceSeconds))
		}
		if o.SinceTime != nil {
			got = append(got, "sinceTime")
		}
		if o.TailLines != nil {
			got = append(got, fmt.Sprintf("tail=%d", *o.TailLines))
		}
		if o.Follow {
			got = append(got, "follow")
		}
		if o.Previous {
			got = append(got, "previous")
		}
		if s := strings.Join(got, " "); s != c.want || !o.Timestamps || o.Container != c.req.Container {
			t.Errorf("%+v: %q, want %q", c.req, s, c.want)
		}
	}
}

func TestSplitTimestamp(t *testing.T) {
	l := splitTimestamp("2026-09-27T10:00:07.276316213Z {\"level\":\"INFO\"}")
	if !l.Time.Equal(time.Date(2026, 9, 27, 10, 0, 7, 276316213, time.UTC)) || l.Text != `{"level":"INFO"}` {
		t.Errorf("got %+v", l)
	}
	for _, s := range []string{"no timestamp here", "", "2026-13-45T00:00:00Z text"} {
		if l := splitTimestamp(s); !l.Time.IsZero() || l.Text != s {
			t.Errorf("%q: %+v", s, l)
		}
	}
	if l := splitTimestamp("2026-09-27T10:00:07Z "); l.Text != "" || l.Time.IsZero() {
		t.Errorf("empty line: %+v", l)
	}
}

func TestLongLines(t *testing.T) {
	big := strings.Repeat("x", 40_000) // measured on the lab: delivered whole
	huge := strings.Repeat("y", MaxLineBytes+10)
	in := "a\r\n" + big + "\n" + huge + "\nlast"
	r := bufio.NewReaderSize(strings.NewReader(in), 4096)
	var got []string
	for {
		l, err := readLine(r)
		if l != "" || err == nil {
			got = append(got, l)
		}
		if err != nil {
			break
		}
	}
	if len(got) != 4 || got[0] != "a" || got[1] != big || got[3] != "last" {
		t.Fatalf("lines: %d", len(got))
	}
	if !strings.HasSuffix(got[2], truncatedMark) || len(got[2]) != MaxLineBytes+len(truncatedMark) {
		t.Errorf("huge line: %d bytes", len(got[2]))
	}
}

func TestStreamAttributesLines(t *testing.T) {
	// The fake clientset answers every log request with "fake logs".
	st, err := client(fixture()).Stream(context.Background(), ports.LogRequest{Scope: scope, Namespace: ns, Pod: "api-7d9f-a1", Container: "app"})
	if err != nil {
		t.Fatal(err)
	}
	var lines []domain.RawLine
	for l := range st.Lines() {
		lines = append(lines, l)
	}
	if st.Err() != nil || len(lines) != 1 || lines[0].Pod != "api-7d9f-a1" || lines[0].Container != "app" || lines[0].Text != "fake logs" {
		t.Fatalf("lines %+v, err %v", lines, st.Err())
	}
}

func TestEventsMerged(t *testing.T) {
	msg := "Back-off restarting failed container"
	got := merge([]domain.Event{
		{Type: "Warning", Reason: "BackOff", Message: msg, Count: 26, FirstSeen: t0, LastSeen: t0.Add(time.Minute)},
		{Type: "Normal", Reason: "Pulled", Message: "pulled", Count: 1, FirstSeen: t0, LastSeen: t0},
		{Type: "Warning", Reason: "BackOff", Message: msg, Count: 4, FirstSeen: t0.Add(time.Hour), LastSeen: t0.Add(2 * time.Hour)},
	})
	if len(got) != 2 || got[0].Reason != "BackOff" || got[0].Count != 30 || !got[0].FirstSeen.Equal(t0) || !got[0].LastSeen.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("merged: %+v", got)
	}
}

var _ API = k8s.Interface(nil)

func TestKubeletNoLogsIsNotFound(t *testing.T) {
	st := &stream{ch: make(chan domain.RawLine, 4)}
	body := io.NopCloser(strings.NewReader("unable to retrieve container logs for containerd://0572\n"))
	st.read(context.Background(), body, ports.LogRequest{Pod: "p", Container: "c"})
	var n int
	for range st.Lines() {
		n++
	}
	if n != 0 || !errors.Is(st.Err(), domain.ErrNotFound) {
		t.Fatalf("%d lines, err %v", n, st.Err())
	}
}

func TestContainerStarted(t *testing.T) {
	p := pod("api-1", "api", "7d9f", nil)
	p.Status.ContainerStatuses[0].State.Running.StartedAt = metav1.NewTime(t0)
	if got := toPod("rec", p).Containers[0].Started; !got.Equal(t0) {
		t.Errorf("running: started %v", got)
	}
	p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{StartedAt: metav1.NewTime(t0.Add(time.Minute))}}
	if got := toPod("rec", p).Containers[0].Started; !got.Equal(t0.Add(time.Minute)) {
		t.Errorf("terminated: started %v", got)
	}
	p.Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}
	if got := toPod("rec", p).Containers[0].Started; !got.IsZero() {
		t.Errorf("waiting: started %v", got)
	}
}

// closeCounter is a log body that records its closing.
type closeCounter struct {
	io.Reader
	closed bool
}

func (c *closeCounter) Close() error { c.closed = true; return nil }

func TestHeadStopsAfterItsLines(t *testing.T) {
	st := &stream{ch: make(chan domain.RawLine, 16)}
	body := &closeCounter{Reader: strings.NewReader(strings.Repeat("2026-09-27T10:00:07Z line\n", 10))}
	st.read(context.Background(), body, ports.LogRequest{Pod: "p", Container: "c", Window: domain.TimeWindow{Head: 3}})
	var n int
	for range st.Lines() {
		n++
	}
	if n != 3 || st.Err() != nil || !body.closed {
		t.Fatalf("%d lines, err %v, closed %v", n, st.Err(), body.closed)
	}
}

// E10: a namespace that does not exist is not an empty one.
func TestMissingNamespace(t *testing.T) {
	cs := fake.NewClientset()
	_, err := client(cs).WatchWorkloads(context.Background(), ports.Scope{Env: "rec", Namespaces: []string{"app-rce"}})
	if !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "app-rce does not exist") {
		t.Fatalf("err = %v", err)
	}
	cs = fake.NewClientset(&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "empty"}})
	if _, err := client(cs).ListWorkloads(context.Background(), ports.Scope{Env: "rec", Namespaces: []string{"empty"}}); err != nil {
		t.Fatalf("an empty namespace is fine: %v", err)
	}
	cs = fake.NewClientset()
	forbid(cs, "get", "namespaces") // no cluster-wide read: assume it exists
	if _, err := client(cs).ListWorkloads(context.Background(), ports.Scope{Env: "rec", Namespaces: []string{"app-rce"}}); err != nil {
		t.Fatalf("unknown existence: %v", err)
	}
}

// E8: kinds skipped for lack of permission are announced before the workloads.
func TestSkippedKindsAreAnnounced(t *testing.T) {
	cs := fixture()
	forbid(cs, "list", "cronjobs")
	forbid(cs, "list", "daemonsets")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := client(cs).WatchWorkloads(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	ev := <-ch
	if ev.Warning != "daemonsets, cronjobs not readable in app-rec" {
		t.Fatalf("first event %+v", ev)
	}
	if ev := <-ch; ev.Workload.Ref.Name != "api" {
		t.Fatalf("then the workloads: %+v", ev)
	}
}

// E2: a list failing because the cluster is unreachable ends the watch,
// so the caller reports it (the informers would retry silently); an
// expired resource version does not.
func TestUnreachableEndsTheWatch(t *testing.T) {
	failing := func(err error) informerSource {
		return informerSource{example: &corev1.Pod{}, lw: cache.ToListWatcherWithWatchListSemantics(&cache.ListWatch{
			ListWithContextFunc: func(context.Context, metav1.ListOptions) (runtime.Object, error) { return nil, err },
			WatchFuncWithContext: func(context.Context, metav1.ListOptions) (watch.Interface, error) {
				return nil, err
			},
		}, noWatchList{})}
	}
	conv := func(any, bool) (int, bool) { return 0, false }
	ch := run(context.Background(), []informerSource{failing(&net.OpError{Op: "dial", Err: errors.New("i/o timeout")})}, nil, conv)
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("unexpected event")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch still open after an unreachable list")
	}
	if !watchBroken(apierrors.NewUnauthorized("expired")) || watchBroken(apierrors.NewResourceExpired("too old")) {
		t.Error("watchBroken")
	}
}
