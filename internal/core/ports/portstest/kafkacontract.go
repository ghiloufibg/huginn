package portstest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// TopicFixture is what a TopicSourceFactory implementation provides to the
// contract suite: a connection reaching a cluster where Topic has at least
// two partitions holding more than Tail records each, all written less
// than a minute ago; Produce, when not nil, writes one new record to Topic
// for a following read to receive.
type TopicFixture struct {
	Factory ports.TopicSourceFactory
	Conn    domain.KafkaConnection
	Topic   string
	Tail    int
	Produce func(t *testing.T)
}

// RunTopicSourceContract checks the behavior every TopicSource must have.
func RunTopicSourceContract(t *testing.T, newFixture func(t *testing.T) TopicFixture) {
	t.Helper()
	open := func(t *testing.T) (TopicFixture, ports.TopicSource) {
		f := newFixture(t)
		src, err := f.Factory.Open(context.Background(), f.Conn)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		t.Cleanup(src.Close)
		return f, src
	}
	collect := func(t *testing.T, ch <-chan ports.RecordBatch) ([]domain.KafkaRecord, bool) {
		t.Helper()
		var recs []domain.KafkaRecord
		done := false
		timeout := time.After(30 * time.Second)
		for {
			select {
			case b, ok := <-ch:
				if !ok {
					return recs, done
				}
				if b.Err != nil {
					t.Fatalf("read failed: %v", b.Err)
				}
				recs = append(recs, b.Records...)
				done = done || b.HistoryDone
			case <-timeout:
				t.Fatal("read did not end")
			}
		}
	}

	t.Run("describe", func(t *testing.T) {
		f, src := open(t)
		infos, err := src.Describe(context.Background(), []string{f.Topic, "huginn-contract-missing-topic"})
		if err != nil || len(infos) != 2 {
			t.Fatalf("Describe: %v %v", infos, err)
		}
		if infos[0].Name != f.Topic || infos[0].Partitions < 2 || infos[0].Err != nil {
			t.Errorf("topic: %+v", infos[0])
		}
		if !errors.Is(infos[1].Err, domain.ErrNotFound) {
			t.Errorf("unknown topic: %+v", infos[1])
		}
	})

	t.Run("tail reads the last records of each partition, then ends", func(t *testing.T) {
		f, src := open(t)
		ch, err := src.Read(context.Background(), ports.TopicRead{Topic: f.Topic, Tail: f.Tail, Limit: f.Tail})
		if err != nil {
			t.Fatal(err)
		}
		recs, done := collect(t, ch)
		if !done {
			t.Error("HistoryDone never set")
		}
		perPart := map[int32][]int64{}
		for _, r := range recs {
			if r.Topic != f.Topic {
				t.Errorf("record of %s", r.Topic)
			}
			perPart[r.Partition] = append(perPart[r.Partition], r.Offset)
		}
		if len(perPart) < 2 {
			t.Errorf("records from %d partitions, want every partition", len(perPart))
		}
		for p, offs := range perPart {
			if len(offs) != f.Tail {
				t.Errorf("partition %d: %d records, want %d", p, len(offs), f.Tail)
			}
			for i := 1; i < len(offs); i++ {
				if offs[i] <= offs[i-1] {
					t.Errorf("partition %d: offsets out of order %v", p, offs)
					break
				}
			}
		}
	})

	t.Run("a window reads records since a time, bounded by limit", func(t *testing.T) {
		f, src := open(t)
		ch, err := src.Read(context.Background(), ports.TopicRead{Topic: f.Topic, Since: time.Hour, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		recs, _ := collect(t, ch)
		per := map[int32]int{}
		for _, r := range recs {
			per[r.Partition]++
		}
		for p, n := range per {
			if n != 1 {
				t.Errorf("partition %d: %d records, limit 1", p, n)
			}
		}
		if len(per) < 2 {
			t.Errorf("window found records in %d partitions", len(per))
		}
	})

	t.Run("unknown topic fails", func(t *testing.T) {
		_, src := open(t)
		ch, err := src.Read(context.Background(), ports.TopicRead{Topic: "huginn-contract-missing-topic", Tail: 1})
		if err == nil {
			for b := range ch {
				err = errors.Join(err, b.Err)
			}
		}
		if !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("follow delivers new records until cancelled", func(t *testing.T) {
		f, src := open(t)
		if f.Produce == nil {
			t.Skip("no producer")
		}
		ctx, cancel := context.WithCancel(context.Background())
		ch, err := src.Read(ctx, ports.TopicRead{Topic: f.Topic, Tail: 1, Limit: 1, Follow: true})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		timeout := time.After(30 * time.Second)
		for done := false; !done; {
			select {
			case b := <-ch:
				done = b.HistoryDone
			case <-timeout:
				t.Fatal("no history")
			}
		}
		f.Produce(t)
		got := false
		for !got {
			select {
			case b, ok := <-ch:
				if !ok {
					t.Fatal("closed while following")
				}
				got = len(b.Records) > 0
			case <-timeout:
				t.Fatal("new record not delivered")
			}
		}
		cancel()
		for range ch { //nolint:revive // drain until closed
		}
	})
}
