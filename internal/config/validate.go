package config

import (
	"fmt"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// validate checks the rules of docs/CONFIG.md on a loaded folder and
// returns every problem, located at the offending key.
func validate(c *Config) []Problem {
	v := &validator{c: c}
	// Files that could not be read are already reported; only the files
	// read are checked.
	for file, val := range map[string]any{FileHuginn: c.Huginn, FileServices: c.Services, FileContainers: c.Containers, FileUI: c.UI} {
		if _, ok := c.pos[file]; ok {
			v.tags(file, reflect.ValueOf(val), "")
		}
	}
	for _, f := range c.Formats {
		v.tags(f.File, reflect.ValueOf(f), "")
	}
	for _, name := range sortedKeys(c.Layouts) {
		v.tags(c.Layouts[name].File, reflect.ValueOf(c.Layouts[name]), "")
	}
	v.huginn()
	v.environments()
	v.services()
	v.ui()
	for _, f := range c.Formats {
		v.format(f)
	}
	for _, name := range sortedKeys(c.Layouts) {
		v.layout(c.Layouts[name])
	}
	return v.probs
}

type validator struct {
	c     *Config
	probs []Problem
}

func (v *validator) add(file, path, format string, args ...any) {
	v.probs = append(v.probs, v.c.Problem(file, path, format, args...))
}

func (v *validator) has(file, path string) bool {
	_, ok := v.c.pos[file][path]
	return ok
}

// tags checks the `required`, `enum` and `keys` struct tags, so these rules
// are declared once, next to the field they describe.
func (v *validator) tags(file string, rv reflect.Value, p string) {
	t := rv.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), rv.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		fp := join(p, name)
		if f.Tag.Get("required") == "true" && !v.has(file, fp) {
			v.add(file, p, "missing required key %q", name)
			continue
		}
		if allowed := f.Tag.Get("enum"); allowed != "" {
			v.enum(file, fp, fv, strings.Split(allowed, ","))
		}
		if allowed := f.Tag.Get("keys"); allowed != "" && fv.Kind() == reflect.Map {
			for _, k := range fv.MapKeys() {
				if !slices.Contains(strings.Split(allowed, ","), k.String()) {
					v.add(file, join(fp, k.String()), "%q is not one of: %s", k.String(), strings.ReplaceAll(allowed, ",", ", "))
				}
			}
		}
		switch fv.Kind() {
		case reflect.Struct:
			if v.has(file, fp) {
				v.tags(file, fv, fp)
			}
		case reflect.Map:
			if fv.Type().Elem().Kind() == reflect.Struct {
				for _, k := range fv.MapKeys() {
					v.tags(file, fv.MapIndex(k), join(fp, k.String()))
				}
			}
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Struct {
				for j := range fv.Len() {
					v.tags(file, fv.Index(j), fmt.Sprintf("%s[%d]", fp, j))
				}
			}
		}
	}
}

func (v *validator) enum(file, p string, fv reflect.Value, allowed []string) {
	check := func(s, at string) {
		if s != "" && !slices.Contains(allowed, s) {
			msg := fmt.Sprintf("%q is not one of: %s", s, strings.Join(allowed, ", "))
			if c := closest(s, allowed); c != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", c)
			}
			v.add(file, at, "%s", msg)
		}
	}
	switch fv.Kind() {
	case reflect.String:
		check(fv.String(), p)
	case reflect.Slice:
		for j := range fv.Len() {
			check(fv.Index(j).String(), fmt.Sprintf("%s[%d]", p, j))
		}
	}
}

func (v *validator) huginn() {
	h, envs := v.c.Huginn, v.c.Environments
	if _, ok := envs.ByName[h.DefaultEnv]; h.DefaultEnv != "" && !ok && len(envs.Names) > 0 {
		v.add(FileHuginn, "default_env", "%q is not in %s (have: %s)", h.DefaultEnv, FileEnvironments, strings.Join(envs.Names, ", "))
	}
	w := h.Windows
	if w.TailLines < 1 {
		v.add(FileHuginn, "windows.tail_lines", "must be at least 1")
	}
	if w.HeadLines < 1 {
		v.add(FileHuginn, "windows.head_lines", "must be at least 1")
	} else if w.HeadLines > h.Logs.BufferLines && h.Logs.BufferLines >= 1000 {
		v.add(FileHuginn, "windows.head_lines", "must not exceed logs.buffer_lines (%d)", h.Logs.BufferLines)
	}
	if len(w.Presets) > 7 {
		v.add(FileHuginn, "windows.presets", "at most 7 presets (keys 1…7), got %d", len(w.Presets))
	}
	for i, s := range w.Presets {
		if tw, err := domain.ParseTimeWindow(s, w.TailLines, w.HeadLines); err != nil || tw.Tail > 0 || tw.Head > 0 {
			v.add(FileHuginn, fmt.Sprintf("windows.presets[%d]", i), "%q is not a duration such as 15m, 1h or 2d", s)
		}
	}
	if tw, err := domain.ParseTimeWindow(w.Default, w.TailLines, w.HeadLines); err != nil {
		v.add(FileHuginn, "windows.default", "%v", err)
	} else if tw.Head > h.Logs.BufferLines && h.Logs.BufferLines >= 1000 {
		v.add(FileHuginn, "windows.default", "head size %d exceeds logs.buffer_lines (%d)", tw.Head, h.Logs.BufferLines)
	}
	if h.Logs.BufferLines < 1000 {
		v.add(FileHuginn, "logs.buffer_lines", "must be at least 1000")
	}
	if h.Demo.Rate < 0 {
		v.add(FileHuginn, "demo.rate", "must not be negative")
	}
}

