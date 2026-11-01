package bootstrap

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/adapters/driving/cli"
	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/diag"
)

// pagedSession serves a range in pages of one record: Next of a page is the
// After of the query for the next one.
type pagedSession struct {
	ports.KafkaSession
	queries []ports.KafkaQuery
	pages   [][]domain.KafkaRecord
}

func (s *pagedSession) Read(_ context.Context, q ports.KafkaQuery) (<-chan ports.KafkaBatch, error) {
	s.queries = append(s.queries, q)
	i := int(q.After[0])
	b := ports.KafkaBatch{Records: s.pages[i], HistoryDone: true}
	if i+1 < len(s.pages) {
		b.Next = map[int32]int64{0: int64(i + 1)}
	}
	ch := make(chan ports.KafkaBatch, 1)
	ch <- b
	close(ch)
	return ch, nil
}

func TestKafkaReadPrintsEveryPageOfARange(t *testing.T) {
	rec := func(v string) []domain.KafkaRecord {
		return []domain.KafkaRecord{{Topic: "t", Value: []byte(v), Time: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}}
	}
	s := &pagedSession{pages: [][]domain.KafkaRecord{rec("first"), rec("second"), rec("third")}}
	w, err := domain.NewRange(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := kafkaReadAll(context.Background(), s, ports.KafkaQuery{Topic: "t", Window: w, After: map[int32]int64{0: 0}}, true, false, &out, &errOut); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(out.String()); len(got) != 3 || got[0] != "first" || got[1] != "second" || got[2] != "third" {
		t.Fatalf("every page must be printed, in order:\n%s", out.String())
	}
	if len(s.queries) != 3 || s.queries[2].After[0] != 2 {
		t.Fatalf("each page starts where the one before ended: %+v", s.queries)
	}
}

func TestKafkaReadRangeInTheDemo(t *testing.T) {
	read := cli.KafkaOptions{Options: cli.Options{Demo: true}, Repo: "payment-service", Topic: "payments.requested", From: "now-30m", To: "now-10m", Raw: true}
	var out bytes.Buffer
	if err := kafkaRead(context.Background(), read, noFiles(), diag.Discard(), &out, &bytes.Buffer{}, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"eventId"`) {
		t.Fatalf("a range of the demo topic holds records:\n%s", out.String())
	}
	read.From = "soon"
	if err := kafkaRead(context.Background(), read, noFiles(), diag.Discard(), &out, &bytes.Buffer{}, false); err == nil || !strings.Contains(err.Error(), "--from/--to") {
		t.Fatalf("a bad --from must be reported: %v", err)
	}
}
