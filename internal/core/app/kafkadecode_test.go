package app

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// registryFactory records the connection the session resolved.
type registryFactory struct {
	*portstest.FakeSchemaRegistry
	conns []domain.SchemaRegistryConn
	err   error
}

func (f *registryFactory) Open(ctx context.Context, conn domain.SchemaRegistryConn) (ports.SchemaDecoder, error) {
	f.conns = append(f.conns, conn)
	if f.err != nil {
		return nil, f.err
	}
	return f.FakeSchemaRegistry.Open(ctx, conn)
}

// avro is an OrderCreated of the contract schemas: id, quantity 3, null note.
func avro(id string) []byte {
	b := binary.AppendUvarint(nil, uint64(len(id))<<1)
	return append(append(b, id...), 6, 0)
}

func registryFixture(t *testing.T) (*KafkaService, *portstest.FakeKafka, *registryFactory) {
	t.Helper()
	s, files, k := kafkaFixture(t)
	files.Env["/repos/orders/deploy/rec/secrets/kafka.env"]["SR_PASSWORD"] = "srpw"
	reg := &registryFactory{FakeSchemaRegistry: portstest.NewContractSchemaRegistry()}
	reg.Schemas[7].Payloads[string(avro("ord-1"))] = `{"orderId":"ord-1","quantity":3,"note":null}`
	s.Registries = reg
	s.Profiles[0].Registry = KafkaRegistrySpec{URL: "https://sr.example", Username: "orders", Password: "${SR_PASSWORD}", Timeout: "3s"}
	s.MaxValueBytes = 1 << 10
	produce := func(value []byte, at int) {
		k.Produce(domain.KafkaRecord{Topic: "orders.requested", Time: kafkaNow.Add(time.Duration(at) * time.Second), Key: portstest.Framed(8, []byte(`"k1"`)), Value: value})
	}
	produce(portstest.Framed(7, avro("ord-1")), 1)
	produce(portstest.Framed(404, []byte{1}), 2)
	produce([]byte(`{"plain":true}`), 3)
	produce(portstest.Framed(9, []byte{0}), 4)
	produce(portstest.Framed(404, []byte{2}), 5)
	return s, k, reg
}

