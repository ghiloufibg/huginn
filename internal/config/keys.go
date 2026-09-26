package config

import (
	"fmt"
	"reflect"
	"strings"

	"go.yaml.in/yaml/v3"
)

// checkKeys walks the YAML tree alongside the Go type and reports unknown
// keys with their position and the closest known key.
func checkKeys(n *yaml.Node, t reflect.Type, path string) []Problem {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.Struct && n.Kind == yaml.MappingNode:
		return checkStruct(n, t, path)
	case t.Kind() == reflect.Map && n.Kind == yaml.MappingNode:
		var probs []Problem
		for i := 0; i+1 < len(n.Content); i += 2 {
			probs = append(probs, checkKeys(n.Content[i+1], t.Elem(), join(path, n.Content[i].Value))...)
		}
		return probs
	case t.Kind() == reflect.Slice && n.Kind == yaml.SequenceNode:
		var probs []Problem
		for i, item := range n.Content {
			probs = append(probs, checkKeys(item, t.Elem(), fmt.Sprintf("%s[%d]", path, i))...)
		}
		return probs
	}
	return nil
}

func checkStruct(n *yaml.Node, t reflect.Type, path string) []Problem {
	fields := yamlFields(t)
	var probs []Problem
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		f, ok := fields[k.Value]
		if !ok {
			msg := fmt.Sprintf("unknown key %q", k.Value)
			if s := closest(k.Value, keys(fields)); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			}
			probs = append(probs, Problem{Path: path, Line: k.Line, Col: k.Column, Msg: msg})
			continue
		}
		probs = append(probs, checkKeys(n.Content[i+1], f.Type, join(path, k.Value))...)
	}
	return probs
}

func yamlFields(t reflect.Type) map[string]reflect.StructField {
	out := map[string]reflect.StructField{}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name != "" && name != "-" {
			out[name] = f
		}
	}
	return out
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func join(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// closest returns the candidate with the smallest edit distance to s, if it
// is close enough to be a plausible typo.
func closest(s string, candidates []string) string {
	best, bestD := "", len(s)/2+2
	for _, c := range candidates {
		if d := levenshtein(strings.ToLower(s), c); d < bestD || (d == bestD && c < best) {
			best, bestD = c, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}

// positions indexes the position of every key path of a YAML document, so
// semantic problems can point at a line.
type positions map[string][2]int

func indexPositions(n *yaml.Node, path string, out positions) {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, c := range n.Content {
			indexPositions(c, path, out)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			p := join(path, k.Value)
			out[p] = [2]int{k.Line, k.Column}
			indexPositions(n.Content[i+1], p, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			p := fmt.Sprintf("%s[%d]", path, i)
			out[p] = [2]int{c.Line, c.Column}
			indexPositions(c, p, out)
		}
	}
}

// at returns the position of path, or of its closest indexed parent.
func (ps positions) at(path string) (line, col int) {
	for p := path; p != ""; {
		if pos, ok := ps[p]; ok {
			return pos[0], pos[1]
		}
		i := strings.LastIndexAny(p, ".[")
		if i < 0 {
			break
		}
		p = p[:i]
	}
	return 0, 0
}