func (v *validator) environments() {
	envs := v.c.Environments
	if _, ok := v.c.pos[FileEnvironments]; ok && len(envs.Names) == 0 {
		v.add(FileEnvironments, "environments", "define at least one environment")
	}
	for _, name := range envs.Names {
		e, p := envs.ByName[name], "environments."+name
		if _, err := domain.ParseEnv(name); err != nil {
			v.add(FileEnvironments, p, "%v", err)
		}
		switch {
		case len(e.Namespaces) == 0 && e.NamespaceFrom == "":
			v.add(FileEnvironments, p, "set namespaces or namespace_from")
		case len(e.Namespaces) > 0 && e.NamespaceFrom != "":
			v.add(FileEnvironments, p, "set namespaces or namespace_from, not both")
		}
		if nf := e.NamespaceFrom; nf != "" && (!strings.HasPrefix(nf, "sops:") || !strings.Contains(nf, "#")) {
			v.add(FileEnvironments, p+".namespace_from", "must look like sops:<file>#<key>")
		}
	}
}

func (v *validator) services() {
	s := v.c.Services
	uses := func(rule string) bool { return slices.Contains(s.Resolve, rule) }
	if uses("labels") && len(s.LabelKeys) == 0 {
		v.add(FileServices, "resolve", "the labels rule needs label_keys")
	}
	if uses("explicit") && len(s.Explicit) == 0 {
		v.add(FileServices, "resolve", "the explicit rule needs an explicit list")
	}
	if uses("manifests") && s.Manifests.OverlayGlob == "" {
		v.add(FileServices, "resolve", "the manifests rule needs manifests.overlay_glob")
	}
	if uses("manifests") && v.c.Huginn.ReposRoot == "" {
		v.add(FileServices, "resolve", "the manifests rule needs repos_root in %s", FileHuginn)
	}
	for i, r := range s.Explicit {
		for j, w := range r.Workloads {
			if _, ok := v.c.Environments.ByName[w.Env]; w.Env != "" && !ok {
				v.add(FileServices, fmt.Sprintf("explicit[%d].workloads[%d].env", i, j), "%q is not in %s", w.Env, FileEnvironments)
			}
		}
	}
}

func (v *validator) ui() {
	known := map[string]bool{"pod": true}
	for _, l := range v.c.Layouts {
		for _, col := range l.Stream.Columns {
			known[col.Name] = true
		}
	}
	for i, name := range v.c.UI.LogColumns {
		if !known[name] {
			v.add(FileUI, fmt.Sprintf("log_columns[%d]", i), "%q is neither pod nor a stream column of layouts/ (have: %s)", name, strings.Join(sortedKeys(known), ", "))
		}
	}
}

func (v *validator) format(f Format) {
	if _, ok := v.c.Layouts[f.Layout]; f.Layout != "" && !ok && len(v.c.Layouts) > 0 {
		v.add(f.File, "layout", "%q is not a file of layouts/ (have: %s)", f.Layout, strings.Join(sortedKeys(v.c.Layouts), ", "))
	}
	for i, g := range f.Match.Repos {
		v.glob(f.File, fmt.Sprintf("match.repos[%d]", i), g)
	}
	for i, g := range f.Match.Containers {
		v.glob(f.File, fmt.Sprintf("match.containers[%d]", i), g)
	}
	for lvl, spellings := range f.Levels {
		if len(spellings) == 0 {
			v.add(f.File, "levels."+lvl, "list at least one spelling")
		}
	}
	switch f.Decoder {
	case "json":
		if len(f.Fields.Message) == 0 {
			v.add(f.File, "fields", "the json decoder needs fields.message")
		}
		for i, g := range f.Hidden {
			v.glob(f.File, fmt.Sprintf("hidden[%d]", i), g)
		}
		if f.Pattern != "" || f.LevelFrom.Field != "" {
			v.add(f.File, "decoder", "pattern and level_from are for the regex decoder")
		}
		v.transforms(f)
	case "regex":
		v.regex(f)
	case "plain":
		if len(f.Fields.Message) > 0 || f.Pattern != "" {
			v.add(f.File, "decoder", "the plain decoder reads no fields or pattern")
		}
	}
	if f.Decoder != "json" && len(f.Transform) > 0 {
		v.add(f.File, "transform", "transform is for the json decoder")
	}
}

