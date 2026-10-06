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
	"cmp"
	"context"
	"crypto/sha256"
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
	DefaultTimeout         = 10 * time.Second
	DefaultMaxSchemas      = 1000
	DefaultFailureTTL      = 30 * time.Second
	DefaultMaxDecodedBytes = 16 << 20 // JSON of one value
	maxResponseBytes       = 1 << 20  // one schema, as the registry returns it
	maxReferenceDepth      = 10
	maxRegistries          = 16 // registries (URL and credentials) kept by a Factory
	// maxFailedIDs is how many schema ids may fail (unknown, invalid)
	// within FailureTTL before the registry is no longer asked for new
	// ones until then: bytes that only look framed (a binary value
	// starting with 0) would otherwise cost one request per record.
	maxFailedIDs       = 32
	maxScratch         = 1 << 20 // decoding buffers kept for reuse up to this size
	maxDownUnreachable = 10 * time.Second
	retryAfter         = 200 * time.Millisecond // one retry of a transient failure
	mediaTypeRegistry  = "application/vnd.schemaregistry.v1+json, application/json"
	schemaTypeAvro     = "AVRO"
	schemaTypeJSON     = "JSON"
	schemaTypeProtobuf = "PROTOBUF"
)

// Factory opens decoders. Its zero value is ready to use; it must not be
// copied once used. Decoders are shared: every session reading the same
// registry with the same credentials gets the same one, with its cache and
// connections, so reopening a Kafka screen fetches no schema again.
type Factory struct {
	// MaxSchemas bounds the schemas kept per registry (DefaultMaxSchemas).
	MaxSchemas int
	// FailureTTL is how long a failed schema read is remembered
	// (DefaultFailureTTL).
	FailureTTL time.Duration
	// MaxDecodedBytes bounds the JSON of one decoded value
	// (DefaultMaxDecodedBytes); a larger one is reported, not decoded.
	MaxDecodedBytes int
	// Now is the clock (time.Now).
	Now func() time.Time
	// Transport replaces the HTTP transport, for tests and the demo; it is
	// still wrapped by the read-only guard.
	Transport http.RoundTripper

	mu         sync.Mutex
	registries map[[sha256.Size]byte]*decoder
}

var _ ports.SchemaDecoderFactory = (*Factory)(nil)

// Open implements ports.SchemaDecoderFactory. Nothing is requested until a
// record needs a schema.
func (f *Factory) Open(_ context.Context, conn domain.SchemaRegistryConn) (ports.SchemaDecoder, error) {
	base, err := url.Parse(strings.TrimRight(conn.URL, "/"))
	if err != nil || (base.Scheme != "http" && base.Scheme != "https") || base.Host == "" {
		return nil, fmt.Errorf("schema registry url %q is not an http or https URL: %w", conn.URL, domain.ErrConfig)
	}
	key := registryKey(conn)
	f.mu.Lock()
	defer f.mu.Unlock()
	if d, ok := f.registries[key]; ok {
		return d, nil
	}
	transport := f.Transport
	if transport == nil {
		t := http.DefaultTransport.(*http.Transport).Clone()
		// A registry answers schema reads: a few connections are plenty,
		// and idle ones are closed soon.
		t.MaxConnsPerHost, t.MaxIdleConnsPerHost, t.IdleConnTimeout = 8, 2, 30*time.Second
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
		maxSchemas: cmp.Or(f.MaxSchemas, DefaultMaxSchemas), failureTTL: cmp.Or(f.FailureTTL, DefaultFailureTTL),
		maxDecoded: cmp.Or(f.MaxDecodedBytes, DefaultMaxDecodedBytes), now: f.Now,
		pending: map[uint32]*entry{}, refs: map[string]registrySchema{},
	}
	if d.now == nil {
		d.now = time.Now
	}
	if f.registries == nil || len(f.registries) >= maxRegistries {
		f.registries = map[[sha256.Size]byte]*decoder{} // sessions keep theirs
	}
	f.registries[key] = d
	return d, nil
}

