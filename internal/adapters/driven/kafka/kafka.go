// Package kafka implements ports.TopicSourceFactory with franz-go, strictly
// read only (docs/DECISIONS.md D-040): partitions are assigned by hand,
// with no consumer group, so no rebalance and no offset commit can happen;
// every connection goes through a guard that refuses any request that is
// not a read (guard.go). It is the only package importing franz-go.
package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Options are the technical settings of every connection.
type Options struct {
	ClientID               string
	ConnectTimeout         time.Duration // dial, TLS handshake and SASL
	RequestTimeout         time.Duration
	FetchMaxBytes          int32
	PartitionFetchMaxBytes int32
	// MaxValueBytes cuts keys, values and header values (0: whole). The
	// bytes kept are copied, so a record never holds on to the buffer of
	// the batch it came in.
	MaxValueBytes int
	// IdleEnd bounds each poll. During the history, a partition that
	// already delivered records and stays silent for two polls in a row
	// is read up to its end (a partition ending with a transaction marker
	// has no record at its last offset); one that delivered nothing yet
	// waits ConnectTimeout + RequestTimeout, so a slow link never ends the
	// history early. Default 2s.
	IdleEnd time.Duration
	// DialContext replaces the TCP dialer (tests). Default net.Dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	Log         *slog.Logger
}

// Factory opens topic sources.
type Factory struct{ Options Options }

var _ ports.TopicSourceFactory = (*Factory)(nil)

// Open connects to the cluster and checks it is reachable with these
// credentials.
func (f *Factory) Open(ctx context.Context, conn domain.KafkaConnection) (ports.TopicSource, error) {
	s := &source{opts: f.Options, conn: conn, violation: &Violation{}}
	if s.opts.ConnectTimeout <= 0 {
		s.opts.ConnectTimeout = 10 * time.Second
	}
	if s.opts.RequestTimeout <= 0 {
		s.opts.RequestTimeout = 30 * time.Second
	}
	if s.opts.IdleEnd <= 0 {
		s.opts.IdleEnd = 2 * time.Second
	}
	base, err := s.baseOpts()
	if err != nil {
		return nil, err
	}
	s.base = base
	admin, err := kgo.NewClient(base...)
	if err != nil {
		return nil, fmt.Errorf("kafka client: %v: %w", err, domain.ErrConfig)
	}
	pctx, cancel := context.WithTimeout(ctx, s.opts.ConnectTimeout+s.opts.RequestTimeout)
	defer cancel()
	if err := admin.Ping(pctx); err != nil {
		admin.Close()
		return nil, s.classify(err)
	}
	s.admin = admin
	s.debug("kafka connected", "brokers", conn.Bootstrap, "security", conn.Security, "mechanism", conn.Mechanism, "truststore_certs", len(conn.CACerts))
	return s, nil
}

// debug logs to the diagnostic log; credentials are never passed to it.
func (s *source) debug(msg string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log.Debug(msg, args...)
	}
}

type source struct {
	opts      Options
	conn      domain.KafkaConnection
	base      []kgo.Opt
	admin     *kgo.Client
	violation *Violation
}

// baseOpts are the options of every client of this source: brokers,
// guarded dialer (TLS inside it), SASL, timeouts, and what makes the
// client read only.
func (s *source) baseOpts() ([]kgo.Opt, error) {
	var tlsCfg *tls.Config
	if s.conn.TLS() {
		tlsCfg = &tls.Config{MinVersion: tls.VersionTLS12}
		if len(s.conn.CACerts) > 0 {
			pool := x509.NewCertPool()
			for _, der := range s.conn.CACerts {
				c, err := x509.ParseCertificate(der)
				if err != nil {
					return nil, fmt.Errorf("truststore: %v: %w", err, domain.ErrConfig)
				}
				pool.AddCert(c)
			}
			tlsCfg.RootCAs = pool
		}
	}
	opts := []kgo.Opt{
		kgo.SeedBrokers(s.conn.Bootstrap...),
		kgo.Dialer(s.dial(tlsCfg)),
		kgo.ClientID(clientID(s.opts.ClientID)),
		kgo.RequestTimeoutOverhead(s.opts.RequestTimeout),
		kgo.RetryTimeout(s.opts.RequestTimeout),
		kgo.MetadataMaxAge(5 * time.Minute),
		kgo.DisableClientMetrics(), // telemetry is a write: never sent
		kgo.WithLogger(nil),
	}
	if s.conn.SASL() {
		user, pass := s.conn.Username.Reveal(), s.conn.Password.Reveal()
		switch s.conn.Mechanism {
		case "plain":
			opts = append(opts, kgo.SASL(plain.Auth{User: user, Pass: pass}.AsMechanism()))
		case "scram-sha-256":
			opts = append(opts, kgo.SASL(scram.Auth{User: user, Pass: pass}.AsSha256Mechanism()))
		case "scram-sha-512":
			opts = append(opts, kgo.SASL(scram.Auth{User: user, Pass: pass}.AsSha512Mechanism()))
		default:
			return nil, fmt.Errorf("SASL mechanism %q is not supported: %w", s.conn.Mechanism, domain.ErrConfig)
		}
	}
	return opts, nil
}

