package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryFollowsArchitecture(t *testing.T) {
	imports, err := Imports("../..")
	if err != nil {
		t.Fatal(err)
	}
	if len(imports) < 10 {
		t.Fatalf("only %d packages found; wrong root?", len(imports))
	}
	for _, v := range Check(imports) {
		t.Error(v)
	}
}

func TestCheckCatchesViolations(t *testing.T) {
	m := Module + "/"
	imports := map[string][]string{
		"internal/core/domain":              {"time", "k8s.io/api/core/v1"},
		"internal/core/ports":               {m + "internal/core/domain", m + "internal/adapters/driven/demo"},
		"internal/core/app":                 {m + "internal/config"},
		"internal/adapters/driven/demo":     {m + "internal/core/ports", m + "internal/adapters/driven/clock", m + "internal/adapters/driven/demo/sub"},
		"internal/adapters/driving/tui":     {"charm.land/bubbletea/v2", m + "internal/adapters/driven/demo", m + "internal/bootstrap"},
		"internal/adapters/driving/cli":     {m + "internal/core/ports"},
		"internal/config":                   {m + "internal/adapters/driving/tui"},
		"internal/bootstrap":                {m + "internal/adapters/driven/demo", "github.com/spf13/cobra"},
		"internal/somethingnew":             nil,
		"internal/adapters/driven/demo/sub": {m + "internal/adapters/driven/demo"},
	}
	var got []string
	for _, v := range Check(imports) {
		got = append(got, v.Package+" -> "+v.Import)
	}
	want := []string{
		"internal/adapters/driven/demo -> " + m + "internal/adapters/driven/clock",
		"internal/adapters/driving/tui -> " + m + "internal/adapters/driven/demo",
		"internal/adapters/driving/tui -> " + m + "internal/bootstrap",
		"internal/config -> " + m + "internal/adapters/driving/tui",
		"internal/core/app -> " + m + "internal/config",
		"internal/core/domain -> k8s.io/api/core/v1",
		"internal/core/ports -> " + m + "internal/adapters/driven/demo",
		"internal/somethingnew -> ",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n%s\n\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestNoApplicationDataInGenericCode(t *testing.T) {
	found, err := AppData("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("application knowledge belongs in the config folder (docs/CONFIG.md), not in code: %s", f)
	}
}

func TestAppDataCatchesLiteralsNotTagsOrComments(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "internal", "core", "x")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "package x\n\n// istio is fine in a comment.\ntype T struct {\n\tA string `doc:\"e.g. app-rec\"`\n}\n\nvar ns = \"app-rec\"\n"
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	found, err := AppData(root)
	if err != nil || len(found) != 1 || !strings.HasPrefix(found[0], "internal/core/x/x.go:8:") {
		t.Fatalf("found %v, %v", found, err)
	}
}
