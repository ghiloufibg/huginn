package config

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExampleIsValidAndMatchesDefaults(t *testing.T) {
	c, err := Parse(Example, "example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultEnv != "rec" || c.Logs.DefaultWindow != "15m" || !c.Logs.RedactEnabled() || c.UI.Theme != "light" {
		t.Fatalf("unexpected values: %+v", c)
	}
	d := Default()
	if got, want := c.LogFormats["logstash"].Fields.TraceID, d.LogFormats["logstash"].Fields.TraceID; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("example logstash profile drifted from built-in default: %v vs %v", got, want)
	}
}

func TestDefaultsValidate(t *testing.T) {
	if err := Validate(Default(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyFileUsesDefaults(t *testing.T) {
	c, err := Parse(nil, "empty.yaml")
	if err != nil || c.DefaultEnv != "rec" || len(c.Environments) != 4 {
		t.Fatalf("got %+v, %v", c, err)
	}
}

func TestUnknownKeysReportLineAndSuggestion(t *testing.T) {
	in := "default_env: rec\nenvronments:\n  rec: {namespaces: [a]}\nlogs:\n  tail_line: 3\n"
	_, err := Parse([]byte(in), "c.yaml")
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *Error, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		`c.yaml:2: unknown key "envronments" (did you mean "environments"?)`,
		`c.yaml:5: logs: unknown key "tail_line" (did you mean "tail_lines"?)`,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("missing %q in:\n%s", want, msg)
		}
	}
}

func TestValidationCollectsAllProblems(t *testing.T) {
	in := `
default_env: staging
environments:
  rec: {}
  Bad_Name: {namespaces: [x]}
logs:
  default_window: soon
  buffer_lines: 10
  format: nope
ui:
  theme: dark
repos:
  - name: ""
    workloads: [{env: prd, name: api}]
`
	_, err := Parse([]byte(in), "c.yaml")
	if err == nil {
		t.Fatal("expected errors")
	}
	for _, want := range []string{
		`default_env: "staging" is not a configured environment`,
		"environments.rec: set namespaces or namespace_from",
		"environments.Bad_Name: invalid environment name",
		`logs.default_window: invalid time window "soon"`,
		"logs.buffer_lines: must be at least 1000",
		`logs.format: "nope" is not a defined log format`,
		`ui.theme: "dark" is not one of: light, accessible, classic, none`,
		"repos[0].name: must not be empty",
		`repos[0].workloads[0].env: "prd" is not a configured environment`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in:\n%v", want, err)
		}
	}
}

func TestPathsAcceptScalar(t *testing.T) {
	in := "log_formats:\n  mine:\n    decoder: json-fields\n    fields:\n      message: msg\nlogs:\n  format: mine\n"
	c, err := Parse([]byte(in), "c.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := c.LogFormats["mine"].Fields.Message; len(got) != 1 || got[0] != "msg" {
		t.Fatalf("got %v", got)
	}
	if _, ok := c.LogFormats["logstash"]; !ok {
		t.Fatal("built-in profile must stay available")
	}
}

func TestRedactCanBeDisabled(t *testing.T) {
	c, err := Parse([]byte("logs: {redact: false}"), "c.yaml")
	if err != nil || c.Logs.RedactEnabled() {
		t.Fatalf("redact should be off: %v", err)
	}
}

func testLocator(files map[string]string, env map[string]string) Locator {
	return Locator{
		Getenv:        func(k string) string { return env[k] },
		UserConfigDir: func() (string, error) { return "/cfg", nil },
		UserHomeDir:   func() (string, error) { return "/home/u", nil },
		ReadFile: func(p string) ([]byte, error) {
			if s, ok := files[filepath.ToSlash(p)]; ok {
				return []byte(s), nil
			}
			return nil, fs.ErrNotExist
		},
	}
}

func TestLoadLookupOrder(t *testing.T) {
	files := map[string]string{
		"/explicit.yaml":                     "default_env: prd",
		"/env.yaml":                          "default_env: dev",
		"/cfg/huginn/config.yaml":            "default_env: prprd",
		"/home/u/.config/huginn/config.yaml": "default_env: rec",
	}
	tests := []struct {
		name, explicit string
		env            map[string]string
		files          map[string]string
		wantEnv        string
		wantPath       string
	}{
		{"flag wins", "/explicit.yaml", map[string]string{EnvConfigPath: "/env.yaml"}, files, "prd", "/explicit.yaml"},
		{"env var", "", map[string]string{EnvConfigPath: "/env.yaml"}, files, "dev", "/env.yaml"},
		{"os config dir", "", nil, files, "prprd", "/cfg/huginn/config.yaml"},
		{"home fallback", "", nil, map[string]string{"/home/u/.config/huginn/config.yaml": "default_env: rec"}, "rec", "/home/u/.config/huginn/config.yaml"},
		{"no file", "", nil, nil, "rec", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, path, err := Load(testLocator(tt.files, tt.env), tt.explicit)
			if err != nil {
				t.Fatal(err)
			}
			if c.DefaultEnv != tt.wantEnv || filepath.ToSlash(path) != tt.wantPath {
				t.Fatalf("got %s from %q", c.DefaultEnv, path)
			}
		})
	}
}

func TestLoadExplicitMissingFails(t *testing.T) {
	if _, _, err := Load(testLocator(nil, nil), "/nope.yaml"); err == nil {
		t.Fatal("missing explicit file must fail")
	}
}

func TestSchemaUpToDate(t *testing.T) {
	want, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../docs/config.schema.json")
	if err != nil {
		t.Fatalf("%v (run: make schema)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(got), bytes.TrimSpace(want)) {
		t.Fatal("docs/config.schema.json is stale; run: make schema")
	}
}

func TestEnvNamesOrder(t *testing.T) {
	c := &Config{Environments: map[string]Environment{"prd": {}, "zeta": {}, "dev": {}, "alpha": {}, "rec": {}}}
	if got := strings.Join(EnvNames(c), ","); got != "dev,rec,prd,alpha,zeta" {
		t.Fatalf("got %s", got)
	}
}

func TestExamplesCopyUpToDate(t *testing.T) {
	got, err := os.ReadFile("../../examples/config.yaml")
	if err != nil || !bytes.Equal(got, Example) {
		t.Fatal("examples/config.yaml is stale; run: make schema")
	}
}