// registryKey identifies a registry and the credentials it is read with;
// secrets enter it hashed only.
func registryKey(c domain.SchemaRegistryConn) [sha256.Size]byte {
	h := sha256.New()
	for _, s := range []string{c.URL, c.Username.Reveal(), c.Password.Reveal(), c.Token.Reveal(), c.Timeout.String()} {
		fmt.Fprintf(h, "%d:%s", len(s), s)
	}
	for _, der := range c.CACerts {
		fmt.Fprintf(h, "%d:", len(der))
		h.Write(der)
	}
	var k [sha256.Size]byte
	h.Sum(k[:0])
	return k
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

// entry is a schema id being fetched (until ready closes) or that failed
// (remembered until expires).
type entry struct {
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
	maxDecoded int
	now        func() time.Time

	// schemas holds the schemas read (uint32 → schema), read without a
	// lock: decoding from every core does not contend on a cache hit.
	schemas sync.Map
	mu      sync.Mutex
	order   []uint32                  // ids of schemas, oldest first, for eviction
	pending map[uint32]*entry         // being fetched, or failed
	refs    map[string]registrySchema // referenced schemas by subject/version
	// down, until downUntil, fails every fetch at once: the registry is
	// unreachable or refuses the credentials, or too many ids failed.
	down      error
	downUntil time.Time
}

// scratch holds decoding buffers: a value is decoded into one, then copied
// at its exact size, so records hold no slack.
var scratch = sync.Pool{New: func() any { b := make([]byte, 0, 4096); return &b }}

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
		buf := scratch.Get().(*[]byte)
		out, err := s.avro.DecodeLimit((*buf)[:0], payload, d.maxDecoded)
		if err != nil {
			scratch.Put(buf)
			return nil, ref, fmt.Errorf("schema %d: %w", id, err)
		}
		exact := bytes.Clone(out)
		if cap(out) <= maxScratch {
			*buf = out[:0]
			scratch.Put(buf)
		}
		return exact, ref, nil
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

// schema returns the schema of id: cached (no lock), being fetched by
// another caller (it waits, or gives up with its context), failed lately
// (the same error, at once), or fetched now.
func (d *decoder) schema(ctx context.Context, id uint32) (schema, error) {
	if s, ok := d.schemas.Load(id); ok {
		return s.(schema), nil
	}
	d.mu.Lock()
	now := d.now()
	if d.down != nil && now.Before(d.downUntil) {
		err := d.down
		d.mu.Unlock()
		return schema{}, err
	}
	if e, ok := d.pending[id]; ok {
		select {
		case <-e.ready:
			if now.Before(e.expires) {
				d.mu.Unlock()
				return schema{}, e.err
			}
			delete(d.pending, id) // a failure that expired: try again
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
	if s, ok := d.schemas.Load(id); ok { // stored while we took the lock
		d.mu.Unlock()
		return s.(schema), nil
	}
	if failed := d.failed(now); failed >= maxFailedIDs {
		d.down = fmt.Errorf("%d schema ids could not be read within %s; the registry is asked again later (are these records written by Schema Registry serializers?): %w",
			failed, d.failureTTL, domain.ErrNotFound)
		d.downUntil = now.Add(d.failureTTL)
		err := d.down
		d.mu.Unlock()
		return schema{}, err
	}
	e := &entry{ready: make(chan struct{})}
	d.pending[id] = e
	d.mu.Unlock()

	// Fetched without the caller's context: others may wait for it, and
	// the client's timeout bounds it.
	e.schema, e.err = d.fetch(context.WithoutCancel(ctx), id)

	d.mu.Lock()
	defer d.mu.Unlock()
	if e.err == nil {
		d.schemas.Store(id, e.schema)
		delete(d.pending, id)
		d.order = append(d.order, id)
		for len(d.order) > d.maxSchemas {
			d.schemas.Delete(d.order[0])
			d.order = d.order[1:]
		}
	} else {
		e.expires = d.now().Add(d.failureTTL)
		if registryWide(e.err) {
			// Every other id would fail the same way: say so at once
			// instead of waiting for a timeout per id. An unreachable
			// registry is tried again sooner than refused credentials.
			until := e.expires
			if errors.Is(e.err, domain.ErrUnreachable) {
				until = d.now().Add(min(d.failureTTL, maxDownUnreachable))
			}
			d.down, d.downUntil = e.err, until
		}
	}
	close(e.ready)
	return e.schema, e.err
}

// failed counts the ids whose failure is remembered, forgetting expired
// ones. d.mu is held.
func (d *decoder) failed(now time.Time) int {
	n := 0
	for id, e := range d.pending {
		select {
		case <-e.ready:
			if !now.Before(e.expires) {
				delete(d.pending, id)
				continue
			}
			n++
		default:
		}
	}
	return n
}

// registryWide reports failures of the registry itself rather than of one
// schema id.
func registryWide(err error) bool {
	return errors.Is(err, domain.ErrUnreachable) || errors.Is(err, domain.ErrUnauthorized) || errors.Is(err, domain.ErrForbidden)
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
		return schema{format: domain.SchemaJSON, name: cmp.Or(doc.Title, doc.ID)}, nil
	case schemaTypeProtobuf:
		return schema{format: domain.SchemaProtobuf}, nil
	}
	return schema{}, fmt.Errorf("schema %d has an unknown type %q: %w", id, rs.SchemaType, domain.ErrNotImplemented)
}

// references returns the schemas rs references, recursively, each after
// the schemas it references itself, as avrojson.NewCodec takes them. A
// subject version is read once per registry: schemas sharing a type do not
// fetch it again.
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
		sub, err := d.reference(ctx, r.Subject, r.Version)
		if err != nil {
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

// reference reads a subject version, cached: a version never changes.
func (d *decoder) reference(ctx context.Context, subject string, version int) (registrySchema, error) {
	key := subject + "/" + strconv.Itoa(version)
	d.mu.Lock()
	rs, ok := d.refs[key]
	d.mu.Unlock()
	if ok {
		return rs, nil // never modified once stored
	}
	path := "/subjects/" + url.PathEscape(subject) + "/versions/" + strconv.Itoa(version)
	if err := d.get(ctx, path, &rs); err != nil {
		return rs, err
	}
	d.mu.Lock()
	if len(d.refs) >= d.maxSchemas {
		clear(d.refs) // rarely many: start over rather than track age
	}
	d.refs[key] = rs
	d.mu.Unlock()
	return rs, nil
}

// get reads one registry resource into v, mapping failures to domain
// kinds. The registry's own message is kept: it says what is wrong. A
// transient failure (the network, 502, 503, 504) is tried once more.
func (d *decoder) get(ctx context.Context, path string, v any) error {
	err := d.getOnce(ctx, path, v)
	var t transient
	if errors.As(err, &t) {
		select {
		case <-time.After(retryAfter):
			err = d.getOnce(ctx, path, v)
		case <-ctx.Done():
		}
	}
	return err
}

// transient marks a failure worth one more try.
type transient struct{ error }

func (t transient) Unwrap() error { return t.error }

func (d *decoder) getOnce(ctx context.Context, path string, v any) error {
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
		return transient{fmt.Errorf("registry %s: %v: %w", d.base.Host, err, domain.ErrUnreachable)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return transient{fmt.Errorf("registry %s: %v: %w", d.base.Host, err, domain.ErrUnreachable)}
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return transient{statusError(resp.StatusCode, body)}
	default:
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
	msg := cmp.Or(e.Message, http.StatusText(status))
	if r := []rune(msg); len(r) > 200 { // a message, not a page
		msg = string(r[:200]) + "…"
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
