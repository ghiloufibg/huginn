package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// EnvConfigDir is the environment variable naming the config folder.
const EnvConfigDir = "HUGINN_CONFIG"

// The fixed names of the config folder (docs/CONFIG.md).
const (
	FileHuginn       = "huginn.yaml"
	FileEnvironments = "environments.yaml"
	FileServices     = "services.yaml"
	FileContainers   = "containers.yaml"
	FileUI           = "ui.yaml"
	DirFormats       = "formats"
	DirLayouts       = "layouts"
)

// Structure is the expected folder tree, printed when no folder is found.
const Structure = `  huginn.yaml         required  default environment, time windows
  environments.yaml   required  kube contexts and namespaces
  services.yaml       required  how workloads map to repositories
  containers.yaml     optional  sidecars to hide
  ui.yaml             optional  theme, keymap, key bar
  formats/*.yaml      at least one: how to read log lines
  layouts/*.yaml      at least one: how to draw log lines`

// Locate returns the config folder: explicit (--config), then
// HUGINN_CONFIG, then <user config dir>/huginn.
func Locate(explicit string, getenv func(string) string, userConfigDir func() (string, error)) (string, error) {
	if explicit != "" {
		return ExpandHome(explicit), nil
	}
	if dir := getenv(EnvConfigDir); dir != "" {
		return ExpandHome(dir), nil
	}
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("no config folder given (--config or %s) and no user config directory: %w", EnvConfigDir, err)
	}
	return filepath.Join(dir, "huginn"), nil
}

// LoadDir reads and validates the config folder at dir.
func LoadDir(dir string) (*Config, error) {
	info, err := os.Stat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("no config folder at %s.\nCreate it with this structure (see docs/CONFIG.md and examples/config/):\n%s", dir, Structure)
	case err != nil:
		return nil, err
	case !info.IsDir():
		return nil, fmt.Errorf("%s is not a folder: --config and %s name the config folder (docs/CONFIG.md)", dir, EnvConfigDir)
	}
	return LoadFS(os.DirFS(dir), dir)
}

// LoadFS reads and validates a config folder from fsys; name is used in
// messages. Every problem is returned at once in an *Error; the config is
// then partial, returned only so later checks (templates, keymap) can add
// their problems to the same report.
func LoadFS(fsys fs.FS, name string) (*Config, error) {
	l := &loader{fsys: fsys, pos: map[string]positions{}}
	c := &Config{Dir: name, Layouts: map[string]Layout{}}
	l.checkRoot()
	if l.read(FileHuginn, &c.Huginn, true) {
		l.version(FileHuginn, c.Huginn.Version)
	}
	var envs EnvironmentsFile
	if l.read(FileEnvironments, &envs, true) {
		l.version(FileEnvironments, envs.Version)
		c.Environments = Environments{Names: l.mapOrder(FileEnvironments, "environments"), ByName: envs.Environments}
	}
	if l.read(FileServices, &c.Services, true) {
		l.version(FileServices, c.Services.Version)
	}
	if l.read(FileContainers, &c.Containers, false) {
		l.version(FileContainers, c.Containers.Version)
	}
	if l.read(FileUI, &c.UI, false) {
		l.version(FileUI, c.UI.Version)
	}
	for _, f := range l.dir(DirFormats) {
		var fm Format
		if l.read(f.file, &fm, true) {
			l.version(f.file, fm.Version)
			fm.Name, fm.File = f.name, f.file
			c.Formats = append(c.Formats, fm)
		}
	}
	for _, f := range l.dir(DirLayouts) {
		var lo Layout
		if l.read(f.file, &lo, true) {
			l.version(f.file, lo.Version)
			lo.Name, lo.File = f.name, f.file
			c.Layouts[f.name] = lo
		}
	}
	c.pos = l.pos
	applyDefaults(c)
	l.probs = append(l.probs, validate(c)...)
	if len(l.probs) > 0 {
		return c, &Error{Dir: name, Problems: l.probs}
	}
	return c, nil
}

type loader struct {
	fsys  fs.FS
	pos   map[string]positions
	probs []Problem
}

func (l *loader) add(file string, line, col int, format string, args ...any) {
	l.probs = append(l.probs, Problem{File: file, Line: line, Col: col, Msg: fmt.Sprintf(format, args...)})
}

var rootNames = []string{FileHuginn, FileEnvironments, FileServices, FileContainers, FileUI, DirFormats, DirLayouts}

// ignored reports names that may sit in the folder without being read:
// hidden files (.git) and documentation (*.md).
func ignored(name string) bool { return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".md") }

