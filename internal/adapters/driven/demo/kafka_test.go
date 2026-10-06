package demo

import (
	"context"
	"testing"
	"time"

	"github.com/ghiloufibg/huginn/internal/core/domain"
	"github.com/ghiloufibg/huginn/internal/core/ports"
	"github.com/ghiloufibg/huginn/internal/core/ports/portstest"
)

func TestKafkaContract(t *testing.T) {
	portstest.RunTopicSourceContract(t, func(t *testing.T) portstest.TopicFixture {
		clock := portstest.NewFakeClock(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
		k := NewKafka(42, clock)
		k.Missing = []string{"huginn-contract-missing-topic"}
		topic := "orders.requested"
		for k.partitions(topic) < 2 {
			topic += "x"
		}
		return portstest.TopicFixture{
			Factory: k, Topic: topic, Tail: 5,
			Produce: func(*testing.T) {
				go func() { // the ticker reads the clock: advance until the record is due
					for range 40 {
						clock.Advance(500 * time.Millisecond)
						time.Sleep(5 * time.Millisecond)
					}
				}()
			},
		}
	})
}

func TestKafkaRecordsAreDeterministicAndVaried(t *testing.T) {
	clock := portstest.NewFakeClock(time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC))
	a, b := NewKafka(7, clock), NewKafka(7, clock)
	kinds := map[domain.PayloadKind]int{}
	for off := range int64(500) {
		ra, rb := a.record("payments.completed", 0, off), b.record("payments.completed", 0, off)
		if string(ra.Value) != string(rb.Value) || !ra.Time.Equal(rb.Time) || ra.Offset != off {
			t.Fatalf("offset %d differs", off)
		}
		k, _ := domain.ClassifyPayload(ra.Value)
		kinds[k]++
	}
	for _, k := range []domain.PayloadKind{domain.PayloadJSON, domain.PayloadText, domain.PayloadFramed, domain.PayloadNull} {
		if kinds[k] == 0 {
			t.Errorf("no payload of kind %d in 500 records: %v", k, kinds)
		}
	}
	src, _ := a.Open(context.Background(), domain.KafkaConnection{})
	ch, err := src.Read(context.Background(), ports.TopicRead{Topic: "payments.completed", Since: time.Minute, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if b := <-ch; len(b.Records) == 0 || !b.HistoryDone {
		t.Fatalf("a minute of history: %d records", len(b.Records))
	}
}
