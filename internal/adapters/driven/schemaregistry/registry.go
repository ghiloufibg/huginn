// Package schemaregistry decodes records written by Schema Registry
// serializers (Confluent framing), as the Java deserializers do: the
// writer schema is read from the registry by id, then the payload is
// decoded into JSON (Avro by avrojson; JSON Schema payloads are JSON
// already). It implements ports.SchemaDecoderFactory.
//
// The registry is only ever read: the HTTP transport refuses any method
// but GET, so no schema can be registered or deleted (D-057). Schemas are
// cached by id (ids never change), fetched once however many records wait
// for them, and failures are remembered for a while so a wrong password or
// an unknown id does not send one request per record.
package schemaregistry

import (
	"bytes"
	"container/list"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driven/schemaregistry/avrojson"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Defaults of a Factory.
const (
	DefaultTimeout     = 10 * time.Second
	DefaultMaxSchemas  = 1000
	DefaultFailureTTL  = 30 * time.Second
	maxResponseBytes   = 1 << 20 // one schema, as the registry returns it
	maxReferenceDepth  = 10
	mediaTypeRegistry  = "application/vnd.schemaregistry.v1+json, application/json"
	schemaTypeAvro     = "AVRO"
	schemaTypeJSON     = "JSON"
	schemaTypeProtobuf = "PROTOBUF"
)

// Factory opens decoders. Its zero value is ready to use.
type Factory struct {
	// MaxSchemas bounds the schemas kept per registry (DefaultMaxSchemas).
	MaxSchemas int
	// FailureTTL is how long a failed schema read is remembered
	// (DefaultFailureTTL).
	FailureTTL time.Duration
	// Now is the clock (time.Now).
	Now func() time.Time
	// Transport replaces the HTTP transport, for tests; it is still
	// wrapped by the read-only guard.
	Transport http.RoundTripper
}

var _ ports.SchemaDecoderFactory = (*Factory)(nil)

// Open implements ports.SchemaDecoderFactory. Nothing is requested until a
// record needs a schema.
func (f *Factory) Open(_ context.Context, conn domain.SchemaRegistryConn) (ports.SchemaDecoder, error) {
	base, err := url.Parse(strings.TrimRight(conn.URL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("schema registry url %q is not an http or https URL: %w", conn.URL, domain.ErrConfig)
	}
	transport := f.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		if len(conn.CACerts) > 0 {
			pool := x509.NewCertPool()
			for _, der := range conn.CACerts {
				cert, err := x509.ParseCertificate(der)
				if err != nil {
					return nil, fmt.Errorf("schema registry certificate: %w: %w", err, domain.ErrConfig)
				}
				pool.AddCert(cert)
			}
			t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		}
		transport = t
	}
	timeout := conn.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	d := &decoder{
		base: base, conn: conn,
		client:     &http.Client{Transport: readOnly{transport}, Timeout: timeout},
		maxSchemas: f.MaxSchemas, failureTTL: f.FailureTTL, now: f.Now,
		entries: map[uint32]*list.Element{}, order: list.New(),
	}
	if d.maxSchemas <= 0 {
		d.maxSchemas = DefaultMaxSchemas
	}
	if d.failureTTL <= 0 {
		d.failureTTL = DefaultFailureTTL
	}
	if d.now == nil {
		d.now = time.Now
	}
	return d, nil
}

// readOnly refuses every request that could change the registry.
type readOnly struct{ next http.RoundTripper }

func (r readOnly) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		return nil, fmt.Errorf("schema registry: %s refused, Huginn only reads the registry: %w", req.Method, domain.ErrForbidden)
	}
	return r.next.RoundTrip(req)
}

// schema is a schema read from the registry, ready to decode with.
type schema struct {
	format string // domain.Schema*
	name   string
	avro   *avrojson.Codec
}

// entry is a schema id in the cache: being fetched until ready closes,
// then a schema or an error (remembered until expires).
type entry struct {
	id      uint32
	ready   chan struct{}
	schema  schema
	err     error
	expires time.Time
}