func clientID(id string) string {
	if id == "" {
		return "huginn"
	}
	return id
}

// dial connects to a broker: TCP, then TLS when asked, then the guard
// above TLS, where requests are still readable.
func (s *source) dial(tlsCfg *tls.Config) func(ctx context.Context, network, host string) (net.Conn, error) {
	return func(ctx context.Context, network, host string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, s.opts.ConnectTimeout)
		defer cancel()
		dial := s.opts.DialContext
		if dial == nil {
			dial = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext
		}
		c, err := dial(ctx, network, host)
		if err != nil {
			return nil, err
		}
		if tlsCfg != nil {
			cfg := tlsCfg.Clone()
			if cfg.ServerName == "" {
				cfg.ServerName, _, _ = net.SplitHostPort(host)
			}
			tc := tls.Client(c, cfg)
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = c.Close()
				return nil, err
			}
			c = tc
		}
		return newGuard(c, s.violation), nil
	}
}

// Close implements ports.TopicSource.
func (s *source) Close() { s.admin.Close() }

// Describe implements ports.TopicSource with one Metadata request that
// never creates a topic.
func (s *source) Describe(ctx context.Context, topics []string) ([]ports.TopicInfo, error) {
	req := kmsg.NewPtrMetadataRequest()
	req.AllowAutoTopicCreation = false
	for _, t := range topics {
		rt := kmsg.NewMetadataRequestTopic()
		rt.Topic = kmsg.StringPtr(t)
		req.Topics = append(req.Topics, rt)
	}
	resp, err := req.RequestWith(ctx, s.admin)
	if err != nil {
		return nil, s.classify(err)
	}
	byName := map[string]kmsg.MetadataResponseTopic{}
	for _, t := range resp.Topics {
		if t.Topic != nil {
			byName[*t.Topic] = t
		}
	}
	out := make([]ports.TopicInfo, len(topics))
	for i, name := range topics {
		out[i].Name = name
		t, ok := byName[name]
		switch {
		case !ok:
			out[i].Err = fmt.Errorf("topic %s: %w", name, domain.ErrNotFound)
		case t.ErrorCode != 0:
			out[i].Err = topicError(name, t.ErrorCode)
		default:
			out[i].Partitions = len(t.Partitions)
		}
	}
	return out, nil
}

// partitionRange is where the history of a partition starts and ends.
type partitionRange struct{ start, end int64 }

