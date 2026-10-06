package domain

import (
	"strings"
	"time"
)

// TopicDirection says how a service uses a topic, as its Kafka profile
// declares it (docs/CONFIG.md, kafka/).
type TopicDirection int

// Topic directions. A topic declared both consumed and produced is Both;
// one listed without a direction or discovered in the sources is None.
const (
	TopicNone TopicDirection = iota
	TopicConsume
	TopicProduce
	TopicBoth
)

// With returns the direction after another declaration of the same topic.
func (d TopicDirection) With(o TopicDirection) TopicDirection {
	switch {
	case d == o || o == TopicNone:
		return d
	case d == TopicNone:
		return o
	}
	return TopicBoth
}

// KafkaConnection is everything needed to reach a cluster, resolved from a
// profile. Credentials stay Secret until the Kafka adapter.
type KafkaConnection struct {
	Bootstrap []string
	// Security is plaintext, ssl, sasl_plaintext or sasl_ssl.
	Security string
	// Mechanism is plain, scram-sha-256 or scram-sha-512 (SASL only).
	Mechanism string
	Username  Secret
	Password  Secret
	// CACerts are DER certificates trusted for the brokers; empty means
	// the system roots.
	CACerts [][]byte
}

// TLS reports whether the connection is encrypted.
func (c KafkaConnection) TLS() bool { return c.Security == "ssl" || c.Security == "sasl_ssl" }

// SASL reports whether the connection authenticates with SASL.
func (c KafkaConnection) SASL() bool { return strings.HasPrefix(c.Security, "sasl_") }

// KafkaHeader is one record header.
type KafkaHeader struct {
	Key   string
	Value []byte
}

// KafkaRecord is one record read from a topic partition. A nil Key or
// Value means null (a nil Value is a tombstone); KeySize and ValueSize are
// the sizes before truncation.
type KafkaRecord struct {
	// Seq numbers records in the order a view received them (RecordBuffer).
	Seq       uint64
	Topic     string
	Partition int32
	Offset    int64
	// Time is the record timestamp; zero when the producer gave none.
	Time time.Time
	// LogAppendTime: Time was set by the broker, not the producer.
	LogAppendTime bool
	Key, Value    []byte
	KeySize       int
	ValueSize     int
	Headers       []KafkaHeader
}

// Truncated reports whether the key or the value was cut.
func (r *KafkaRecord) Truncated() bool { return len(r.Key) < r.KeySize || len(r.Value) < r.ValueSize }

// Bytes is the memory a record holds, as RecordBuffer counts it.
func (r *KafkaRecord) Bytes() int {
	n := len(r.Key) + len(r.Value) + len(r.Topic) + 64
	for _, h := range r.Headers {
		n += len(h.Key) + len(h.Value) + 16
	}
	return n
}

// TruncateRecord cuts the key and the value to at most limit bytes each,
// keeping their real sizes; limit <= 0 keeps them whole.
func TruncateRecord(r *KafkaRecord, limit int) {
	if r.KeySize < len(r.Key) {
		r.KeySize = len(r.Key)
	}
	if r.ValueSize < len(r.Value) {
		r.ValueSize = len(r.Value)
	}
	if limit <= 0 {
		return
	}
	if len(r.Key) > limit {
		r.Key = r.Key[:limit:limit]
	}
	if len(r.Value) > limit {
		r.Value = r.Value[:limit:limit]
	}
}

// RecordBuffer keeps the newest records of a view, bounded by a count and
// by bytes: appending evicts the oldest records beyond either limit and
// counts them as dropped. Records receive contiguous sequence numbers so a
// view can keep pointing at one while older ones leave.
type RecordBuffer struct {
	recs     []KafkaRecord
	start    int // index of the oldest record held in recs
	bytes    int
	maxCount int
	maxBytes int
	nextSeq  uint64
	dropped  uint64
}

// NewRecordBuffer returns a buffer holding at most maxCount records and
// maxBytes bytes (each at least one record).
func NewRecordBuffer(maxCount, maxBytes int) *RecordBuffer {
	return &RecordBuffer{maxCount: max(maxCount, 1), maxBytes: max(maxBytes, 1), nextSeq: 1}
}

// Append stores r with the next sequence number and evicts the oldest
// records beyond the limits (the newest record always stays).
func (b *RecordBuffer) Append(r KafkaRecord) {
	r.Seq = b.nextSeq
	b.nextSeq++
	b.recs = append(b.recs, r)
	b.bytes += r.Bytes()
	for b.Len() > 1 && (b.Len() > b.maxCount || b.bytes > b.maxBytes) {
		b.bytes -= b.recs[b.start].Bytes()
		b.recs[b.start] = KafkaRecord{} // release its bytes
		b.start++
		b.dropped++
	}
	if b.start > len(b.recs)/2 && b.start > 64 { // compact, amortized O(1)
		n := copy(b.recs, b.recs[b.start:])
		clear(b.recs[n:])
		b.recs, b.start = b.recs[:n], 0
	}
}

// Len is the number of records held.
func (b *RecordBuffer) Len() int { return len(b.recs) - b.start }

// Bytes is the memory held, as KafkaRecord.Bytes counts it.
func (b *RecordBuffer) Bytes() int { return b.bytes }

// Dropped is the number of records evicted since creation or Reset.
func (b *RecordBuffer) Dropped() uint64 { return b.dropped }

// At returns the i-th record, 0 being the oldest held.
func (b *RecordBuffer) At(i int) *KafkaRecord { return &b.recs[b.start+i] }

// FirstSeq is the sequence number of the oldest record held.
func (b *RecordBuffer) FirstSeq() uint64 { return b.nextSeq - uint64(b.Len()) }

// Index returns the position of the record with sequence seq, if held.
func (b *RecordBuffer) Index(seq uint64) (int, bool) {
	first := b.FirstSeq()
	if seq < first || seq >= b.nextSeq {
		return 0, false
	}
	return int(seq - first), true
}

// Reset empties the buffer; sequence numbers keep increasing.
func (b *RecordBuffer) Reset() {
	b.recs, b.start, b.bytes, b.dropped = nil, 0, 0, 0
}

// KafkaSecurities are the security protocols a profile may name.
var KafkaSecurities = []string{"plaintext", "ssl", "sasl_plaintext", "sasl_ssl"}

// KafkaMechanisms are the SASL mechanisms a profile may name.
var KafkaMechanisms = []string{"plain", "scram-sha-256", "scram-sha-512"}

// NormalizeKafkaSecurity spells a security protocol as KafkaSecurities do
// (lower case, '_'), so SASL_SSL read from a dotenv file works.
func NormalizeKafkaSecurity(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "-", "_")
}

// NormalizeKafkaMechanism spells a SASL mechanism as KafkaMechanisms do
// (lower case, '-'), so SCRAM-SHA-512 works.
func NormalizeKafkaMechanism(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
}