type decoder struct {
	base       *url.URL
	conn       domain.SchemaRegistryConn
	client     *http.Client
	maxSchemas int
	failureTTL time.Duration
	now        func() time.Time

	mu      sync.Mutex
	entries map[uint32]*list.Element // of *entry
	order   *list.List               // oldest first, for eviction
}

// Decode implements ports.SchemaDecoder.
func (d *decoder) Decode(ctx context.Context, framed []byte) ([]byte, domain.SchemaRef, error) {
	id, ok := domain.FramedSchemaID(framed)
	if !ok {
		return nil, domain.SchemaRef{}, fmt.Errorf("not in the Schema Registry framing: %w", domain.ErrInvalidPayload)
	}
	ref := domain.SchemaRef{ID: id}
	s, err := d.schema(ctx, id)
	if err != nil {
		return nil, ref, err
	}
	ref.Format, ref.Name = s.format, s.name
	payload := framed[domain.FramedHeader:]
	switch s.format {
	case domain.SchemaAvro:
		out, err := s.avro.Decode(make([]byte, 0, 2*len(payload)+16), payload)
		if err != nil {
			return nil, ref, fmt.Errorf("schema %d: %w", id, err)
		}
		return out, ref, nil
	case domain.SchemaJSON:
		if !json.Valid(payload) {
			return nil, ref, fmt.Errorf("schema %d: payload is not JSON: %w", id, domain.ErrInvalidPayload)
		}
		return bytes.Clone(payload), ref, nil
	}
	return nil, ref, fmt.Errorf("schema %d is %s, not decoded: %w", id, s.format, domain.ErrNotImplemented)
}

// Check implements ports.SchemaDecoder: one GET /schemas/types, which
// every registry answers and which needs the same credentials as schemas.
func (d *decoder) Check(ctx context.Context) error {
	var types []string
	return d.get(ctx, "/schemas/types", &types)
}

// schema returns the schema of id: cached, being fetched by another
// caller (it waits), or fetched now.
func (d *decoder) schema(ctx context.Context, id uint32) (schema, error) {
	d.mu.Lock()
	if el, ok := d.entries[id]; ok {
		e := el.Value.(*entry)
		select {
		case <-e.ready:
			if e.err == nil || d.now().Before(e.expires) {
				d.mu.Unlock()
				return e.schema, e.err
			}
			d.order.Remove(el) // a failure that expired: try again
			delete(d.entries, id)
		default:
			d.mu.Unlock()
			select {
			case <-e.ready:
				return e.schema, e.err
			case <-ctx.Done():
				return schema{}, ctx.Err()
			}
		}
	}
	e := &entry{id: id, ready: make(chan struct{})}
	d.entries[id] = d.order.PushBack(e)
	for d.order.Len() > d.maxSchemas {
		oldest := d.order.Front()
		if old := oldest.Value.(*entry); old != e {
			d.order.Remove(oldest)
			delete(d.entries, old.id)
		}
	}
	d.mu.Unlock()

	// Fetched without the caller's context: others may wait for it, and
	// the client's timeout bounds it.
	e.schema, e.err = d.fetch(context.WithoutCancel(ctx), id)
	d.mu.Lock()
	if e.err != nil {
		e.expires = d.now().Add(d.failureTTL)
	}
	close(e.ready)
	d.mu.Unlock()
	return e.schema, e.err
}

// registrySchema is a schema as the registry returns it.
type registrySchema struct {
	Schema     string `json:"schema"`
	SchemaType string `json:"schemaType"`
	References []struct {
		Name    string `json:"name"`
		Subject string `json:"subject"`
		Version int    `json:"version"`
	} `json:"references"`
}

