package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/clock"
	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/diag"
)

// kafkaRun is what both kafka subcommands need: the use case, the
// environment and the configuration.
type kafkaRun struct {
	kafka ports.Kafka
	env   domain.Env
	cfg   *config.Config
}

// newKafkaRun loads the folder and builds the Kafka use case as a real
// run does, without the TUI nor any Kubernetes client.
func newKafkaRun(o cli.KafkaOptions, e Env, log *slog.Logger) (*kafkaRun, error) {
	c, err := LoadConfig(o.Options, e)
	if err != nil {
		return nil, err
	}
	env, err := ResolveEnv(o.Options, e.Getenv, c)
	if err != nil {
		return nil, err
	}
	if len(c.Kafka) == 0 {
		return nil, fmt.Errorf("the config folder %s has no kafka/ profile (docs/CONFIG.md, section 10)", c.Dir)
	}
	source := "kubernetes"
	if o.Demo {
		source = "demo"
	}
	home := ""
	if e.UserHomeDir != nil {
		home, _ = e.UserHomeDir()
	}
	k := newKafka(c, source, clock.New(), home, e.Getenv, log)
	if k == nil {
		return nil, fmt.Errorf("no topic source for %s", source)
	}
	return &kafkaRun{kafka: k, env: env, cfg: c}, nil
}

func withDiag(o cli.KafkaOptions, fn func(log *slog.Logger) error) error {
	log, closer, _, err := diag.Open(diag.Options{Level: o.LogLevel, Debug: os.Getenv(diag.EnvDebug), Dir: cacheDir()})
	if err != nil {
		return err
	}
	defer func() { _ = closer.Close() }()
	return fn(log)
}

func kafkaCheckCommand(ctx context.Context, o cli.KafkaOptions, w io.Writer) error {
	return withDiag(o, func(log *slog.Logger) error { return kafkaCheck(ctx, o, SystemEnv(), log, w) })
}

func kafkaReadCommand(ctx context.Context, o cli.KafkaOptions, w io.Writer) error {
	return withDiag(o, func(log *slog.Logger) error {
		return kafkaRead(ctx, o, SystemEnv(), log, w, os.Stderr, term.IsTerminal(int(os.Stdout.Fd())))
	})
}

// kafkaCheck prints the profile that applies and the state of each topic;
// it fails when a topic cannot be read, so a script can rely on it.
func kafkaCheck(ctx context.Context, o cli.KafkaOptions, e Env, log *slog.Logger, w io.Writer) error {
	r, err := newKafkaRun(o, e, log)
	if err != nil {
		return err
	}
	sess, err := r.kafka.Open(ctx, r.env, o.Repo)
	if err != nil {
		return err
	}
	defer sess.Close()
	fmt.Fprintf(w, "%s in %s: profile kafka/%s, read only\n\n", o.Repo, r.env, sess.Profile())
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TOPIC\tDIR\tPARTITIONS\tSTATE")
	bad := 0
	for _, t := range sess.Topics() {
		parts, state := "", "ready"
		if t.Partitions > 0 {
			parts = fmt.Sprint(t.Partitions)
		}
		if t.Err != nil {
			bad++
			state = t.Err.Error()
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", domain.EscapeControls(t.Name), direction(t.Direction), parts, state)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	named, regErr := sess.CheckRegistry(ctx)
	switch {
	case !named:
	case regErr != nil:
		fmt.Fprintf(w, "\nschema registry: %s\n", domain.EscapeControls(regErr.Error()))
	default:
		fmt.Fprintln(w, "\nschema registry: ready (records written by its serializers are decoded)")
	}
	switch {
	case bad > 0:
		return fmt.Errorf("%d of %d topics cannot be read", bad, len(sess.Topics()))
	case regErr != nil:
		return fmt.Errorf("the schema registry cannot be used")
	}
	return nil
}

func direction(d domain.TopicDirection) string {
	return map[domain.TopicDirection]string{domain.TopicConsume: "in", domain.TopicProduce: "out", domain.TopicBoth: "in+out"}[d]
}

// kafkaRead prints the records of a topic, one line each, or the raw
// values; notices go to errw. Without --follow it ends with the history.
func kafkaRead(ctx context.Context, o cli.KafkaOptions, e Env, log *slog.Logger, w, errw io.Writer, terminal bool) error {
	r, err := newKafkaRun(o, e, log)
	if err != nil {
		return err
	}
	window := domain.TimeWindow{Tail: r.cfg.Huginn.Kafka.TailRecords}
	if o.Tail > 0 {
		window.Tail = o.Tail
	}
	if o.Since != "" {
		tw, err := domain.ParseTimeWindow(o.Since, 1, 1)
		if err != nil || tw.Since <= 0 {
			return fmt.Errorf("--since %q: a duration such as 15m, 1h or 2d", o.Since)
		}
		window = tw
	}
	sess, err := r.kafka.Open(ctx, r.env, o.Repo)
	if err != nil {
		return err
	}
	defer sess.Close()
	ch, err := sess.Read(ctx, ports.KafkaQuery{Topic: o.Topic, Window: window, Follow: o.Follow, ReadCommitted: o.Committed, Raw: o.NoDecode})
	if err != nil {
		return err
	}
	for b := range ch {
		for _, n := range b.Notices {
			fmt.Fprintln(errw, "huginn:", n)
		}
		for i := range b.Records {
			if _, err := io.WriteString(w, recordLine(&b.Records[i], o.Raw, terminal)); err != nil {
				if errors.Is(err, syscall.EPIPE) {
					return nil // the reader is gone (| head): not an error
				}
				return err
			}
		}
		if b.Err != nil {
			return b.Err
		}
	}
	return nil // the history is printed, or the follow was interrupted
}

// recordLine is a record as printed: time, partition, offset, key and the
// value on one line, or the raw value. Record data is untrusted: on a
// terminal its control characters are escaped.
func recordLine(rec *domain.KafkaRecord, raw, terminal bool) string {
	if raw {
		if rec.Value == nil {
			return "null\n"
		}
		v := strings.ReplaceAll(string(rec.Value), "\n", `\n`) // one value per line
		if terminal {
			v = domain.EscapeControls(v)
		}
		return v + "\n"
	}
	ts := "-"
	if !rec.Time.IsZero() {
		ts = rec.Time.UTC().Format(time.RFC3339Nano)
	}
	value := domain.SchemaPayloadPreview(rec.Value, rec.ValueSize, rec.ValueSchema, 0)
	if rec.Value == nil {
		value = "tombstone"
	}
	return fmt.Sprintf("%s  p%d  #%d  key=%s  %s\n", ts, rec.Partition, rec.Offset, domain.SchemaPayloadPreview(rec.Key, rec.KeySize, rec.KeySchema, 64), value)
}