// offsets lists the partitions of a topic with the range to read: from
// the tail or the window's start, bounded by Limit, up to the end offset
// at this moment (the last stable offset with read_committed).
func (s *source) offsets(ctx context.Context, q ports.TopicRead) (map[int32]partitionRange, error) {
	infos, err := s.Describe(ctx, []string{q.Topic})
	if err != nil {
		return nil, err
	}
	if infos[0].Err != nil {
		return nil, infos[0].Err
	}
	var parts []int32
	for p := range int32(infos[0].Partitions) {
		if len(q.Partitions) == 0 || slices.Contains(q.Partitions, p) {
			parts = append(parts, p)
		}
	}
	list := func(ts int64) (map[int32]int64, error) {
		req := kmsg.NewPtrListOffsetsRequest()
		req.ReplicaID = -1
		if q.ReadCommitted {
			req.IsolationLevel = 1
		}
		rt := kmsg.NewListOffsetsRequestTopic()
		rt.Topic = q.Topic
		for _, p := range parts {
			rp := kmsg.NewListOffsetsRequestTopicPartition()
			rp.Partition, rp.Timestamp, rp.CurrentLeaderEpoch = p, ts, -1
			rt.Partitions = append(rt.Partitions, rp)
		}
		req.Topics = []kmsg.ListOffsetsRequestTopic{rt}
		out := map[int32]int64{}
		for _, shard := range s.admin.RequestSharded(ctx, req) {
			if shard.Err != nil {
				return nil, s.classify(shard.Err)
			}
			for _, t := range shard.Resp.(*kmsg.ListOffsetsResponse).Topics {
				for _, p := range t.Partitions {
					if p.ErrorCode != 0 {
						return nil, topicError(q.Topic, p.ErrorCode)
					}
					out[p.Partition] = p.Offset
				}
			}
		}
		return out, nil
	}
	ends, err := list(-1) // latest
	if err != nil {
		return nil, err
	}
	earliest, err := list(-2)
	if err != nil {
		return nil, err
	}
	var since map[int32]int64
	if q.Since > 0 {
		if since, err = list(time.Now().Add(-q.Since).UnixMilli()); err != nil {
			return nil, err
		}
	}
	out := map[int32]partitionRange{}
	for _, p := range parts {
		end := ends[p]
		start := max(end-int64(q.Tail), earliest[p])
		if q.Since > 0 {
			start = since[p]
			if start < 0 { // no record since then
				start = end
			}
		}
		if q.Limit > 0 {
			start = max(start, end-int64(q.Limit))
		}
		out[p] = partitionRange{start: max(start, earliest[p]), end: end}
	}
	return out, nil
}

// Read implements ports.TopicSource: a client of its own consumes the
// partitions directly (no group), from the computed offsets.
func (s *source) Read(ctx context.Context, q ports.TopicRead) (<-chan ports.RecordBatch, error) {
	if err := s.violation.Err(); err != nil {
		return nil, err
	}
	ranges, err := s.offsets(ctx, q)
	if err != nil {
		return nil, err
	}
	pending := map[int32]int64{} // partitions whose history is not read yet → end offset
	assign := map[int32]kgo.Offset{}
	for p, r := range ranges {
		if r.start < r.end {
			pending[p] = r.end
		}
		if r.start < r.end || q.Follow {
			assign[p] = kgo.NewOffset().At(r.start)
		}
	}
	out := make(chan ports.RecordBatch, 4)
	if len(assign) == 0 { // nothing to read and not following
		out <- ports.RecordBatch{HistoryDone: true}
		close(out)
		return out, nil
	}
	isolation := kgo.ReadUncommitted()
	if q.ReadCommitted {
		isolation = kgo.ReadCommitted()
	}
	opts := append(slices.Clone(s.base),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{q.Topic: assign}),
		kgo.FetchIsolationLevel(isolation),
		kgo.FetchMaxWait(500*time.Millisecond),
	)
	if s.opts.FetchMaxBytes > 0 {
		opts = append(opts, kgo.FetchMaxBytes(s.opts.FetchMaxBytes))
	}
	if s.opts.PartitionFetchMaxBytes > 0 {
		opts = append(opts, kgo.FetchMaxPartitionBytes(s.opts.PartitionFetchMaxBytes))
	}
	h := &health{}
	cl, err := kgo.NewClient(append(opts, kgo.WithHooks(h))...)
	if err != nil {
		return nil, fmt.Errorf("kafka client: %v: %w", err, domain.ErrConfig)
	}
	s.debug("kafka read", "topic", q.Topic, "partitions", len(assign), "history_partitions", len(pending), "follow", q.Follow, "read_committed", q.ReadCommitted)
	go s.poll(ctx, cl, h, q, pending, out)
	return out, nil
}

