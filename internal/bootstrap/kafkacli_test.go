package bootstrap

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/diag"
)

func TestKafkaCheckAndReadCommands(t *testing.T) {
	ctx := context.Background()
	demo := cli.KafkaOptions{Options: cli.Options{Demo: true}, Repo: "payment-service"}
	var out bytes.Buffer
	if err := kafkaCheck(ctx, demo, noFiles(), diag.Discard(), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "profile kafka/demo") || !strings.Contains(out.String(), "payments.requested  in") {
		t.Fatalf("check:\n%s", out.String())
	}

	out.Reset()
	read := demo
	read.Topic, read.Tail = "payments.requested", 2
	if err := kafkaRead(ctx, read, noFiles(), diag.Discard(), &out, &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines)%2 != 0 || len(lines) < 2 || !strings.Contains(lines[0], "  key=") {
		t.Fatalf("read --tail 2: %d lines\n%s", len(lines), out.String())
	}

	out.Reset()
	read.Raw, read.Since = true, "1m"
	if err := kafkaRead(ctx, read, noFiles(), diag.Discard(), &out, &bytes.Buffer{}, true); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(out.String(), 0x1b) || !strings.Contains(out.String(), `"eventId"`) {
		t.Fatalf("raw:\n%s", out.String())
	}
	read.Since = "soon"
	if err := kafkaRead(ctx, read, noFiles(), diag.Discard(), &out, &bytes.Buffer{}, false); err == nil {
		t.Fatal("bad --since accepted")
	}
}

func TestKafkaCheckFailsOnUnreadableTopics(t *testing.T) {
	dir := t.TempDir()
	if err := os.CopyFS(dir, os.DirFS("../../examples/config")); err != nil {
		t.Fatal(err)
	}
	profile := "version: 1\nconnection: {bootstrap: 'b:1', security: plaintext}\ntopics: {list: [ok.topic, \"${MISSING_KEY}\"]}\n"
	if err := os.WriteFile(filepath.Join(dir, "kafka", "demo.yaml"), []byte(profile), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	err := kafkaCheck(context.Background(), cli.KafkaOptions{Options: cli.Options{Demo: true, ConfigPath: dir}, Repo: "any"}, noFiles(), diag.Discard(), &out)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 topics cannot be read") || !strings.Contains(out.String(), "MISSING_KEY not found") {
		t.Fatalf("err %v\n%s", err, out.String())
	}
	if err := os.RemoveAll(filepath.Join(dir, "kafka")); err != nil {
		t.Fatal(err)
	}
	if err := kafkaCheck(context.Background(), cli.KafkaOptions{Options: cli.Options{Demo: true, ConfigPath: dir}, Repo: "any"}, noFiles(), diag.Discard(), &out); err == nil || !strings.Contains(err.Error(), "no kafka/ profile") {
		t.Fatalf("no kafka/: %v", err)
	}
}

func TestRecordLineEscapesOnTerminals(t *testing.T) {
	rec := domain.KafkaRecord{Value: []byte("a\x1b]52;c;evil\x07b\nc"), Key: []byte("k")}
	if l := recordLine(&rec, true, true); strings.ContainsRune(l, 0x1b) || l != `a\x1b]52;c;evil\x07b\nc`+"\n" {
		t.Fatalf("raw on a terminal: %q", l)
	}
	if l := recordLine(&rec, true, false); !strings.ContainsRune(l, 0x1b) {
		t.Fatalf("raw into a pipe is the value as received: %q", l)
	}
	if l := recordLine(&domain.KafkaRecord{}, true, false); l != "null\n" {
		t.Fatalf("tombstone: %q", l)
	}
	if l := recordLine(&rec, false, false); strings.ContainsRune(l, 0x1b) {
		t.Fatalf("a record line never carries control characters: %q", l)
	}
}

type brokenPipe struct{}

func (brokenPipe) Write([]byte) (int, error) {
	return 0, &os.PathError{Op: "write", Path: "|1", Err: syscall.EPIPE}
}

func TestKafkaReadIntoAClosedPipeIsQuiet(t *testing.T) {
	o := cli.KafkaOptions{Options: cli.Options{Demo: true}, Repo: "payment-service", Topic: "payments.requested", Tail: 3}
	if err := kafkaRead(context.Background(), o, noFiles(), diag.Discard(), brokenPipe{}, &bytes.Buffer{}, false); err != nil {
		t.Fatalf("| head must not fail: %v", err)
	}
}

// With --demo the demo registry answers in memory: check reports it, read
// prints decoded records, --no-decode their bytes.
func TestKafkaCommandsWithTheSchemaRegistry(t *testing.T) {
	ctx := context.Background()
	demo := cli.KafkaOptions{Options: cli.Options{Demo: true}, Repo: "payment-service"}
	var out bytes.Buffer
	if err := kafkaCheck(ctx, demo, noFiles(), diag.Discard(), &out); err != nil || !strings.Contains(out.String(), "schema registry: ready") {
		t.Fatalf("check: %v\n%s", err, out.String())
	}

	read := demo
	read.Topic, read.Tail = "payments.requested", 100
	out.Reset()
	var notices bytes.Buffer
	if err := kafkaRead(ctx, read, noFiles(), diag.Discard(), &out, &notices, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `avro 1 · {"id":"PAY-`) || !strings.Contains(out.String(), `"occurredAt":"20`) || !strings.Contains(out.String(), "json 2 · ") {
		t.Fatalf("decoded records:\n%s", out.String())
	}
	if !strings.Contains(notices.String(), "schema 999") || strings.Count(notices.String(), "schema 999") != 1 {
		t.Fatalf("one notice for the unknown schema:\n%s", notices.String())
	}

	read.NoDecode = true
	out.Reset()
	if err := kafkaRead(ctx, read, noFiles(), diag.Discard(), &out, &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "avro 1 · ") || !strings.Contains(out.String(), "schema 1, ") {
		t.Fatalf("--no-decode:\n%s", out.String())
	}
}
