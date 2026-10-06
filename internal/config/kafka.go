package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Kafka is the kafka: section of huginn.yaml: technical limits of the
// Kafka screen, the same for every profile (docs/CONFIG.md).
type Kafka struct {
	TailRecords            int    `yaml:"tail_records" doc:"Records per partition loaded by the tail (key 0). 1 to 10000. Default 100."`
	MaxRecords             int    `yaml:"max_records" doc:"Records kept in memory per Kafka screen; the oldest are dropped first. Default 20000."`
	MaxBufferBytes         string `yaml:"max_buffer_bytes" doc:"Bytes of keys, values and headers kept per Kafka screen; the oldest records are dropped first. A number of bytes or a size such as 64MiB. Default 64MiB."`
	MaxValueBytes          string `yaml:"max_value_bytes" doc:"A larger key or value is kept truncated, with its real size shown. Default 256KiB."`
	FetchMaxBytes          string `yaml:"fetch_max_bytes" doc:"Bytes per fetch response from a broker. Default 1MiB."`
	PartitionFetchMaxBytes string `yaml:"partition_fetch_max_bytes" doc:"Bytes per partition per fetch response. Default 256KiB."`
	ConnectTimeout         string `yaml:"connect_timeout" doc:"Time to reach a broker and authenticate before it is reported unreachable, e.g. 10s. Default 10s."`
	RequestTimeout         string `yaml:"request_timeout" doc:"Time allowed for one request to a broker, e.g. 30s. Default 30s."`
	ClientID               string `yaml:"client_id" doc:"Kafka client id, so the brokers' operators can recognize Huginn. Default huginn."`
	Isolation              string `yaml:"isolation" doc:"Isolation when a Kafka screen opens (key i switches it): read_uncommitted shows every record, read_committed hides aborted transactions. Default read_uncommitted." enum:"read_uncommitted,read_committed"`
}

// KafkaLimits are the kafka: section with sizes and durations parsed.
type KafkaLimits struct {
	TailRecords, MaxRecords int
	MaxBufferBytes          int64
	MaxValueBytes           int64
	FetchMaxBytes           int64
	PartitionFetchMaxBytes  int64
	ConnectTimeout          time.Duration
	RequestTimeout          time.Duration
	ClientID                string
	ReadCommitted           bool
}

// Limits returns the section parsed. It is meant for a validated folder:
// a value that does not parse reads as zero.
func (k Kafka) Limits() KafkaLimits {
	size := func(s string) int64 { n, _ := ParseByteSize(s); return n }
	dur := func(s string) time.Duration { d, _ := time.ParseDuration(s); return d }
	return KafkaLimits{
		TailRecords: k.TailRecords, MaxRecords: k.MaxRecords,
		MaxBufferBytes: size(k.MaxBufferBytes), MaxValueBytes: size(k.MaxValueBytes),
		FetchMaxBytes: size(k.FetchMaxBytes), PartitionFetchMaxBytes: size(k.PartitionFetchMaxBytes),
		ConnectTimeout: dur(k.ConnectTimeout), RequestTimeout: dur(k.RequestTimeout),
		ClientID: k.ClientID, ReadCommitted: k.Isolation == "read_committed",
	}
}