// poll delivers fetched records until the history is read (then stops,
// unless following) or ctx ends.
func (s *source) poll(ctx context.Context, cl *kgo.Client, h *health, q ports.TopicRead, pending map[int32]int64, out chan<- ports.RecordBatch) {
	defer close(out)
	defer cl.Close()
	historyDone := len(pending) == 0
	send := func(b ports.RecordBatch) bool {
		select {
		case out <- b:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if historyDone && !send(ports.RecordBatch{HistoryDone: true}) {
		return
	}
	started := time.Now()
	seen := map[int32]bool{} // partitions that delivered records
	idlePolls := 0
	for {
		// Polls are bounded so a lost connection is reported while
		// following, and the end of the history found while reading it.
		pctx, cancel := context.WithTimeout(ctx, s.opts.IdleEnd)
		fetches := cl.PollFetches(pctx)
		failing, changed := h.take()
		idle := pctx.Err() != nil && ctx.Err() == nil && !failing
		cancel()
		if idle {
			idlePolls++
		} else {
			idlePolls = 0
		}
		if ctx.Err() != nil {
			return
		}
		if err := s.violation.Err(); err != nil {
			if s.opts.Log != nil {
				s.opts.Log.Error("kafka guard refused a request", "err", err)
			}
			send(ports.RecordBatch{Err: err})
			return
		}
		if err := s.fetchError(fetches); err != nil {
			send(ports.RecordBatch{Err: err})
			return
		}
		var recs []domain.KafkaRecord
		fetches.EachRecord(func(r *kgo.Record) {
			recs = append(recs, toDomain(r, s.opts.MaxValueBytes))
			seen[r.Partition] = true
			if end, ok := pending[r.Partition]; ok && r.Offset+1 >= end {
				delete(pending, r.Partition)
			}
		})
		if idlePolls >= 2 {
			patient := time.Since(started) > s.opts.ConnectTimeout+s.opts.RequestTimeout
			for p := range pending {
				if seen[p] || patient {
					delete(pending, p)
				}
			}
		}
		b := ports.RecordBatch{Records: recs}
		if changed {
			b.Notices = []string{map[bool]string{true: "brokers unreachable, retrying…", false: "reconnected"}[failing]}
		}
		if !historyDone && len(pending) == 0 {
			historyDone, b.HistoryDone = true, true
		}
		if (len(b.Records) > 0 || b.HistoryDone || len(b.Notices) > 0) && !send(b) {
			return
		}
		if historyDone && !q.Follow {
			return
		}
	}
}

// fetchError returns the first error of a poll that stops the read;
// errors the client retries by itself are not returned to the caller.
func (s *source) fetchError(f kgo.Fetches) error {
	var first error
	f.EachError(func(topic string, _ int32, err error) {
		if first == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			first = s.classify(err)
		}
	})
	return first
}

func toDomain(r *kgo.Record, limit int) domain.KafkaRecord {
	d := domain.KafkaRecord{
		Topic: r.Topic, Partition: r.Partition, Offset: r.Offset,
		Key: own(r.Key, limit), Value: own(r.Value, limit), KeySize: len(r.Key), ValueSize: len(r.Value),
		LogAppendTime: r.Attrs.TimestampType() == 1,
	}
	if !r.Timestamp.IsZero() && r.Timestamp.UnixMilli() >= 0 {
		d.Time = r.Timestamp
	}
	for _, h := range r.Headers {
		d.Headers = append(d.Headers, domain.KafkaHeader{Key: h.Key, Value: own(h.Value, limit)})
	}
	return d
}

// own copies at most limit bytes of b (all of it when limit <= 0),
// keeping nil as nil: franz-go's slices point into the decompressed batch,
// which one kept record would otherwise keep alive whole.
func own(b []byte, limit int) []byte {
	if b == nil {
		return nil
	}
	if limit > 0 && len(b) > limit {
		b = b[:limit]
	}
	return append(make([]byte, 0, len(b)), b...)
}

// health follows the connections of a read through franz-go's connect
// hook, broker by broker: the brokers are unreachable when every broker
// tried lately failed (the client keeps retrying); one stale seed among
// reachable brokers is not reported.
type health struct {
	mu      sync.Mutex
	nodes   map[int32]bool // node → its last connect failed
	failing bool
	changed bool
}

// OnBrokerConnect implements kgo.HookBrokerConnect.
func (h *health) OnBrokerConnect(meta kgo.BrokerMetadata, _ time.Duration, _ net.Conn, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.nodes == nil {
		h.nodes = map[int32]bool{}
	}
	h.nodes[meta.NodeID] = err != nil
	// Seed connections (negative ids) are used once at start: once real
	// brokers are known, only they count.
	real := false
	for id := range h.nodes {
		real = real || id >= 0
	}
	failing := true
	for id, f := range h.nodes {
		if id >= 0 || !real {
			failing = failing && f
		}
	}
	if failing != h.failing {
		h.failing, h.changed = failing, true
	}
}

// take returns the state and whether it changed since the last call.
func (h *health) take() (failing, changed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	changed, h.changed = h.changed, false
	return h.failing, changed
}
