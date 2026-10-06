package config

import (
	"fmt"
	"net/url"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// Placeholders every Kafka profile string may use besides its variables.
var kafkaReserved = []string{"env", "repo", "repo_dir"}

var (
	kafkaSecurity   = domain.KafkaSecurities
	kafkaMechanisms = domain.KafkaMechanisms
)

// literal reports whether s is a value known at load time: no ${KEY}
// reference, no env: value and no placeholder.
func literal(s string) bool {
	return !strings.Contains(s, "${") && !strings.HasPrefix(s, "env:") && len(domain.VarNames(s)) == 0
}

func (v *validator) kafkaLimits() {
	if _, ok := v.c.pos[FileHuginn]; !ok {
		return
	}
	k, f := v.c.Huginn.Kafka, FileHuginn
	if k.TailRecords < 1 || k.TailRecords > 10000 {
		v.add(f, "kafka.tail_records", "must be between 1 and 10000")
	}
	if k.MaxRecords < 1 {
		v.add(f, "kafka.max_records", "must be at least 1")
	}
	sizes := map[string]string{
		"max_buffer_bytes": k.MaxBufferBytes, "max_value_bytes": k.MaxValueBytes,
		"fetch_max_bytes": k.FetchMaxBytes, "partition_fetch_max_bytes": k.PartitionFetchMaxBytes,
	}
	for _, key := range sortedKeys(sizes) {
		if n, err := ParseByteSize(sizes[key]); err != nil {
			v.add(f, "kafka."+key, "%v", err)
		} else if n < 1 {
			v.add(f, "kafka."+key, "must be at least 1 byte")
		}
	}
	buf, _ := ParseByteSize(k.MaxBufferBytes)
	if val, err := ParseByteSize(k.MaxValueBytes); err == nil && buf > 0 && val > buf {
		v.add(f, "kafka.max_value_bytes", "must not exceed kafka.max_buffer_bytes (%s)", k.MaxBufferBytes)
	}
	for _, d := range []struct{ key, val string }{{"connect_timeout", k.ConnectTimeout}, {"request_timeout", k.RequestTimeout}} {
		if dur, err := time.ParseDuration(d.val); err != nil || dur <= 0 {
			v.add(f, "kafka."+d.key, "%q is not a positive duration such as 10s or 1m", d.val)
		}
	}
	if strings.TrimSpace(k.ClientID) == "" {
		v.add(f, "kafka.client_id", "must not be blank")
	}
}

func (v *validator) kafka(p KafkaProfile) {
	file := p.File
	known := v.kafkaVars(p)
	for i, g := range p.Match.Repos {
		v.glob(file, fmt.Sprintf("match.repos[%d]", i), g)
	}
	v.kafkaStrings(file, reflect.ValueOf(p), "", known)
	v.kafkaRepoDir(p)
	v.kafkaConnection(file, "connection", p.Connection, true)
	v.kafkaRegistry(file, "schema_registry", p.SchemaRegistry, p.SchemaRegistry, p.SchemaRegistry, false)
	v.kafkaTopics(file, "topics", p.Topics)
	anyTopics := p.Topics.Any()
	for _, name := range sortedKeys(p.Repos) {
		r, rp := p.Repos[name], "repos."+name
		if strings.TrimSpace(name) == "" {
			v.add(file, rp, "repository name must not be blank")
		}
		v.kafkaConnection(file, rp+".connection", r.Connection, false)
		v.kafkaRegistry(file, rp+".schema_registry", r.SchemaRegistry, p.SchemaRegistry.over(r.SchemaRegistry), p.SchemaRegistry, true)
		v.kafkaTopics(file, rp+".topics", r.Topics)
		anyTopics = anyTopics || r.Topics.Any()
	}
	if !anyTopics {
		v.add(file, "topics", "list topics (consume, produce, list) or discover them, here or under repos")
	}
}

// kafkaVars checks variable names and values and returns every placeholder
// name the profile may use: the reserved ones and every variable defined
// anywhere in it (a profile string may use a variable each repository
// defines).
func (v *validator) kafkaVars(p KafkaProfile) []string {
	known := slices.Clone(kafkaReserved)
	check := func(at string, vars map[string]string) {
		for _, name := range sortedKeys(vars) {
			vp := join(at, name)
			switch {
			case slices.Contains(kafkaReserved, name):
				v.add(p.File, vp, "%q is reserved (%s are set by Huginn)", name, strings.Join(kafkaReserved, ", "))
			case !domain.IsVarName(name):
				v.add(p.File, vp, "%q is not a variable name: use lower-case letters, digits and '_', starting with a letter", name)
			}
			if len(domain.VarNames(vars[name])) > 0 {
				v.add(p.File, vp, "placeholders are not expanded inside variables")
			}
			if !slices.Contains(known, name) {
				known = append(known, name)
			}
		}
	}
	topicVars := func(at string, ts KafkaTopics) {
		for _, l := range topicLists(ts) {
			for i, t := range l.topics {
				check(fmt.Sprintf("%s.%s[%d].vars", at, l.name, i), t.Vars)
			}
		}
	}
	check("vars", p.Vars)
	topicVars("topics", p.Topics)
	for _, name := range sortedKeys(p.Repos) {
		check("repos."+name+".vars", p.Repos[name].Vars)
		topicVars("repos."+name+".topics", p.Repos[name].Topics)
	}
	return known
}

// kafkaStrings checks every string of the profile: placeholders known and
// ${…} references well formed. Variable values are checked by kafkaVars.
func (v *validator) kafkaStrings(file string, rv reflect.Value, p string, known []string) {
	switch rv.Kind() {
	case reflect.String:
		s := rv.String()
		for _, name := range domain.VarNames(s) {
			if !slices.Contains(known, name) {
				msg := fmt.Sprintf("unknown placeholder {%s} (known: %s)", name, strings.Join(known, ", "))
				if c := closest(name, known); c != "" {
					msg += fmt.Sprintf(" (did you mean {%s}?)", c)
				}
				v.add(file, p, "%s", msg)
			}
		}
		expanded, _ := domain.ExpandVars(s, func(string) (string, bool) { return "X", true })
		if err := domain.CheckRefs(expanded); err != nil {
			v.add(file, p, "%v", err)
		}
	case reflect.Struct:
		t := rv.Type()
		for i := range t.NumField() {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
			if name == "" || name == "-" || name == "vars" {
				continue
			}
			v.kafkaStrings(file, rv.Field(i), join(p, name), known)
		}
	case reflect.Slice:
		for i := range rv.Len() {
			v.kafkaStrings(file, rv.Index(i), fmt.Sprintf("%s[%d]", p, i), known)
		}
	case reflect.Map:
		keys := rv.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })
		for _, k := range keys {
			v.kafkaStrings(file, rv.MapIndex(k), join(p, k.String()), known)
		}
	}
}