// ParseByteSize reads a number of bytes, optionally followed by B, KiB,
// MiB or GiB (1024-based): 1048576, 512KiB, 64MiB.
func ParseByteSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}} {
		if rest, ok := strings.CutSuffix(t, u.suffix); ok {
			t, mult = strings.TrimSpace(rest), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil || n < 0 || n > (1<<62)/mult {
		return 0, fmt.Errorf("%q is not a size such as 1048576, 512KiB or 64MiB", s)
	}
	return n * mult, nil
}

// KafkaProfile is one file of kafka/: where the Kafka settings of the
// repositories it matches are, and which topics to show. Profiles are
// tried in file name order; the first whose match accepts a repository
// wins.
type KafkaProfile struct {
	// Name is the file name without extension; File the path in the folder.
	Name, File string            `yaml:"-"`
	Version    int               `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Match      KafkaMatch        `yaml:"match" doc:"Repositories this profile applies to. No match section: every repository."`
	Sources    []KafkaSource     `yaml:"sources" doc:"Dotenv files read in order and merged (a later key replaces an earlier one); their keys are written ${KEY} in the values below."`
	Vars       map[string]string `yaml:"vars" doc:"Free variables written {name} in any string of the profile, overridden per repository and per topic, e.g. {account: ORDERS} makes ${{account}_PASSWORD} read ORDERS_PASSWORD."`
	Connection KafkaConnection   `yaml:"connection" doc:"How to reach the brokers. bootstrap and security are required." required:"true"`
	Topics     KafkaTopics       `yaml:"topics" doc:"Topics shown for every repository of this profile."`
	// SchemaRegistry decodes records written by Schema Registry
	// serializers (docs/CONFIG.md).
	SchemaRegistry KafkaSchemaRegistry  `yaml:"schema_registry" doc:"Schema Registry used to decode records written by its serializers (Avro, JSON Schema), read only. Without it, such records are shown undecoded."`
	Repos          map[string]KafkaRepo `yaml:"repos" doc:"Per repository additions and overrides, by repository name. Listed repositories are matched even if match.repos does not name them."`
}

// KafkaMatch selects the repositories of a profile.
type KafkaMatch struct {
	Repos []string `yaml:"repos" doc:"Globs on the repository name, e.g. [\"payment-*\"]. Empty: any repository."`
	Files []string `yaml:"files" doc:"Path globs that must each match an existing file, e.g. [\"{repo_dir}/deploy/{env}/secrets.env\"]; only checked for existence. Limits the profile to repositories that have Kafka settings in the current environment."`
}

// KafkaSource is one dotenv file of a profile.
type KafkaSource struct {
	File     string `yaml:"file" doc:"Path of a dotenv file (KEY=VALUE lines). A glob must match exactly one file." required:"true"`
	Sops     bool   `yaml:"sops" doc:"Decrypt the file with sops first, in memory."`
	Optional bool   `yaml:"optional" doc:"A missing file is skipped instead of being an error."`
}

// KafkaConnection says how to reach the brokers. Every value may use
// placeholders, ${KEY} references and env:VAR.
type KafkaConnection struct {
	Bootstrap string    `yaml:"bootstrap" doc:"Brokers as host:port, comma separated."`
	Security  string    `yaml:"security" doc:"plaintext, ssl, sasl_plaintext or sasl_ssl (case ignored)."`
	SASL      KafkaSASL `yaml:"sasl" doc:"SASL authentication; required with sasl_plaintext and sasl_ssl."`
	TLS       KafkaTLS  `yaml:"tls" doc:"Certificates trusted for the brokers (ssl and sasl_ssl)."`
}

// KafkaSchemaRegistry is the Schema Registry of a profile. Every value may
// use placeholders, ${KEY} references and env:VAR.
type KafkaSchemaRegistry struct {
	URL         string         `yaml:"url" doc:"Base URL of the registry, http or https, e.g. https://schema-registry.example:8081."`
	BasicAuth   KafkaBasicAuth `yaml:"basic_auth" doc:"Basic authentication (Java: basic.auth.user.info)."`
	BearerToken string         `yaml:"bearer_token" doc:"Bearer token (Java: bearer.auth.token), instead of basic_auth."`
	TLS         KafkaTLS       `yaml:"tls" doc:"Certificates trusted for the registry (https)."`
	Decode      []string       `yaml:"decode" doc:"What to decode: value (the default), key, or both. Keys only when listed: a big-endian number key also starts with 0." enum:"key,value"`
	Timeout     string         `yaml:"timeout" doc:"Time allowed for one request to the registry, e.g. 10s. Default 10s."`
}

// over returns r with the keys set in o replacing its own, key by key.
func (r KafkaSchemaRegistry) over(o KafkaSchemaRegistry) KafkaSchemaRegistry {
	pick := func(a, b string) string {
		if b != "" {
			return b
		}
		return a
	}
	out := KafkaSchemaRegistry{
		URL:         pick(r.URL, o.URL),
		BasicAuth:   KafkaBasicAuth{Username: pick(r.BasicAuth.Username, o.BasicAuth.Username), Password: pick(r.BasicAuth.Password, o.BasicAuth.Password)},
		BearerToken: pick(r.BearerToken, o.BearerToken),
		TLS:         KafkaTLS{CA: pick(r.TLS.CA, o.TLS.CA), CAPassword: pick(r.TLS.CAPassword, o.TLS.CAPassword)},
		Decode:      r.Decode,
		Timeout:     pick(r.Timeout, o.Timeout),
	}
	if len(o.Decode) > 0 {
		out.Decode = o.Decode
	}
	return out
}

// IsZero reports whether the section is absent.
func (r KafkaSchemaRegistry) IsZero() bool {
	return r.URL == "" && r.BasicAuth == (KafkaBasicAuth{}) && r.BearerToken == "" && r.TLS == (KafkaTLS{}) && len(r.Decode) == 0 && r.Timeout == ""
}

// KafkaBasicAuth is basic authentication.
type KafkaBasicAuth struct {
	Username string `yaml:"username" doc:"User name."`
	Password string `yaml:"password" doc:"Password."`
}

// KafkaSASL is SASL authentication.
type KafkaSASL struct {
	Mechanism string `yaml:"mechanism" doc:"plain, scram-sha-256 or scram-sha-512 (case ignored)."`
	Username  string `yaml:"username" doc:"User name."`
	Password  string `yaml:"password" doc:"Password."`
}

// KafkaTLS lists the certificates trusted for the brokers.
type KafkaTLS struct {
	CA         string `yaml:"ca" doc:"Truststore: a PEM file (.pem, .crt, .cer) or a PKCS12 file (.p12, .pfx). A glob must match exactly one file. Without it, the system roots."`
	CAPassword string `yaml:"ca_password" doc:"Password of a PKCS12 truststore."`
}

// KafkaTopics lists the topics of a profile or repository.
type KafkaTopics struct {
	Consume  []KafkaTopic `yaml:"consume" doc:"Topics the service consumes."`
	Produce  []KafkaTopic `yaml:"produce" doc:"Topics the service produces."`
	List     []KafkaTopic `yaml:"list" doc:"Topics without a direction."`
	Discover []string     `yaml:"discover" doc:"Globs on the keys of the sources; each value found is a topic without a direction (comma-separated values are split), e.g. [\"KAFKA_TOPIC*\"]."`
}

// Any reports whether at least one topic is listed or discovered.
func (t KafkaTopics) Any() bool {
	return len(t.Consume)+len(t.Produce)+len(t.List)+len(t.Discover) > 0
}

// KafkaTopic is a topic name, or an object with variables for that topic
// only. In YAML it may be written as a string.
type KafkaTopic struct {
	Name string            `yaml:"name" doc:"Topic name: a literal or a ${KEY} reference."`
	Vars map[string]string `yaml:"vars" doc:"Variables for this topic only, e.g. another SASL account."`
}

// UnmarshalYAML accepts a scalar (the name) or a mapping.
func (t *KafkaTopic) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*t = KafkaTopic{Name: n.Value}
		return nil
	}
	type plain KafkaTopic
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*t = KafkaTopic(p)
	return nil
}

// KafkaRepo adds to or overrides a profile for one repository.
type KafkaRepo struct {
	Path       string            `yaml:"path" doc:"The repository folder when it is not repos_root/<repo>; {repo_dir} is this path."`
	Enabled    *bool             `yaml:"enabled" doc:"false: no Kafka screen for this repository. Default true."`
	Vars       map[string]string `yaml:"vars" doc:"Variables merged over the profile's."`
	Connection KafkaConnection   `yaml:"connection" doc:"Connection keys merged over the profile's, key by key."`
	Sources    []KafkaSource     `yaml:"sources" doc:"Sources read after the profile's."`
	Topics     KafkaTopics       `yaml:"topics" doc:"Topics added to the profile's."`
	// SchemaRegistry overrides the profile's registry, key by key.
	SchemaRegistry KafkaSchemaRegistry `yaml:"schema_registry" doc:"Schema Registry keys merged over the profile's, key by key."`
}

// IsEnabled reports whether the repository has a Kafka screen (default
// yes).
func (r KafkaRepo) IsEnabled() bool { return r.Enabled == nil || *r.Enabled }
