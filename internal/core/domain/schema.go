package domain

import (
	"encoding/binary"
	"time"
)

// Schema formats of the Schema Registry (its schemaType, lower-cased).
const (
	SchemaAvro     = "avro"
	SchemaJSON     = "json"
	SchemaProtobuf = "protobuf"
)

// SchemaRef says how a record key or value was decoded from the Schema
// Registry's framing (FramedSchemaID), or why it could not be. A zero
// SchemaRef means the bytes were not framed, or not decoded.
type SchemaRef struct {
	// ID is the schema id the bytes carry; 0 when not framed.
	ID uint32
	// Format is SchemaAvro, SchemaJSON or SchemaProtobuf; empty when the
	// schema could not be read.
	Format string
	// Name is the schema's full name (Avro) or title (JSON Schema).
	Name string
	// Err says why the bytes were not decoded; empty when they were.
	Err string
}

// Decoded reports whether the bytes were decoded into JSON.
func (r SchemaRef) Decoded() bool { return r.ID != 0 && r.Err == "" && r.Format != "" }

// FramedHeader is the size of the Schema Registry framing: a 0 magic byte
// and a 4-byte big-endian schema id.
const FramedHeader = 5

// FramedSchemaID returns the schema id of bytes in the Schema Registry
// framing, and false for other bytes. Registries number schemas from 1,
// so bytes starting with five zeros (a small big-endian number) are not
// framed.
func FramedSchemaID(b []byte) (uint32, bool) {
	if len(b) < FramedHeader || b[0] != 0 {
		return 0, false
	}
	id := binary.BigEndian.Uint32(b[1:FramedHeader])
	return id, id != 0
}

// SchemaRegistryConn is everything needed to read a Schema Registry,
// resolved from a Kafka profile. Credentials stay Secret until the
// adapter. Huginn only reads the registry.
type SchemaRegistryConn struct {
	// URL is the registry's base URL (http or https).
	URL string
	// Username and Password are basic authentication; Token a bearer
	// token. At most one of the two is set.
	Username, Password Secret
	Token              Secret
	// CACerts are DER certificates trusted for the registry; empty means
	// the system roots.
	CACerts [][]byte
	// Timeout bounds one request.
	Timeout time.Duration
}
