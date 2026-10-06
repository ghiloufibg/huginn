package portstest

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// SchemaFixture is a schema the registry under test must serve under its
// id: Type is the registry's schemaType (AVRO, JSON or PROTOBUF), Schema
// its text.
type SchemaFixture struct {
	ID     uint32
	Type   string
	Schema string
}

// ContractSchemas are the schemas the contract suite reads.
var ContractSchemas = []SchemaFixture{
	{ID: 7, Type: "AVRO", Schema: `{"type":"record","name":"OrderCreated","namespace":"com.example","fields":[` +
		`{"name":"orderId","type":"string"},{"name":"quantity","type":"long"},{"name":"note","type":["null","string"],"default":null}]}`},
	{ID: 8, Type: "JSON", Schema: `{"title":"Shipment","type":"object","properties":{"id":{"type":"string"}}}`},
	{ID: 9, Type: "PROTOBUF", Schema: `syntax = "proto3"; message Ping { string id = 1; }`},
}

// Framed returns payload in the Schema Registry framing for schema id.
func Framed(id uint32, payload []byte) []byte {
	b := make([]byte, domain.FramedHeader, domain.FramedHeader+len(payload))
	binary.BigEndian.PutUint32(b[1:], id)
	return append(b, payload...)
}

// avroOrder encodes an OrderCreated of ContractSchemas by hand (Avro
// binary: zigzag varints, length-prefixed strings, union branch index).
func avroOrder(orderID string, quantity int64, note *string) []byte {
	var b []byte
	long := func(n int64) { b = binary.AppendUvarint(b, uint64((n<<1)^(n>>63))) }
	str := func(s string) { long(int64(len(s))); b = append(b, s...) }
	str(orderID)
	long(quantity)
	if note == nil {
		long(0)
	} else {
		long(1)
		str(*note)
	}
	return b
}

// RunSchemaDecoderContract checks the behavior every SchemaDecoder must
// have. newDecoder returns a decoder reading a registry that serves
// ContractSchemas, and nothing else.
func RunSchemaDecoderContract(t *testing.T, newDecoder func(t *testing.T) ports.SchemaDecoder) {
	t.Helper()
	ctx := context.Background()
	note := "gift"
	jsonEqual := func(t *testing.T, got []byte, want string) {
		t.Helper()
		var g, w any
		if err := json.Unmarshal(got, &g); err != nil {
			t.Fatalf("not JSON: %q (%v)", got, err)
		}
		if err := json.Unmarshal([]byte(want), &w); err != nil {
			t.Fatal(err)
		}
		gb, _ := json.Marshal(g)
		wb, _ := json.Marshal(w)
		if string(gb) != string(wb) {
			t.Fatalf("got %s, want %s", got, want)
		}
	}

	t.Run("avro", func(t *testing.T) {
		d := newDecoder(t)
		got, ref, err := d.Decode(ctx, Framed(7, avroOrder("ord-1", 3, &note)))
		if err != nil {
			t.Fatal(err)
		}
		if ref != (domain.SchemaRef{ID: 7, Format: domain.SchemaAvro, Name: "com.example.OrderCreated"}) {
			t.Fatalf("ref %+v", ref)
		}
		jsonEqual(t, got, `{"orderId":"ord-1","quantity":3,"note":{"string":"gift"}}`)
		got, _, err = d.Decode(ctx, Framed(7, avroOrder("ord-2", -1, nil)))
		if err != nil {
			t.Fatal(err)
		}
		jsonEqual(t, got, `{"orderId":"ord-2","quantity":-1,"note":null}`)
	})

	t.Run("json schema", func(t *testing.T) {
		got, ref, err := newDecoder(t).Decode(ctx, Framed(8, []byte(`{"id":"s-1"}`)))
		if err != nil {
			t.Fatal(err)
		}
		if ref != (domain.SchemaRef{ID: 8, Format: domain.SchemaJSON, Name: "Shipment"}) {
			t.Fatalf("ref %+v", ref)
		}
		jsonEqual(t, got, `{"id":"s-1"}`)
	})

	t.Run("failures", func(t *testing.T) {
		d := newDecoder(t)
		for _, tc := range []struct {
			name   string
			framed []byte
			kind   error
			ref    domain.SchemaRef
		}{
			{"unknown id", Framed(404, []byte{1}), domain.ErrNotFound, domain.SchemaRef{ID: 404}},
			{"protobuf", Framed(9, []byte{0}), domain.ErrNotImplemented, domain.SchemaRef{ID: 9, Format: domain.SchemaProtobuf}},
			{"not framed", []byte(`{"id":1}`), domain.ErrInvalidPayload, domain.SchemaRef{}},
			{"truncated avro", Framed(7, avroOrder("ord-1", 3, &note)[:4]), domain.ErrInvalidPayload, domain.SchemaRef{ID: 7, Format: domain.SchemaAvro, Name: "com.example.OrderCreated"}},
			{"huge length prefix", Framed(7, binary.AppendUvarint(nil, 1<<40)), domain.ErrInvalidPayload, domain.SchemaRef{ID: 7, Format: domain.SchemaAvro, Name: "com.example.OrderCreated"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, ref, err := d.Decode(ctx, tc.framed)
				if !errors.Is(err, tc.kind) || got != nil {
					t.Fatalf("Decode = %q, %v; want an error of kind %v", got, err, tc.kind)
				}
				if ref.ID != tc.ref.ID || ref.Format != tc.ref.Format || (tc.ref.Name != "" && ref.Name != tc.ref.Name) {
					t.Fatalf("ref %+v, want %+v", ref, tc.ref)
				}
			})
		}
	})

	t.Run("concurrent", func(t *testing.T) {
		d := newDecoder(t)
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		for range 64 {
			wg.Go(func() {
				if _, _, err := d.Decode(ctx, Framed(7, avroOrder("ord-1", 3, nil))); err != nil {
					errs <- err
				}
			})
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
	})
}

// NewContractSchemaRegistry returns a fake registry serving
// ContractSchemas, for the contract suite and the use cases' tests.
func NewContractSchemaRegistry() *FakeSchemaRegistry {
	note := "gift"
	return &FakeSchemaRegistry{Schemas: map[uint32]FakeSchema{
		7: {Format: domain.SchemaAvro, Name: "com.example.OrderCreated", Payloads: map[string]string{
			string(avroOrder("ord-1", 3, &note)): `{"orderId":"ord-1","quantity":3,"note":{"string":"gift"}}`,
			string(avroOrder("ord-2", -1, nil)):  `{"orderId":"ord-2","quantity":-1,"note":null}`,
			string(avroOrder("ord-1", 3, nil)):   `{"orderId":"ord-1","quantity":3,"note":null}`,
		}},
		8: {Format: domain.SchemaJSON, Name: "Shipment"},
		9: {Format: domain.SchemaProtobuf, Name: "Ping"},
	}}
}