// kafkaRepoDir checks that {repo_dir} can be resolved: from repos_root, or
// from the path of each repository using it.
func (v *validator) kafkaRepoDir(p KafkaProfile) {
	if v.c.Huginn.ReposRoot != "" {
		return
	}
	uses := func(x any) bool {
		found := false
		walkStrings(reflect.ValueOf(x), func(s string) { found = found || slices.Contains(domain.VarNames(s), "repo_dir") })
		return found
	}
	shared := KafkaProfile{Match: p.Match, Sources: p.Sources, Connection: p.Connection, Topics: p.Topics, SchemaRegistry: p.SchemaRegistry}
	if uses(shared) {
		v.add(p.File, "", "{repo_dir} needs repos_root in %s", FileHuginn)
		return
	}
	for _, name := range sortedKeys(p.Repos) {
		if r := p.Repos[name]; r.Path == "" && uses(r) {
			v.add(p.File, "repos."+name, "{repo_dir} needs repos_root in %s or a path for this repository", FileHuginn)
		}
	}
}

func walkStrings(rv reflect.Value, fn func(string)) {
	switch rv.Kind() {
	case reflect.String:
		fn(rv.String())
	case reflect.Struct:
		for i := range rv.NumField() {
			walkStrings(rv.Field(i), fn)
		}
	case reflect.Slice:
		for i := range rv.Len() {
			walkStrings(rv.Index(i), fn)
		}
	case reflect.Map:
		for _, k := range rv.MapKeys() {
			walkStrings(rv.MapIndex(k), fn)
		}
	case reflect.Pointer:
		if !rv.IsNil() {
			walkStrings(rv.Elem(), fn)
		}
	}
}

