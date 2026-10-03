package bootstrap

import (
	"log/slog"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/demo"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/localfiles"
	"github.com/ghiloufibg/huginn/internal/adapters/driven/sops"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/app"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// kafkaRegistry lists the topic sources, selected like the cluster
// client: demo with --demo, kafka otherwise (milestone M5 K2).
func kafkaRegistry() *ports.Registry[func(c *config.Config, clock ports.Clock) ports.TopicSourceFactory] {
	r := ports.NewRegistry[func(c *config.Config, clock ports.Clock) ports.TopicSourceFactory]("topic source")
	r.Register("demo", func(c *config.Config, clock ports.Clock) ports.TopicSourceFactory {
		return demo.NewKafka(c.Huginn.Demo.Seed, clock)
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
	factory, err := kafkaRegistry().Lookup(source)
	if err != nil {
		log.Info("kafka profiles present but no topic source for this run", "source", source)
		return nil
	}
	l := c.Huginn.Kafka.Limits()
	return &app.KafkaService{
		Profiles:  kafkaProfiles(c),
		Files:     &localfiles.Files{Secrets: &sops.Provider{Dir: c.Dir}},
		Sources:   factory(c, clock),
		ConfigDir: c.Dir, ReposRoot: c.Huginn.ReposRoot, Home: home, Getenv: getenv,
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
			Vars: p.Vars, Conn: conn(p.Connection), Topics: topics(p.Topics), Repos: map[string]app.KafkaRepoSpec{},
		}
		for name, r := range p.Repos {
			ap.Repos[name] = app.KafkaRepoSpec{
				Path: r.Path, Enabled: r.IsEnabled(), Vars: r.Vars, Conn: conn(r.Connection),
				Sources: sources(r.Sources), Topics: topics(r.Topics),
			}
		}
		out = append(out, ap)
	}
	return out
}
