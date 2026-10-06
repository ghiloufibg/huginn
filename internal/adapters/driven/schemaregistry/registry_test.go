package schemaregistry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// registry is a fake Schema Registry over HTTP: schemas by id, subject
// versions for references, a hit counter per path, and optional checks.
type registry struct {
	mu       sync.Mutex
	ids      map[uint32]any // response body by id
	subjects map[string]any // by "subject/version"
	hits     map[string]int
	status   int           // when set, every answer has this status
	delay    time.Duration // before answering
	auth     func(*http.Request) bool
	methods  []string
}

func newRegistry() *registry {
	r := &registry{ids: map[uint32]any{}, subjects: map[string]any{}, hits: map[string]int{}}
	for _, s := range portstest.ContractSchemas {
		r.ids[s.ID] = map[string]any{"schema": s.Schema, "schemaType": s.Type}
	}
	return r
}

func (r *registry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.hits[req.URL.Path]++
	r.methods = append(r.methods, req.Method)
	status, delay, auth := r.status, r.delay, r.auth
	r.mu.Unlock()
	time.Sleep(delay)
	if auth != nil && !auth(req) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error_code":401,"message":"Unauthorized"}`)
		return
	}
	if status != 0 {
		w.WriteHeader(status)
		fmt.Fprintf(w, `{"error_code":%d,"message":"forced"}`, status)
		return
	}
	var body any
	var ok bool
	if req.URL.Path == "/schemas/types" {
		body, ok = []string{"JSON", "PROTOBUF", "AVRO"}, true
	} else if id, found := strings.CutPrefix(req.URL.Path, "/schemas/ids/"); found {
		var n uint32
		if _, err := fmt.Sscan(id, &n); err == nil {
			body, ok = r.ids[n]
		}
	} else if rest, found := strings.CutPrefix(req.URL.Path, "/subjects/"); found {
		subject, version, _ := strings.Cut(rest, "/versions/")
		body, ok = r.subjects[subject+"/"+version]
	}
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error_code":40403,"message":"Schema not found"}`)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.schemaregistry.v1+json")
	_ = json.NewEncoder(w).Encode(body)
}

func (r *registry) hitsOf(path string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits[path]
}

func open(t *testing.T, f *Factory, conn domain.SchemaRegistryConn) ports.SchemaDecoder {
	t.Helper()
	d, err := f.Open(context.Background(), conn)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func serve(t *testing.T, r *registry) string {
	t.Helper()
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestContract(t *testing.T) {
	portstest.RunSchemaDecoderContract(t, func(t *testing.T) ports.SchemaDecoder {
		return open(t, &Factory{}, domain.SchemaRegistryConn{URL: serve(t, newRegistry())})
	})
}

var order = portstest.Framed(7, []byte{10, 'o', 'r', 'd', '-', '1', 6, 0}) // ord-1, 3, null

// One request per schema id, whatever the records and the callers.
func TestSchemaFetchedOnce(t *testing.T) {
	r := newRegistry()
	r.delay = 20 * time.Millisecond
	d := open(t, &Factory{}, domain.SchemaRegistryConn{URL: serve(t, r)})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			if _, _, err := d.Decode(context.Background(), order); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if n := r.hitsOf("/schemas/ids/7"); n != 1 {
		t.Fatalf("%d requests for schema 7, want 1", n)
	}
}

// A failure is remembered for FailureTTL, then tried again.
func TestFailuresRemembered(t *testing.T) {
	r := newRegistry()
	now := time.Now()
	f := &Factory{Now: func() time.Time { return now }, FailureTTL: time.Minute}
	d := open(t, f, domain.SchemaRegistryConn{URL: serve(t, r)})
	unknown := portstest.Framed(404, []byte{0})
	for range 10 {
		if _, _, err := d.Decode(context.Background(), unknown); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err %v", err)
		}
	}
	if n := r.hitsOf("/schemas/ids/404"); n != 1 {
		t.Fatalf("%d requests during the failure TTL, want 1", n)
	}
	now = now.Add(2 * time.Minute)
	if _, _, err := d.Decode(context.Background(), unknown); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err %v", err)
	}
	if n := r.hitsOf("/schemas/ids/404"); n != 2 {
		t.Fatalf("%d requests after the TTL, want 2", n)
	}
}