func readTopic(t *testing.T, s *KafkaService, q ports.KafkaQuery) ([]domain.KafkaRecord, []string) {
	t.Helper()
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	q.Topic, q.Window = "orders.requested", domain.TimeWindow{Tail: 10}
	ch, err := sess.Read(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var recs []domain.KafkaRecord
	var notices []string
	for b := range ch {
		recs = append(recs, b.Records...)
		notices = append(notices, b.Notices...)
	}
	return recs, notices
}

func TestKafkaRecordsDecodedWithTheRegistry(t *testing.T) {
	s, _, reg := registryFixture(t)
	s.Profiles[0].Registry.Decode = []string{"key", "value"}
	recs, notices := readTopic(t, s, ports.KafkaQuery{})
	if len(reg.conns) != 1 {
		t.Fatalf("%d registries opened", len(reg.conns))
	}
	if c := reg.conns[0]; c.URL != "https://sr.example" || c.Username.Reveal() != "orders" || c.Password.Reveal() != "srpw" || c.Timeout != 3*time.Second {
		t.Fatalf("connection %+v", c)
	}
	byValue := func(i int) (string, domain.SchemaRef) { return string(recs[i].Value), recs[i].ValueSchema }
	if v, ref := byValue(0); v != `{"orderId":"ord-1","quantity":3,"note":null}` || !ref.Decoded() || ref.Name != "com.example.OrderCreated" || recs[0].ValueSize != len(v) {
		t.Fatalf("avro value: %s %+v size %d", v, ref, recs[0].ValueSize)
	}
	if string(recs[0].Key) != `"k1"` || recs[0].KeySchema.Format != domain.SchemaJSON {
		t.Fatalf("json schema key: %s %+v", recs[0].Key, recs[0].KeySchema)
	}
	if v, ref := byValue(1); ref.ID != 404 || !strings.Contains(ref.Err, "not found") || v != string(portstest.Framed(404, []byte{1})) {
		t.Fatalf("unknown id keeps its bytes and says why: %q %+v", v, ref)
	}
	if _, ref := byValue(2); ref != (domain.SchemaRef{}) {
		t.Fatalf("plain JSON is left alone: %+v", ref)
	}
	if _, ref := byValue(3); ref.Format != domain.SchemaProtobuf || ref.Err == "" {
		t.Fatalf("protobuf: %+v", ref)
	}
	// One notice per distinct problem (two unknown-id records, one
	// protobuf), not one per record.
	if len(notices) != 2 {
		t.Fatalf("notices %q", notices)
	}
}

func TestKafkaRawReadsLeaveRecordsAsBytes(t *testing.T) {
	s, _, reg := registryFixture(t)
	recs, notices := readTopic(t, s, ports.KafkaQuery{Raw: true})
	if string(recs[0].Value) != string(portstest.Framed(7, avro("ord-1"))) || recs[0].ValueSchema != (domain.SchemaRef{}) || len(notices) != 0 {
		t.Fatalf("raw read decoded: %q %+v %q", recs[0].Value, recs[0].ValueSchema, notices)
	}
	if reg.Decodes() != 0 {
		t.Fatalf("%d decodes for a raw read", reg.Decodes())
	}
}

func TestKafkaDecodeOnlyWhatTheProfileSays(t *testing.T) {
	s, _, _ := registryFixture(t)
	s.Profiles[0].Repos["orders"] = KafkaRepoSpec{Enabled: true, Topics: s.Profiles[0].Repos["orders"].Topics, Registry: KafkaRegistrySpec{Decode: []string{"value"}}}
	recs, _ := readTopic(t, s, ports.KafkaQuery{})
	if !recs[0].ValueSchema.Decoded() || recs[0].KeySchema != (domain.SchemaRef{}) {
		t.Fatalf("decode: [value] decoded the key: %+v", recs[0].KeySchema)
	}
}

// A value cut at max_value_bytes cannot be decoded: it says so.
func TestKafkaTruncatedValuesNotDecoded(t *testing.T) {
	s, k, reg := registryFixture(t)
	s.MaxValueBytes = 8
	k.Produce(domain.KafkaRecord{Topic: "orders.requested", Time: kafkaNow.Add(9 * time.Second), Value: portstest.Framed(7, avro("ord-1"))})
	recs, _ := readTopic(t, s, ports.KafkaQuery{})
	last := recs[len(recs)-1]
	if last.ValueSchema.Err != "not decoded: cut at kafka.max_value_bytes" || reg.Decodes() == 0 {
		t.Fatalf("%+v", last.ValueSchema)
	}
}

// A registry the profile names but that cannot be used leaves records
// undecoded, says why once, and never fails the read.
func TestKafkaRegistryProblemsAreNotices(t *testing.T) {
	s, _, reg := registryFixture(t)
	s.Profiles[0].Registry.Password = "${SR_MISSING}"
	recs, notices := readTopic(t, s, ports.KafkaQuery{})
	if len(recs) != 5 || recs[0].ValueSchema != (domain.SchemaRef{}) || len(reg.conns) != 0 {
		t.Fatalf("records %d, ref %+v", len(recs), recs[0].ValueSchema)
	}
	if len(notices) != 1 || !strings.Contains(notices[0], "schema_registry.basic_auth.password") || strings.Contains(notices[0], "srpw") {
		t.Fatalf("notices %q", notices)
	}

	s, _, reg = registryFixture(t)
	reg.Err = errors.New("unreachable: test")
	recs, notices = readTopic(t, s, ports.KafkaQuery{})
	if recs[0].ValueSchema.Err == "" || len(notices) != 1 {
		t.Fatalf("an unreachable registry: %+v %q", recs[0].ValueSchema, notices)
	}
}

func TestKafkaSessionsWithoutRegistryDoNotDecode(t *testing.T) {
	s, _, reg := registryFixture(t)
	s.Profiles[0].Registry = KafkaRegistrySpec{}
	recs, _ := readTopic(t, s, ports.KafkaQuery{})
	if recs[0].ValueSchema != (domain.SchemaRef{}) || len(reg.conns) != 0 {
		t.Fatalf("decoded without a registry: %+v", recs[0].ValueSchema)
	}
}

// Keys are decoded only when the profile asks: a big-endian number key
// starts with 0 too, and Java decodes keys only with a key deserializer.
func TestKafkaKeysNotDecodedByDefault(t *testing.T) {
	s, _, _ := registryFixture(t)
	recs, _ := readTopic(t, s, ports.KafkaQuery{})
	if !recs[0].ValueSchema.Decoded() || recs[0].KeySchema != (domain.SchemaRef{}) || string(recs[0].Key) != string(portstest.Framed(8, []byte(`"k1"`))) {
		t.Fatalf("value %+v, key %q %+v", recs[0].ValueSchema, recs[0].Key, recs[0].KeySchema)
	}
}

// A decoded value larger than max_value_bytes is cut as the source cuts,
// copied: the record keeps no more than the limit.
func TestKafkaDecodedValuesCutAndCopied(t *testing.T) {
	s, _, reg := registryFixture(t)
	s.MaxValueBytes = 16
	long := `{"orderId":"ord-1","quantity":3,"note":null}`
	reg.Schemas[7].Payloads[string(avro("ord-1"))] = long
	recs, _ := readTopic(t, s, ports.KafkaQuery{})
	r := recs[0]
	if string(r.Value) != long[:16] || r.ValueSize != len(long) || !r.Truncated() || cap(r.Value) > 16+16 || !r.ValueSchema.Decoded() {
		t.Fatalf("value %q (cap %d) size %d %+v", r.Value, cap(r.Value), r.ValueSize, r.ValueSchema)
	}
}
