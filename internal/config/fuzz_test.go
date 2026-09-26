package config

import (
	"testing"
	"testing/fstest"
)

// FuzzFolder loads folders whose layout file is arbitrary YAML: the loader
// must report problems, never panic.
func FuzzFolder(f *testing.F) {
	for _, s := range []string{"version: 1\nstream:\n  columns:\n    - {name: t, show: \"{time}\"}\n", "version: [1]", "stream: {columns: 3}", "- a\n- b", ":\n:", "version: 1\nstream:\n  columns:\n    - {name: t, key: pp, show: x, hide_below: -1}\n  separator: {after: [z]}\n"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, layout string) {
		fsys := valid()
		fsys["layouts/basic.yaml"] = &fstest.MapFile{Data: []byte(layout)}
		_, _ = LoadFS(fsys, "cfg")
	})
}
