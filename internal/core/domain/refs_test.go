package domain

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestExpandVars(t *testing.T) {
	vars := map[string]string{"env": "rec", "repo": "orders", "account": "ORDERS"}
	lookup := func(k string) (string, bool) { v, ok := vars[k]; return v, ok }
	cases := []struct{ in, want, missing string }{
		{in: "plain", want: "plain"},
		{in: "{repo}/deploy/{env}/app.env", want: "orders/deploy/rec/app.env"},
		{in: "${{account}_PASSWORD}", want: "${ORDERS_PASSWORD}"},
		{in: "${KEY}", want: "${KEY}"},     // a key, not a placeholder
		{in: "${lower}", want: "${lower}"}, // "{" after "$" opens a key
		{in: "{UPPER} {} { x } {", want: "{UPPER} {} { x } {"},
		{in: "a{env}b{env}", want: "arecbrec"},
		{in: "{nope}/x", missing: "nope"},
	}
	for _, c := range cases {
		got, err := ExpandVars(c.in, lookup)
		var mv *MissingVarError
		switch {
		case c.missing != "":
			if !errors.As(err, &mv) || mv.Name != c.missing {
				t.Errorf("%q: want missing %q, got %q %v", c.in, c.missing, got, err)
			}
		case err != nil || got != c.want:
			t.Errorf("%q: got %q %v, want %q", c.in, got, err, c.want)
		}
	}
	if got := VarNames("{repo_dir}/x/{env}/{env}/${{account}_X}${KEY}"); !slices.Equal(got, []string{"repo_dir", "env", "account"}) {
		t.Errorf("VarNames = %v", got)
	}
}

func TestResolveKeys(t *testing.T) {
	src := map[string]string{"HOST": "broker:9093", "EMPTY": "", "spring.kafka.servers": "a:1"}
	lookup := func(k string) (string, bool) { v, ok := src[k]; return v, ok }
	cases := []struct{ in, want, missing string }{
		{in: "${HOST}", want: "broker:9093"},
		{in: "tcp://${HOST}/x", want: "tcp://broker:9093/x"},
		{in: "${MISSING:-plaintext}", want: "plaintext"},
		{in: "${EMPTY:-dflt}", want: "dflt"},
		{in: "${EMPTY}", want: ""},
		{in: "${spring.kafka.servers}", want: "a:1"},
		{in: "price $5 and $$HOME", want: "price $5 and $HOME"},
		{in: "${MISSING}", missing: "MISSING"},
	}
	for _, c := range cases {
		got, err := ResolveKeys(c.in, lookup)
		var mk *MissingKeyError
		switch {
		case c.missing != "":
			if !errors.As(err, &mk) || mk.Key != c.missing {
				t.Errorf("%q: want missing %q, got %q %v", c.in, c.missing, got, err)
			}
		case err != nil || got != c.want:
			t.Errorf("%q: got %q %v, want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"${", "${}", "${A B}", "x ${UNCLOSED", "${:-x}"} {
		if err := CheckRefs(bad); !errors.Is(err, ErrRefSyntax) {
			t.Errorf("CheckRefs(%q) = %v", bad, err)
		}
	}
	if got := KeyNames("${A}-${B:-x}-${A}"); !slices.Equal(got, []string{"A", "B"}) {
		t.Errorf("KeyNames = %v", got)
	}
}

func TestParseDotenv(t *testing.T) {
	in := "# comment\n\nexport HOST=broker:9093\nPASS=p#ss word\nQ1='single # kept'\nQ2=\"line\\nnext \\\"q\\\"\"\nnoequals\n=novalue\nDUP=1\nDUP=2\n  SPACED  =  v  \n"
	got := ParseDotenv([]byte(in))
	want := map[string]string{"HOST": "broker:9093", "PASS": "p#ss word", "Q1": "single # kept", "Q2": "line\nnext \"q\"", "DUP": "2", "SPACED": "v"}
	if len(got) != len(want) {
		t.Fatalf("got %d keys: %v", len(got), got)
	}
	for k, v := range want {
		if got[k].Reveal() != v {
			t.Errorf("%s = %q, want %q", k, got[k].Reveal(), v)
		}
	}
}

func FuzzParseDotenv(f *testing.F) {
	f.Add([]byte("A=1\nexport B='x'\nC=\"y\\n\"\n#c\n"))
	f.Fuzz(func(t *testing.T, b []byte) {
		for k := range ParseDotenv(b) {
			if k == "" {
				t.Fatal("empty key")
			}
		}
	})
}

func FuzzRefs(f *testing.F) {
	f.Add("${{account}_X:-d}/{env}$${Y}")
	f.Fuzz(func(t *testing.T, s string) {
		out, err := ExpandVars(s, func(string) (string, bool) { return "V", true })
		if err != nil {
			t.Fatal(err)
		}
		_, _ = ResolveKeys(out, func(string) (string, bool) { return "", false })
		_ = CheckRefs(out)
		_ = KeyNames(out)
	})
}

func TestRefErrorsDoNotQuoteTheValue(t *testing.T) {
	for _, s := range []string{"hunter2${", "hunter2${}", "hunter2${:-x}"} {
		if err := CheckRefs(s); err == nil || strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%q: %v", s, err)
		}
	}
}
