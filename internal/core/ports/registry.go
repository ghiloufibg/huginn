package ports

import (
	"fmt"
	"slices"
	"strings"
)

// Registry maps adapter names used in configuration (for example
// "kubernetes" or "demo") to factories. F is the factory type of one port.
// Registries are built explicitly by the composition root; there is no
// global registry.
type Registry[F any] struct {
	kind      string
	factories map[string]F
}

// NewRegistry returns an empty registry. kind names the port in error
// messages ("cluster client", "log decoder"…).
func NewRegistry[F any](kind string) *Registry[F] {
	return &Registry[F]{kind: kind, factories: map[string]F{}}
}

// Register adds a factory. Registering the same name twice panics, since it
// is a programming error in the composition root.
func (r *Registry[F]) Register(name string, f F) {
	if _, dup := r.factories[name]; dup {
		panic(fmt.Sprintf("%s %q registered twice", r.kind, name))
	}
	r.factories[name] = f
}

// Lookup returns the factory registered under name.
func (r *Registry[F]) Lookup(name string) (F, error) {
	f, ok := r.factories[name]
	if !ok {
		var zero F
		return zero, fmt.Errorf("unknown %s %q (available: %s)", r.kind, name, strings.Join(r.Names(), ", "))
	}
	return f, nil
}

// Names returns the registered names, sorted.
func (r *Registry[F]) Names() []string {
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
