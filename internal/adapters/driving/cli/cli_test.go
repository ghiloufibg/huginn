package cli

import (
	"bytes"
	"context"
	"io"
	"testing"
)

func run(t *testing.T, args ...string) (Options, string, error) {
	t.Helper()
	var got Options
	h := Handlers{Run: func(_ context.Context, o Options) error { got = o; return nil }}
	cmd := NewRootCommand(h, "v1.0.0")
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.ExecuteContext(context.Background())
	return got, out.String(), err
}

func TestFlagsAndPositionalEnv(t *testing.T) {
	o, _, err := run(t, "prd", "--repo", "payment-service", "--since", "1h", "--demo", "--theme", "none", "--config", "/c", "--log-level", "debug")
	if err != nil {
		t.Fatal(err)
	}
	want := Options{EnvArg: "prd", Repo: "payment-service", Since: "1h", Demo: true, Theme: "none", ConfigPath: "/c", LogLevel: "debug"}
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

func TestNoConfigSubcommands(t *testing.T) {
	if _, _, err := run(t, "config", "validate"); err == nil {
		t.Fatal("the config folder is validated at startup; there are no config subcommands")
	}
}

func TestKafkaSubcommands(t *testing.T) {
	var got KafkaOptions
	var which string
	h := Handlers{
		Run:        func(context.Context, Options) error { which = "run"; return nil },
		KafkaCheck: func(_ context.Context, o KafkaOptions, _ io.Writer) error { which, got = "check", o; return nil },
		KafkaRead:  func(_ context.Context, o KafkaOptions, _ io.Writer) error { which, got = "read", o; return nil },
	}
	exec := func(args ...string) error {
		cmd := NewRootCommand(h, "v1")
		cmd.SetArgs(args)
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		return cmd.ExecuteContext(context.Background())
	}
	if err := exec("kafka", "check", "orders", "-e", "rec", "--config", "/c"); err != nil || which != "check" || got.Repo != "orders" || got.EnvFlag != "rec" || got.ConfigPath != "/c" {
		t.Fatalf("check: %v %s %+v", err, which, got)
	}
	if err := exec("kafka", "read", "orders", "orders.in", "--since", "1h", "-f", "--committed", "--raw", "--no-decode", "--demo"); err != nil || which != "read" ||
		got.Topic != "orders.in" || got.Since != "1h" || !got.Follow || !got.Committed || !got.Raw || !got.NoDecode || !got.Demo {
		t.Fatalf("read: %v %+v", err, got)
	}
	if err := exec("kafka", "read", "orders", "t", "--since", "1h", "--tail", "5"); err == nil {
		t.Fatal("--since and --tail together")
	}
	if err := exec("kafka", "read", "orders"); err == nil {
		t.Fatal("read needs a topic")
	}
	if err := exec("prd"); err != nil || which != "run" {
		t.Fatalf("an environment argument still opens the TUI: %v %s", err, which)
	}
}
