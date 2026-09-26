package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (Options, string, error) {
	t.Helper()
	var got Options
	h := Handlers{
		Run:           func(_ context.Context, o Options) error { got = o; return nil },
		ConfigExample: func() []byte { return []byte("default_env: rec\n") },
		ConfigValidate: func(data []byte, name string) error {
			if strings.Contains(string(data), "bad") {
				return errors.New(name + ": bad config")
			}
			return nil
		},
	}
	cmd := NewRootCommand(h, "v1.0.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader("bad: true"))
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return got, out.String(), err
}

func TestFlagsAndPositionalEnv(t *testing.T) {
	o, _, err := run(t, "prd", "--repo", "payment-service", "--since", "1h", "--demo", "--theme", "none", "--config", "/c.yaml", "--log-level", "debug")
	if err != nil {
		t.Fatal(err)
	}
	want := Options{EnvArg: "prd", Repo: "payment-service", Since: "1h", Demo: true, Theme: "none", ConfigPath: "/c.yaml", LogLevel: "debug"}
	if o != want {
		t.Fatalf("got %+v", o)
	}
	o, _, _ = run(t, "-e", "dev")
	if o.EnvFlag != "dev" || o.EnvArg != "" {
		t.Fatalf("got %+v", o)
	}
}

func TestTooManyArgs(t *testing.T) {
	if _, _, err := run(t, "rec", "prd"); err == nil {
		t.Fatal("expected error")
	}
}

func TestVersion(t *testing.T) {
	_, out, err := run(t, "--version")
	if err != nil || out != "huginn v1.0.0\n" {
		t.Fatalf("%q %v", out, err)
	}
}

func TestConfigSubcommands(t *testing.T) {
	_, out, err := run(t, "config", "example")
	if err != nil || out != "default_env: rec\n" {
		t.Fatalf("example: %q %v", out, err)
	}
	_, _, err = run(t, "config", "validate", "-")
	if err == nil || !strings.Contains(err.Error(), "stdin: bad config") {
		t.Fatalf("validate: %v", err)
	}
}
