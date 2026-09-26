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
	"syscall"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/clock"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/springlayout"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/tui"
	"github.com/ghiloufibg/huginn/internal/buildinfo"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/diag"
)

// Main runs Huginn with the given arguments and returns the exit code.
func Main(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd := cli.NewRootCommand(cli.Handlers{
		Run:            run,
		ConfigExample:  func() []byte { return config.Example },
		ConfigValidate: func(data []byte, name string) error { _, err := config.Parse(data, name); return err },
	}, buildinfo.String())
	cmd.SetArgs(args)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	if err := cmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(stderr, "huginn:", err)
		return 1
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

// Build resolves options and configuration into an App without starting
// the UI, so the wiring can be tested.
func Build(o cli.Options, loc config.Locator, getenv func(string) string, log *slog.Logger) (*App, error) {
	c, path, err := config.Load(loc, o.ConfigPath)
	if err != nil {
		return nil, err
	}
	log.Info("configuration loaded", "path", path)
	env, err := ResolveEnv(o, getenv, c)
	if err != nil {
		return nil, err
	}
	window, err := domain.ParseTimeWindow(c.Logs.DefaultWindow, c.Logs.TailLines)
	if err != nil {
		return nil, err
	}
	if o.Since != "" {
		if window, err = domain.ParseTimeWindow(o.Since, c.Logs.TailLines); err != nil {
			return nil, fmt.Errorf("--since: %w", err)
		}
	}
	clientName := c.Cluster.Client
	if o.Demo {
		clientName = "demo"
	}
	factory, err := clusterRegistry().Lookup(clientName)
	if err != nil {
		return nil, err
	}
	clk := clock.New()
	cluster, err := factory(c, clk)
	if err != nil {
		return nil, err
	}
	theme, err := tui.NewTheme(ResolveTheme(o.Theme, getenv, c), c.UI.PaintBackground)
	if err != nil {
		return nil, err
	}
	keys, err := tui.NewKeymap(c.UI.Keymap)
	if err != nil {
		return nil, err
	}
	filter := containerFilter(c)
	dec, renderer, err := logParts(c)
	if err != nil {
		return nil, err
	}
	envs := make([]tui.EnvInfo, 0, len(c.Environments))
	var current tui.EnvInfo
	for _, name := range config.EnvNames(c) {
		e := c.Environments[name]
		info := tui.EnvInfo{Name: name, Context: e.Context, Namespaces: e.Namespaces, Production: e.Production}
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
			Sessions: newLogSessions(c, cluster, clk, filter, dec, log),
			Renderer: renderer, FullRenderer: springlayout.NewFull(),
			Windows: domain.DefaultWindowPresets(c.Logs.TailLines), Window: window,
			BufferLines: c.Logs.BufferLines,
		},
	}, nil
}

func run(ctx context.Context, o cli.Options) error {
	log, closer, _, err := diag.Open(diag.Options{Level: o.LogLevel, Debug: os.Getenv(diag.EnvDebug), Dir: cacheDir()})
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	app, err := Build(o, config.SystemLocator(), os.Getenv, log)
	if err != nil {
		return err
	}
	log.Info("starting", "version", buildinfo.String(), "env", app.Env, "cluster", app.UI.Source)
	err = tui.Run(ctx, app.UI)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func cacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "huginn")
}
