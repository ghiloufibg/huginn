package kubernetes

import (
	"bufio"
	"log/slog"
	"os"
	"strings"
	"sync"

	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Credential plugins (on GKE, gke-gcloud-auth-plugin) are run by client-go
// with the process's stdin and stderr. Under the TUI, a plugin that
// prompts would steal keystrokes, and one that fails prints its advice
// ("Reauthentication failed… run gcloud auth login") over the screen, on
// every retry. client-go has no option for either, so: the plugin is never
// interactive, and its stderr goes to the log (D-053).

var (
	pluginMu     sync.Mutex // serializes the os.Stderr swap
	pluginOnce   sync.Once
	pluginStderr *os.File // write end of the pipe read by forwardPlugin
)

// quietPlugin makes cfg's credential plugin non-interactive and runs build,
// which creates the clientsets (client-go captures os.Stderr then, in the
// plugin's authenticator, cached for the life of the process), with
// os.Stderr pointing to a pipe whose lines are logged.
func quietPlugin(cfg *rest.Config, log *slog.Logger, build func() error) error {
	if cfg.ExecProvider == nil {
		return build()
	}
	cfg.ExecProvider.InteractiveMode = clientcmdapi.NeverExecInteractiveMode
	cfg.ExecProvider.StdinUnavailable = true
	pluginOnce.Do(func() {
		r, w, err := os.Pipe()
		if err != nil {
			return // keep the terminal: better garbled than no client
		}
		pluginStderr = w
		go forwardPlugin(r, log.With("source", "auth-plugin"))
	})
	if pluginStderr == nil {
		return build()
	}
	pluginMu.Lock()
	defer pluginMu.Unlock()
	saved := os.Stderr
	os.Stderr = pluginStderr
	defer func() { os.Stderr = saved }()
	return build()
}

// forwardPlugin logs each non-blank line a credential plugin writes.
func forwardPlugin(r *os.File, log *slog.Logger) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			log.Warn(line)
		}
	}
}
