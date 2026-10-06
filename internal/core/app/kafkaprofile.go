package app

// The Kafka profiles of the config folder, as plain data for the use case
// (the composition root converts them from internal/config). Strings keep
// their placeholders and references; KafkaService resolves them.

// KafkaProfile is one file of kafka/.
type KafkaProfile struct {
	Name       string
	MatchRepos []string
	MatchFiles []string
	Sources    []KafkaSourceSpec
	Vars       map[string]string
	Conn       KafkaConnSpec
	Topics     KafkaTopicsSpec
	Registry   KafkaRegistrySpec
	Repos      map[string]KafkaRepoSpec
}

// KafkaSourceSpec is a dotenv file of a profile.
type KafkaSourceSpec struct {
	File     string
	Sops     bool
	Optional bool
}

// KafkaConnSpec is a connection as written; empty fields are not set.
type KafkaConnSpec struct {
	Bootstrap, Security           string
	Mechanism, Username, Password string
	CA, CAPassword                string
}

// over returns c with the fields set in o replacing its own.
func (c KafkaConnSpec) over(o KafkaConnSpec) KafkaConnSpec {
	pick := func(a, b string) string {
		if b != "" {
			return b
		}
		return a
	}
	return KafkaConnSpec{
		Bootstrap: pick(c.Bootstrap, o.Bootstrap), Security: pick(c.Security, o.Security),
		Mechanism: pick(c.Mechanism, o.Mechanism), Username: pick(c.Username, o.Username), Password: pick(c.Password, o.Password),
		CA: pick(c.CA, o.CA), CAPassword: pick(c.CAPassword, o.CAPassword),
	}
}

// KafkaRegistrySpec is a Schema Registry as written; empty fields are not
// set, and a zero spec means no registry.
type KafkaRegistrySpec struct {
	URL                string
	Username, Password string
	Token              string
	CA, CAPassword     string
	Decode             []string // key, value; empty means both
	Timeout            string
}

// KafkaTopicSpec is a topic as written.
type KafkaTopicSpec struct {
	Name string
	Vars map[string]string
}

// KafkaTopicsSpec lists topics.
type KafkaTopicsSpec struct {
	Consume, Produce, List []KafkaTopicSpec
	Discover               []string
}

// KafkaRepoSpec adds to or overrides a profile for one repository.
type KafkaRepoSpec struct {
	Path     string
	Enabled  bool
	Vars     map[string]string
	Conn     KafkaConnSpec
	Sources  []KafkaSourceSpec
	Topics   KafkaTopicsSpec
	Registry KafkaRegistrySpec
}