// kafkaConnection checks the values known at load time; references are
// checked when the Kafka screen opens. A profile's connection must name
// the brokers and the protocol; a repository's only overrides.
func (v *validator) kafkaConnection(file, p string, c KafkaConnection, profile bool) {
	if profile {
		if strings.TrimSpace(c.Bootstrap) == "" {
			v.add(file, p, "missing required key %q", "bootstrap")
		}
		if strings.TrimSpace(c.Security) == "" {
			v.add(file, p, "missing required key %q", "security")
		}
	}
	sec := domain.NormalizeKafkaSecurity(c.Security)
	if c.Security != "" && literal(c.Security) && !slices.Contains(kafkaSecurity, sec) {
		v.enumProblem(file, p+".security", c.Security, kafkaSecurity)
	}
	if m := c.SASL.Mechanism; m != "" && literal(m) && !slices.Contains(kafkaMechanisms, domain.NormalizeKafkaMechanism(m)) {
		v.enumProblem(file, p+".sasl.mechanism", m, kafkaMechanisms)
	}
	sasl := c.SASL != (KafkaSASL{})
	if literal(c.Security) && c.Security != "" {
		switch {
		case strings.HasPrefix(sec, "sasl_") && profile:
			for _, k := range []struct{ name, val string }{{"mechanism", c.SASL.Mechanism}, {"username", c.SASL.Username}, {"password", c.SASL.Password}} {
				if k.val == "" {
					v.add(file, p+".sasl", "%s needs sasl.%s", c.Security, k.name)
				}
			}
		case !strings.HasPrefix(sec, "sasl_") && sasl:
			v.add(file, p+".sasl", "sasl is only used with sasl_plaintext or sasl_ssl, not %s", c.Security)
		}
	}
	if c.TLS.CAPassword != "" && c.TLS.CA == "" && profile {
		v.add(file, p+".tls.ca_password", "set tls.ca: the password is for a PKCS12 truststore")
	}
}

// kafkaRegistry checks a schema_registry section r at p. eff is the
// section in effect there: the profile's own, or the profile's merged with
// a repository's override, base the profile's own section. A repository is
// told only the problems its override adds. Values known at load time are
// checked here; references when the Kafka screen opens.
func (v *validator) kafkaRegistry(file, p string, r, eff, base KafkaSchemaRegistry, repo bool) {
	if r.IsZero() {
		return
	}
	inherited := map[registryProblem]bool{}
	if repo && !base.IsZero() {
		for _, pr := range registryProblems(base, base) {
			inherited[pr] = true
		}
	}
	for _, pr := range registryProblems(r, eff) {
		at := p
		if pr.key != "" {
			at = join(p, pr.key)
		}
		if !inherited[pr] {
			v.add(file, at, "%s", pr.msg)
		}
	}
}

type registryProblem struct{ key, msg string }

// registryProblems lists the problems of section r in effect as eff.
func registryProblems(r, eff KafkaSchemaRegistry) []registryProblem {
	var out []registryProblem
	add := func(key, format string, args ...any) {
		out = append(out, registryProblem{key, fmt.Sprintf(format, args...)})
	}
	if strings.TrimSpace(eff.URL) == "" {
		add("", "missing required key %q", "url")
	} else if r.URL != "" && literal(r.URL) {
		if u, err := url.Parse(r.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			add("url", "%q is not an http or https URL such as https://schema-registry.example:8081", r.URL)
		}
	}
	if (eff.BasicAuth.Username == "") != (eff.BasicAuth.Password == "") {
		add("basic_auth", "set both username and password")
	}
	if eff.BasicAuth != (KafkaBasicAuth{}) && eff.BearerToken != "" {
		add("", "set basic_auth or bearer_token, not both")
	}
	if eff.TLS.CAPassword != "" && eff.TLS.CA == "" {
		add("tls.ca_password", "set tls.ca: the password is for a PKCS12 truststore")
	}
	if r.Timeout != "" && literal(r.Timeout) {
		if d, err := time.ParseDuration(r.Timeout); err != nil || d <= 0 {
			add("timeout", "%q is not a positive duration such as 10s", r.Timeout)
		}
	}
	return out
}

func (v *validator) enumProblem(file, p, s string, allowed []string) {
	msg := fmt.Sprintf("%q is not one of: %s", s, strings.Join(allowed, ", "))
	if c := closest(s, allowed); c != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", c)
	}
	v.add(file, p, "%s", msg)
}

func (v *validator) kafkaTopics(file, p string, ts KafkaTopics) {
	for _, l := range topicLists(ts) {
		for i, t := range l.topics {
			if strings.TrimSpace(t.Name) == "" {
				v.add(file, fmt.Sprintf("%s.%s[%d]", p, l.name, i), "a topic needs a name")
			}
		}
	}
	for i, g := range ts.Discover {
		if _, err := path.Match(g, ""); err != nil || g == "" {
			v.add(file, fmt.Sprintf("%s.discover[%d]", p, i), "invalid glob %q", g)
		}
	}
}

// topicLists returns the topic lists in file order, so problems are
// reported in the same order on every run.
func topicLists(ts KafkaTopics) []struct {
	name   string
	topics []KafkaTopic
} {
	return []struct {
		name   string
		topics []KafkaTopic
	}{{"consume", ts.Consume}, {"produce", ts.Produce}, {"list", ts.List}}
}