func TestStatusesMapToKinds(t *testing.T) {
	for status, kind := range map[int]error{
		http.StatusUnauthorized: domain.ErrUnauthorized, http.StatusForbidden: domain.ErrForbidden,
		http.StatusInternalServerError: domain.ErrUnreachable, http.StatusBadRequest: domain.ErrInvalidPayload,
	} {
		r := newRegistry()
		r.status = status
		_, ref, err := open(t, &Factory{}, domain.SchemaRegistryConn{URL: serve(t, r)}).Decode(context.Background(), order)
		if !errors.Is(err, kind) || ref.ID != 7 || !strings.Contains(err.Error(), "forced") {
			t.Errorf("status %d: %v (ref %+v), want %v with the registry's message", status, err, ref, kind)
		}
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close() // nothing listens there now
	_, _, err := open(t, &Factory{}, domain.SchemaRegistryConn{URL: srv.URL, Timeout: time.Second}).Decode(context.Background(), order)
	if !errors.Is(err, domain.ErrUnreachable) || strings.Contains(err.Error(), "\n") {
		t.Fatalf("unreachable: %q", err)
	}
}

func TestAuthentication(t *testing.T) {
	r := newRegistry()
	r.auth = func(req *http.Request) bool { u, p, ok := req.BasicAuth(); return ok && u == "me" && p == "pw" }
	url := serve(t, r)
	conn := domain.SchemaRegistryConn{URL: url, Username: domain.NewSecret("me"), Password: domain.NewSecret("pw")}
	if _, _, err := open(t, &Factory{}, conn).Decode(context.Background(), order); err != nil {
		t.Fatalf("basic: %v", err)
	}
	conn.Password = domain.NewSecret("wrong")
	if _, _, err := open(t, &Factory{}, conn).Decode(context.Background(), order); !errors.Is(err, domain.ErrUnauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	r.auth = func(req *http.Request) bool { return req.Header.Get("Authorization") == "Bearer tok" }
	if _, _, err := open(t, &Factory{}, domain.SchemaRegistryConn{URL: url, Token: domain.NewSecret("tok")}).Decode(context.Background(), order); err != nil {
		t.Fatalf("bearer: %v", err)
	}
}

func TestTLSWithTrustedCA(t *testing.T) {
	srv := httptest.NewUnstartedServer(newRegistry())
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the untrusted handshake below is expected
	srv.StartTLS()
	t.Cleanup(srv.Close)
	conn := domain.SchemaRegistryConn{URL: srv.URL, CACerts: [][]byte{srv.Certificate().Raw}}
	if _, _, err := open(t, &Factory{}, conn).Decode(context.Background(), order); err != nil {
		t.Fatalf("trusted CA: %v", err)
	}
	conn.CACerts = nil // the system roots do not trust the test server
	if _, _, err := open(t, &Factory{}, conn).Decode(context.Background(), order); !errors.Is(err, domain.ErrUnreachable) {
		t.Fatalf("untrusted: %v", err)
	}
}

// The registry is only read: the transport refuses anything but GET.
func TestReadOnly(t *testing.T) {
	r := newRegistry()
	url := serve(t, r)
	f := &Factory{}
	if _, _, err := open(t, f, domain.SchemaRegistryConn{URL: url}).Decode(context.Background(), order); err != nil {
		t.Fatal(err)
	}
	guard := readOnly{http.DefaultTransport}
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req, _ := http.NewRequest(m, url+"/subjects/x/versions", strings.NewReader("{}"))
		if _, err := guard.RoundTrip(req); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: %v", m, err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.methods {
		if m != http.MethodGet {
			t.Fatalf("the registry received %s", m)
		}
	}
}

// References are read recursively, each before the schemas using it.
func TestReferences(t *testing.T) {
	r := newRegistry()
	r.ids[20] = map[string]any{
		"schema":     `{"type":"record","name":"Order","namespace":"com.example","fields":[{"name":"to","type":"com.example.Address"}]}`,
		"references": []map[string]any{{"name": "com.example.Address", "subject": "address", "version": 2}},
	}
	r.subjects["address/2"] = map[string]any{
		"schema":     `{"type":"record","name":"Address","namespace":"com.example","fields":[{"name":"city","type":"com.example.City"}]}`,
		"references": []map[string]any{{"name": "com.example.City", "subject": "city", "version": 1}},
	}
	r.subjects["city/1"] = map[string]any{"schema": `{"type":"record","name":"City","namespace":"com.example","fields":[{"name":"name","type":"string"}]}`}
	d := open(t, &Factory{}, domain.SchemaRegistryConn{URL: serve(t, r)})
	got, ref, err := d.Decode(context.Background(), portstest.Framed(20, []byte{8, 'L', 'y', 'o', 'n'}))
	if err != nil || string(got) != `{"to":{"city":{"name":"Lyon"}}}` || ref.Name != "com.example.Order" {
		t.Fatalf("%s %+v %v", got, ref, err)
	}
	delete(r.subjects, "city/1")
	r.ids[21] = r.ids[20]
	if _, _, err := d.Decode(context.Background(), portstest.Framed(21, []byte{0})); !errors.Is(err, domain.ErrNotFound) || !strings.Contains(err.Error(), "city version 1") {
		t.Fatalf("missing reference: %v", err)
	}
}

// The cache keeps at most MaxSchemas schemas.
func TestCacheBounded(t *testing.T) {
	r := newRegistry()
	for id := uint32(100); id < 110; id++ {
		r.ids[id] = map[string]any{"schema": `"string"`}
	}
	d := open(t, &Factory{MaxSchemas: 4}, domain.SchemaRegistryConn{URL: serve(t, r)}).(*decoder)
	for id := uint32(100); id < 110; id++ {
		if _, _, err := d.Decode(context.Background(), portstest.Framed(id, []byte{0})); err != nil {
			t.Fatal(err)
		}
	}
	if d.order.Len() != 4 || len(d.entries) != 4 {
		t.Fatalf("%d schemas cached, want 4", d.order.Len())
	}
	if _, _, err := d.Decode(context.Background(), portstest.Framed(109, []byte{0})); err != nil {
		t.Fatal(err)
	}
	if n := r.hitsOf("/schemas/ids/109"); n != 1 {
		t.Fatalf("a recent schema was fetched again (%d)", n)
	}
}

func TestOpenChecksTheURL(t *testing.T) {
	for _, u := range []string{"", "schema-registry:8081", "ftp://x"} {
		if _, err := (&Factory{}).Open(context.Background(), domain.SchemaRegistryConn{URL: u}); !errors.Is(err, domain.ErrConfig) {
			t.Errorf("%q: %v", u, err)
		}
	}
}

// A slow registry does not hold callers whose context ends.
func TestWaitersFollowTheirContext(t *testing.T) {
	r := newRegistry()
	r.delay = 300 * time.Millisecond
	d := open(t, &Factory{}, domain.SchemaRegistryConn{URL: serve(t, r)})
	go func() { _, _, _ = d.Decode(context.Background(), order) }() // fetching
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, _, err := d.Decode(ctx, order); !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("waited %v: %v", time.Since(start), err)
	}
}

// BenchmarkDecodeCached decodes framed records whose schemas are cached
// (every record after the first of its schema), from 20 schemas.
func BenchmarkDecodeCached(b *testing.B) {
	r := newRegistry()
	recs := make([][]byte, 20)
	for i := range recs {
		id := uint32(1000 + i)
		r.ids[id] = r.ids[7]
		recs[i] = portstest.Framed(id, []byte{10, 'o', 'r', 'd', '-', '1', 6, 2, 8, 'g', 'i', 'f', 't'})
	}
	srv := httptest.NewServer(r)
	defer srv.Close()
	d, err := (&Factory{}).Open(context.Background(), domain.SchemaRegistryConn{URL: srv.URL})
	if err != nil {
		b.Fatal(err)
	}
	for _, rec := range recs {
		if _, _, err := d.Decode(context.Background(), rec); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		if _, _, err := d.Decode(context.Background(), recs[i%len(recs)]); err != nil {
			b.Fatal(err)
		}
		i++
	}
}
