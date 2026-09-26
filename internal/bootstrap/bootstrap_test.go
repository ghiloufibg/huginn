package bootstrap

import (
	"bytes"
	"io/fs"
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/diag"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolveEnv(t *testing.T) {
	c := config.Default()
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
		{"unknown", cli.Options{EnvArg: "qa"}, nil, "", `unknown environment "qa" (configured: dev, rec, prprd, prd)`},
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
	c := config.Default()
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

func noFiles() config.Locator {
	return config.Locator{
		Getenv:        func(string) string { return "" },
		UserConfigDir: func() (string, error) { return "/none", nil },
		UserHomeDir:   func() (string, error) { return "/none", nil },
		ReadFile:      func(string) ([]byte, error) { return nil, fs.ErrNotExist },
	}
}

func TestBuildDemo(t *testing.T) {
	app, err := Build(cli.Options{EnvArg: "prd", Demo: true, Since: "1h"}, noFiles(), env(nil), diag.Discard())
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
	_, err := Build(cli.Options{Demo: true, Since: "soon"}, noFiles(), env(nil), diag.Discard())
	if err == nil || !strings.Contains(err.Error(), "--since") {
		t.Fatalf("err = %v", err)
	}
}

func TestMainVersionAndConfigExample(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := Main([]string{"--version"}, &out, &errOut); code != 0 || !strings.HasPrefix(out.String(), "huginn ") {
		t.Fatalf("version: %d %q %q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := Main([]string{"config", "example"}, &out, &errOut); code != 0 || !bytes.Equal(out.Bytes(), config.Example) {
		t.Fatalf("config example: %d", code)
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
	e := a.Config.Environments[a.Env.String()]
	return ports.Scope{Env: a.Env, Context: e.Context, Namespaces: e.Namespaces}
}
