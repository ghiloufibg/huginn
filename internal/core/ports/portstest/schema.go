package portstest

import (
	"bytes"
	"context"
	"fmt"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// FakeSchema is a schema of the fake registry: its format and name, and
// the JSON each payload decodes to. A payload it does not list is invalid.
type FakeSchema struct {
	Format, Name string
	Payloads     map[string]string // payload bytes → JSON
}

// FakeSchemaRegistry is a ports.SchemaDecoderFactory and SchemaDecoder
// serving fixed schemas by id. Err, when set, fails every decode as an
// unreachable registry would. It counts the decodes.
type FakeSchemaRegistry struct {
	mu      sync.Mutex
	Schemas map[uint32]FakeSchema
	Err     error
	decodes int
}

var (
	_ ports.SchemaDecoderFactory = (*FakeSchemaRegistry)(nil)
	_ ports.SchemaDecoder        = (*FakeSchemaRegistry)(nil)
)

// Open implements ports.SchemaDecoderFactory.
func (f *FakeSchemaRegistry) Open(context.Context, domain.SchemaRegistryConn) (ports.SchemaDecoder, error) {
	return f, nil
}

// Decode implements ports.SchemaDecoder.
func (f *FakeSchemaRegistry) Decode(_ context.Context, framed []byte) ([]byte, domain.SchemaRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.decodes++
	id, ok := domain.FramedSchemaID(framed)
	if !ok {
		return nil, domain.SchemaRef{}, fmt.Errorf("not in the Schema Registry framing: %w", domain.ErrInvalidPayload)
	}
	ref := domain.SchemaRef{ID: id}
	if f.Err != nil {
		return nil, ref, f.Err
	}
	s, ok := f.Schemas[id]
	if !ok {
		return nil, ref, fmt.Errorf("schema %d: %w", id, domain.ErrNotFound)
	}
	ref.Format, ref.Name = s.Format, s.Name
	if s.Format == domain.SchemaProtobuf {
		return nil, ref, fmt.Errorf("schema %d is protobuf: %w", id, domain.ErrNotImplemented)
	}
	payload := framed[domain.FramedHeader:]
	if s.Format == domain.SchemaJSON && s.Payloads == nil {
		return bytes.Clone(payload), ref, nil
	}
	j, ok := s.Payloads[string(payload)]
	if !ok {
		return nil, ref, fmt.Errorf("schema %d: payload does not follow it: %w", id, domain.ErrInvalidPayload)
	}
	return []byte(j), ref, nil
}

// Check implements ports.SchemaDecoder.
func (f *FakeSchemaRegistry) Check(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.Err
}

// Decodes is the number of Decode calls so far.
func (f *FakeSchemaRegistry) Decodes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.decodes
}