func (d *decoder) fetch(ctx context.Context, id uint32) (schema, error) {
	var rs registrySchema
	if err := d.get(ctx, "/schemas/ids/"+strconv.FormatUint(uint64(id), 10), &rs); err != nil {
		return schema{}, fmt.Errorf("schema %d: %w", id, err)
	}
	switch strings.ToUpper(rs.SchemaType) {
	case "", schemaTypeAvro:
		refs, err := d.references(ctx, rs, map[string]bool{}, 0)
		if err != nil {
			return schema{}, fmt.Errorf("schema %d: %w", id, err)
		}
		codec, err := avrojson.NewCodec(rs.Schema, refs...)
		if err != nil {
			return schema{}, fmt.Errorf("schema %d is not a valid Avro schema: %w: %w", id, err, domain.ErrInvalidPayload)
		}
		return schema{format: domain.SchemaAvro, name: codec.Name(), avro: codec}, nil
	case schemaTypeJSON:
		var doc struct {
			Title string `json:"title"`
			ID    string `json:"$id"`
		}
		_ = json.Unmarshal([]byte(rs.Schema), &doc) // the name is a nicety
		name := doc.Title
		if name == "" {
			name = doc.ID
		}
		return schema{format: domain.SchemaJSON, name: name}, nil
	case schemaTypeProtobuf:
		return schema{format: domain.SchemaProtobuf}, nil
	}
	return schema{}, fmt.Errorf("schema %d has an unknown type %q: %w", id, rs.SchemaType, domain.ErrNotImplemented)
}

// references returns the schemas rs references, recursively, each after
// the schemas it references itself, as avrojson.NewCodec takes them.
func (d *decoder) references(ctx context.Context, rs registrySchema, seen map[string]bool, depth int) ([]string, error) {
	if depth > maxReferenceDepth {
		return nil, fmt.Errorf("references nested more than %d deep: %w", maxReferenceDepth, domain.ErrInvalidPayload)
	}
	var out []string
	for _, r := range rs.References {
		key := r.Subject + "/" + strconv.Itoa(r.Version)
		if seen[key] {
			continue // already included (shared, or a cycle)
		}
		seen[key] = true
		var sub registrySchema
		path := "/subjects/" + url.PathEscape(r.Subject) + "/versions/" + strconv.Itoa(r.Version)
		if err := d.get(ctx, path, &sub); err != nil {
			return nil, fmt.Errorf("reference %s (%s version %d): %w", r.Name, r.Subject, r.Version, err)
		}
		deeper, err := d.references(ctx, sub, seen, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, deeper...)
		out = append(out, sub.Schema)
	}
	return out, nil
}

// get reads one registry resource into v, mapping failures to domain
// kinds. The registry's own message is kept: it says what is wrong.
func (d *decoder) get(ctx context.Context, path string, v any) error {
	u := *d.base
	u.Path += path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", mediaTypeRegistry)
	switch {
	case !d.conn.Token.IsZero():
		req.Header.Set("Authorization", "Bearer "+d.conn.Token.Reveal())
	case !d.conn.Username.IsZero():
		req.SetBasicAuth(d.conn.Username.Reveal(), d.conn.Password.Reveal())
	}
	resp, err := d.client.Do(req)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			return err
		}
		return fmt.Errorf("registry %s: %v: %w", d.base.Host, err, domain.ErrUnreachable)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("registry %s: %v: %w", d.base.Host, err, domain.ErrUnreachable)
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode, body)
	}
	if len(body) > maxResponseBytes {
		return fmt.Errorf("registry response larger than %d bytes: %w", maxResponseBytes, domain.ErrInvalidPayload)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("registry response is not a schema: %w: %w", err, domain.ErrInvalidPayload)
	}
	return nil
}

// statusError maps a registry status to a domain kind, with the
// registry's message ({"error_code":40403,"message":"Schema not found"}).
func statusError(status int, body []byte) error {
	var e struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(status)
	}
	var kind error
	switch {
	case status == http.StatusUnauthorized:
		kind = domain.ErrUnauthorized
	case status == http.StatusForbidden:
		kind = domain.ErrForbidden
	case status == http.StatusNotFound:
		kind = domain.ErrNotFound
	case status >= 500:
		kind = domain.ErrUnreachable
	default:
		kind = domain.ErrInvalidPayload
	}
	return fmt.Errorf("registry answered %d %s: %w", status, msg, kind)
}
