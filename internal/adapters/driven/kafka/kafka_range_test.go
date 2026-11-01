package kafka

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
)

// readHistory reads until the history is done and returns its records and
// the batch that ended it.
func readHistory(t *testing.T, src ports.TopicSource, q ports.TopicRead) ([]domain.KafkaRecord, ports.RecordBatch, <-chan ports.RecordBatch) {
	t.Helper()
	q.Topic = topic
	ch, err := src.Read(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var recs []domain.KafkaRecord
	timeout := time.After(10 * time.Second)
	for {
		select {
		case b, ok := <-ch:
			if !ok {
				t.Fatal("the read ended before its history was done")
			}
			if b.Err != nil {
				t.Fatal(b.Err)
			}
			recs = append(recs, b.Records...)
			if b.HistoryDone {
				return recs, b, ch
			}
		case <-timeout:
			t.Fatal("history did not end")
		}
	}
}

func nOf(recs []domain.KafkaRecord) []string {
	var out []string
	for _, r := range recs {
		out = append(out, string(r.Value))
	}
	slices.Sort(out)
	return out
}

// rangeFixture writes 30 records, one a second, record n in partition n%3,
// and returns the source with the times that frame records 10 to 19.
func rangeFixture(t *testing.T) (src ports.TopicSource, from, to time.Time) {
	t.Helper()
	c, produceN := newCluster(t)
	produceN(30)
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(src.Close)
	all, _, _ := readHistory(t, src, ports.TopicRead{Tail: 100, Limit: 100})
	at := map[string]time.Time{}
	for _, r := range all {
		at[string(r.Value)] = r.Time
	}
	return src, at[`{"n":10}`], at[`{"n":20}`]
}

func want(from, to int) []string {
	var out []string
	for n := from; n < to; n++ {
		out = append(out, fmt.Sprintf(`{"n":%d}`, n))
	}
	slices.Sort(out)
	return out
}

func TestRangeReadsFromStartToEndExclusive(t *testing.T) {
	src, from, to := rangeFixture(t)
	recs, done, ch := readHistory(t, src, ports.TopicRead{From: from, To: to})
	// A fetch returns whole batches: records past the end offset are in it
	// and must not be delivered.
	if got := nOf(recs); !slices.Equal(got, want(10, 20)) {
		t.Fatalf("records %v, want 10..19", got)
	}
	if done.More || len(done.Next) != 3 {
		t.Fatalf("a range that fits has no more: %+v", done)
	}
	if _, open := <-ch; open {
		t.Fatal("a closed range ends after its history")
	}
}

func TestRangePagesByOffset(t *testing.T) {
	src, from, to := rangeFixture(t)
	var all []string
	var after map[int32]int64
	pages := 0
	for range 10 {
		recs, done, _ := readHistory(t, src, ports.TopicRead{From: from, To: to, Limit: 2, After: after})
		pages++
		all = append(all, nOf(recs)...)
		if !done.More {
			break
		}
		if len(recs) != 6 {
			t.Fatalf("a full page holds Limit records of each of the 3 partitions: %d", len(recs))
		}
		after = done.Next
	}
	slices.Sort(all)
	if !slices.Equal(all, want(10, 20)) || pages != 2 {
		t.Fatalf("%d pages gave %v, want 10..19 exactly once in 2 pages", pages, all)
	}
}

func TestRangeWithoutRecordsAtOrAfterItsStart(t *testing.T) {
	src, _, _ := rangeFixture(t)
	recs, done, _ := readHistory(t, src, ports.TopicRead{From: time.Now().Add(time.Hour), To: time.Now().Add(2 * time.Hour)})
	if len(recs) != 0 || done.More {
		t.Fatalf("a range in the future of the log holds nothing: %v", nOf(recs))
	}
}

func TestOpenRangeFollowsWhenItFitsAPage(t *testing.T) {
	c, produceN := newCluster(t)
	produceN(9)
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := src.Read(ctx, ports.TopicRead{Topic: topic, From: time.Now().Add(-time.Hour), Follow: true, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	got, timeout := 0, time.After(10*time.Second)
	for got < 9 {
		select {
		case b := <-ch:
			got += len(b.Records)
			if b.HistoryDone && b.More {
				t.Fatal("nine records fit one page")
			}
		case <-timeout:
			t.Fatalf("history: %d of 9", got)
		}
	}
	produce(t, c, 1)
	for live := false; !live; {
		select {
		case b, ok := <-ch:
			if !ok {
				t.Fatal("an open range follows: the read ended")
			}
			live = len(b.Records) > 0
		case <-timeout:
			t.Fatal("no live record")
		}
	}
}

func TestPageWithASuccessorDoesNotFollow(t *testing.T) {
	c, produceN := newCluster(t)
	produceN(30)
	src, err := (&Factory{Options: fastOptions(nil)}).Open(context.Background(), conn(c))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	_, done, ch := readHistory(t, src, ports.TopicRead{From: time.Now().Add(-time.Hour), Follow: true, Limit: 2})
	if !done.More {
		t.Fatal("30 records do not fit pages of 6")
	}
	select {
	case _, open := <-ch:
		if open {
			t.Fatal("unexpected batch")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a page with a successor must end the read, whatever Follow says")
	}
}
