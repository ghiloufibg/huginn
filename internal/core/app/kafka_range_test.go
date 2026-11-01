package app

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

// rangeTopic fills orders.requested (two partitions) with n records, one a
// second, record i in partition i%2.
func rangeTopic(k *portstest.FakeKafka, n int) {
	for i := range n {
		k.Produce(domain.KafkaRecord{
			Topic: "orders.requested", Partition: int32(i % 2), Time: kafkaNow.Add(time.Duration(i) * time.Second),
			Value: []byte(fmt.Sprint("v", i)),
		})
	}
}

func kafkaRange(t *testing.T, from, to int) domain.TimeWindow {
	t.Helper()
	var end time.Time
	if to >= 0 {
		end = kafkaNow.Add(time.Duration(to) * time.Second)
	}
	w, err := domain.NewRange(kafkaNow.Add(time.Duration(from)*time.Second), end)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// readPage reads a topic until its history is done and returns the records
// and where the next page starts (nil on the last page).
func readPage(t *testing.T, sess ports.KafkaSession, q ports.KafkaQuery) ([]domain.KafkaRecord, map[int32]int64) {
	t.Helper()
	q.Topic = "orders.requested"
	ch, err := sess.Read(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	var recs []domain.KafkaRecord
	timeout := time.After(5 * time.Second)
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
				return recs, b.Next
			}
		case <-timeout:
			t.Fatal("timeout")
		}
	}
}

func values(recs []domain.KafkaRecord) []string {
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = string(r.Value)
	}
	return out
}

func openOrders(t *testing.T, s *KafkaService) ports.KafkaSession {
	t.Helper()
	sess, err := s.Open(context.Background(), "rec", "orders")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	return sess
}

func TestKafkaRangeReadsOnlyItsRecords(t *testing.T) {
	s, _, k := kafkaFixture(t)
	rangeTopic(k, 20)
	sess := openOrders(t, s)
	recs, next := readPage(t, sess, ports.KafkaQuery{Window: kafkaRange(t, 5, 15), Follow: true})
	if want := []string{"v5", "v6", "v7", "v8", "v9", "v10", "v11", "v12", "v13", "v14"}; !slices.Equal(values(recs), want) {
		t.Fatalf("records %v, want %v", values(recs), want) // [From, To): v15 is at To
	}
	if next != nil {
		t.Fatalf("the range fits one page, next %v", next)
	}
	r := k.Reads[0]
	if !r.From.Equal(kafkaNow.Add(5*time.Second)) || !r.To.Equal(kafkaNow.Add(15*time.Second)) || r.Follow || r.Since != 0 || r.Tail != 0 || r.Limit != 500 {
		t.Fatalf("a closed range does not follow and reads a page of MaxRecords/partitions: %+v", r)
	}
}

func TestKafkaRangeIsFilteredByTimestamp(t *testing.T) {
	s, _, k := kafkaFixture(t)
	at := func(sec int) time.Time { return kafkaNow.Add(time.Duration(sec) * time.Second) }
	for _, r := range []struct {
		sec  int
		text string
	}{{0, "a"}, {-3600, "late"}, {2, "b"}, {4, "c"}, {6, "after"}} {
		// "late" is in the middle of the partition but stamped an hour
		// before the range: a producer's clock was behind.
		k.Produce(domain.KafkaRecord{Topic: "orders.requested", Partition: 0, Time: at(r.sec), Value: []byte(r.text)})
	}
	sess := openOrders(t, s)
	recs, _ := readPage(t, sess, ports.KafkaQuery{Window: kafkaRange(t, 0, 5)})
	if got := values(recs); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("records outside [From, To) by their own time must go: %v", got)
	}
}

func TestKafkaRangePagesCoverTheRangeExactly(t *testing.T) {
	s, _, k := kafkaFixture(t)
	rangeTopic(k, 20)
	s.MaxRecords = 4 // two per partition and page
	sess := openOrders(t, s)
	var all []string
	var after map[int32]int64
	pages := 0
	for range 20 {
		recs, next := readPage(t, sess, ports.KafkaQuery{Window: kafkaRange(t, 0, 20), After: after})
		pages++
		if len(recs) == 0 || len(recs) > 4 {
			t.Fatalf("page %d holds %d records", pages, len(recs))
		}
		all = append(all, values(recs)...)
		if next == nil {
			break
		}
		after = next
	}
	slices.Sort(all)
	var want []string
	for i := range 20 {
		want = append(want, fmt.Sprint("v", i))
	}
	slices.Sort(want)
	if !slices.Equal(all, want) || pages != 5 {
		t.Fatalf("%d pages gave %v, want every record once", pages, all)
	}
}

// The view holds a number of bytes too: a page cut by that bound must give
// the records it dropped to the next page, not lose them.
func TestKafkaRangePageCutByBytesLosesNothing(t *testing.T) {
	s, _, k := kafkaFixture(t)
	rangeTopic(k, 12)
	one := domain.KafkaRecord{Value: []byte("v0")}
	s.MaxBufferBytes = 3 * one.Bytes() // about three records
	sess := openOrders(t, s)
	seen := map[string]bool{}
	var after map[int32]int64
	for range 40 {
		recs, next := readPage(t, sess, ports.KafkaQuery{Window: kafkaRange(t, 0, 12), After: after})
		if len(recs) == 0 {
			t.Fatal("a page must hold at least one record, or paging never ends")
		}
		for _, v := range values(recs) {
			seen[v] = true
		}
		if next == nil {
			break
		}
		after = next
	}
	if len(seen) != 12 {
		t.Fatalf("saw %d of 12 records: %v", len(seen), seen)
	}
}

func TestKafkaOpenRangeFollowsOnlyOnItsLastPage(t *testing.T) {
	s, _, k := kafkaFixture(t)
	rangeTopic(k, 6)
	sess := openOrders(t, s)
	open := kafkaRange(t, 0, -1)

	// Everything fits one page: the read goes on with live records.
	ch, err := sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.requested", Window: open, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	if recs, done := readAll(t, ch, true); len(recs) != 6 || !done {
		t.Fatalf("history: %d records", len(recs))
	}
	k.Produce(domain.KafkaRecord{Topic: "orders.requested", Partition: 0, Time: kafkaNow.Add(time.Minute), Value: []byte("live")})
	for live, timeout := false, time.After(5*time.Second); !live; {
		select {
		case b, ok := <-ch:
			if !ok {
				t.Fatal("an open range follows: the read ended")
			}
			for _, r := range b.Records {
				live = live || string(r.Value) == "live"
			}
		case <-timeout:
			t.Fatal("an open range follows: no live record")
		}
	}

	// More than a page: the page is not live and the read ends after it.
	s.MaxRecords = 2
	ch, err = sess.Read(context.Background(), ports.KafkaQuery{Topic: "orders.requested", Window: open, Follow: true})
	if err != nil {
		t.Fatal(err)
	}
	var next map[int32]int64
	timeout := time.After(5 * time.Second)
	for closed := false; !closed; {
		select {
		case b, ok := <-ch:
			if !ok {
				closed = true
				break
			}
			if b.HistoryDone {
				next = b.Next
			}
		case <-timeout:
			t.Fatal("a page with a successor must end the read")
		}
	}
	if next == nil {
		t.Fatal("the first page must say where the next one starts")
	}
}
