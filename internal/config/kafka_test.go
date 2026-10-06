package config

import (
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// withKafka returns the minimal valid folder plus kafka/ files.
func withKafka(files map[string]string) fstest.MapFS {
	fsys := valid()
	for name, data := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(data)}
	}
	return fsys
}

const fullProfile = `version: 1
match:
  repos: ["*"]
  files: ["{repo_dir}/deploy/overlays/{env}/secrets/kafka.env"]
sources:
  - { file: "{repo_dir}/deploy/base/kafka.env" }
  - { file: "{repo_dir}/deploy/overlays/{env}/kafka.env", optional: true }
  - { file: "{repo_dir}/deploy/overlays/{env}/secrets/kafka.env", sops: true }
vars: { account: DEFAULT }
connection:
  bootstrap: ${BROKERS}
  security: ${PROTOCOL:-plaintext}
  sasl:
    mechanism: scram-sha-512
    username: ${{account}_USERNAME}
    password: ${{account}_PASSWORD}
  tls:
    ca: "{repo_dir}/resources/truststore.p12"
    ca_password: ${{account}_TRUSTSTORE_PASSWORD:-changeme}
topics:
  discover: ["TOPIC_*"]
repos:
  orders:
    vars: { account: ORDERS }
    topics:
      consume: ["${TOPIC_IN}"]
      produce:
        - { name: "${TOPIC_OUT}", vars: { account: OUT } }
  billing:
    path: /src/billing-svc
    enabled: false
    connection:
      tls: { ca: "{repo_dir}/other/ca.pem" }
`

func TestKafkaProfileLoads(t *testing.T) {
	fsys := withKafka(map[string]string{"kafka/10-repo.yaml": fullProfile, "kafka/zz-dev.yaml": "version: 1\nconnection: {bootstrap: 'localhost:9092', security: PLAINTEXT}\ntopics: {list: [orders]}\n"})
	fsys["huginn.yaml"].Data = []byte("version: 1\ndefault_env: rec\nrepos_root: /src\n")
	c, msg := load(t, fsys)
	if msg != "" {
		t.Fatal(msg)
	}
	if len(c.Kafka) != 2 || c.Kafka[0].Name != "10-repo" || c.Kafka[1].File != "kafka/zz-dev.yaml" {
		t.Fatalf("profiles in file order: %+v", c.Kafka)
	}
	p := c.Kafka[0]
	if len(p.Sources) != 3 || !p.Sources[2].Sops || !p.Sources[1].Optional {
		t.Fatalf("sources: %+v", p.Sources)
	}
	orders := p.Repos["orders"]
	if orders.Topics.Consume[0].Name != "${TOPIC_IN}" || orders.Topics.Produce[0].Vars["account"] != "OUT" || !orders.IsEnabled() {
		t.Fatalf("topics as string and object: %+v", orders)
	}
	if p.Repos["billing"].IsEnabled() || p.Repos["billing"].Path != "/src/billing-svc" {
		t.Fatalf("billing: %+v", p.Repos["billing"])
	}
	l := c.Huginn.Kafka.Limits()
	want := KafkaLimits{TailRecords: 100, MaxRecords: 20000, MaxBufferBytes: 64 << 20, MaxValueBytes: 256 << 10, FetchMaxBytes: 1 << 20, PartitionFetchMaxBytes: 256 << 10, ConnectTimeout: 10 * time.Second, RequestTimeout: 30 * time.Second, ClientID: "huginn"}
	if l != want {
		t.Fatalf("limits defaults:\ngot  %+v\nwant %+v", l, want)
	}
}

func TestNoKafkaFolderMeansNoProfiles(t *testing.T) {
	c, msg := load(t, valid())
	if msg != "" || c.Kafka != nil {
		t.Fatalf("%q %+v", msg, c.Kafka)
	}
	fsys := valid()
	fsys["kafka/README.md"] = &fstest.MapFile{Data: []byte("notes")}
	if c, msg = load(t, fsys); msg != "" || len(c.Kafka) != 0 {
		t.Fatalf("an empty kafka/ is allowed: %q", msg)
	}
}

