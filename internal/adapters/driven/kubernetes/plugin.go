package kubernetes

import (
	"bufio"
	"io"
	"log/slog"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
	pluginStderr *os.File                     // write end of the pipe read by pluginOut
	pluginOut    atomic.Pointer[pluginOutput] // nil until a plugin is configured
)

// quietPlugin makes cfg's credential plugin non-interactive and runs build,
// which creates the clientsets (client-go captures os.Stderr then, in the
// plugin's authenticator, cached for the life of the process), with
// os.Stderr pointing to a pipe read by pluginOut.
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
		out := &pluginOutput{log: log.With("source", "auth-plugin"), logged: map[string]time.Time{}, now: time.Now}
		pluginOut.Store(out)
		go out.read(r)
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

const (
	// burstGap separates the output of two plugin runs.
	burstGap = 500 * time.Millisecond
	// maxBurst bounds the lines kept from one run.
	maxBurst = 50
	// relogAfter is how long a line already logged is not logged again:
	// a plugin that fails prints the same advice on every retry.
	relogAfter = 10 * time.Minute
)

// pluginOutput reads what credential plugins write: it logs each line once
// in a while, and keeps the lines of the latest run, whose error message
// says why the plugin failed. Diagnostic logging is off by default, so
// that reason is shown with the error.
type pluginOutput struct {
	log *slog.Logger
	now func() time.Time

	mu     sync.Mutex
	last   time.Time // when the latest line arrived
	burst  []string  // lines of the latest run, without klog headers
	logged map[string]time.Time
}

func (p *pluginOutput) read(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		p.add(sc.Text())
	}
}

func (p *pluginOutput) add(raw string) {
	line := strings.TrimSpace(klogHeader.ReplaceAllString(strings.TrimSpace(raw), ""))
	if line == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	if now.Sub(p.last) > burstGap {
		p.burst = p.burst[:0]
	}
	p.last = now
	if len(p.burst) < maxBurst {
		p.burst = append(p.burst, line)
	}
	if t, ok := p.logged[line]; ok && now.Sub(t) < relogAfter {
		return
	}
	if len(p.logged) > 256 {
		clear(p.logged)
	}
	p.logged[line] = now
	p.log.Warn(line)
}

// reason returns why the plugin run that just failed did, from its output,
// or "". The plugin has exited, so its output is in the pipe; it waits
// briefly for the reader to drain it.
func (p *pluginOutput) reason() string {
	const settle, maxWait, fresh = 20 * time.Millisecond, 150 * time.Millisecond, 2 * time.Second
	start := time.Now()
	for {
		time.Sleep(5 * time.Millisecond)
		p.mu.Lock()
		idle, lines := p.now().Sub(p.last), slices.Clone(p.burst)
		p.mu.Unlock()
		if waited := time.Since(start); waited >= settle && idle >= settle || waited >= maxWait {
			if idle > fresh {
				return "" // nothing from this run
			}
			return pickReason(lines)
		}
	}
}

// klogHeader is the prefix of klog lines (F1001 09:12:40.104512 48211
// cred.go:145] ), which Go plugins such as gke-gcloud-auth-plugin write.
var klogHeader = regexp.MustCompile(`^[IWEF]\d{4} \d{2}:\d{2}:\d{2}\.\d+\s+\d+ [^ \]]+\] `)

// cliCommand is the "(gcloud.config.config-helper) " a CLI's error starts
// with: the subcommand that failed, which tells the user nothing.
var cliCommand = regexp.MustCompile(`^\([\w.-]+\) `)

// pickReason picks the line that says why a plugin failed: the text after
// the last "ERROR: " (plugins wrap the error of the CLI they run), else the
// first line.
func pickReason(lines []string) string {
	reason := ""
	for i := len(lines) - 1; i >= 0 && reason == ""; i-- {
		if j := strings.LastIndex(lines[i], "ERROR: "); j >= 0 {
			reason = lines[i][j+len("ERROR: "):]
		}
	}
	if reason == "" && len(lines) > 0 {
		reason = lines[0]
	}
	reason = strings.TrimSpace(cliCommand.ReplaceAllString(reason, ""))
	if r := []rune(reason); len(r) > 300 {
		reason = string(r[:299]) + "…"
	}
	return reason
}

// pluginReason is why the credential plugin that just failed did, or "".
func pluginReason() string {
	if p := pluginOut.Load(); p != nil {
		return p.reason()
	}
	return ""
}
