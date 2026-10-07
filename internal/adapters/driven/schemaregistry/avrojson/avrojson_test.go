package avrojson

import (
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/hamba/avro/v2"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// everySchema uses every Avro type and logical type.
const everySchema = `{
  "type": "record", "name": "Order", "namespace": "com.example",
  "fields": [
    {"name": "id", "type": "string"},
    {"name": "quantity", "type": "int"},
    {"name": "total", "type": "long"},
    {"name": "price", "type": "float"},
    {"name": "ratio", "type": "double"},
    {"name": "paid", "type": "boolean"},
    {"name": "blob", "type": "bytes"},
    {"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["NEW", "PAID", "SHIPPED"]}},
    {"name": "tags", "type": {"type": "array", "items": "string"}},
    {"name": "counts", "type": {"type": "map", "values": "long"}},
    {"name": "note", "type": ["null", "string"]},
    {"name": "address", "type": {"type": "record", "name": "Address", "fields": [
      {"name": "city", "type": "string"},
      {"name": "zip", "type": ["null", "int"]}
    ]}},
    {"name": "billing", "type": ["null", "Address"]},
    {"name": "amount", "type": {"type": "bytes", "logicalType": "decimal", "precision": 10, "scale": 2}},
    {"name": "fee", "type": {"type": "fixed", "name": "Fee", "size": 4, "logicalType": "decimal", "precision": 6, "scale": 3}},
    {"name": "at", "type": {"type": "long", "logicalType": "timestamp-millis"}},
    {"name": "atMicros", "type": {"type": "long", "logicalType": "timestamp-micros"}},
    {"name": "localAt", "type": {"type": "long", "logicalType": "local-timestamp-millis"}},
    {"name": "day", "type": {"type": "int", "logicalType": "date"}},
    {"name": "timeOfDay", "type": {"type": "int", "logicalType": "time-millis"}},
    {"name": "timeMicros", "type": {"type": "long", "logicalType": "time-micros"}},
    {"name": "uid", "type": {"type": "string", "logicalType": "uuid"}},
    {"name": "wait", "type": {"type": "fixed", "name": "Wait", "size": 12, "logicalType": "duration"}},
    {"name": "hash", "type": {"type": "fixed", "name": "Hash", "size": 4}}
  ]
}`

var at = time.Date(2026, 10, 6, 19, 14, 2, 113000000, time.UTC)

func everyValue() map[string]any {
	return map[string]any{
		"id": "ord-1 \"quoted\"\n", "quantity": 3, "total": int64(-9007199254740993), "price": float32(1.5), "ratio": 0.25,
		"paid": true, "blob": []byte{0, 1, 0xfe}, "status": "PAID",
		"tags": []any{"a", "b"}, "counts": map[string]any{"x": int64(1)},
		"note":    "gift",
		"address": map[string]any{"city": "Lyon", "zip": nil},
		"billing": map[string]any{"com.example.Address": map[string]any{"city": "Paris", "zip": map[string]any{"int": 75001}}},
		"amount":  big.NewRat(-125, 10), "fee": big.NewRat(1, 2),
		"at": at, "atMicros": at.Add(456 * time.Microsecond), "localAt": at,
		"day": at.Truncate(24 * time.Hour), "timeOfDay": 19*time.Hour + 14*time.Minute + 2113*time.Millisecond,
		"timeMicros": 1*time.Hour + 5*time.Microsecond,
		"uid":        "8f91a2d4-1c2b-4e5f-9a0b-7fd28c90e24a",
		"wait":       avro.LogicalDuration{Months: 1, Days: 2, Milliseconds: 3},
		"hash":       [4]byte{0xde, 0xad, 0xbe, 0xef},
	}
}

// everyJSON is what everyValue decodes to: Avro's JSON encoding (fields in
// order, unions wrapped, null plain) with readable logical types.
//
// localAt is built from at.In(time.Local), not a hardcoded UTC string:
// hamba/avro's own local-timestamp Encode (used by encode(), below) converts
// through time.Local when writing the test's input bytes, same as a real
// local-timestamp producer would through its own host's zone. The decoder
// under test (avrojson.go) is already zone-independent -- it only reports
// whatever wall-clock reading the bytes encode, via UTC as a neutral
// calendar -- so hardcoding a UTC string here made the test depend on
// which zone happened to run it, not on the decoder's own correctness.
func everyJSON() string {
	local := at.In(time.Local).Format("2006-01-02T15:04:05.000")
	return `{"id":"ord-1 \"quoted\"\n","quantity":3,"total":-9007199254740993,"price":1.5,"ratio":0.25,` +
		`"paid":true,"blob":"0x0001fe","status":"PAID","tags":["a","b"],"counts":{"x":1},` +
		`"note":{"string":"gift"},"address":{"city":"Lyon","zip":null},` +
		`"billing":{"com.example.Address":{"city":"Paris","zip":{"int":75001}}},` +
		`"amount":"-12.50","fee":"0.500",` +
		`"at":"2026-10-06T19:14:02.113Z","atMicros":"2026-10-06T19:14:02.113456Z","localAt":"` + local + `",` +
		`"day":"2026-10-06","timeOfDay":"19:14:02.113","timeMicros":"01:00:00.000005",` +
		`"uid":"8f91a2d4-1c2b-4e5f-9a0b-7fd28c90e24a","wait":{"months":1,"days":2,"millis":3},"hash":"0xdeadbeef"}`
}

func encode(t testing.TB, schema string, v any) []byte {
	t.Helper()
	b, err := avro.Marshal(avro.MustParse(schema), v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestDecodeEveryType(t *testing.T) {
	c, err := NewCodec(everySchema)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "com.example.Order" {
		t.Fatalf("name %q", c.Name())
	}
	got, err := c.Decode(nil, encode(t, everySchema, everyValue()))
	if err != nil {
		t.Fatal(err)
	}
	want := everyJSON()
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if !json.Valid(got) {
		t.Fatal("not valid JSON")
	}
}

// Schema Registry references: the referenced schema is parsed first and
// its types used by name.
func TestDecodeWithReferences(t *testing.T) {
	address := `{"type":"record","name":"Address","namespace":"com.example","fields":[{"name":"city","type":"string"}]}`
	order := `{"type":"record","name":"Order","namespace":"com.example","fields":[{"name":"to","type":"com.example.Address"}]}`
	c, err := NewCodec(order, address)
	if err != nil {
		t.Fatal(err)
	}
	full := `{"type":"record","name":"Order","namespace":"com.example","fields":[{"name":"to","type":` + address + `}]}`
	got, err := c.Decode(nil, encode(t, full, map[string]any{"to": map[string]any{"city": "Lyon"}}))
	if err != nil || string(got) != `{"to":{"city":"Lyon"}}` {
		t.Fatalf("%s, %v", got, err)
	}
	if _, err := NewCodec(order); err == nil {
		t.Fatal("a missing reference must fail to parse")
	}
}

func TestDecodeEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		value        any
		want         string
	}{
		{"top-level primitive", `"string"`, "x", `"x"`},
		{"invalid utf-8 and controls", `"string"`, "a\xffb\x01", `"a�b\u0001"`},
		{"empty array", `{"type":"array","items":"int"}`, []any{}, `[]`},
		{"union of named types", `["null",{"type":"enum","name":"E","symbols":["A"]},"long"]`, map[string]any{"long": int64(5)}, `{"long":5}`},
		{
			"recursive record", `{"type":"record","name":"Node","fields":[{"name":"next","type":["null","Node"]}]}`,
			map[string]any{"next": map[string]any{"Node": map[string]any{"next": nil}}},
			`{"next":{"Node":{"next":null}}}`,
		},
		{"decimal zero and small", `{"type":"bytes","logicalType":"decimal","precision":4,"scale":3}`, big.NewRat(-1, 1000), `"-0.001"`},
		{"date before 1970", `{"type":"int","logicalType":"date"}`, time.Date(1969, 12, 31, 0, 0, 0, 0, time.UTC), `"1969-12-31"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewCodec(tc.schema)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.Decode(nil, encode(t, tc.schema, tc.value))
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

// Malformed and hostile data fails with ErrInvalidPayload, quickly and
// without large allocations.
func TestDecodeInvalid(t *testing.T) {
	c, err := NewCodec(everySchema)
	if err != nil {
		t.Fatal(err)
	}
	good := encode(t, everySchema, everyValue())
	arrays, err := NewCodec(`{"type":"array","items":"null"}`)
	if err != nil {
		t.Fatal(err)
	}
	deep, err := NewCodec(`{"type":"record","name":"N","fields":[{"name":"n","type":["null","N"]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	enum, err := NewCodec(`{"type":"enum","name":"E","symbols":["A"]}`)
	if err != nil {
		t.Fatal(err)
	}
	boolean, err := NewCodec(`"boolean"`)
	if err != nil {
		t.Fatal(err)
	}
	var nested []byte
	for range 100 {
		nested = append(nested, 2) // branch 1: another N
	}
	for _, tc := range []struct {
		name string
		c    *Codec
		data []byte
		want string
	}{
		{"empty", c, nil, "data ends early"},
		{"truncated", c, good[:len(good)/2], "data ends early"},
		{"huge length", c, []byte{0xfe, 0xff, 0xff, 0xff, 0x0f}, "data ends early"},
		{"one field only", c, []byte{0}, "data ends early"},
		{"enum index out of range", enum, []byte{10}, "enum index 5 out of 1 symbols"},
		{"boolean byte", boolean, []byte{2}, "boolean byte 2"},
		{"union branch out of range", deep, []byte{4}, "union branch 2 out of 2"},
		{"too many null items", arrays, []byte{0xfe, 0xff, 0xff, 0xff, 0x0f}, "too many items"},
		{"too deep", deep, nested, "nested too deep"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.c.Decode(nil, tc.data)
			if !errors.Is(err, domain.ErrInvalidPayload) || !strings.Contains(err.Error(), tc.want) || got != nil {
				t.Fatalf("got %q, %v; want ErrInvalidPayload, %q", got, err, tc.want)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	c, err := NewCodec(everySchema)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encode(f, everySchema, everyValue()))
	f.Add([]byte{})
	f.Add([]byte{0xfe, 0xff, 0xff, 0xff, 0x0f})
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := c.Decode(nil, data)
		if err == nil && !json.Valid(got) {
			t.Fatalf("invalid JSON for %x: %s", data, got)
		}
		if err != nil && !errors.Is(err, domain.ErrInvalidPayload) {
			t.Fatalf("error of another kind: %v", err)
		}
	})
}

// BenchmarkDecode decodes a record using every type.
func BenchmarkDecode(b *testing.B) {
	c, err := NewCodec(everySchema)
	if err != nil {
		b.Fatal(err)
	}
	data := encode(b, everySchema, everyValue())
	dst := make([]byte, 0, 1024)
	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	for b.Loop() {
		if _, err := c.Decode(dst[:0], data); err != nil {
			b.Fatal(err)
		}
	}
}
