package config

import (
	"encoding/json"
	"reflect"
	"strings"
)

// Schema returns a JSON Schema (draft 2020-12) of the configuration, built
// from the Go types and their `doc` and `enum` tags so it never drifts.
func Schema() ([]byte, error) {
	s := schemaFor(typeOfConfig, "")
	s["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	s["title"] = "Huginn configuration"
	return json.MarshalIndent(s, "", "  ")
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
			props[name] = p
		}
		s["type"], s["properties"], s["additionalProperties"] = "object", props, false
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
