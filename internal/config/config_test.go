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
	if c.Huginn.Windows.Default != "15m" || c.Huginn.Windows.HeadLines != 500 || c.Huginn.Logs.BufferLines != 50000 || c.UI.Theme != "light" {
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
	dirs = append(dirs, "../../deploy/lab/config")
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

func TestHeadWindow(t *testing.T) {
	fsys := valid()
	fsys["huginn.yaml"].Data = []byte("version: 1\ndefault_env: rec\nwindows:\n  head_lines: 200\n  default: head\n")
	c, msg := load(t, fsys)
	if msg != "" {
		t.Fatal(msg)
	}
	if c.Huginn.Windows.HeadLines != 200 || c.Huginn.Windows.Default != "head" {
		t.Fatalf("windows: %+v", c.Huginn.Windows)
	}
	fsys["huginn.yaml"].Data = []byte("version: 1\ndefault_env: rec\nwindows:\n  presets: [15m, head]\n  head_lines: 60000\n  default: head:70000\n")
	_, msg = load(t, fsys)
	wantErrors(t, msg,
		`windows.presets[1]: "head" is not a duration`,
		"windows.head_lines: must not exceed logs.buffer_lines (50000)",
		"windows.default: head size 70000 exceeds logs.buffer_lines (50000)",
	)
}

func TestMute(t *testing.T) {
	fsys := valid()
	fsys["formats/app.yaml"].Data = []byte("version: 1\ndecoder: json\nfields: {message: msg, logger: logger}\nmute:\n  loggers: [com.example.pool.Pool, com.example.metrics.*]\n  keep: [error]\nlayout: basic\n")
	c, msg := load(t, fsys)
	if msg != "" {
		t.Fatal(msg)
	}
	if m := c.Formats[0].Mute; len(m.Loggers) != 2 || m.Keep[0] != "error" {
		t.Fatalf("mute: %+v", m)
	}
	fsys["formats/app.yaml"].Data = []byte("version: 1\ndecoder: json\nfields: {message: msg}\nmute:\n  loggers: [a.*, \"*\", com.*.Pool, a.*]\n  keep: [fatal]\nlayout: basic\n")
	fsys["formats/web.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndecoder: regex\npattern: '(?P<message>.*)'\nmute: {loggers: [x]}\nlayout: basic\n")}
	fsys["formats/zz.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndecoder: plain\nmute: {loggers: [x]}\nlayout: basic\n")}
	fsys["formats/zzz.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndecoder: plain\nmute: {keep: [error]}\nlayout: basic\n")}
	_, msg = load(t, fsys)
	wantErrors(t, msg,
		`mute.loggers[1]: logger pattern "*" matches every logger`,
		`mute.loggers[2]: logger pattern "com.*.Pool": * is only allowed at the end`,
		`mute.loggers[3]: "a.*" is already mute.loggers[0]`,
		`mute.keep[0]: "fatal" is not one of: error, warn, info, debug`,
		"formats/app.yaml:4:1  mute: muting loggers needs fields.logger",
		"formats/web.yaml:4:1  mute: muting loggers needs a group (?P<logger>…) in pattern",
		"formats/zz.yaml:3:1  mute: the plain decoder reads no logger to mute",
		"mute.keep: keep needs mute.loggers",
	)
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

func TestContainersModeAndStandalonePods(t *testing.T) {
	c, msg := load(t, valid())
	if msg != "" || c.Containers.DefaultMode != "" || !c.Services.ShowStandalone() {
		t.Fatalf("defaults: %q %+v", msg, c.Services)
	}
	fs := valid()
	fs["containers.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndefault_mode: all\n")}
	fs["services.yaml"] = &fstest.MapFile{Data: []byte("version: 1\nresolve: [labels]\nlabel_keys: [app]\nstandalone_pods: false\n")}
	c, msg = load(t, fs)
	if msg != "" || c.Containers.DefaultMode != "all" || c.Services.ShowStandalone() {
		t.Fatalf("set: %q %+v %+v", msg, c.Containers, c.Services)
	}
	fs["containers.yaml"] = &fstest.MapFile{Data: []byte("version: 1\ndefault_mode: sidecars\n")}
	_, msg = load(t, fs)
	wantErrors(t, msg, "containers.yaml", "default_mode")
}
