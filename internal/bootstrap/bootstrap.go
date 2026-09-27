package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/pprof"
	"syscall"

	"github.com/ghiloufibg/huginn/examples"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/clock"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/tui"
	"github.com/ghiloufibg/huginn/internal/buildinfo"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/diag"
)

// Exit codes.
const (
	ExitError  = 1 // runtime error
	ExitConfig = 2 // the config folder is missing or invalid, or no terminal to run in
)

// Main runs Huginn with the given arguments and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := cli.NewRootCommand(cli.Handlers{Run: run}, buildinfo.String())
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, "huginn:", err)
		var ce *config.Error
		var nc noConfigError
		if errors.As(err, &ce) || errors.As(err, &nc) || errors.Is(err, tui.ErrNoTerminal) {
			return ExitConfig
		}
		return ExitError
	}
	return 0
}

// App is everything run needs, resolved from options and configuration.
type App struct {
	Config  *config.Config
	Env     domain.Env
	Cluster Cluster
	UI      tui.Options
	Log     *slog.Logger
}

// Env is how Build reaches the process environment (injected for tests).
type Env struct {
	Getenv        func(string) string
	UserConfigDir func() (string, error)
}

// SystemEnv is the process environment.
func SystemEnv() Env { return Env{Getenv: os.Getenv, UserConfigDir: os.UserConfigDir} }

// noConfigError reports a config folder that cannot be found or read.
type noConfigError struct{ error }

func (e noConfigError) Unwrap() error { return e.error }

// LoadConfig reads the config folder: --config, else (with --demo) the
// embedded example folder, else HUGINN_CONFIG or the user config
// directory. The problems found while compiling it (layout templates,
// keymap) are reported with the load problems, all at once.
func LoadConfig(o cli.Options, e Env) (*config.Config, error) {
	var c *config.Config
	var err error
	if o.Demo && o.ConfigPath == "" {
		c, err = config.LoadFS(examples.Demo(), "examples/config (embedded, --demo)")
	} else {
		dir, lerr := config.Locate(o.ConfigPath, e.Getenv, e.UserConfigDir)
		if lerr != nil {
			return nil, noConfigError{lerr}
		}
		c, err = config.LoadDir(dir)
	}
	var ce *config.Error
	switch {
	case err != nil && !errors.As(err, &ce):
		return nil, noConfigError{err}
	case err != nil:
		ce.Problems = append(ce.Problems, compileProblems(c)...)
		return nil, ce
	}
	if probs := compileProblems(c); len(probs) > 0 {
		return nil, &config.Error{Dir: c.Dir, Problems: probs}
	}
	return c, nil
}

// compileProblems checks what only the adapters can: layout templates and
// the keymap.
func compileProblems(c *config.Config) []config.Problem {
	_, probs := compileLayouts(c)
	if _, err := tui.NewKeymap(c.UI.Keymap); err != nil {
		probs = append(probs, c.Problem(config.FileUI, "keymap", "%v", err))
	}
	return probs
}

// Build resolves options and configuration into an App without starting
// the UI, so the wiring can be tested. Problems found while compiling the
// folder (layout templates, keymap) are reported like load problems.
func Build(o cli.Options, e Env, log *slog.Logger) (*App, error) {
	c, err := LoadConfig(o, e)
	if err != nil {
		return nil, err
	}
	log.Info("configuration loaded", "folder", c.Dir)
	lp, probs := logParts(c)
	if len(probs) > 0 {
		return nil, &config.Error{Dir: c.Dir, Problems: probs}
	}
	keys, err := tui.NewKeymap(c.UI.Keymap)
	if err != nil {
		return nil, err
	}
	env, err := ResolveEnv(o, e.Getenv, c)
	if err != nil {
		return nil, err
	}
	w := c.Huginn.Windows
	window, err := domain.ParseTimeWindow(w.Default, w.TailLines)
	if err != nil {
		return nil, err
	}
	if o.Since != "" {
		if window, err = domain.ParseTimeWindow(o.Since, w.TailLines); err != nil {
			return nil, fmt.Errorf("--since: %w", err)
		}
	}
	clientName := "kubernetes"
	if o.Demo {
		clientName = "demo"
	}
	factory, err := clusterRegistry().Lookup(clientName)
	if err != nil {
		return nil, err
	}
	clk := clock.New()
	cluster, err := factory(c, clk, log)
	if err != nil {
		return nil, err
	}
	theme, err := tui.NewTheme(ResolveTheme(o.Theme, e.Getenv, c), c.UI.PaintBackground)
	if err != nil {
		return nil, err
	}
	filter := containerFilter(c)
	envs := make([]tui.EnvInfo, 0, len(c.Environments.Names))
	var current tui.EnvInfo
	for _, name := range c.Environments.Names {
		ce := c.Environments.ByName[name]
		info := tui.EnvInfo{Name: name, Context: ce.Context, Namespaces: ce.Namespaces, Production: ce.Production}
		envs = append(envs, info)
		if name == env.String() {
			current = info
		}
	}
	return &App{
		Config: c, Env: env, Cluster: cluster, Log: log,
		UI: tui.Options{
			Env: current, Envs: envs, Theme: theme, Keys: keys, Source: clientName, Repo: o.Repo,
			Catalog: newCatalog(c, cluster, clk, filter, log), Filter: filter,
			Sessions: newLogSessions(c, cluster, clk, filter, lp.decoders, log),
			Events:   newPodEvents(c, cluster),
			Layouts:  lp.layouts, Layout: lp.fallback, Columns: lp.columns,
			Windows: windows(c), Window: window,
			BufferLines: c.Huginn.Logs.BufferLines, KeyBar: c.UI.KeyBar, LogColumns: c.UI.LogColumns,
		},
	}, nil
}

// windows returns the presets of keys 1…7 followed by the tail window.
func windows(c *config.Config) []domain.TimeWindow {
	w := c.Huginn.Windows
	var out []domain.TimeWindow
	for _, p := range w.Presets {
		if tw, err := domain.ParseTimeWindow(p, w.TailLines); err == nil {
			out = append(out, tw)
		}
	}
	return append(out, domain.TimeWindow{Tail: w.TailLines})
}

func run(ctx context.Context, o cli.Options) error {
	log, closer, _, err := diag.Open(diag.Options{Level: o.LogLevel, Debug: os.Getenv(diag.EnvDebug), Dir: cacheDir()})
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	app, err := Build(o, SystemEnv(), log)
	if err != nil {
		return err
	}
	log.Info("starting", "version", buildinfo.String(), "env", app.Env, "cluster", app.UI.Source)
	stopProfile, err := startCPUProfile(os.Getenv(EnvCPUProfile))
	if err != nil {
		return err
	}
	defer stopProfile()
	err = tui.Run(ctx, app.UI)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// EnvCPUProfile names a file receiving a CPU profile of the whole session
// (go tool pprof), to diagnose performance on a user's machine.
const EnvCPUProfile = "HUGINN_CPUPROFILE"

func startCPUProfile(path string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvCPUProfile, err)
	}
	if err := pprof.StartCPUProfile(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", EnvCPUProfile, err)
	}
	return func() { pprof.StopCPUProfile(); _ = f.Close() }, nil
}

func cacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "huginn")
}