func TestKafkaLimitsValidation(t *testing.T) {
	fsys := valid()
	fsys["huginn.yaml"].Data = []byte(`version: 1
default_env: rec
kafka:
  tail_records: 20000
  max_buffer_bytes: 1KiB
  max_value_bytes: 2KiB
  fetch_max_bytes: lots
  partition_fetch_max_bytes: 1048576
  connect_timeout: 10
  isolation: committed
`)
	c, msg := load(t, fsys)
	wantErrors(t, msg,
		"huginn.yaml:4:3  kafka.tail_records: must be between 1 and 10000",
		`kafka.max_value_bytes: must not exceed kafka.max_buffer_bytes (1KiB)`,
		`kafka.fetch_max_bytes: "lots" is not a size`,
		`kafka.connect_timeout: "10" is not a positive duration`,
		`kafka.isolation: "committed" is not one of: read_uncommitted, read_committed`,
	)
	_ = c
	fsys["huginn.yaml"].Data = []byte("version: 1\ndefault_env: rec\nkafka: {partition_fetch_max_bytes: 1048576, isolation: read_committed}\n")
	if c, msg = load(t, fsys); msg != "" || c.Huginn.Kafka.Limits().PartitionFetchMaxBytes != 1<<20 || !c.Huginn.Kafka.Limits().ReadCommitted {
		t.Fatalf("an integer size and read_committed: %q", msg)
	}
}

func TestKafkaProfileProblems(t *testing.T) {
	fsys := withKafka(map[string]string{"kafka/bad.yaml": `version: 1
match: {repos: ["[x"]}
vars: {env: x, Bad: y, ok: "{repo}"}
sources:
  - {optional: true}
connection:
  security: sasl_ssl
  sasl: {mechanism: scram-sha-1}
  tls: {ca_password: x}
topics:
  consume: [{vars: {a: b}}]
  discover: ["["]
repos:
  orders:
    connection: {bootstrap: "{nope}/${BROKEN", security: plaintext, sasl: {username: u}}
`})
	_, msg := load(t, fsys)
	wantErrors(t, msg,
		`match.repos[0]: invalid glob "[x"`,
		`vars.env: "env" is reserved`,
		`vars.Bad: "Bad" is not a variable name`,
		`vars.ok: placeholders are not expanded inside variables`,
		`sources[0]: missing required key "file"`,
		`connection: missing required key "bootstrap"`,
		`connection.sasl.mechanism: "scram-sha-1" is not one of: plain, scram-sha-256, scram-sha-512`,
		`connection.sasl: sasl_ssl needs sasl.username`,
		`connection.tls.ca_password: set tls.ca`,
		`topics.consume[0]: a topic needs a name`,
		`topics.discover[0]: invalid glob "["`,
		`repos.orders.connection.bootstrap: unknown placeholder {nope}`,
		`repos.orders.connection.bootstrap: malformed reference: unclosed ${`,
		`repos.orders.connection.sasl: sasl is only used with sasl_plaintext or sasl_ssl, not plaintext`,
	)
}

func TestKafkaRepoDirNeedsReposRoot(t *testing.T) {
	fsys := withKafka(map[string]string{"kafka/p.yaml": `version: 1
connection: {bootstrap: b:1, security: plaintext}
topics: {list: [t]}
repos:
  a: {path: /src/a, sources: [{file: "{repo_dir}/x.env"}]}
  b: {sources: [{file: "{repo_dir}/x.env"}]}
`})
	_, msg := load(t, fsys)
	wantErrors(t, msg, "repos.b: {repo_dir} needs repos_root in huginn.yaml or a path for this repository")
	if strings.Contains(msg, "repos.a:") {
		t.Errorf("a repository with a path is fine:\n%s", msg)
	}
	fsys["kafka/p.yaml"].Data = []byte("version: 1\nconnection: {bootstrap: b:1, security: plaintext}\nsources: [{file: \"{repo_dir}/x.env\"}]\ntopics: {list: [t]}\n")
	_, msg = load(t, fsys)
	wantErrors(t, msg, "kafka/p.yaml  {repo_dir} needs repos_root in huginn.yaml")
}

