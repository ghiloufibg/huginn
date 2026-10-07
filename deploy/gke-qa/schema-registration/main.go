// One-off QA tool (M14-gke-qa.md §3.4): registers an Avro schema and a JSON
// Schema with a real Schema Registry (Karapace), then produces real
// Confluent-wire-format records onto the "catalog" repo's existing Kafka
// topics -- reusing the exact libraries Huginn's own Schema Registry
// decoder uses (hamba/avro, franz-go), so what's produced is guaranteed to
// be real wire format, not a hand-rolled approximation. Run once; not a
// standing service.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/hamba/avro/v2"
	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	registryURL = "http://localhost:8081"
	kafkaBroker = "localhost:9092"
)

var orderSchema = `{
  "type": "record",
  "name": "Order",
  "namespace": "com.huginnqa.catalog",
  "fields": [
    {"name": "id", "type": "string"},
    {"name": "total", "type": {"type": "bytes", "logicalType": "decimal", "precision": 10, "scale": 2}},
    {"name": "placedAt", "type": {"type": "long", "logicalType": "timestamp-millis"}}
  ]
}`

var catalogJSONSchema = `{
  "$id": "catalog-event",
  "title": "catalog-event",
  "type": "object",
  "properties": {
    "sku": {"type": "string"},
    "inStock": {"type": "boolean"}
  },
  "required": ["sku", "inStock"]
}`

func registerSchema(subject, schema, schemaType string) (int, error) {
	body := map[string]string{"schema": schema}
	if schemaType != "" {
		body["schemaType"] = schemaType
	}
	b, _ := json.Marshal(body)
	resp, err := http.Post(fmt.Sprintf("%s/subjects/%s/versions", registryURL, subject), "application/vnd.schemaregistry.v1+json", bytes.NewReader(b))
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return 0, fmt.Errorf("register %s: %s: %s", subject, resp.Status, data)
	}
	var out struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return 0, err
	}
	return out.ID, nil
}

func frame(schemaID int, payload []byte) []byte {
	buf := make([]byte, 5+len(payload))
	buf[0] = 0
	binary.BigEndian.PutUint32(buf[1:5], uint32(schemaID))
	copy(buf[5:], payload)
	return buf
}

func main() {
	avroID, err := registerSchema("topic-a-value", orderSchema, "")
	if err != nil {
		log.Fatalf("register avro schema: %v", err)
	}
	fmt.Printf("registered Avro schema, id=%d\n", avroID)

	jsonID, err := registerSchema("topic-c-value", catalogJSONSchema, "JSON")
	if err != nil {
		log.Fatalf("register json schema: %v", err)
	}
	fmt.Printf("registered JSON schema, id=%d\n", jsonID)

	codec, err := avro.Parse(orderSchema)
	if err != nil {
		log.Fatalf("parse avro schema: %v", err)
	}

	cl, err := kgo.NewClient(kgo.SeedBrokers(kafkaBroker))
	if err != nil {
		log.Fatalf("new kafka client: %v", err)
	}
	defer cl.Close()
	ctx := context.Background()

	// 1-2. A few real Avro-framed records with a logical-typed field
	// (decimal total, timestamp-millis placedAt), on topic-a.
	type orderRec struct {
		ID       string `avro:"id"`
		Total    []byte `avro:"total"`
		PlacedAt int64  `avro:"placedAt"`
	}
	for i := 0; i < 3; i++ {
		rec := orderRec{
			ID:       fmt.Sprintf("order-%d", i+1),
			Total:    bigDecimalBytes(int64(1999 + i*100)), // e.g. 19.99, scale 2
			PlacedAt: time.Now().Add(-time.Duration(i) * time.Hour).UnixMilli(),
		}
		payload, err := avro.Marshal(codec, rec)
		if err != nil {
			log.Fatalf("avro marshal: %v", err)
		}
		produce(ctx, cl, "topic-a", nil, frame(avroID, payload))
	}
	fmt.Println("produced 3 Avro-framed records on topic-a")

	// 3. A record with a bogus/non-existent schema id -- the "registry
	// names it but cannot use it" error path (D-067).
	produce(ctx, cl, "topic-a", nil, frame(999999, []byte("irrelevant")))
	fmt.Println("produced 1 record with a bogus schema id (999999) on topic-a")

	// 4. A record with a plain big-endian numeric key (no framing) --
	// confirms the default decode:[value] leaves keys alone (D-068).
	key := make([]byte, 8)
	binary.BigEndian.PutUint64(key, 42)
	produce(ctx, cl, "topic-a", key, []byte(`{"note":"plain numeric key, unframed value"}`))
	fmt.Println("produced 1 record with a plain big-endian numeric key on topic-a")

	// 5. JSON-Schema-framed records on topic-c (catalog's "list" topic).
	for i := 0; i < 2; i++ {
		payload, _ := json.Marshal(map[string]any{"sku": fmt.Sprintf("SKU-%03d", i+1), "inStock": i%2 == 0})
		produce(ctx, cl, "topic-c", nil, frame(jsonID, payload))
	}
	fmt.Println("produced 2 JSON-Schema-framed records on topic-c")

	if err := cl.Flush(ctx); err != nil {
		log.Fatalf("flush: %v", err)
	}
	fmt.Println("done")
}

// bigDecimalBytes encodes a scale-2 decimal as Avro's bytes logical type
// (two's-complement big-endian), e.g. 1999 -> 19.99.
func bigDecimalBytes(unscaled int64) []byte {
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, uint64(unscaled))
	// Trim leading zero bytes but keep at least one, and keep the sign byte
	// correct for two's complement -- unscaled is always positive here, so
	// a single trim-to-minimal-bytes pass is safe.
	i := 0
	for i < len(buf)-1 && buf[i] == 0 {
		i++
	}
	return buf[i:]
}

func produce(ctx context.Context, cl *kgo.Client, topic string, key, value []byte) {
	rec := &kgo.Record{Topic: topic, Key: key, Value: value}
	if err := cl.ProduceSync(ctx, rec).FirstErr(); err != nil {
		log.Fatalf("produce to %s: %v", topic, err)
	}
}
