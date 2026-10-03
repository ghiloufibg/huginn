package bootstrap

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/ghiloufibg/huginn/internal/config"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// TestKafkaEndToEnd runs the whole chain of a real run against an
// in-memory cluster: a config folder whose profile reads a repository's
// dotenv overlays, per-topic SASL accounts, discovery, then records.
func TestKafkaEndToEnd(t *testing.T) {
	c, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(2, "orders.in", "orders.out"), kfake.EnableSASL(),
		kfake.Superuser("SCRAM-SHA-512", "orders-app", "pw-in"), kfake.Superuser("SCRAM-SHA-512", "orders-pub", "pw-out"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	prod, err := kgo.NewClient(kgo.SeedBrokers(c.ListenAddrs()...), kgo.SASL(scram.Auth{User: "orders-app", Pass: "pw-in"}.AsSha512Mechanism()))
	if err != nil {
		t.Fatal(err)
	}
	for i := range 6 {
		if err := prod.ProduceSync(context.Background(), &kgo.Record{Topic: "orders.in", Value: fmt.Appendf(nil, `{"n":%d}`, i)}).FirstErr(); err != nil {
			t.Fatal(err)
		}
	}
	prod.Close()

	root := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.CopyFS(filepath.Join(root, "cfg"), os.DirFS("../../examples/config")); err != nil {
		t.Fatal(err)
	}
	_ = os.RemoveAll(filepath.Join(root, "cfg", "kafka"))
	write("cfg/huginn.yaml", "version: 1\ndefault_env: rec\nrepos_root: "+filepath.Join(root, "repos")+"\nkafka: {connect_timeout: 3s, request_timeout: 5s}\n")
	write("cfg/kafka/10-repo.yaml", `version: 1
match:
  files: ["{repo_dir}/deploy/overlays/{env}/kafka.env"]
sources:
  - file: "{repo_dir}/deploy/base/kafka.env"
  - file: "{repo_dir}/deploy/overlays/{env}/kafka.env"
vars: {account: IN}
connection:
  bootstrap: ${KAFKA_BOOTSTRAP_SERVERS}
  security: ${KAFKA_SECURITY_PROTOCOL}
  sasl:
    mechanism: SCRAM-SHA-512
    username: ${{account}_USERNAME}
    password: ${{account}_PASSWORD}
topics:
  discover: ["KAFKA_TOPIC_*"]
repos:
  orders:
    topics:
      produce: [{name: "${KAFKA_TOPIC_OUT}", vars: {account: OUT}}]
`)
	write("repos/orders/deploy/base/kafka.env", "KAFKA_SECURITY_PROTOCOL=SASL_PLAINTEXT\nKAFKA_TOPIC_IN=orders.in\nKAFKA_TOPIC_OUT=orders.out\n")
	write("repos/orders/deploy/overlays/rec/kafka.env", fmt.Sprintf("KAFKA_BOOTSTRAP_SERVERS=%s\nIN_USERNAME=orders-app\nIN_PASSWORD='pw-in'\nOUT_USERNAME=orders-pub\nOUT_PASSWORD=pw-out\n", c.ListenAddrs()[0]))

	cfg, err := config.LoadDir(filepath.Join(root, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	k := newKafka(cfg, "kubernetes", nil, "", os.Getenv, log)
	ctx := context.Background()
	if got := k.Repos(ctx, "rec", []string{"orders", "billing"}); len(got) != 1 || !got["orders"] {
		t.Fatalf("repos: %v", got)
	}
	sess, err := k.Open(ctx, "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	ts := sess.Topics()
	if len(ts) != 2 || ts[0].Name != "orders.out" || ts[0].Direction != domain.TopicProduce || ts[1].Name != "orders.in" {
		t.Fatalf("topics: %+v", ts)
	}
	for _, x := range ts {
		if x.Err != nil || x.Partitions != 2 {
			t.Fatalf("%s: %+v", x.Name, x)
		}
	}
	ch, err := sess.Read(ctx, ports.KafkaQuery{Topic: "orders.in", Window: domain.TimeWindow{Since: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for b := range ch {
		if b.Err != nil {
			t.Fatal(b.Err)
		}
		n += len(b.Records)
	}
	if n != 6 {
		t.Fatalf("%d records, want 6", n)
	}

	// A wrong password, a missing key: errors and the diagnostic log name
	// keys and files, never a secret value.
	write("repos/orders/deploy/overlays/rec/kafka.env", fmt.Sprintf("KAFKA_BOOTSTRAP_SERVERS=%s\nIN_USERNAME=orders-app\nIN_PASSWORD=hunter2-wrong\n", c.ListenAddrs()[0]))
	sess2, err := newKafka(cfg, "kubernetes", nil, "", os.Getenv, log).Open(ctx, "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	defer sess2.Close()
	var msgs []string
	for _, x := range sess2.Topics() {
		if x.Err == nil {
			t.Fatalf("%s: read with a wrong password or a missing key", x.Name)
		}
		msgs = append(msgs, x.Err.Error())
	}
	all := strings.Join(msgs, "\n") + "\n" + logBuf.String()
	for _, secret := range []string{"pw-in", "pw-out", "hunter2-wrong"} {
		if strings.Contains(all, secret) {
			t.Errorf("secret %q leaked:\n%s", secret, all)
		}
	}
	if !strings.Contains(all, "credentials rejected") && !strings.Contains(all, "closed the connection during authentication") {
		t.Errorf("the wrong password is not explained:\n%s", strings.Join(msgs, "\n"))
	}
	if !strings.Contains(all, "KAFKA_TOPIC_OUT") && !strings.Contains(all, "OUT_USERNAME") {
		t.Errorf("the missing key is not named:\n%s", strings.Join(msgs, "\n"))
	}
}
