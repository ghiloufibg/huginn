package bootstrap

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/diag"
)

func TestDemoKafka(t *testing.T) {
	a, err := Build(cli.Options{Demo: true}, noFiles(), diag.Discard())
	if err != nil {
		t.Fatal(err)
	}
	k := a.UI.Kafka
	if k == nil || a.UI.KafkaTail != 100 || a.UI.KafkaMaxRecords != 20000 || a.UI.KafkaMaxBytes != 64<<20 {
		t.Fatalf("kafka options: %+v", a.UI)
	}
	ctx := context.Background()
	got := k.Repos(ctx, a.Env, []string{"payment-service", "user-api", "ledger-writer"})
	if len(got) != 2 || !got["payment-service"] || !got["ledger-writer"] {
		t.Fatalf("repos: %v", got)
	}
	sess, err := k.Open(ctx, a.Env, "payment-service")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	ts := sess.Topics()
	if len(ts) != 3 || ts[0].Name != "payments.requested" || ts[0].Direction != domain.TopicConsume || ts[0].Partitions == 0 || ts[0].Err != nil {
		t.Fatalf("topics: %+v", ts)
	}
	ch, err := sess.Read(ctx, ports.KafkaQuery{Topic: ts[0].Name, Window: domain.TimeWindow{Tail: 5}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for b := range ch {
		n += len(b.Records)
	}
	if n != 5*ts[0].Partitions {
		t.Fatalf("%d records, want 5 per partition", n)
	}
}

func TestNoKafkaFolderNoKafka(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../examples/config")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "kafka")); err != nil {
		t.Fatal(err)
	}
	a, err := Build(cli.Options{Demo: true, ConfigPath: dir}, noFiles(), diag.Discard())
	if err != nil {
		t.Fatal(err)
	}
	if a.UI.Kafka != nil {
		t.Fatal("without kafka/, the feature must not exist")
	}
	// Profiles but no topic source for a real cluster yet: absent too.
	if k := newKafka(demoConfig(t), "kubernetes", nil, "", nil, diag.Discard()); k != nil {
		t.Fatal("no topic source registered for kubernetes yet")
	}
}
