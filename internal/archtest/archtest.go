// Package archtest enforces the layer rules of docs/ARCHITECTURE.md by
// inspecting the import graph of every package under internal/ and cmd/.
// It has no production code: the rules live in Rules and the check runs
// as a test, so a forbidden import fails the build in CI.
package archtest

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Module is the module path.
const Module = "github.com/ghiloufibg/huginn"

// Rule constrains the imports of the packages whose path (relative to the
// module) starts with Scope.
type Rule struct {
	Scope string
	// Allow lists module-relative prefixes this scope may import; the
	// standard library is always allowed.
	Allow []string
	// ThirdParty allows imports outside the standard library and module.
	ThirdParty bool
	// DenySiblings forbids importing other packages under the same
	// directory as Scope (adapters must not import each other).
	DenySiblings bool
	Why          string
}

// Rules is the executable form of docs/ARCHITECTURE.md. The most specific
// scope wins.
var Rules = []Rule{
	{Scope: "internal/core/domain", Why: "domain depends on the standard library only"},
	{Scope: "internal/core/ports", Allow: []string{"internal/core/domain", "internal/core/ports"}, Why: "ports speak domain types only"},
	{Scope: "internal/core/app", Allow: []string{"internal/core/domain", "internal/core/ports", "internal/core/app"}, Why: "use cases orchestrate ports, no I/O libraries"},
	{Scope: "internal/adapters/driven/", Allow: []string{"internal/core/domain", "internal/core/ports"}, ThirdParty: true, DenySiblings: true, Why: "driven adapters depend on the core only, never on each other"},
	{Scope: "internal/adapters/driving/", Allow: []string{"internal/core/domain", "internal/core/ports"}, ThirdParty: true, DenySiblings: true, Why: "driving adapters use driving ports, never other adapters"},
	{Scope: "internal/config", Allow: []string{"internal/core/domain", "internal/config"}, ThirdParty: true, Why: "configuration is data"},
	{Scope: "internal/diag", Why: "diagnostics use the standard library only"},
	{Scope: "internal/buildinfo", Why: "build info uses the standard library only"},
	{Scope: "internal/archtest", Why: "the architecture test uses the standard library only"},
	{Scope: "internal/bootstrap", Allow: []string{"internal/"}, ThirdParty: true, Why: "the composition root wires everything"},
	{Scope: "cmd/", Allow: []string{"internal/bootstrap"}, Why: "main only starts the composition root"},
}

// Violation is a forbidden import.
type Violation struct {
	Package, Import, Why string
}

func (v Violation) String() string {
	return fmt.Sprintf("%s imports %s: %s", v.Package, v.Import, v.Why)
}

// Check returns the violations in imports, a map from module-relative
// package path to the import paths of its non-test files.
func Check(imports map[string][]string) []Violation {
	var out []Violation
	pkgs := make([]string, 0, len(imports))
	for p := range imports {
		pkgs = append(pkgs, p)
	}
	slices.Sort(pkgs)
	for _, pkg := range pkgs {
		rule, ok := ruleFor(pkg)
		if !ok {
			out = append(out, Violation{Package: pkg, Why: "no architecture rule covers this package; add one to archtest.Rules"})
			continue
		}
		for _, imp := range imports[pkg] {
			if !allowed(rule, pkg, imp) {
				out = append(out, Violation{Package: pkg, Import: imp, Why: rule.Why})
			}
		}
	}
	return out
}

func ruleFor(pkg string) (Rule, bool) {
	best, found := Rule{}, false
	for _, r := range Rules {
		if hasPathPrefix(pkg, r.Scope) && len(r.Scope) > len(best.Scope) {
			best, found = r, true
		}
	}
	return best, found
}

func hasPathPrefix(p, prefix string) bool {
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(p, prefix)
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}

func allowed(r Rule, pkg, imp string) bool {
	rel, internal := strings.CutPrefix(imp, Module+"/")
	if !internal {
		return isStdlib(imp) || r.ThirdParty
	}
	if r.DenySiblings && strings.HasPrefix(rel, r.Scope) {
		// An adapter may use its own sub-packages, never another adapter.
		return owner(rel, r.Scope) == owner(pkg, r.Scope)
	}
	for _, a := range r.Allow {
		if hasPathPrefix(rel, a) {
			return true
		}
	}
	// A single-package scope may import its own sub-packages.
	return !strings.HasSuffix(r.Scope, "/") && hasPathPrefix(rel, r.Scope)
}

// owner returns the first path element under scope: the adapter name.
func owner(p, scope string) string {
	rest := strings.TrimPrefix(p, scope)
	name, _, _ := strings.Cut(rest, "/")
	return name
}

func isStdlib(imp string) bool {
	first, _, _ := strings.Cut(imp, "/")
	return !strings.Contains(first, ".")
}

// Imports parses every non-test Go file under root's internal/ and cmd/
// directories and returns imports by module-relative package path.
func Imports(root string) (map[string][]string, error) {
	out := map[string][]string{}
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, filepath.Dir(path))
			if err != nil {
				return err
			}
			pkg := filepath.ToSlash(rel)
			if _, ok := out[pkg]; !ok {
				out[pkg] = nil
			}
			for _, s := range f.Imports {
				imp := strings.Trim(s.Path.Value, `"`)
				if !slices.Contains(out[pkg], imp) {
					out[pkg] = append(out[pkg], imp)
				}
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}
	return out, nil
}