func TestKafkaProfileNeedsTopics(t *testing.T) {
	fsys := withKafka(map[string]string{"kafka/p.yaml": "version: 1\nconnection: {bootstrap: b:1, security: plaintext}\n"})
	_, msg := load(t, fsys)
	wantErrors(t, msg, "topics: list topics (consume, produce, list) or discover them")
}

func TestUnexpectedKafkaEntries(t *testing.T) {
	fsys := withKafka(map[string]string{"kafka/notes.txt": "x", "kafka/Bad Name.yaml": "version: 1"})
	_, msg := load(t, fsys)
	wantErrors(t, msg, "kafka/notes.txt  unexpected entry", `invalid name "Bad Name"`)
}

func TestParseByteSize(t *testing.T) {
	for in, want := range map[string]int64{"0": 0, "1048576": 1 << 20, "512KiB": 512 << 10, "64 MiB": 64 << 20, "1GiB": 1 << 30, "10B": 10} {
		if got, err := ParseByteSize(in); err != nil || got != want {
			t.Errorf("%q = %d %v, want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "1MB", "-1", "x", "99999999999GiB"} {
		if _, err := ParseByteSize(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

const registryProfile = `version: 1
connection: {bootstrap: 'localhost:9092', security: plaintext}
topics: {list: [orders]}
schema_registry:
  url: ${SCHEMA_REGISTRY_URL}
  basic_auth: {username: "${SR_USER}", password: "${SR_PASSWORD}"}
  tls: {ca: /etc/sr/ca.pem}
  decode: [value]
  timeout: 5s
repos:
  billing:
    schema_registry:
      url: https://billing-registry.example:8081
`

func TestKafkaSchemaRegistryLoads(t *testing.T) {
	c, msg := load(t, withKafka(map[string]string{"kafka/dev.yaml": registryProfile}))
	if msg != "" {
		t.Fatal(msg)
	}
	r := c.Kafka[0].SchemaRegistry
	if r.URL != "${SCHEMA_REGISTRY_URL}" || r.BasicAuth.Password != "${SR_PASSWORD}" || r.TLS.CA != "/etc/sr/ca.pem" || r.Decode[0] != "value" || r.Timeout != "5s" {
		t.Fatalf("schema_registry: %+v", r)
	}
	if got := r.over(c.Kafka[0].Repos["billing"].SchemaRegistry); got.URL != "https://billing-registry.example:8081" || got.BasicAuth.Username != "${SR_USER}" || got.Timeout != "5s" {
		t.Fatalf("repository override, key by key: %+v", got)
	}
}

func TestKafkaSchemaRegistryProblems(t *testing.T) {
	_, msg := load(t, withKafka(map[string]string{"kafka/dev.yaml": `version: 1
connection: {bootstrap: 'localhost:9092', security: plaintext}
topics: {list: [orders]}
schema_registry:
  url: schema-registry:8081
  basic_auth: {username: me}
  bearer_token: env:SR_TOKEN
  tls: {ca_password: secret}
  decode: [values]
  timeout: soon
repos:
  billing:
    schema_registry: {timeout: 3s}
`, "kafka/zz.yaml": `version: 1
connection: {bootstrap: 'localhost:9092', security: plaintext}
topics: {list: [orders]}
repos:
  orders:
    schema_registry: {decode: [key]}
`}))
	wantErrors(t, msg,
		`kafka/dev.yaml:5:3  schema_registry.url: "schema-registry:8081" is not an http or https URL`,
		"schema_registry.basic_auth: set both username and password",
		"schema_registry: set basic_auth or bearer_token, not both",
		"schema_registry.tls.ca_password: set tls.ca",
		`schema_registry.decode[0]: "values" is not one of: key, value (did you mean "value"?)`,
		`schema_registry.timeout: "soon" is not a positive duration`,
		`kafka/zz.yaml:6:5  repos.orders.schema_registry: missing required key "url"`,
	)
	if strings.Contains(msg, "repos.billing.schema_registry") {
		t.Errorf("a repository overriding one key of a valid section is fine, except for the section's own problems:\n%s", msg)
	}
}
