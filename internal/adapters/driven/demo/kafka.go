package demo

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// Kafka is the demo implementation of ports.TopicSourceFactory: every
// topic exists, with records generated from its name, so the Kafka screen
// works in --demo without a broker. Records are a pure function of topic,
// partition and offset; the end offset grows with the clock.
type Kafka struct {
	Seed  int64
	Clock ports.Clock
	// Every is the interval between two records of a partition (default
	// 3s).
	Every time.Duration
	// Missing names topics that do not exist, to show that state.
	Missing []string

	epoch time.Time // offset 0 of every partition
}

// NewKafka returns the demo cluster; records go back two days.
func NewKafka(seed int64, clock ports.Clock) *Kafka {
	return &Kafka{Seed: seed, Clock: clock, Every: 3 * time.Second, epoch: clock.Now().Add(-48 * time.Hour)}
}

// Open implements ports.TopicSourceFactory.
func (k *Kafka) Open(_ context.Context, _ domain.KafkaConnection) (ports.TopicSource, error) {
	return kafkaSource{k}, nil
}

type kafkaSource struct{ k *Kafka }

func (kafkaSource) Close() {}

// partitions is 1 to 6, from the topic name.
func (k *Kafka) partitions(topic string) int { return int(hashOf(k.Seed, topic)%6) + 1 }

// interval spreads partitions a little so records interleave.
func (k *Kafka) interval(topic string, p int32) time.Duration {
	return k.Every + time.Duration(hashOf(topic, p)%1000)*time.Millisecond
}

// end is the next offset of a partition at t.
func (k *Kafka) end(topic string, p int32, t time.Time) int64 {
	return int64(t.Sub(k.epoch) / k.interval(topic, p))
}

// offsetAt is the first offset at or after t.
func (k *Kafka) offsetAt(topic string, p int32, t time.Time) int64 {
	iv := k.interval(topic, p)
	return max(int64((t.Sub(k.epoch)+iv-1)/iv), 0)
}

func (s kafkaSource) Describe(_ context.Context, topics []string) ([]ports.TopicInfo, error) {
	out := make([]ports.TopicInfo, len(topics))
	for i, t := range topics {
		out[i] = ports.TopicInfo{Name: t, Partitions: s.k.partitions(t)}
		if slices.Contains(s.k.Missing, t) {
			out[i] = ports.TopicInfo{Name: t, Err: fmt.Errorf("topic %s: %w", t, domain.ErrNotFound)}
		}
	}
	return out, nil
}

func (s kafkaSource) Read(ctx context.Context, q ports.TopicRead) (<-chan ports.RecordBatch, error) {
	k := s.k
	if slices.Contains(k.Missing, q.Topic) {
		return nil, fmt.Errorf("topic %s: %w", q.Topic, domain.ErrNotFound)
	}
	now := k.Clock.Now()
	parts := k.partitions(q.Topic)
	next := map[int32]int64{}
	var hist []domain.KafkaRecord
	for p := range int32(parts) {
		if len(q.Partitions) > 0 && !slices.Contains(q.Partitions, p) {
			continue
		}
		end := k.end(q.Topic, p, now)
		start := max(end-int64(q.Tail), 0)
		if q.Since > 0 {
			start = k.offsetAt(q.Topic, p, now.Add(-q.Since))
		}
		if q.Limit > 0 {
			start = max(start, end-int64(q.Limit))
		}
		for off := start; off < end; off++ {
			hist = append(hist, k.record(q.Topic, p, off))
		}
		next[p] = end
	}
	out := make(chan ports.RecordBatch, 4)
	go func() {
		defer close(out)
		select {
		case out <- ports.RecordBatch{Records: hist, HistoryDone: true}:
		case <-ctx.Done():
			return
		}
		if !q.Follow {
			return
		}
		tick := k.Clock.NewTicker(500 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case t := <-tick.C():
				var live []domain.KafkaRecord
				for p, off := range next {
					for end := k.end(q.Topic, p, t); off < end; off++ {
						live = append(live, k.record(q.Topic, p, off))
					}
					next[p] = off
				}
				if len(live) == 0 {
					continue
				}
				select {
				case out <- ports.RecordBatch{Records: live}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

var demoEvents = []string{"Created", "Updated", "Validated", "Rejected", "Completed"}

// record is the record of a topic partition at an offset: mostly JSON
// events, sometimes plain text, a schema-registry framed value or a
// tombstone, so every rendering shows in the demo.
func (k *Kafka) record(topic string, p int32, off int64) domain.KafkaRecord {
	h := hashOf(k.Seed, topic, p, off)
	r := rand.New(rand.NewPCG(h, h>>17|h<<47)) // cheap to seed: one per record
	at := k.epoch.Add(time.Duration(off) * k.interval(topic, p))
	word := topicNoun(topic)
	id := fmt.Sprintf("%s-%05d", strings.ToUpper(word[:min(3, len(word))]), r.IntN(100000))
	rec := domain.KafkaRecord{
		Topic: topic, Partition: p, Offset: off, Time: at,
		Key:     []byte(id),
		Headers: []domain.KafkaHeader{{Key: "traceId", Value: fmt.Appendf(nil, "%016x", r.Uint64())}, {Key: "source", Value: []byte("demo")}},
	}
	switch n := r.IntN(100); {
	case n < 2:
		rec.Value = nil // tombstone
	case n < 5:
		v := make([]byte, 5, 40)
		v[0] = 0
		binary.BigEndian.PutUint32(v[1:], uint32(100+r.IntN(400)))
		rec.Value = append(v, fmt.Appendf(nil, "\x02%s", id)...)
	case n < 10:
		rec.Value = fmt.Appendf(nil, "%s %s at %s", id, strings.ToLower(demoEvents[r.IntN(len(demoEvents))]), at.Format(time.RFC3339))
	default:
		rec.Value = fmt.Appendf(nil, `{"eventId":"%08x","type":"%s%s","id":"%s","amount":%d.%02d,"currency":"EUR","attempt":%d,"occurredAt":"%s"}`,
			r.Uint32(), className(strings.TrimSuffix(word, "s")), demoEvents[r.IntN(len(demoEvents))], id, r.IntN(900)+10, r.IntN(100), r.IntN(3)+1, at.Format(time.RFC3339Nano))
	}
	rec.KeySize, rec.ValueSize = len(rec.Key), len(rec.Value)
	return rec
}

// topicNoun is the first word of a topic name: "orders.requested" gives
// "orders".
func topicNoun(topic string) string {
	w, _, _ := strings.Cut(strings.Map(func(r rune) rune {
		if r == '.' || r == '_' || r == '-' {
			return ' '
		}
		return r
	}, topic), " ")
	if w == "" {
		return "event"
	}
	return strings.ToLower(w)
}
