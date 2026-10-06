package kubernetes

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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
	if !errors.Is(err, domain.ErrUnauthorized) || !strings.HasSuffix(err.Error(), "executable fake-auth-plugin failed with exit code 1: Reauthentication failed.") {
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

// TestMissingCredentialPlugin: a kubeconfig naming a plugin that is not
// installed is a setup error, not a login one.
func TestMissingCredentialPlugin(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ command, want string }{
		{"huginn-no-such-plugin", "executable huginn-no-such-plugin not found. Install it first."},
		// A path: "fork/exec no-such-plugin: no such file or directory" on
		// Unix, "executable no-such-plugin not found" on Windows (LookPath
		// tries the .exe extensions).
		{filepath.Join(dir, "no-such-plugin"), "no-such-plugin"},
	} {
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
      command: ` + c.command + `
      installHint: Install it first.
`
		if err := os.WriteFile(cfg, []byte(kubeconfig), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KUBECONFIG", cfg)
		_, err := New(Options{UserAgent: "huginn/test"}).ListPods(context.Background(), ports.Scope{Env: "rec", Context: "gke", Namespaces: []string{ns}}, nil)
		if !errors.Is(err, domain.ErrConfig) || !strings.Contains(err.Error(), c.want) || strings.Contains(err.Error(), dir) {
			t.Errorf("%s: err = %v", c.command, err)
		}
	}
}

func TestPickReason(t *testing.T) {
	gcloud := []string{
		"print credential failed with error: Failed to retrieve access token:: failure while executing gcloud, with args [config config-helper --format=json]: exit status 1 (err: ERROR: (gcloud.config.config-helper) There was a problem refreshing your current auth tokens: Reauthentication failed. cannot prompt during non-interactive execution.",
		"Please run:",
		"$ gcloud auth login",
	}
	cases := []struct {
		lines []string
		want  string
	}{
		{gcloud, "There was a problem refreshing your current auth tokens: Reauthentication failed. cannot prompt during non-interactive execution."},
		{[]string{"token expired", "try again"}, "token expired"},
		{nil, ""},
		{[]string{strings.Repeat("x", 400)}, strings.Repeat("x", 299) + "…"},
	}
	for _, c := range cases {
		if got := pickReason(c.lines); got != c.want {
			t.Errorf("pickReason(%q) = %q, want %q", c.lines, got, c.want)
		}
	}
}

// TestPluginOutput: klog headers are dropped, the latest run is kept, and
// a plugin failing on every retry is logged once in a while, not each time.
func TestPluginOutput(t *testing.T) {
	var log bytes.Buffer
	now := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	p := &pluginOutput{log: slog.New(slog.NewTextHandler(&log, nil)), logged: map[string]time.Time{}, now: func() time.Time { return now }}
	run := func() {
		p.add("F1001 09:12:40.104512   48211 cred.go:145] ERROR: (gcloud.auth) Reauthentication failed.")
		p.add("")
		p.add("  Please run: gcloud auth login")
	}
	run()
	if !slices.Equal(p.burst, []string{"ERROR: (gcloud.auth) Reauthentication failed.", "Please run: gcloud auth login"}) {
		t.Fatalf("burst = %q", p.burst)
	}
	now = now.Add(30 * time.Second)
	run()
	if len(p.burst) != 2 || strings.Count(log.String(), "Reauthentication failed") != 1 {
		t.Fatalf("a retry starts a new run and is not logged again: %q\n%s", p.burst, log.String())
	}
	now = now.Add(relogAfter)
	run()
	if strings.Count(log.String(), "Reauthentication failed") != 2 {
		t.Fatalf("logged again after %v:\n%s", relogAfter, log.String())
	}
	if strings.Contains(log.String(), "cred.go") {
		t.Errorf("klog header logged:\n%s", log.String())
	}
}
