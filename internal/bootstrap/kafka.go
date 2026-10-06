package bootstrap

import (
	"log/slog"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/kafka"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/localfiles"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/schemaregistry"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/sops"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// kafkaRegistry lists the topic sources, selected like the cluster
// client: demo with --demo, the franz-go adapter for a real cluster.
func kafkaRegistry(log *slog.Logger) *ports.Registry[func(c *config.Config, clock ports.Clock) ports.TopicSourceFactory] {
	r := ports.NewRegistry[func(c *config.Config, clock ports.Clock) ports.TopicSourceFactory]("topic source")
	r.Register("demo", func(c *config.Config, clock ports.Clock) ports.TopicSourceFactory {
		return demo.NewKafka(c.Huginn.Demo.Seed, clock)
	})
	r.Register("kubernetes", func(c *config.Config, _ ports.Clock) ports.TopicSourceFactory {
		l := c.Huginn.Kafka.Limits()
		return &kafka.Factory{Options: kafka.Options{
			ClientID: l.ClientID, ConnectTimeout: l.ConnectTimeout, RequestTimeout: l.RequestTimeout,
			FetchMaxBytes: clampInt32(l.FetchMaxBytes), PartitionFetchMaxBytes: clampInt32(l.PartitionFetchMaxBytes),
			MaxValueBytes: int(l.MaxValueBytes),
			Log:           log,
		}}
	})
	return r
}

func clampInt32(n int64) int32 { return int32(min(n, 1<<31-1)) }

// schemaRegistries lists the Schema Registry readers, selected like the
// topic sources. A run without one leaves framed records undecoded.
func schemaRegistries() *ports.Registry[func(maxDecoded int) ports.SchemaDecoderFactory] {
	r := ports.NewRegistry[func(maxDecoded int) ports.SchemaDecoderFactory]("schema registry")
	r.Register("kubernetes", func(maxDecoded int) ports.SchemaDecoderFactory {
		return &schemaregistry.Factory{MaxDecodedBytes: maxDecoded}
	})
	// The real client, answered in memory: --demo decodes as a real run.
	r.Register("demo", func(maxDecoded int) ports.SchemaDecoderFactory {
		return &schemaregistry.Factory{MaxDecodedBytes: maxDecoded, Transport: demo.RegistryTransport()}
	})
	return r
}

// newKafka builds the Kafka use case, or nil when the folder has no
// kafka/ profile or no topic source exists for this run: the feature is
// then absent from the UI, with nothing initialised.
func newKafka(c *config.Config, source string, clock ports.Clock, home string, getenv func(string) string, log *slog.Logger) ports.Kafka {
	if len(c.Kafka) == 0 {
		return nil
	}
	factory, err := kafkaRegistry(log).Lookup(source)
	if err != nil {
		log.Info("kafka profiles present but no topic source for this run", "source", source)
		return nil
	}
	l := c.Huginn.Kafka.Limits()
	var registries ports.SchemaDecoderFactory
	if newRegistry, err := schemaRegistries().Lookup(source); err == nil {
		// The view keeps max_value_bytes of a value: decoding more than a
		// few times that only costs memory and time.
		registries = newRegistry(int(min(max(8*l.MaxValueBytes, 1<<20), 16<<20)))
	}
	return &app.KafkaService{
		Registries: registries,
		Profiles:   kafkaProfiles(c),
		Files:      &localfiles.Files{Secrets: &sops.Provider{Dir: c.Dir}},
		Sources:    factory(c, clock),
		ConfigDir:  c.Dir, ReposRoot: c.Huginn.ReposRoot, Home: home, Getenv: getenv,
		MaxRecords: l.MaxRecords, MaxBufferBytes: int(l.MaxBufferBytes), MaxValueBytes: int(l.MaxValueBytes),
		Log: log,
	}
}

// kafkaProfiles converts the profiles of kafka/ to the use case's data.
func kafkaProfiles(c *config.Config) []app.KafkaProfile {
	conn := func(k config.KafkaConnection) app.KafkaConnSpec {
		return app.KafkaConnSpec{
			Bootstrap: k.Bootstrap, Security: k.Security, Mechanism: k.SASL.Mechanism,
			Username: k.SASL.Username, Password: k.SASL.Password, CA: k.TLS.CA, CAPassword: k.TLS.CAPassword,
		}
	}
	registry := func(r config.KafkaSchemaRegistry) app.KafkaRegistrySpec {
		return app.KafkaRegistrySpec{
			URL: r.URL, Username: r.BasicAuth.Username, Password: r.BasicAuth.Password, Token: r.BearerToken,
			CA: r.TLS.CA, CAPassword: r.TLS.CAPassword, Decode: r.Decode, Timeout: r.Timeout,
		}
	}
	sources := func(ss []config.KafkaSource) []app.KafkaSourceSpec {
		out := make([]app.KafkaSourceSpec, len(ss))
		for i, s := range ss {
			out[i] = app.KafkaSourceSpec{File: s.File, Sops: s.Sops, Optional: s.Optional}
		}
		return out
	}
	topics := func(t config.KafkaTopics) app.KafkaTopicsSpec {
		list := func(ts []config.KafkaTopic) []app.KafkaTopicSpec {
			out := make([]app.KafkaTopicSpec, len(ts))
			for i, x := range ts {
				out[i] = app.KafkaTopicSpec{Name: x.Name, Vars: x.Vars}
			}
			return out
		}
		return app.KafkaTopicsSpec{Consume: list(t.Consume), Produce: list(t.Produce), List: list(t.List), Discover: t.Discover}
	}
	var out []app.KafkaProfile
	for _, p := range c.Kafka {
		ap := app.KafkaProfile{
			Name: p.Name, MatchRepos: p.Match.Repos, MatchFiles: p.Match.Files, Sources: sources(p.Sources),
			Vars: p.Vars, Conn: conn(p.Connection), Topics: topics(p.Topics), Registry: registry(p.SchemaRegistry),
			Repos: map[string]app.KafkaRepoSpec{},
		}
		for name, r := range p.Repos {
			ap.Repos[name] = app.KafkaRepoSpec{
				Path: r.Path, Enabled: r.IsEnabled(), Vars: r.Vars, Conn: conn(r.Connection),
				Sources: sources(r.Sources), Topics: topics(r.Topics), Registry: registry(r.SchemaRegistry),
			}
		}
		out = append(out, ap)
	}
	return out
}
