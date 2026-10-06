package demo

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"
)

// Schema ids of the demo registry; demoSchemaUnknown is never registered,
// so the demo also shows a record that cannot be decoded.
const (
	demoSchemaAvro    = 1
	demoSchemaJSON    = 2
	demoSchemaUnknown = 999
)

// demoAvroSchema exercises readable logical types: an enum, a decimal, a
// timestamp and an optional field.
const demoAvroSchema = `{"type":"record","name":"DemoEvent","namespace":"io.huginn.demo","fields":[` +
	`{"name":"id","type":"string"},` +
	`{"name":"type","type":{"type":"enum","name":"EventType","symbols":["Created","Updated","Validated","Rejected","Completed"]}},` +
	`{"name":"amount","type":{"type":"bytes","logicalType":"decimal","precision":10,"scale":2}},` +
	`{"name":"occurredAt","type":{"type":"long","logicalType":"timestamp-millis"}},` +
	`{"name":"note","type":["null","string"],"default":null}]}`

const demoJSONSchema = `{"$schema":"http://json-schema.org/draft-07/schema#","title":"DemoNotice","type":"object"}`

// RegistryTransport is an http.RoundTripper answering as a Schema Registry
// holding the demo's schemas, so --demo decodes records with the real
// registry client, without a network.
func RegistryTransport() http.RoundTripper { return registry{} }

type registry struct{}

func (registry) RoundTrip(req *http.Request) (*http.Response, error) {
	status, body := http.StatusNotFound, `{"error_code":40403,"message":"Schema not found"}`
	quote := func(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) }
	switch req.URL.Path {
	case "/schemas/types":
		status, body = http.StatusOK, `["JSON","PROTOBUF","AVRO"]`
	case fmt.Sprintf("/schemas/ids/%d", demoSchemaAvro):
		status, body = http.StatusOK, `{"schema":"`+quote(demoAvroSchema)+`"}`
	case fmt.Sprintf("/schemas/ids/%d", demoSchemaJSON):
		status, body = http.StatusOK, `{"schemaType":"JSON","schema":"`+quote(demoJSONSchema)+`"}`
	}
	return &http.Response{
		StatusCode: status, Status: http.StatusText(status), Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header:  http.Header{"Content-Type": {"application/vnd.schemaregistry.v1+json"}},
		Body:    io.NopCloser(strings.NewReader(body)),
		Request: req,
	}, nil
}

// framed puts payload in the Schema Registry framing for schema id.
func framed(id uint32, payload []byte) []byte {
	b := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(b[1:], id)
	return append(b, payload...)
}

// avroEvent encodes a DemoEvent (Avro binary, written by hand: zigzag
// varints, length-prefixed strings, the decimal's big-endian bytes).
func avroEvent(id string, event int, cents int64, at time.Time, note string) []byte {
	var b []byte
	long := func(n int64) { b = binary.AppendUvarint(b, uint64((n<<1)^(n>>63))) }
	str := func(s string) { long(int64(len(s))); b = append(b, s...) }
	str(id)
	long(int64(event))
	unscaled := big.NewInt(cents).Bytes() // positive: no sign byte needed below 2^7
	if len(unscaled) > 0 && unscaled[0]&0x80 != 0 {
		unscaled = append([]byte{0}, unscaled...)
	}
	long(int64(len(unscaled)))
	b = append(b, unscaled...)
	long(at.UnixMilli())
	if note == "" {
		long(0)
	} else {
		long(1)
		str(note)
	}
	return b
}

// jsonNotice is a JSON Schema payload.
func jsonNotice(id, event string, at time.Time) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, `{"id":%q,"notice":%q,"at":%q}`, id, strings.ToLower(event), at.Format(time.RFC3339))
	return b.Bytes()
}
