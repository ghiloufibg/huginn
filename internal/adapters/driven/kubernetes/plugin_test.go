package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// syncBuffer is a bytes.Buffer safe for the plugin forwarder's goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestFailingCredentialPlugin: a kubeconfig whose exec plugin fails, as
// gke-gcloud-auth-plugin does when the gcloud session has expired. The
// failure reads as "not logged in", and the plugin's advice goes to the
// log, not over the screen.
func TestFailingCredentialPlugin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake plugin is a shell script")
	}
	dir := t.TempDir()
	plugin := filepath.Join(dir, "fake-auth-plugin")
	script := "#!/bin/sh\necho 'ERROR: Reauthentication failed.' >&2\necho 'Please run: gcloud auth login' >&2\nexit 1\n"
	if err := os.WriteFile(plugin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config")
	kubeconfig := `apiVersion: v1
kind: Config
current-context: gke
clusters:
- name: gke
  cluster: {server: "https://127.0.0.1:1"}
contexts:
- name: gke
  context: {cluster: gke, user: gke}
users:
- name: gke
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: ` + plugin + `
      interactiveMode: Always
`
	if err := os.WriteFile(cfg, []byte(kubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", cfg)
	var log syncBuffer
	c := New(Options{UserAgent: "huginn/test", Log: slog.New(slog.NewTextHandler(&log, nil))})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := c.ListPods(ctx, ports.Scope{Env: "rec", Context: "gke", Namespaces: []string{ns}}, nil)
	// interactiveMode Always would fail without a terminal ("exec plugin
	// cannot support interactive mode"): the plugin must have run.
	if !errors.Is(err, domain.ErrUnauthorized) || !strings.Contains(err.Error(), "executable fake-auth-plugin failed with exit code 1") {
		t.Fatalf("err = %v", err)
	}
	for deadline := time.Now().Add(5 * time.Second); !strings.Contains(log.String(), "Please run: gcloud auth login"); {
		if time.Now().After(deadline) {
			t.Fatalf("the plugin's stderr is not in the log:\n%s", log.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(log.String(), "source=auth-plugin") {
		t.Errorf("log lines name their source:\n%s", log.String())
	}
}
