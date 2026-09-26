package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// valid is a minimal valid folder; tests change one file at a time.
func valid() fstest.MapFS {
	return fstest.MapFS{
		"huginn.yaml":        {Data: []byte("version: 1\ndefault_env: rec\n")},
		"environments.yaml":  {Data: []byte("version: 1\nenvironments:\n  rec: {namespaces: [app-rec]}\n  prd: {namespaces: [app-prd], production: true}\n")},
		"services.yaml":      {Data: []byte("version: 1\nresolve: [labels]\nlabel_keys: [app]\n")},
		"formats/app.yaml":   {Data: []byte("version: 1\ndecoder: json\nfields: {message: msg}\nlayout: basic\n")},
		"layouts/basic.yaml": {Data: []byte("version: 1\nstream:\n  columns:\n    - {name: time, key: t, show: \"{time}\", role: time}\n")},
	}
}

func load(t *testing.T, fsys fstest.MapFS) (*Config, string) {
	t.Helper()
	c, err := LoadFS(fsys, "cfg")
	if err == nil {
		return c, ""
	}
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *Error, got %v", err)
	}
	return nil, err.Error()
}

func wantErrors(t *testing.T, msg string, wants ...string) {
	t.Helper()
	if msg == "" {
		t.Fatal("expected errors, the folder loaded")
	}
	msg = strings.Join(strings.Fields(msg), " ") // alignment is not part of the contract
	for _, w := range wants {
		if !strings.Contains(msg, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("missing %q in:\n%s", w, msg)
		}
	}
}

func TestMinimalFolderLoadsWithNeutralDefaults(t *testing.T) {
	c, msg := load(t, valid())
	if msg != "" {
		t.Fatal(msg)
	}
	if strings.Join(c.Environments.Names, ",") != "rec,prd" {
		t.Fatalf("environments keep file order: %v", c.Environments.Names)
	}
	if c.Huginn.Windows.Default != "15m" || c.Huginn.Logs.BufferLines != 50000 || c.UI.Theme != "light" {
		t.Fatalf("defaults: %+v %+v", c.Huginn, c.UI)
	}
	l := c.Layouts["basic"]
	if l.File != "layouts/basic.yaml" || len(l.Zoom.Columns) != 1 || l.Stream.TimeFormat != "15:04:05.000" {
		t.Fatalf("layout defaults: %+v", l)
	}
	if c.Formats[0].Name != "app" || c.Formats[0].File != "formats/app.yaml" {
		t.Fatalf("format name from file: %+v", c.Formats[0])
	}
}

