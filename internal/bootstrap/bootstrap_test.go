package bootstrap

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/diag"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

// demoConfig is the embedded example folder, as --demo loads it.
func demoConfig(t *testing.T) *config.Config {
	t.Helper()
	c, err := LoadConfig(cli.Options{Demo: true}, noFiles())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// noFiles is an environment without HUGINN_CONFIG whose user config
// directory has no huginn folder.
func noFiles() Env {
	return Env{Getenv: func(string) string { return "" }, UserConfigDir: func() (string, error) { return "/none", nil }}
}

func TestResolveEnv(t *testing.T) {
	c := demoConfig(t)
	tests := []struct {
		name    string
		o       cli.Options
		vars    map[string]string
		want    string
		wantErr string
	}{
		{"default", cli.Options{}, nil, "rec", ""},
		{"env var", cli.Options{}, map[string]string{EnvEnv: "dev"}, "dev", ""},
		{"positional beats env var", cli.Options{EnvArg: "prd"}, map[string]string{EnvEnv: "dev"}, "prd", ""},
		{"flag", cli.Options{EnvFlag: "prprd"}, nil, "prprd", ""},
		{"same twice", cli.Options{EnvFlag: "prd", EnvArg: "prd"}, nil, "prd", ""},
		{"conflict", cli.Options{EnvFlag: "prd", EnvArg: "dev"}, nil, "", "given twice"},
		{"unknown", cli.Options{EnvArg: "qa"}, nil, "", `unknown environment "qa" (environments.yaml has: dev, rec, prprd, prd)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveEnv(tt.o, env(tt.vars), c)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got.String() != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestResolveTheme(t *testing.T) {
	c := demoConfig(t)
	if got := ResolveTheme("", env(nil), c); got != "light" {
		t.Errorf("default: %s", got)
	}
	if got := ResolveTheme("", env(map[string]string{EnvNoColor: "1", EnvTheme: "classic"}), c); got != "none" {
		t.Errorf("NO_COLOR: %s", got)
	}
	if got := ResolveTheme("classic", env(map[string]string{EnvNoColor: "1"}), c); got != "classic" {
		t.Errorf("flag: %s", got)
	}
	if got := ResolveTheme("", env(map[string]string{EnvTheme: "accessible"}), c); got != "accessible" {
		t.Errorf("HUGINN_THEME: %s", got)
	}
}

func TestBuildDemo(t *testing.T) {
	app, err := Build(cli.Options{EnvArg: "prd", Demo: true, Since: "1h"}, noFiles(), diag.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if app.UI.Source != "demo" || !app.UI.Env.Production || app.UI.Env.Namespaces[0] != "app-prd" {
		t.Fatalf("unexpected app: %+v", app.UI)
	}
	ws, err := app.Cluster.ListWorkloads(t.Context(), scopeOf(app))
	if err != nil || len(ws) == 0 {
		t.Fatalf("demo cluster not wired: %d, %v", len(ws), err)
	}
}

func TestBuildRejectsBadSince(t *testing.T) {
	_, err := Build(cli.Options{Demo: true, Since: "soon"}, noFiles(), diag.Discard())
	if err == nil || !strings.Contains(err.Error(), "--since") {
		t.Fatalf("err = %v", err)
	}
}

func TestMainVersion(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"--version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "huginn ") {
		t.Fatalf("version: %d %q %q", code, out.String(), errOut.String())
	}
}

func TestMissingFolderExitsWithTheStructure(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Main([]string{"--config", filepath.Join(t.TempDir(), "nope")}, &out, &errOut)
	if code != ExitConfig || !strings.Contains(errOut.String(), "formats/*.yaml") {
		t.Fatalf("code %d, stderr %q", code, errOut.String())
	}
}

func TestInvalidFolderListsEveryProblem(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../examples/config")); err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("layouts/spring.yaml", "version: 1\nstream:\n  columns:\n    - {name: time, show: \"{tme}\"}\n")
	write("ui.yaml", "version: 1\nkeymap: {folow: [f]}\n")
	var out, errOut bytes.Buffer
	code := Main([]string{"--config", dir}, &out, &errOut)
	msg := strings.Join(strings.Fields(errOut.String()), " ") // alignment is not part of the contract
	for _, want := range []string{
		"has 2 errors",
		`layouts/spring.yaml:4:20  stream.columns[0].show: unknown field "tme"`,
		`ui.yaml:2:1  keymap: unknown action "folow"`,
	} {
		if !strings.Contains(msg, strings.Join(strings.Fields(want), " ")) {
			t.Errorf("missing %q in:\n%s", want, errOut.String())
		}
	}
	if code != ExitConfig {
		t.Fatalf("exit code %d, want %d", code, ExitConfig)
	}
}

func TestMainReportsErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"rec", "prd"}, &out, &errOut); code != 1 || !strings.Contains(errOut.String(), "huginn:") {
		t.Fatalf("code %d, stderr %q", code, errOut.String())
	}
}

// scopeOf returns the cluster scope of the app's environment.
func scopeOf(a *App) ports.Scope {
	e := a.Config.Environments.ByName[a.Env.String()]
	return ports.Scope{Env: a.Env, Context: e.Context, Namespaces: e.Namespaces}
}

func TestContainersFlag(t *testing.T) {
	app, err := Build(cli.Options{Demo: true, Containers: "all"}, noFiles(), diag.Discard())
	if err != nil || app.UI.ContainerMode != domain.ContainersAll {
		t.Fatalf("--containers all: %v", err)
	}
	if _, err := Build(cli.Options{Demo: true, Containers: "sidecars"}, noFiles(), diag.Discard()); err == nil || !strings.Contains(err.Error(), "--containers") {
		t.Fatalf("bad value: %v", err)
	}
}
