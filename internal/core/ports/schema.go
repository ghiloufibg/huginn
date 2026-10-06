package ports

import (
	"context"

	"github.com/ghiloufibg/huginn/internal/core/domain"
)

// SchemaDecoder decodes record keys and values written by Schema Registry
// serializers (Confluent framing: a 0 magic byte, a 4-byte schema id, the
// payload), as their Java deserializers do: the writer schema is read
// from the registry by id and the payload decoded into JSON. It only ever
// reads the registry. Implementations are safe for concurrent use.
type SchemaDecoder interface {
	// Decode returns the payload as JSON and what it was. The returned
	// SchemaRef carries the id (and the format and name once the schema
	// is known) even with an error. Errors wrap domain kinds:
	// ErrNotFound (unknown schema id), ErrUnauthorized, ErrForbidden,
	// ErrUnreachable, ErrInvalidPayload (bytes not framed, or not
	// following their schema), ErrNotImplemented (a schema type that is
	// not decoded, such as Protobuf).
	Decode(ctx context.Context, framed []byte) ([]byte, domain.SchemaRef, error)
}

// SchemaDecoderFactory reads one Schema Registry.
type SchemaDecoderFactory interface {
	Open(ctx context.Context, conn domain.SchemaRegistryConn) (SchemaDecoder, error)
}