// checkRoot reports unexpected entries, so a misspelled file is never
// silently skipped.
func (l *loader) checkRoot() {
	entries, err := fs.ReadDir(l.fsys, ".")
	if err != nil {
		l.add("", 0, 0, "cannot read the folder: %v", err)
		return
	}
	for _, e := range entries {
		n := e.Name()
		if ignored(n) || slices.Contains(rootNames, n) {
			continue
		}
		msg := "unexpected " + map[bool]string{true: "folder", false: "file"}[e.IsDir()]
		if s := closest(n, rootNames); s != "" {
			msg += fmt.Sprintf(" (did you mean %s?)", s)
		} else {
			msg += " (expected: " + strings.Join(rootNames, ", ") + ")"
		}
		l.add(n, 0, 0, "%s", msg)
	}
}

type dirFile struct{ file, name string }

var fileNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// dir lists the YAML files of formats/ or layouts/, sorted by name; at
// least one is required.
func (l *loader) dir(d string) []dirFile {
	entries, err := fs.ReadDir(l.fsys, d)
	if errors.Is(err, fs.ErrNotExist) {
		l.add(d+"/", 0, 0, "missing folder: at least one file is required")
		return nil
	}
	if err != nil {
		l.add(d+"/", 0, 0, "cannot read: %v", err)
		return nil
	}
	var out []dirFile
	for _, e := range entries {
		n := e.Name()
		if ignored(n) {
			continue
		}
		file := path.Join(d, n)
		ext := path.Ext(n)
		stem := strings.TrimSuffix(n, ext)
		switch {
		case e.IsDir() || (ext != ".yaml" && ext != ".yml"):
			l.add(file, 0, 0, "unexpected entry: only .yaml files are read here")
		case !fileNamePattern.MatchString(stem):
			l.add(file, 0, 0, "invalid name %q: use lower-case letters, digits, '.', '_' and '-'", stem)
		default:
			out = append(out, dirFile{file, stem})
		}
	}
	if len(out) == 0 {
		l.add(d+"/", 0, 0, "no .yaml file: at least one is required")
	}
	return out
}

// read decodes one file strictly into v and indexes its key positions. It
// reports whether the file was read without problems.
func (l *loader) read(file string, v any, required bool) bool {
	data, err := fs.ReadFile(l.fsys, file)
	if errors.Is(err, fs.ErrNotExist) {
		if required {
			l.add(file, 0, 0, "missing file (required)")
		}
		return false
	}
	if err != nil {
		l.add(file, 0, 0, "cannot read: %v", err)
		return false
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		l.yamlError(file, err)
		return false
	}
	if len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		l.add(file, 1, 1, "expected a mapping starting with version: %d", Version)
		return false
	}
	before := len(l.probs)
	for _, p := range checkKeys(root.Content[0], reflect.TypeOf(v).Elem(), "") {
		p.File = file
		l.probs = append(l.probs, p)
	}
	if len(l.probs) > before {
		return false
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(v); err != nil {
		l.yamlError(file, err)
		return false
	}
	ps := positions{}
	indexPositions(&root, "", ps)
	l.pos[file] = ps
	return true
}

var yamlLine = regexp.MustCompile(`^(?:yaml: )?line (\d+): (.*)$`)

// yamlError turns YAML syntax and type errors into positioned problems.
func (l *loader) yamlError(file string, err error) {
	var te *yaml.TypeError
	msgs := []string{err.Error()}
	if errors.As(err, &te) {
		msgs = te.Errors
	}
	for _, m := range msgs {
		line := 0
		if sm := yamlLine.FindStringSubmatch(m); sm != nil {
			_, _ = fmt.Sscan(sm[1], &line)
			m = sm[2]
		}
		l.add(file, line, 0, "%s", strings.TrimPrefix(m, "yaml: "))
	}
}

func (l *loader) version(file string, v int) {
	if v != Version {
		line, col := l.pos[file].at("version")
		if line == 0 {
			line, col = 1, 1
		}
		l.add(file, line, col, "version: must be %d (the structure version this Huginn reads), got %d", Version, v)
	}
}

// mapOrder returns the keys of the mapping at key in file order.
func (l *loader) mapOrder(file, key string) []string {
	var out []string
	prefix := key + "."
	type kp struct {
		k    string
		line int
	}
	var ks []kp
	for p, pos := range l.pos[file] {
		if rest, ok := strings.CutPrefix(p, prefix); ok && !strings.ContainsAny(rest, ".[") {
			ks = append(ks, kp{rest, pos[0]})
		}
	}
	slices.SortFunc(ks, func(a, b kp) int { return a.line - b.line })
	for _, k := range ks {
		out = append(out, k.k)
	}
	return out
}

// ExpandHome replaces a leading "~" with the home folder.
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
