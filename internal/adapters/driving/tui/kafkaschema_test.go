package tui

import (
	"strings"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// schemaRecords are records as a session decoding with a Schema Registry
// delivers them: one decoded, one that could not be.
func schemaRecords() []domain.KafkaRecord {
	recs := kafkaRecords(2)
	recs[0].Value = []byte(`{"orderId":"ord-1","quantity":3,"note":{"string":"gift"}}`)
	recs[0].ValueSize = len(recs[0].Value)
	recs[0].ValueSchema = domain.SchemaRef{ID: 7, Format: domain.SchemaAvro, Name: "com.example.OrderCreated"}
	recs[1].Value = []byte{0, 0, 0, 1, 0x94, 2}
	recs[1].ValueSize = len(recs[1].Value)
	recs[1].ValueSchema = domain.SchemaRef{ID: 404, Err: "registry answered 404 Schema not found: not found\x1b[2J"}
	return recs
}

func TestKafkaSchemaRecords(t *testing.T) {
	m, fk := newKafkaModel(t)
	fk.session.decodes = true
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	r := m.top().(*kafkaRecordsScreen)
	feedKafka(m, r, ports.KafkaBatch{Records: schemaRecords(), HistoryDone: true})
	out := render(m, 160, 12)
	if !strings.Contains(out, `avro 7 · {"orderId":"ord-1"`) {
		t.Fatalf("decoded value:\n%s", out)
	}
	if !strings.Contains(out, "schema 404, 6 B · registry answered 404 Schema not found") || strings.Contains(out, "\x1b[2J") {
		t.Fatalf("undecoded value with its reason, control characters escaped:\n%s", out)
	}
	golden(t, "kafka_records_schema_160x12", out)

	press(m, "g", "enter") // the first record
	zoom := render(m, 100, 20)
	if !strings.Contains(zoom, "avro, schema 7 (com.example.OrderCreated)") || !strings.Contains(zoom, `"orderId": "ord-1"`) {
		t.Fatalf("zoom:\n%s", zoom)
	}
	press(m, "esc")

	before := len(fk.session.queries)
	press(m, "D")
	if q := fk.session.queries; len(q) != before+1 || !q[len(q)-1].Raw || !r.raw {
		t.Fatalf("D reads again undecoded: %+v", q[before:])
	}
	if !strings.Contains(render(m, 200, 12), "not decoded") {
		t.Fatal("the status bar says the records are not decoded")
	}
	press(m, "D")
	if q := fk.session.queries; q[len(q)-1].Raw {
		t.Fatal("D again decodes")
	}
}

func TestKafkaRawKeyWithoutRegistry(t *testing.T) {
	m, fk := newKafkaModel(t)
	selectRepo(t, m, "payment-service")
	press(m, "M", "enter")
	before := len(fk.session.queries)
	press(m, "D")
	if len(fk.session.queries) != before || !strings.Contains(m.flashText, "no schema_registry") {
		t.Fatalf("D without a registry: %d reads, flash %q", len(fk.session.queries)-before, m.flashText)
	}
}
