package config

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Validate checks semantic rules and returns an *Error listing every
// problem, or nil. name is the file name used in messages.
func Validate(c *Config, name string) error {
	v := &validator{}
	v.enums(reflect.ValueOf(c).Elem(), "")
	v.environments(c)
	v.repos(c)
	v.logs(c)
	if len(v.probs) == 0 {
		return nil
	}
	return &Error{File: name, Problems: v.probs}
}

type validator struct{ probs []Problem }

func (v *validator) add(path, format string, args ...any) {
	v.probs = append(v.probs, Problem{Path: path, Msg: fmt.Sprintf(format, args...)})
}

// enums checks every field carrying an `enum` tag (strings and string
// slices), so allowed values are declared once, next to the field.
func (v *validator) enums(rv reflect.Value, path string) {
	t := rv.Type()
	for i := range t.NumField() {
		f, fv := t.Field(i), rv.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		p := join(path, name)
		if allowed := f.Tag.Get("enum"); allowed != "" {
			v.enumValue(fv, p, strings.Split(allowed, ","))
		}
		switch fv.Kind() {
		case reflect.Struct:
			v.enums(fv, p)
		case reflect.Map:
			if fv.Type().Elem().Kind() == reflect.Struct {
				ks := fv.MapKeys()
				sort.Slice(ks, func(a, b int) bool { return ks[a].String() < ks[b].String() })
				for _, k := range ks {
					v.enums(fv.MapIndex(k), join(p, k.String()))
				}
			}
		case reflect.Slice:
			if fv.Type().Elem().Kind() == reflect.Struct {
				for j := range fv.Len() {
					v.enums(fv.Index(j), fmt.Sprintf("%s[%d]", p, j))
				}
			}
		}
	}
}

func (v *validator) enumValue(fv reflect.Value, path string, allowed []string) {
	check := func(s string) {
		if s != "" && !slices.Contains(allowed, s) {
			v.add(path, "%q is not one of: %s", s, strings.Join(allowed, ", "))
		}
	}
	switch fv.Kind() {
	case reflect.String:
		check(fv.String())
	case reflect.Slice:
		for j := range fv.Len() {
			check(fv.Index(j).String())
		}
	}
}

func (v *validator) environments(c *Config) {
	if _, ok := c.Environments[c.DefaultEnv]; !ok {
		v.add("default_env", "%q is not a configured environment (have: %s)", c.DefaultEnv, strings.Join(EnvNames(c), ", "))
	}
	for _, name := range EnvNames(c) {
		e := c.Environments[name]
		p := "environments." + name
		if _, err := domain.ParseEnv(name); err != nil {
			v.add(p, "%v", err)
		}
		if len(e.Namespaces) == 0 && e.NamespaceFrom == "" {
			v.add(p, "set namespaces or namespace_from")
		}
		if e.NamespaceFrom != "" && !strings.HasPrefix(e.NamespaceFrom, "sops:") {
			v.add(p+".namespace_from", "must look like sops:<file>#<key>")
		}
	}
}

func (v *validator) repos(c *Config) {
	for i, r := range c.Repos {
		p := fmt.Sprintf("repos[%d]", i)
		if r.Name == "" {
			v.add(p+".name", "must not be empty")
		}
		for j, w := range r.Workloads {
			wp := fmt.Sprintf("%s.workloads[%d]", p, j)
			if _, ok := c.Environments[w.Env]; !ok {
				v.add(wp+".env", "%q is not a configured environment", w.Env)
			}
			if w.Name == "" {
				v.add(wp+".name", "must not be empty")
			}
		}
	}
}

func (v *validator) logs(c *Config) {
	if _, err := domain.ParseTimeWindow(c.Logs.DefaultWindow, c.Logs.TailLines); err != nil {
		v.add("logs.default_window", "%v", err)
	}
	if c.Logs.TailLines < 1 {
		v.add("logs.tail_lines", "must be at least 1")
	}
	if c.Logs.BufferLines < 1000 {
		v.add("logs.buffer_lines", "must be at least 1000")
	}
	if _, ok := c.LogFormats[c.Logs.Format]; !ok {
		v.add("logs.format", "%q is not a defined log format (have: %s)", c.Logs.Format, strings.Join(sortedKeys(c.LogFormats), ", "))
	}
	for _, name := range sortedKeys(c.LogFormats) {
		f := c.LogFormats[name]
		if f.Decoder == "" {
			v.add("log_formats."+name+".decoder", "must be set")
		}
		for alias, lvl := range f.LevelAliases {
			if _, ok := domain.ParseLevel(lvl); !ok {
				v.add("log_formats."+name+".level_aliases."+alias, "%q is not a level (debug, info, warn, error)", lvl)
			}
		}
	}
	if c.Demo.Rate < 0 {
		v.add("demo.rate", "must not be negative")
	}
}

// EnvNames returns the configured environment names, well-known ones first
// in promotion order, then others alphabetically.
func EnvNames(c *Config) []string {
	order := map[string]int{}
	for i, e := range domain.DefaultEnvs() {
		order[e.String()] = i + 1
	}
	names := sortedKeys(c.Environments)
	sort.SliceStable(names, func(a, b int) bool {
		oa, ob := order[names[a]], order[names[b]]
		if oa == 0 {
			oa = 1 << 30
		}
		if ob == 0 {
			ob = 1 << 30
		}
		return oa < ob
	})
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
