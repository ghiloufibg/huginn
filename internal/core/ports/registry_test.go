package ports

import (
	"strings"
	"testing"
)

func TestRegistry(t *testing.T) {
	r := NewRegistry[func() string]("widget")
	r.Register("b", func() string { return "B" })
	r.Register("a", func() string { return "A" })
	f, err := r.Lookup("a")
	if err != nil || f() != "A" {
		t.Fatalf("lookup a: %v", err)
	}
	_, err = r.Lookup("zzz")
	if err == nil || !strings.Contains(err.Error(), `unknown widget "zzz" (available: a, b)`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	r := NewRegistry[int]("n")
	r.Register("x", 1)
	r.Register("x", 2)
}

func TestSelectorMatches(t *testing.T) {
	s := Selector{"app": "api"}
	if !s.Matches(map[string]string{"app": "api", "x": "y"}) || s.Matches(map[string]string{"app": "web"}) {
		t.Fatal("selector mismatch")
	}
	if !(Selector{}).Matches(nil) {
		t.Fatal("empty selector must match")
	}
}
