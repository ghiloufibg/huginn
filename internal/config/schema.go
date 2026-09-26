package config

//go:generate go run ./internal/genschema -out ../../docs/schema

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Schemas returns one JSON Schema (draft 2020-12) per file kind of the
// config folder, keyed by schema file name. They are built from the Go
// types and their `doc`, `enum` and `required` tags so they never drift;
// editors use them for completion (docs/CONFIG.md).
func Schemas() (map[string][]byte, error) {
	kinds := []struct {
		name, title string
		t           reflect.Type
	}{
		{"huginn", "huginn.yaml", reflect.TypeFor[Huginn]()},
		{"environments", "environments.yaml", reflect.TypeFor[EnvironmentsFile]()},
		{"services", "services.yaml", reflect.TypeFor[Services]()},
		{"containers", "containers.yaml", reflect.TypeFor[Containers]()},
		{"ui", "ui.yaml", reflect.TypeFor[UI]()},
		{"format", "formats/<name>.yaml", reflect.TypeFor[Format]()},
		{"layout", "layouts/<name>.yaml", reflect.TypeFor[Layout]()},
	}
	out := map[string][]byte{}
	for _, k := range kinds {
		s := schemaFor(k.t, "")
		s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		s["title"] = "Huginn config folder: " + k.title
		b, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return nil, err
		}
		out[k.name+".schema.json"] = append(b, '\n')
	}
	return out, nil
}

func schemaFor(t reflect.Type, doc string) map[string]any {
	s := map[string]any{}
	if doc != "" {
		s["description"] = doc
	}
	if t == reflect.TypeFor[Paths]() {
		s["anyOf"] = []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}
		return s
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			p := schemaFor(f.Type, f.Tag.Get("doc"))
			if enum := f.Tag.Get("enum"); enum != "" {
				values := strings.Split(enum, ",")
				if f.Type.Kind() == reflect.Slice {
					p["items"] = map[string]any{"type": "string", "enum": values}
				} else {
					p["enum"] = values
				}
			}
			if ks := f.Tag.Get("keys"); ks != "" {
				p["propertyNames"] = map[string]any{"enum": strings.Split(ks, ",")}
			}
			if f.Tag.Get("required") == "true" {
				required = append(required, name)
			}
			props[name] = p
		}
		s["type"], s["properties"], s["additionalProperties"] = "object", props, false
		if len(required) > 0 {
			s["required"] = required
		}
	case reflect.Map:
		s["type"], s["additionalProperties"] = "object", schemaFor(t.Elem(), "")
	case reflect.Slice:
		s["type"], s["items"] = "array", schemaFor(t.Elem(), "")
	case reflect.String:
		s["type"] = "string"
	case reflect.Bool:
		s["type"] = "boolean"
	case reflect.Int, reflect.Int64:
		s["type"] = "integer"
	case reflect.Float64:
		s["type"] = "number"
	}
	return s
}