// transforms checks the transform section of a json format: each pattern
// compiles, reads a mapped field and has exactly one named group, named
// after that field.
func (v *validator) transforms(f Format) {
	for _, field := range sortedKeys(f.Transform) {
		at := "transform." + field
		paths, known := f.Fields.byName(field)
		if !known || field == "time" || field == "level" || field == "stack" {
			continue // reported by the keys tag
		}
		if len(paths) == 0 {
			v.add(f.File, at, "fields.%s is not mapped", field)
		}
		pattern := f.Transform[field].Pattern
		if pattern == "" {
			continue // reported by the required tag
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			v.add(f.File, at+".pattern", "invalid regular expression: %v", err)
			continue
		}
		groups := re.SubexpNames()
		if !slices.Contains(groups, field) {
			v.add(f.File, at+".pattern", "missing group (?P<%s>…)", field)
		}
		for _, g := range groups {
			if g != "" && g != field {
				v.add(f.File, at+".pattern", "named group %q: only (?P<%s>…) is allowed; use (?:…) for the other parts", g, field)
			}
		}
	}
}

func (v *validator) regex(f Format) {
	if f.Pattern == "" {
		v.add(f.File, "decoder", "the regex decoder needs a pattern")
		return
	}
	re, err := regexp.Compile(f.Pattern)
	if err != nil {
		v.add(f.File, "pattern", "invalid regular expression: %v", err)
		return
	}
	groups := re.SubexpNames()
	if !slices.Contains(groups, "message") {
		v.add(f.File, "pattern", "missing group (?P<message>…)")
	}
	if lf := f.LevelFrom; lf.Field != "" {
		if !slices.Contains(groups, lf.Field) {
			v.add(f.File, "level_from.field", "%q is not a group of pattern", lf.Field)
		}
		for glob, lvl := range lf.Map {
			v.glob(f.File, "level_from.map."+glob, glob)
			if !slices.Contains([]string{"error", "warn", "info", "debug"}, lvl) {
				v.add(f.File, "level_from.map."+glob, "%q is not one of: error, warn, info, debug", lvl)
			}
		}
	}
	if len(f.Fields.Message) > 0 || len(f.Hidden) > 0 {
		v.add(f.File, "fields", "fields and hidden are for the json decoder; name regex groups instead")
	}
}

func (v *validator) glob(file, p, g string) {
	if _, err := path.Match(g, ""); err != nil {
		v.add(file, p, "invalid glob %q", g)
	}
}

// reservedKeys are letters of the columns picker that are not columns.
var reservedKeys = []string{"p", "z", "r", "f"}

func (v *validator) layout(l Layout) {
	v.line(l, "stream", l.Stream, true)
	if v.has(l.File, "zoom") {
		v.line(l, "zoom", l.Zoom, false)
	}
}

func (v *validator) line(l Layout, p string, line Line, stream bool) {
	if v.has(l.File, p+".columns") && len(line.Columns) == 0 {
		v.add(l.File, p+".columns", "list at least one column")
	}
	names, keys := map[string]string{}, map[string]string{}
	for i, col := range line.Columns {
		cp := fmt.Sprintf("%s.columns[%d]", p, i)
		if prev, dup := names[col.Name]; dup && col.Name != "" {
			v.add(l.File, cp+".name", "%q is already the name of %s", col.Name, prev)
		}
		names[col.Name] = cp
		if !stream && (col.Key != "" || col.HideBelow != 0 || col.Visible != nil) {
			v.add(l.File, cp, "key, hide_below and visible only apply to stream columns")
		}
		if col.Key != "" {
			switch {
			case len([]rune(col.Key)) != 1:
				v.add(l.File, cp+".key", "must be one character")
			case slices.Contains(reservedKeys, col.Key):
				v.add(l.File, cp+".key", "%q is used by the columns picker (p pod, z message only, r reset, f time format)", col.Key)
			case keys[col.Key] != "":
				v.add(l.File, cp+".key", "%q is already the key of column %s", col.Key, keys[col.Key])
			}
			keys[col.Key] = col.Name
		}
		if col.HideBelow < 0 {
			v.add(l.File, cp+".hide_below", "must not be negative")
		}
	}
	for i, name := range line.Separator.After {
		if _, ok := names[name]; !ok {
			v.add(l.File, fmt.Sprintf("%s.separator.after[%d]", p, i), "%q is not a column of %s", name, p)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := keys(m)
	slices.Sort(out)
	return out
}
