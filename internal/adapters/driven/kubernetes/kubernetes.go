// Package kubernetes implements ports.ClusterClient and ports.LogSource
// with client-go. It is strictly read-only: it only gets, lists and
// watches objects and reads pod logs (a test checks every verb it uses).
//
// One clientset is built per kube context, from the user's kubeconfig
// (KUBECONFIG or ~/.kube/config); authentication is whatever that
// kubeconfig says (on GKE, the gke-gcloud-auth-plugin exec plugin).
// Workloads and pods are watched with shared informers per namespace,
// never cluster-wide, so namespaced read permissions are enough
// (docs/DECISIONS.md D-004, D-034).
package kubernetes

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"

	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	batchv1client "k8s.io/client-go/kubernetes/typed/batch/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// API is the part of a clientset Huginn uses: only the groups it reads,
// which keeps the rest of client-go out of the binary.
// k8s.io/client-go/kubernetes.Interface (and its fake) satisfies it.
type API interface {
	CoreV1() corev1client.CoreV1Interface
	AppsV1() appsv1client.AppsV1Interface
	BatchV1() batchv1client.BatchV1Interface
}

type clients struct {
	core  *corev1client.CoreV1Client
	apps  *appsv1client.AppsV1Client
	batch *batchv1client.BatchV1Client
}

func (c clients) CoreV1() corev1client.CoreV1Interface    { return c.core }
func (c clients) AppsV1() appsv1client.AppsV1Interface    { return c.apps }
func (c clients) BatchV1() batchv1client.BatchV1Interface { return c.batch }

// Options configure the client.
type Options struct {
	// UserAgent identifies Huginn to the API server.
	UserAgent string
	// NewClientset builds the clientset of a kube context; tests inject a
	// fake. Default: from the kubeconfig.
	NewClientset func(context string) (API, error)
	// Log receives client-go's own messages (watch failures, throttling),
	// which would otherwise be written to stderr, over the screen.
	// Default: discarded.
	Log *slog.Logger
}

// Client implements ports.ClusterClient and ports.LogSource.
type Client struct {
	opts Options

	mu         sync.Mutex
	clientsets map[string]API
}

// New returns a client; nothing is contacted until a method is called.
func New(o Options) *Client {
	routeKlog(o.Log)
	if o.NewClientset == nil {
		o.NewClientset = func(ctx string) (API, error) { return fromKubeconfig(ctx, o.UserAgent) }
	}
	return &Client{opts: o, clientsets: map[string]API{}}
}

// clientset returns the clientset of a kube context ("" is the current
// context), built once.
func (c *Client) clientset(context string) (API, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cs, ok := c.clientsets[context]; ok {
		return cs, nil
	}
	cs, err := c.opts.NewClientset(context)
	if err != nil {
		return nil, err
	}
	c.clientsets[context] = cs
	return cs, nil
}

// fromKubeconfig loads the kubeconfig with the standard rules (KUBECONFIG,
// then ~/.kube/config) for one context.
func fromKubeconfig(context, userAgent string) (API, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: context}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		name := context
		if name == "" {
			name = "(current context)"
		}
		return nil, fmt.Errorf("kube context %s: %v: %w", name, err, domain.ErrConfig)
	}
	cfg.UserAgent = userAgent
	cfg.QPS, cfg.Burst = 20, 40 // one watch per namespace plus log streams
	var cs clients
	if cs.core, err = corev1client.NewForConfig(cfg); err == nil {
		if cs.apps, err = appsv1client.NewForConfig(cfg); err == nil {
			cs.batch, err = batchv1client.NewForConfig(cfg)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("kube context %s: %v: %w", context, err, domain.ErrConfig)
	}
	return cs, nil
}

var klogOnce sync.Once

// routeKlog sends client-go's logs (klog, a process-wide logger) to log,
// or discards them: nothing may write to the terminal under the TUI.
func routeKlog(log *slog.Logger) {
	klogOnce.Do(func() {
		if log == nil {
			log = slog.New(slog.NewTextHandler(io.Discard, nil))
		}
		klog.SetSlogLogger(log.With("source", "client-go"))
		utilruntime.ErrorHandlers = []utilruntime.ErrorHandler{
			func(_ context.Context, err error, msg string, kv ...any) {
				log.Debug(msg, append([]any{"source", "client-go", "err", err}, kv...)...)
			},
		}
	})
}