func TestExamplesLoad(t *testing.T) {
	dirs, _ := filepath.Glob("../../examples/config*")
	if len(dirs) == 0 {
		t.Fatal("no example folder")
	}
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		if _, err := LoadDir(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
}

func TestMissingAndUnexpectedFiles(t *testing.T) {
	fsys := valid()
	delete(fsys, "services.yaml")
	delete(fsys, "layouts/basic.yaml")
	fsys["enviroment.yaml"] = &fstest.MapFile{Data: []byte("x: 1")}
	fsys["README.md"] = &fstest.MapFile{Data: []byte("notes")}
	fsys[".git/HEAD"] = &fstest.MapFile{Data: []byte("ref")}
	fsys["formats/notes.txt"] = &fstest.MapFile{Data: []byte("x")}
	_, msg := load(t, fsys)
	wantErrors(t, msg,
		"services.yaml  missing file (required)",
		"enviroment.yaml  unexpected file (did you mean environments.yaml?)",
		"layouts/  missing folder",
		"formats/notes.txt  unexpected entry",
	)
	if strings.Contains(msg, "README") || strings.Contains(msg, ".git") {
		t.Fatalf("documentation and hidden files are ignored:\n%s", msg)
	}
}

func TestUnknownKeysAndTypesArePositioned(t *testing.T) {
	fsys := valid()
	fsys["services.yaml"].Data = []byte("version: 1\nresolver: [labels]\nlabel_keys: [app]\n")
	fsys["huginn.yaml"].Data = []byte("version: 1\ndefault_env: rec\nlogs:\n  buffer_lines: many\n")
	_, msg := load(t, fsys)
	wantErrors(t, msg,
		`services.yaml:2:1  unknown key "resolver" (did you mean "resolve"?)`,
		"huginn.yaml:4",
		"cannot unmarshal",
	)
}

func TestAllProblemsReportedAtOnce(t *testing.T) {
	fsys := valid()
	fsys["huginn.yaml"].Data = []byte("version: 2\ndefault_env: staging\nwindows:\n  presets: [15m, tail, soon]\n")
	fsys["environments.yaml"].Data = []byte("version: 1\nenvironments:\n  rec: {namespaces: [a], namespace_from: \"sops:x#K\"}\n  Prd: {}\n")
	fsys["services.yaml"].Data = []byte("version: 1\nresolve: [labels, magic]\n")
	fsys["formats/app.yaml"].Data = []byte("version: 1\ndecoder: regex\npattern: '(?P<time>\\S+) (?P<status>\\d+'\nlayout: fancy\n")
	fsys["formats/web.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndecoder: regex\npattern: '(?P<time>\\S+) (?P<status>\\d+)'\nlevel_from: {field: code, map: {\"5*\": fatal}}\nlayout: basic\n")}
	fsys["layouts/basic.yaml"].Data = []byte(`version: 1
stream:
  columns:
    - {name: time, key: t, show: "{time}"}
    - {name: time, key: p, show: "{level}", role: colour}
  separator: {text: ": ", after: [class]}
`)
	fsys["ui.yaml"] = &fstest.MapFile{Data: []byte("version: 1\nlog_columns: [time, trace]\n")}
	_, msg := load(t, fsys)
	wantErrors(t, msg,
		"huginn.yaml:1:1  version: must be 1",
		`huginn.yaml:2:1  default_env: "staging" is not in environments.yaml`,
		`huginn.yaml:4:18  windows.presets[1]: "tail" is not a duration`,
		`windows.presets[2]: "soon"`,
		"environments.rec: set namespaces or namespace_from, not both",
		"environments.Prd: invalid environment name",
		"environments.Prd: set namespaces or namespace_from",
		`services.yaml:2:19  resolve[1]: "magic" is not one of: explicit, labels, manifests`,
		"the labels rule needs label_keys",
		"formats/app.yaml:3:1  pattern: invalid regular expression",
		`formats/app.yaml:4:1  layout: "fancy" is not a file of layouts/`,
		`level_from.field: "code" is not a group of pattern`,
		"missing group (?P<message>…)",
		`"fatal" is not one of: error, warn, info, debug`,
		`stream.columns[1].name: "time" is already the name of stream.columns[0]`,
		`stream.columns[1].key: "p" is used by the columns picker`,
		`stream.columns[1].role: "colour" is not one of`,
		`stream.separator.after[0]: "class" is not a column of stream`,
		`ui.yaml:2:21  log_columns[1]: "trace" is neither pod nor a stream column`,
	)
	if !strings.HasPrefix(msg, "the config folder cfg has ") {
		t.Fatalf("header: %s", msg)
	}
}

func TestRequiredKeys(t *testing.T) {
	fsys := valid()
	fsys["huginn.yaml"].Data = []byte("version: 1\n")
	fsys["formats/app.yaml"].Data = []byte("version: 1\ndecoder: json\n")
	_, msg := load(t, fsys)
	wantErrors(t, msg,
		`huginn.yaml  missing required key "default_env"`,
		`formats/app.yaml  missing required key "layout"`,
		"the json decoder needs fields.message",
	)
}

func TestLocate(t *testing.T) {
	getenv := func(v string) func(string) string { return func(string) string { return v } }
	home := func() (string, error) { return "/home/u/.config", nil }
	cases := []struct {
		explicit, env, want string
	}{
		{"/a", "/b", "/a"},
		{"", "/b", "/b"},
		{"", "", filepath.Join("/home/u/.config", "huginn")},
	}
	for _, c := range cases {
		got, err := Locate(c.explicit, getenv(c.env), home)
		if err != nil || got != c.want {
			t.Errorf("Locate(%q, %q) = %q, %v; want %q", c.explicit, c.env, got, err, c.want)
		}
	}
}

func TestLoadDirWithoutFolderShowsTheStructure(t *testing.T) {
	_, err := LoadDir(filepath.Join(t.TempDir(), "missing"))
	if err == nil || !strings.Contains(err.Error(), "environments.yaml   required") {
		t.Fatalf("got %v", err)
	}
}

func TestSchemas(t *testing.T) {
	s, err := Schemas()
	if err != nil || len(s) != 7 || !strings.Contains(string(s["format.schema.json"]), `"required"`) {
		t.Fatalf("schemas: %d %v", len(s), err)
	}
}
