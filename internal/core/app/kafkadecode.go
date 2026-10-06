package app

import (
	"context"
	"fmt"
	"runtime"
	"sync"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// schemaDecoding decodes the framed keys and values of records with a
// Schema Registry (docs/plan/M13-schema-registry.md). Its zero value
// decodes nothing.
type schemaDecoding struct {
	dec        ports.SchemaDecoder
	key, value bool
}

// decodeParallel is the number of records from which a batch is decoded
// by several goroutines.
const decodeParallel = 256

// decodeAll decodes recs in place, then cuts what was decoded to limit
// bytes as the source cut the original. Records whose bytes cannot be
// decoded keep them and say why in their SchemaRef; each distinct reason
// becomes one notice.
func (d schemaDecoding) decodeAll(ctx context.Context, recs []domain.KafkaRecord, limit int, notices *decodeNotices) {
	if d.dec == nil || len(recs) == 0 {
		return
	}
	workers := 1
	if len(recs) >= decodeParallel {
		workers = min(runtime.GOMAXPROCS(0), len(recs)/decodeParallel+1)
	}
	chunk := (len(recs) + workers - 1) / workers
	var wg sync.WaitGroup
	for lo := 0; lo < len(recs); lo += chunk {
		part := recs[lo:min(lo+chunk, len(recs))]
		wg.Go(func() {
			for i := range part {
				d.decode(ctx, &part[i], limit, notices)
			}
		})
	}
	wg.Wait()
}

func (d schemaDecoding) decode(ctx context.Context, r *domain.KafkaRecord, limit int, notices *decodeNotices) {
	if d.key {
		r.Key, r.KeySize, r.KeySchema = d.field(ctx, r.Key, r.KeySize, notices)
	}
	if d.value {
		r.Value, r.ValueSize, r.ValueSchema = d.field(ctx, r.Value, r.ValueSize, notices)
	}
	domain.TruncateRecord(r, limit)
}

// field decodes one framed key or value; other bytes are returned as is.
func (d schemaDecoding) field(ctx context.Context, b []byte, size int, notices *decodeNotices) ([]byte, int, domain.SchemaRef) {
	id, ok := domain.FramedSchemaID(b)
	if !ok {
		return b, size, domain.SchemaRef{}
	}
	if len(b) < size {
		return b, size, domain.SchemaRef{ID: id, Err: "not decoded: cut at kafka.max_value_bytes"}
	}
	out, ref, err := d.dec.Decode(ctx, b)
	if err != nil {
		ref.ID, ref.Err = id, err.Error()
		notices.add(fmt.Sprintf("schema registry: %v", err))
		return b, size, ref
	}
	return out, len(out), ref
}

// decodeNotices collects distinct decoding failures, safe for concurrent
// use. At most maxDecodeNotices are reported per read: one per registry
// problem, not one per record.
type decodeNotices struct {
	mu      sync.Mutex
	seen    map[string]bool
	pending []string
}

const maxDecodeNotices = 5

func (n *decodeNotices) add(msg string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen[msg] || len(n.seen) >= maxDecodeNotices {
		return
	}
	if n.seen == nil {
		n.seen = map[string]bool{}
	}
	n.seen[msg] = true
	n.pending = append(n.pending, msg)
}

// take returns the notices not reported yet.
func (n *decodeNotices) take() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := n.pending
	n.pending = nil
	return out
}
